package api

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	sysopt "github.com/harveyxiacn/ZenithPanel/backend/internal/service/system"
)

func registerSystemOptimizationRoutes(authGroup *gin.RouterGroup) {
	// ======================================
	// System Optimization (BBR, Swap, Sysctl, Cleanup)
	// ======================================
	authGroup.GET("/system/bbr/status", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"code": 200, "data": sysopt.GetBBRStatus()})
	})
	authGroup.POST("/system/bbr/enable", func(c *gin.Context) {
		if err := sysopt.EnableBBR(); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": fmt.Sprintf("Failed to enable BBR: %v", err)})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "BBR enabled successfully"})
	})
	authGroup.POST("/system/bbr/disable", func(c *gin.Context) {
		if err := sysopt.DisableBBR(); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": fmt.Sprintf("Failed to disable BBR: %v", err)})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "BBR disabled successfully"})
	})

	authGroup.GET("/system/swap/status", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"code": 200, "data": sysopt.GetSwapStatus()})
	})
	authGroup.POST("/system/swap/create", func(c *gin.Context) {
		var req struct {
			SizeMB int `json:"size_mb"`
		}
		if err := c.ShouldBindJSON(&req); err != nil || req.SizeMB == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "size_mb is required (256-16384)"})
			return
		}
		if err := sysopt.CreateSwap(req.SizeMB); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": fmt.Sprintf("Failed to create swap: %v", err)})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 200, "msg": fmt.Sprintf("Swap file created (%d MB)", req.SizeMB)})
	})
	authGroup.POST("/system/swap/remove", func(c *gin.Context) {
		if err := sysopt.RemoveSwap(); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": fmt.Sprintf("Failed to remove swap: %v", err)})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "Swap removed successfully"})
	})

	authGroup.GET("/system/sysctl/status", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"code": 200, "data": sysopt.GetSysctlTuningStatus()})
	})
	authGroup.POST("/system/sysctl/enable", func(c *gin.Context) {
		if err := sysopt.EnableSysctlTuning(); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": fmt.Sprintf("Failed to apply tuning: %v", err)})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "Network tuning applied successfully"})
	})
	authGroup.POST("/system/sysctl/disable", func(c *gin.Context) {
		if err := sysopt.DisableSysctlTuning(); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": fmt.Sprintf("Failed to revert tuning: %v", err)})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "Network tuning reverted to defaults"})
	})

	authGroup.GET("/system/cleanup/info", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"code": 200, "data": sysopt.GetCleanupInfo()})
	})
	authGroup.POST("/system/cleanup/run", func(c *gin.Context) {
		result := sysopt.RunCleanup()
		c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "Cleanup completed", "data": result})
	})

}
