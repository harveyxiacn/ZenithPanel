package api

import (
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/service/fs"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/service/terminal"
)

// registerTerminalFileRoutes owns the two host-level interactive surfaces.
// Scope authorization remains on the parent group: terminal tickets require
// the terminal scope, while file operations require infra read/write scopes.
func registerTerminalFileRoutes(authGroup *gin.RouterGroup) {
	authGroup.POST("/terminal/ticket", func(c *gin.Context) {
		ticket, expires, err := terminal.IssueTicket(c.GetString("principal"), c.ClientIP())
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "Failed to issue terminal ticket"})
			return
		}
		c.Header("Cache-Control", "no-store")
		c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "Success", "data": gin.H{
			"ticket":     ticket,
			"expires_at": expires.UTC().Format(time.RFC3339),
		}})
	})

	fsGroup := authGroup.Group("/fs")
	fsGroup.GET("/list", func(c *gin.Context) {
		path := c.Query("path")
		if path == "" {
			path = "/"
		}
		safePath, ok := isPathSafe(path)
		if !ok {
			c.JSON(http.StatusForbidden, gin.H{"code": 403, "msg": "Access denied: path outside sandbox"})
			return
		}
		files, err := fs.ListDirectory(safePath)
		if err != nil {
			log.Printf("FS list error: %v", err)
			c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "Failed to list directory"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "Success", "data": files})
	})

	fsGroup.GET("/read", func(c *gin.Context) {
		safePath, ok := isPathSafe(c.Query("path"))
		if !ok {
			c.JSON(http.StatusForbidden, gin.H{"code": 403, "msg": "Access denied: path outside sandbox"})
			return
		}
		content, err := fs.ReadFileContent(safePath)
		if err != nil {
			log.Printf("FS read error: %v", err)
			c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "Failed to read file"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "Success", "data": content})
	})

	fsGroup.POST("/write", func(c *gin.Context) {
		var request struct {
			Path    string `json:"path"`
			Content string `json:"content"`
		}
		if err := c.ShouldBindJSON(&request); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "Invalid params"})
			return
		}
		safePath, ok := isPathSafe(request.Path)
		if !ok {
			c.JSON(http.StatusForbidden, gin.H{"code": 403, "msg": "Access denied: path outside sandbox"})
			return
		}
		if err := fs.WriteFileContent(safePath, request.Content); err != nil {
			log.Printf("FS write error: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "Failed to write file"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "File saved"})
	})
}
