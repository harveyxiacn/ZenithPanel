package api

import (
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-contrib/cors"

	"github.com/gin-gonic/gin"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/api/middleware"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/config"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/docker"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/model"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/pkg/jwtutil"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/service/cert"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/service/diagnostic"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/service/fs"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/service/monitor"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/service/proxy"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/service/sub"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/service/terminal"
	"golang.org/x/crypto/bcrypt"
)

// fsSandboxRoot is the allowed root for file operations.
// In production this could be configurable.
var fsSandboxRoot = "/home"

// isPathSafe ensures the resolved path stays within the sandbox root
func isPathSafe(userPath string) (string, bool) {
	cleaned := filepath.Clean(userPath)
	abs, err := filepath.Abs(cleaned)
	if err != nil {
		return "", false
	}
	return abs, strings.HasPrefix(abs, fsSandboxRoot)
}

// SetupRoutes configures all the Gin routes.
func SetupRoutes(r *gin.Engine, dm *docker.Manager, xm *proxy.XrayManager, sm *proxy.SingboxManager) {
	
	// Apply Setup Guard Globally
	r.Use(middleware.SetupGuardMiddleware())

	// CORS Middleware
	r.Use(cors.New(cors.Config{
		AllowOrigins:     []string{"*"},
		AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Accept", "Authorization"},
		ExposeHeaders:    []string{"Content-Length"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	}))

	// Embedded Static Files
	staticFS := GetStaticAssets()
	r.StaticFS("/assets", staticFS)
	
	// Separate handling for index.html at root and for SPA routes
	r.NoRoute(func(c *gin.Context) {
		if strings.HasPrefix(c.Request.URL.Path, "/api") {
			c.JSON(http.StatusNotFound, gin.H{"code": 404, "msg": "Endpoint not found"})
			return
		}
		c.FileFromFS("/", staticFS)
	})

	// ======================================
	// Setup Wizard APIs
	// ======================================
	setupGroup := r.Group("/api/setup")
	{
		setupGroup.POST("/login", func(c *gin.Context) {
			var req struct {
				Password string `json:"password"`
			}
			if err := c.ShouldBindJSON(&req); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "Invalid parameters"})
				return
			}
			cfg := config.GetConfig()
			if req.Password != cfg.SetupOneTimeToken {
				c.JSON(http.StatusUnauthorized, gin.H{"code": 401, "msg": "Invalid one-time password"})
				return
			}
			
			// Issue real JWT for setup process
			token, err := jwtutil.GenerateToken("setup-admin", "admin", time.Minute*30)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "Failed to generate token"})
				return
			}

			c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "Setup login successful", "data": gin.H{"token": token}})
		})

		setupGroup.POST("/complete", middleware.JWTAuthMiddleware(), func(c *gin.Context) {
			var req struct {
				Username string `json:"username"`
				Password string `json:"password"`
			}
			if err := c.ShouldBindJSON(&req); err != nil || req.Username == "" || req.Password == "" {
				c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "Username and password are required"})
				return
			}

			// Hash password with bcrypt
			hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "Failed to hash password"})
				return
			}

			// Create admin user in DB
			admin := model.AdminUser{
				Username:     req.Username,
				PasswordHash: string(hash),
			}
			if err := config.DB.Create(&admin).Error; err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "Failed to create admin user: " + err.Error()})
				return
			}

			// Mark setup as complete in persistent storage
			if err := config.MarkSetupDone(); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "Failed to persist setup state"})
				return
			}
			cfg := config.GetConfig()
			cfg.IsSetupComplete = true

			c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "Setup complete! You can now login with your credentials."})
		})
	}

	// ======================================
	// Public API (Post-Setup) - Unprotected
	// ======================================
	apiGroup := r.Group("/api/v1")
	{
		apiGroup.GET("/ping", func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "pong"})
		})

		apiGroup.POST("/login", func(c *gin.Context) {
			var req struct {
				Username string `json:"username"`
				Password string `json:"password"`
			}
			if err := c.ShouldBindJSON(&req); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "Invalid parameters"})
				return
			}

			// Find admin user in DB
			var admin model.AdminUser
			if err := config.DB.Where("username = ?", req.Username).First(&admin).Error; err != nil {
				c.JSON(http.StatusUnauthorized, gin.H{"code": 401, "msg": "Invalid username or password"})
				return
			}

			// Verify bcrypt password
			if err := bcrypt.CompareHashAndPassword([]byte(admin.PasswordHash), []byte(req.Password)); err != nil {
				c.JSON(http.StatusUnauthorized, gin.H{"code": 401, "msg": "Invalid username or password"})
				return
			}

			token, err := jwtutil.GenerateToken(
				strconv.Itoa(int(admin.ID)),
				admin.Username,
				time.Hour*24,
			)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "Failed to generate token"})
				return
			}
			c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "Login success", "data": gin.H{"token": token}})
		})

		// Subscription endpoint (Unprotected, uses UUID parameter)
		apiGroup.GET("/sub/:uuid", sub.GenerateSubscription)
	}

	// ======================================
	// Protected API (Post-Setup) - Needs JWT
	// ======================================
	authGroup := r.Group("/api/v1")
	authGroup.Use(middleware.JWTAuthMiddleware())
	{
		authGroup.GET("/system/monitor", func(c *gin.Context) {
			stats, err := monitor.GetSystemStats()
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "Failed to get system stats"})
				return
			}
			c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "Success", "data": stats})
		})

		// Docker Management
		authGroup.GET("/docker/containers", func(c *gin.Context) {
			if dm == nil {
				c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "Docker engine not reachable"})
				return
			}
			containers, err := dm.ListContainers(c.Request.Context(), true)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": err.Error()})
				return
			}
			c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "Success", "data": containers})
		})

		// Terminal WebSocket
		authGroup.GET("/terminal", terminal.HandleTerminalWebSocket)

		// File System Management (with sandbox protection)
		fsGroup := authGroup.Group("/fs")
		{
			fsGroup.GET("/list", func(c *gin.Context) {
				path := c.Query("path")
				if path == "" {
					path = fsSandboxRoot
				}
				safePath, ok := isPathSafe(path)
				if !ok {
					c.JSON(http.StatusForbidden, gin.H{"code": 403, "msg": "Access denied: path outside sandbox"})
					return
				}
				files, err := fs.ListDirectory(safePath)
				if err != nil {
					c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": err.Error()})
					return
				}
				c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "Success", "data": files})
			})

			fsGroup.GET("/read", func(c *gin.Context) {
				path := c.Query("path")
				safePath, ok := isPathSafe(path)
				if !ok {
					c.JSON(http.StatusForbidden, gin.H{"code": 403, "msg": "Access denied: path outside sandbox"})
					return
				}
				content, err := fs.ReadFileContent(safePath)
				if err != nil {
					c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": err.Error()})
					return
				}
				c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "Success", "data": content})
			})

			fsGroup.POST("/write", func(c *gin.Context) {
				var req struct {
					Path    string `json:"path"`
					Content string `json:"content"`
				}
				if err := c.ShouldBindJSON(&req); err != nil {
					c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "Invalid params"})
					return
				}
				safePath, ok := isPathSafe(req.Path)
				if !ok {
					c.JSON(http.StatusForbidden, gin.H{"code": 403, "msg": "Access denied: path outside sandbox"})
					return
				}
				if err := fs.WriteFileContent(safePath, req.Content); err != nil {
					c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": err.Error()})
					return
				}
				c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "File saved"})
			})
		}

		// Diagnostics
		authGroup.GET("/diagnostics/network", func(c *gin.Context) {
			output, err := diagnostic.RunNetworkDiagnostic()
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": err.Error(), "data": output})
				return
			}
			c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "Success", "data": output})
		})

		// Cron Management Dummy Endpoints
		authGroup.GET("/cron/list", func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "Success", "data": []string{}})
		})

		// Firewall Management Dummy Endpoints
		authGroup.GET("/firewall/rules", func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "Success", "data": []string{}})
		})

		// Proxy Core Management
		proxyGroup := authGroup.Group("/proxy")
		{
			proxyGroup.GET("/config/xray", func(c *gin.Context) {
				cfg, err := xm.GenerateConfig()
				if err != nil {
					c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": err.Error()})
					return
				}
				c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "Success", "data": cfg})
			})

			proxyGroup.GET("/config/singbox", func(c *gin.Context) {
				cfg, err := sm.GenerateConfig()
				if err != nil {
					c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": err.Error()})
					return
				}
				c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "Success", "data": cfg})
			})

			// TLS Certificates Management
			proxyGroup.POST("/tls/issue", func(c *gin.Context) {
				var req struct {
					Domain string `json:"domain"`
					Email  string `json:"email"`
				}
				if err := c.ShouldBindJSON(&req); err != nil {
					c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "Invalid parameters"})
					return
				}

				if err := cert.IssueCertificate(req.Domain, req.Email); err != nil {
					c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "Failed to issue certificate: " + err.Error()})
					return
				}

				c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "Certificate issued successfully"})
			})
		}
	}
}
