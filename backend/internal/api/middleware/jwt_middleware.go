package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/pkg/jwtutil"
)

// JWTAuthMiddleware verifies the setup JWT in the Authorization header.
func JWTAuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		var tokenString string
		authSource := "header"

		// Try Authorization header first
		authHeader := c.GetHeader("Authorization")
		if authHeader != "" {
			parts := strings.Split(authHeader, " ")
			if len(parts) == 2 && parts[0] == "Bearer" {
				tokenString = parts[1]
			}
		}
		if tokenString == "" {
			if cookieToken, err := c.Cookie(SessionCookieName); err == nil {
				tokenString = cookieToken
				authSource = "cookie"
			}
		}

		if tokenString == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"code": 401, "msg": "Authorization required"})
			return
		}
		claims, err := jwtutil.ValidateToken(tokenString)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"code": 401, "msg": "Invalid or expired token"})
			return
		}
		if claims.Purpose != jwtutil.PurposeSetup || claims.UserID != "setup-admin" {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": 403, "msg": "Setup token required"})
			return
		}
		if authSource == "cookie" && !sameOriginCookieRequest(c) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": 403, "msg": "Cross-origin setup request rejected"})
			return
		}

		// Store user info in context for downstream handlers
		c.Set("user_id", claims.UserID)
		c.Set("username", claims.Username)
		c.Next()
	}
}
