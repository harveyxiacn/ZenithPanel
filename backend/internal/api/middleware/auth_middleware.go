package middleware

import (
	"crypto/subtle"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/config"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/model"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/pkg/apitoken"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/pkg/jwtutil"
)

// SetupAdminUserID is the JWT subject issued by the one-time setup login.
// Such tokens may only complete the setup wizard, never call /api/v1.
const SetupAdminUserID = "setup-admin"

// SessionCookieName is the HttpOnly browser-session cookie set after login.
const SessionCookieName = "zenith_session"

// AuthMiddleware accepts three principal types:
//
//  1. trusted_local — request arrived on the unix socket; no header needed
//  2. token:<name> — Authorization: Bearer ztk_…
//  3. admin:<u>    — Authorization: Bearer <JWT> (existing browser flow)
//
// Downstream handlers can read:
//
//	c.GetString("principal")  -> "local-root" | "token:foo" | "admin:harvey"
//	c.GetString("scopes")     -> for token principals, comma-separated; "*" for the others
func AuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 1) Unix socket trusts the caller implicitly (root-only FS perms).
		if c.GetBool("trusted_local") {
			c.Set("principal", "local-root")
			c.Set("scopes", "*")
			c.Next()
			return
		}

		var token string
		authSource := "header"
		if h := c.GetHeader("Authorization"); h != "" {
			parts := strings.SplitN(h, " ", 2)
			if len(parts) == 2 && parts[0] == "Bearer" {
				token = parts[1]
			}
		}
		if token == "" {
			if cookieToken, err := c.Cookie(SessionCookieName); err == nil {
				token = cookieToken
				authSource = "cookie"
			}
		}
		if token == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"code": 401, "msg": "Authorization required"})
			return
		}

		// 2) API token path
		if strings.HasPrefix(token, apitoken.Prefix) {
			if !apitoken.IsWellFormed(token) {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"code": 401, "msg": "Invalid api token"})
				return
			}
			hash := apitoken.Hash(token)
			var row model.ApiToken
			if err := config.DB.Where("token_hash = ?", hash).First(&row).Error; err != nil {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"code": 401, "msg": "Unknown api token"})
				return
			}
			// Constant-time recheck against the stored hash to avoid hash-collision games.
			if subtle.ConstantTimeCompare([]byte(row.TokenHash), []byte(hash)) != 1 {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"code": 401, "msg": "Unknown api token"})
				return
			}
			if row.Revoked {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"code": 401, "msg": "Token has been revoked"})
				return
			}
			if row.ExpiresAt > 0 && time.Now().Unix() > row.ExpiresAt {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"code": 401, "msg": "Token has expired"})
				return
			}
			// Best-effort and throttled: writing last_used_at on every dashboard
			// poll would turn read-only API traffic into constant SQLite writer
			// contention. One update per token per minute is sufficient for audit.
			now := time.Now().Unix()
			if row.LastUsedAt == 0 || now-row.LastUsedAt >= 60 {
				config.DB.Model(&model.ApiToken{}).
					Where("id = ? AND (last_used_at = 0 OR last_used_at <= ?)", row.ID, now-60).
					Update("last_used_at", now)
			}

			c.Set("principal", "token:"+row.Name)
			c.Set("scopes", row.Scopes)
			c.Set("token_id", row.ID)
			c.Next()
			return
		}

		// 3) Existing JWT path
		claims, err := jwtutil.ValidateToken(token)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"code": 401, "msg": "Invalid or expired token"})
			return
		}
		// Only explicitly admin-purpose JWTs may become full panel sessions.
		// Rejecting empty/unknown purposes deliberately expires pre-hardening
		// browser sessions once during upgrade instead of weakening separation.
		if claims.Purpose != jwtutil.PurposeAdmin || claims.UserID == "setup-admin" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"code": 401, "msg": "Setup token is not valid for the panel API"})
			return
		}
		adminID, parseErr := strconv.ParseUint(claims.UserID, 10, 64)
		if parseErr != nil || claims.LoggedInAt <= 0 {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"code": 401, "msg": "Invalid session"})
			return
		}
		var admin model.AdminUser
		if err := config.ReadDB().WithContext(c.Request.Context()).Select("id", "username", "session_version").
			Where("id = ? AND username = ?", uint(adminID), claims.Username).First(&admin).Error; err != nil || admin.SessionVersion != claims.SessionVersion {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"code": 401, "msg": "Session has been revoked"})
			return
		}
		c.Set("user_id", claims.UserID)
		c.Set("username", claims.Username)
		c.Set("session_version", claims.SessionVersion)
		c.Set("logged_in_at", claims.LoggedInAt)
		c.Set("principal", "admin:"+claims.Username)
		c.Set("scopes", "*")
		c.Set("auth_source", authSource)
		if authSource == "cookie" && !sameOriginCookieRequest(c) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": 403, "msg": "Cross-origin session request rejected"})
			return
		}
		c.Next()
	}
}

func sameOriginCookieRequest(c *gin.Context) bool {
	switch c.Request.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	origin := strings.TrimSpace(c.GetHeader("Origin"))
	if origin == "" {
		return false
	}
	parsed, err := url.Parse(origin)
	return err == nil && parsed.Scheme != "" && strings.EqualFold(parsed.Host, c.Request.Host)
}

// HasScope returns true when the principal in `c` is allowed to perform an
// action requiring `required` (e.g. "write", "admin"). Wildcard `*` grants
// everything; an explicit listed scope grants exactly that one.
func HasScope(c *gin.Context, required string) bool {
	scopes := c.GetString("scopes")
	if scopes == "" || scopes == "*" {
		return scopes == "*"
	}
	for s := range strings.SplitSeq(scopes, ",") {
		scope := strings.TrimSpace(s)
		if scope == required || scope == "*" || scope == ScopeAdmin {
			return true
		}
	}
	return false
}
