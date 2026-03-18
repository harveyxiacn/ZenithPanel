package api

import (
	"github.com/gin-gonic/gin"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/config"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/model"
)

func recordAudit(c *gin.Context, action, detail string) {
	username := ""
	if u, exists := c.Get("username"); exists {
		username, _ = u.(string)
	}
	ip := c.ClientIP()
	// Run in background so audit failures never affect the main request
	go func() {
		defer func() { recover() }()
		config.DB.Create(&model.AuditLog{
			Username: username,
			Action:   action,
			Detail:   detail,
			IP:       ip,
		})
	}()
}
