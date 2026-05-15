package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/config"
)

// SetupGuardMiddleware ensures no one can access the panel API
// until the setup wizard is complete, EXCEPT the setup wizard itself.
func SetupGuardMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		cfg := config.GetConfig()

		// If system is already set up, we just let it pass to standard JWT auth (handled later)
		if cfg.IsSetupComplete {
			c.Next()
			return
		}

		// System is NOT set up.
		path := c.Request.URL.Path

		// The ONLY allowed endpoints are the specific setup wizard frontend
		// and the setup API routes, which must match the generated suffix.
		expectedSetupPrefix := "/zenith-setup-" + cfg.SetupURLSuffix
		expectedSetupAPI := "/api/setup"

		// Allow static assets so the setup wizard page can load its CSS/JS
		if strings.HasPrefix(path, "/assets/") || path == "/vite.svg" {
			c.Next()
			return
		}

		// Health / ping are intentionally unauthenticated AND pre-setup-safe
		// so external monitors (UptimeRobot, Prometheus, docker-smoke) can
		// probe the panel without negotiating the setup wizard first. The
		// handlers themselves report only non-secret state (uptime, db ok,
		// proxy running/stopped).
		if path == "/api/v1/health" || path == "/api/v1/ping" {
			c.Next()
			return
		}

		if strings.HasPrefix(path, expectedSetupPrefix) || strings.HasPrefix(path, expectedSetupAPI) {
			c.Next()
			return
		}

		// Any other route is blocked. They get a 403.
		c.JSON(http.StatusForbidden, gin.H{
			"code": 403,
			"msg":  "System not initialized. Please check your terminal for the setup URL.",
		})
		c.Abort()
	}
}
