package api

import (
	"fmt"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/service/updater"
)

func registerUpdateRoutes(g *gin.RouterGroup) {
	g.GET("/system/update/check", func(c *gin.Context) {
		info, err := updater.CheckForUpdate(c.Request.Context())
		if err != nil {
			log.Printf("Update check error: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "Update check failed"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "Success", "data": info})
	})

	g.GET("/system/update/status", func(c *gin.Context) {
		status, err := updater.ReadStatus()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": fmt.Sprintf("Read update status failed: %v", err)})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "Success", "data": status})
	})

	g.POST("/system/update/apply", func(c *gin.Context) {
		if err := updater.PerformUpdate(c.Request.Context()); err != nil {
			log.Printf("Update apply error: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": fmt.Sprintf("Update failed: %v", err)})
			return
		}
		recordAudit(c, "system.update", "helper_started")
		c.JSON(http.StatusAccepted, gin.H{"code": 202, "msg": "Update started. Poll /api/v1/system/update/status for commit or rollback."})
	})
}
