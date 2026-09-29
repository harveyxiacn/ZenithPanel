package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// RequiredScope maps a request on the authenticated /api/v1 group to the
// API-token scope it needs (see docs/cli_design.md §6). Browser JWTs and the
// unix socket carry "*" and so pass every check; this only narrows what a
// scoped `ztk_…` token can reach.
func RequiredScope(method, path string) string {
	p := strings.TrimPrefix(path, "/api/v1")
	isRead := method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions

	switch {
	// Shell / arbitrary file / scheduled-command / container access is
	// equivalent to root on the host: never grant it through `read`.
	case p == "/terminal" || strings.HasPrefix(p, "/terminal/"),
		strings.HasPrefix(p, "/fs/"),
		strings.HasPrefix(p, "/cron/"),
		strings.HasPrefix(p, "/admin/"):
		return "admin"
	case strings.HasPrefix(p, "/docker/"):
		if isRead {
			return "read"
		}
		return "admin"
	case strings.HasPrefix(p, "/firewall/"):
		if isRead {
			return "read"
		}
		return "firewall"
	case strings.HasPrefix(p, "/system/"):
		if isRead {
			return "read"
		}
		return "system"
	case p == "/proxy/apply":
		return "proxy:apply"
	case isRead:
		return "read"
	default:
		return "write"
	}
}

// ScopeMiddleware enforces RequiredScope for every route it is mounted on.
// It must run after AuthMiddleware so that "scopes" is populated.
func ScopeMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		need := RequiredScope(c.Request.Method, c.Request.URL.Path)
		if HasScope(c, need) {
			c.Next()
			return
		}
		// `write` implies `read`; `admin` implies everything below it.
		if (need == "read" && HasScope(c, "write")) || (need != "admin" && HasScope(c, "admin")) {
			c.Next()
			return
		}
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": 403, "msg": "Scope '" + need + "' required"})
	}
}
