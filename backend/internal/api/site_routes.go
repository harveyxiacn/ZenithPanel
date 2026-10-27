package api

import (
	"context"
	"fmt"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/config"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/model"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/service/cert"
)

func registerSiteRoutes(authGroup *gin.RouterGroup, apply ...func(context.Context) error) {
	// ======================================
	// Sites (built-in web server / reverse proxy)
	// ======================================
	authGroup.GET("/sites", func(c *gin.Context) {
		var sites []model.Site
		if err := config.DB.Find(&sites).Error; err != nil {
			c.JSON(500, gin.H{"code": 500, "msg": "Failed to list sites"})
			return
		}
		c.JSON(200, gin.H{"code": 200, "msg": "Success", "data": sites})
	})

	authGroup.POST("/sites", func(c *gin.Context) {
		var s model.Site
		if err := c.ShouldBindJSON(&s); err != nil {
			c.JSON(400, gin.H{"code": 400, "msg": "Invalid parameters"})
			return
		}
		if s.Name == "" || s.Domain == "" || s.Type == "" {
			c.JSON(400, gin.H{"code": 400, "msg": "name, domain and type are required"})
			return
		}
		if err := config.DB.Create(&s).Error; err != nil {
			if strings.Contains(err.Error(), "UNIQUE") {
				c.JSON(409, gin.H{"code": 409, "msg": "Site name already exists"})
				return
			}
			c.JSON(500, gin.H{"code": 500, "msg": "Failed to create site"})
			return
		}
		go webserverReload()
		recordAudit(c, "site.create", s.Name)
		c.JSON(200, gin.H{"code": 200, "msg": "Created", "data": s})
	})

	authGroup.PUT("/sites/:id", func(c *gin.Context) {
		id, ok := parseUintID(c)
		if !ok {
			return
		}
		var s model.Site
		if err := config.DB.First(&s, "id = ?", id).Error; err != nil {
			c.JSON(404, gin.H{"code": 404, "msg": "Site not found"})
			return
		}
		if err := c.ShouldBindJSON(&s); err != nil {
			c.JSON(400, gin.H{"code": 400, "msg": "Invalid parameters"})
			return
		}
		s.ID = id
		if err := config.DB.Save(&s).Error; err != nil {
			c.JSON(500, gin.H{"code": 500, "msg": "Failed to update site"})
			return
		}
		go webserverReload()
		recordAudit(c, "site.update", s.Name)
		c.JSON(200, gin.H{"code": 200, "msg": "Updated", "data": s})
	})

	authGroup.DELETE("/sites/:id", func(c *gin.Context) {
		id, ok := parseUintID(c)
		if !ok {
			return
		}
		if err := config.DB.Delete(&model.Site{}, "id = ?", id).Error; err != nil {
			c.JSON(500, gin.H{"code": 500, "msg": "Failed to delete site"})
			return
		}
		go webserverReload()
		recordAudit(c, "site.delete", fmt.Sprintf("id=%d", id))
		c.JSON(200, gin.H{"code": 200, "msg": "Deleted"})
	})

	authGroup.POST("/sites/:id/enable", func(c *gin.Context) {
		id, ok := parseUintID(c)
		if !ok {
			return
		}
		var s model.Site
		if err := config.DB.First(&s, "id = ?", id).Error; err != nil {
			c.JSON(404, gin.H{"code": 404, "msg": "Site not found"})
			return
		}
		s.Enable = !s.Enable
		config.DB.Save(&s)
		go webserverReload()
		c.JSON(200, gin.H{"code": 200, "msg": "Updated", "data": gin.H{"enable": s.Enable}})
	})

	authGroup.POST("/sites/:id/cert", func(c *gin.Context) {
		id, ok := parseUintID(c)
		if !ok {
			return
		}
		var s model.Site
		if err := config.DB.First(&s, "id = ?", id).Error; err != nil {
			c.JSON(404, gin.H{"code": 404, "msg": "Site not found"})
			return
		}
		if s.TLSEmail == "" {
			c.JSON(400, gin.H{"code": 400, "msg": "tls_email is required for ACME certificate"})
			return
		}
		certPath, keyPath, err := cert.ObtainCert(s.Domain, s.TLSEmail)
		if err != nil {
			c.JSON(500, gin.H{"code": 500, "msg": fmt.Sprintf("ACME failed: %v", err)})
			return
		}
		s.CertPath = certPath
		s.KeyPath = keyPath
		s.TLSMode = "custom"
		config.DB.Save(&s)
		go webserverReload()
		c.JSON(200, gin.H{"code": 200, "msg": "Certificate issued", "data": gin.H{
			"cert_path": certPath, "key_path": keyPath,
		}})
	})

	// Smart Deploy — preset-driven one-click egress with reversible
	// tuning. See docs/superpowers/specs/2026-04-21-smart-deploy-design.md.
	RegisterDeployRoutes(authGroup, apply...)
}
