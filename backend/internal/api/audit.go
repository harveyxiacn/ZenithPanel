package api

import (
	"github.com/gin-gonic/gin"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/config"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/model"
)

func recordAudit(c *gin.Context, action, detail string) {
	// Prefer the new "principal" set by AuthMiddleware (e.g. "token:ci",
	// "local-root", "admin:harvey"). Fall back to the legacy "username"
	// claim so handlers using JWTAuthMiddleware (setup wizard) keep working.
	principal := ""
	if p, exists := c.Get("principal"); exists {
		principal, _ = p.(string)
	}
	if principal == "" {
		if u, exists := c.Get("username"); exists {
			principal, _ = u.(string)
		}
	}
	ip := c.ClientIP()
	// Run in background so audit failures never affect the main request
	go func() {
		defer func() { recover() }()
		config.DB.Create(&model.AuditLog{
			Username: principal,
			Action:   action,
			Detail:   detail,
			IP:       ip,
		})
	}()
}
