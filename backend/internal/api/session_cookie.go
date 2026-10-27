package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/api/middleware"
)

func requestUsesHTTPS(c *gin.Context) bool {
	if c.Request.TLS != nil {
		return true
	}
	if !trustedProxyPeer(c.Request.RemoteAddr) {
		return false
	}
	forwardedProto, _, _ := strings.Cut(c.GetHeader("X-Forwarded-Proto"), ",")
	return strings.EqualFold(strings.TrimSpace(forwardedProto), "https")
}

func setSessionCookie(c *gin.Context, token string, lifetime time.Duration) {
	c.SetSameSite(http.SameSiteStrictMode)
	c.SetCookie(
		middleware.SessionCookieName,
		token,
		int(lifetime.Seconds()),
		"/",
		"",
		requestUsesHTTPS(c),
		true,
	)
}

func clearSessionCookie(c *gin.Context) {
	c.SetSameSite(http.SameSiteStrictMode)
	c.SetCookie(middleware.SessionCookieName, "", -1, "/", "", requestUsesHTTPS(c), true)
}
