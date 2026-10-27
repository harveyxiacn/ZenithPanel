package api

import (
	"io"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/docker"
)

func registerDockerRoutes(authGroup *gin.RouterGroup, dm *docker.Manager) {
	// ======================================
	// Docker Management
	// ======================================
	authGroup.GET("/docker/containers", func(c *gin.Context) {
		if dm == nil {
			c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "Docker engine not reachable"})
			return
		}
		containers, err := dm.ListContainers(c.Request.Context(), true)
		if err != nil {
			log.Printf("Docker list error: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "Failed to list containers"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "Success", "data": containers})
	})

	authGroup.POST("/docker/containers/:id/start", func(c *gin.Context) {
		if dm == nil {
			c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "Docker not available"})
			return
		}
		id := c.Param("id")
		if !isValidContainerID(id) {
			c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "Invalid container ID"})
			return
		}
		if err := dm.StartContainer(c.Request.Context(), id); err != nil {
			log.Printf("Docker start error: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "Failed to start container"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "Container started"})
	})

	authGroup.POST("/docker/containers/:id/stop", func(c *gin.Context) {
		if dm == nil {
			c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "Docker not available"})
			return
		}
		id := c.Param("id")
		if !isValidContainerID(id) {
			c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "Invalid container ID"})
			return
		}
		if err := dm.StopContainer(c.Request.Context(), id); err != nil {
			log.Printf("Docker stop error: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "Failed to stop container"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "Container stopped"})
	})

	authGroup.POST("/docker/containers/:id/restart", func(c *gin.Context) {
		if dm == nil {
			c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "Docker not available"})
			return
		}
		id := c.Param("id")
		if !isValidContainerID(id) {
			c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "Invalid container ID"})
			return
		}
		if err := dm.RestartContainer(c.Request.Context(), id); err != nil {
			log.Printf("Docker restart error: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "Failed to restart container"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "Container restarted"})
	})

	authGroup.DELETE("/docker/containers/:id", func(c *gin.Context) {
		if dm == nil {
			c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "Docker not available"})
			return
		}
		id := c.Param("id")
		if !isValidContainerID(id) {
			c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "Invalid container ID"})
			return
		}
		force := c.Query("force") == "true"
		if err := dm.RemoveContainer(c.Request.Context(), id, force); err != nil {
			log.Printf("Docker remove error: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "Failed to remove container"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "Container removed"})
	})

	// Container logs
	authGroup.GET("/docker/containers/:id/logs", func(c *gin.Context) {
		if dm == nil {
			c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "Docker not available"})
			return
		}
		id := c.Param("id")
		if !isValidContainerID(id) {
			c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "Invalid container ID"})
			return
		}
		tail := c.DefaultQuery("tail", "100")
		output, err := dm.GetContainerLogs(c.Request.Context(), id, tail)
		if err != nil {
			log.Printf("Docker logs error: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "Failed to get logs"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "Success", "data": output})
	})

	// Container resource stats (one-shot)
	authGroup.GET("/docker/containers/:id/stats", func(c *gin.Context) {
		if dm == nil {
			c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "Docker not available"})
			return
		}
		id := c.Param("id")
		if !isValidContainerID(id) {
			c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "Invalid container ID"})
			return
		}
		stats, err := dm.GetContainerStats(c.Request.Context(), id)
		if err != nil {
			log.Printf("Docker stats error: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "Failed to get stats"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "Success", "data": stats})
	})

	// Container inspect
	authGroup.GET("/docker/containers/:id/inspect", func(c *gin.Context) {
		if dm == nil {
			c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "Docker not available"})
			return
		}
		id := c.Param("id")
		if !isValidContainerID(id) {
			c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "Invalid container ID"})
			return
		}
		info, err := dm.InspectContainer(c.Request.Context(), id)
		if err != nil {
			log.Printf("Docker inspect error: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "Failed to inspect container"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "Success", "data": info})
	})

	// Create and start a new container
	authGroup.POST("/docker/containers/run", func(c *gin.Context) {
		if dm == nil {
			c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "Docker not available"})
			return
		}
		var req docker.RunContainerRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "Invalid request"})
			return
		}
		if req.Image == "" {
			c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "image is required"})
			return
		}
		id, err := dm.RunContainer(c.Request.Context(), req)
		if err != nil {
			log.Printf("Docker run error: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "Container started", "data": gin.H{"id": id}})
	})

	// Image list
	authGroup.GET("/docker/images", func(c *gin.Context) {
		if dm == nil {
			c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "Docker not available"})
			return
		}
		images, err := dm.ListImages(c.Request.Context())
		if err != nil {
			log.Printf("Docker image list error: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "Failed to list images"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "Success", "data": images})
	})

	// Pull image (synchronous; returns after pull completes)
	authGroup.POST("/docker/images/pull", func(c *gin.Context) {
		if dm == nil {
			c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "Docker not available"})
			return
		}
		var body struct {
			Image string `json:"image"`
		}
		if err := c.ShouldBindJSON(&body); err != nil || body.Image == "" {
			c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "image field required"})
			return
		}
		rc, err := dm.PullImage(c.Request.Context(), body.Image)
		if err != nil {
			log.Printf("Docker pull error: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": err.Error()})
			return
		}
		io.Copy(io.Discard, rc)
		rc.Close()
		c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "Image pulled"})
	})

	// Remove image
	authGroup.DELETE("/docker/images/:id", func(c *gin.Context) {
		if dm == nil {
			c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "Docker not available"})
			return
		}
		id := c.Param("id")
		force := c.Query("force") == "true"
		deleted, err := dm.RemoveImage(c.Request.Context(), id, force)
		if err != nil {
			log.Printf("Docker image remove error: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "Image removed", "data": deleted})
	})

	// Volume list
	authGroup.GET("/docker/volumes", func(c *gin.Context) {
		if dm == nil {
			c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "Docker not available"})
			return
		}
		vols, err := dm.ListVolumes(c.Request.Context())
		if err != nil {
			log.Printf("Docker volume list error: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "Failed to list volumes"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "Success", "data": vols})
	})

	// Network list
	authGroup.GET("/docker/networks", func(c *gin.Context) {
		if dm == nil {
			c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "Docker not available"})
			return
		}
		nets, err := dm.ListNetworks(c.Request.Context())
		if err != nil {
			log.Printf("Docker network list error: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "Failed to list networks"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "Success", "data": nets})
	})

}
