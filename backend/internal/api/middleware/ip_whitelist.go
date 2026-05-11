package middleware

import (
	"net"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/config"
)

// IPWhitelistMiddleware blocks requests from IPs not in the configured whitelist.
// Returns 404 (not 403) to avoid revealing the panel's existence.
// If the whitelist setting is empty, all IPs are allowed.
func IPWhitelistMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		whitelist := config.GetSetting("panel_ip_whitelist")
		if whitelist == "" {
			c.Next()
			return
		}

		clientIP := net.ParseIP(c.ClientIP())
		if clientIP == nil {
			c.AbortWithStatus(404)
			return
		}

		for _, entry := range strings.Split(whitelist, ",") {
			entry = strings.TrimSpace(entry)
			if entry == "" {
				continue
			}
			if strings.Contains(entry, "/") {
				_, cidr, err := net.ParseCIDR(entry)
				if err == nil && cidr.Contains(clientIP) {
					c.Next()
					return
				}
			} else {
				if allowed := net.ParseIP(entry); allowed != nil && allowed.Equal(clientIP) {
					c.Next()
					return
				}
			}
		}

		c.AbortWithStatus(404)
	}
}
