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

		if strings.HasPrefix(path, expectedSetupPrefix) || strings.HasPrefix(path, expectedSetupAPI) {
			// They are trying to access the setup system. 
			// We can let them hit the HTML or the setup API.
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
