package middleware

import (
	"crypto/subtle"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/config"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/model"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/pkg/apitoken"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/pkg/jwtutil"
)

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
		if h := c.GetHeader("Authorization"); h != "" {
			parts := strings.SplitN(h, " ", 2)
			if len(parts) == 2 && parts[0] == "Bearer" {
				token = parts[1]
			}
		}
		if token == "" {
			token = c.Query("token")
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
			// Best-effort last_used update; ignore errors so a failed UPDATE
			// never fails an otherwise-good auth.
			config.DB.Model(&model.ApiToken{}).Where("id = ?", row.ID).Update("last_used_at", time.Now().Unix())

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
		c.Set("user_id", claims.UserID)
		c.Set("username", claims.Username)
		c.Set("principal", "admin:"+claims.Username)
		c.Set("scopes", "*")
		c.Next()
	}
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
		if strings.TrimSpace(s) == required || strings.TrimSpace(s) == "*" {
			return true
		}
	}
	return false
}
