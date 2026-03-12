package api

import (
	"crypto/rand"
	"fmt"
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
	"github.com/harveyxiacn/ZenithPanel/backend/internal/service/firewall"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/service/fs"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/service/monitor"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/service/proxy"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/service/scheduler"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/service/sub"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/service/terminal"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/time/rate"
)

// fsSandboxRoot is the allowed root for file operations.
var fsSandboxRoot = "/home"

// loginLimiter restricts login attempts to 5 per second
var loginLimiter = rate.NewLimiter(rate.Every(time.Second), 5)

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
func SetupRoutes(r *gin.Engine, dm *docker.Manager, xm *proxy.XrayManager, sm *proxy.SingboxManager, sched *scheduler.Scheduler) {

	// Security Headers
	r.Use(func(c *gin.Context) {
		c.Header("X-Frame-Options", "DENY")
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-XSS-Protection", "1; mode=block")
		c.Header("Referrer-Policy", "strict-origin-when-cross-origin")
		c.Next()
	})

	// Apply Setup Guard Globally
	r.Use(middleware.SetupGuardMiddleware())

	// CORS Middleware
	r.Use(cors.New(cors.Config{
		AllowOrigins:     []string{"*"},
		AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Accept", "Authorization"},
		ExposeHeaders:    []string{"Content-Length"},
		AllowCredentials: false,
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
			// Rate limiting
			if !loginLimiter.Allow() {
				c.JSON(http.StatusTooManyRequests, gin.H{"code": 429, "msg": "Too many login attempts, please try again later"})
				return
			}

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
				c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": err.Error()})
				return
			}
			c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "Success", "data": containers})
		})

		authGroup.POST("/docker/containers/:id/start", func(c *gin.Context) {
			if dm == nil {
				c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "Docker not available"})
				return
			}
			if err := dm.StartContainer(c.Request.Context(), c.Param("id")); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": err.Error()})
				return
			}
			c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "Container started"})
		})

		authGroup.POST("/docker/containers/:id/stop", func(c *gin.Context) {
			if dm == nil {
				c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "Docker not available"})
				return
			}
			if err := dm.StopContainer(c.Request.Context(), c.Param("id")); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": err.Error()})
				return
			}
			c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "Container stopped"})
		})

		authGroup.POST("/docker/containers/:id/restart", func(c *gin.Context) {
			if dm == nil {
				c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "Docker not available"})
				return
			}
			if err := dm.RestartContainer(c.Request.Context(), c.Param("id")); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": err.Error()})
				return
			}
			c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "Container restarted"})
		})

		authGroup.DELETE("/docker/containers/:id", func(c *gin.Context) {
			if dm == nil {
				c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "Docker not available"})
				return
			}
			force := c.Query("force") == "true"
			if err := dm.RemoveContainer(c.Request.Context(), c.Param("id"), force); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": err.Error()})
				return
			}
			c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "Container removed"})
		})

		// Terminal WebSocket
		authGroup.GET("/terminal", terminal.HandleTerminalWebSocket)

		// ======================================
		// File System Management
		// ======================================
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

		// ======================================
		// Inbound CRUD
		// ======================================
		authGroup.GET("/inbounds", func(c *gin.Context) {
			var inbounds []model.Inbound
			if err := config.DB.Find(&inbounds).Error; err != nil {
				c.JSON(500, gin.H{"code": 500, "msg": err.Error()})
				return
			}
			c.JSON(200, gin.H{"code": 200, "msg": "Success", "data": inbounds})
		})

		authGroup.POST("/inbounds", func(c *gin.Context) {
			var inbound model.Inbound
			if err := c.ShouldBindJSON(&inbound); err != nil {
				c.JSON(400, gin.H{"code": 400, "msg": "Invalid parameters"})
				return
			}
			if err := config.DB.Create(&inbound).Error; err != nil {
				c.JSON(500, gin.H{"code": 500, "msg": err.Error()})
				return
			}
			c.JSON(200, gin.H{"code": 200, "msg": "Created", "data": inbound})
		})

		authGroup.PUT("/inbounds/:id", func(c *gin.Context) {
			id := c.Param("id")
			var inbound model.Inbound
			if err := config.DB.First(&inbound, id).Error; err != nil {
				c.JSON(404, gin.H{"code": 404, "msg": "Inbound not found"})
				return
			}
			if err := c.ShouldBindJSON(&inbound); err != nil {
				c.JSON(400, gin.H{"code": 400, "msg": "Invalid parameters"})
				return
			}
			if err := config.DB.Save(&inbound).Error; err != nil {
				c.JSON(500, gin.H{"code": 500, "msg": err.Error()})
				return
			}
			c.JSON(200, gin.H{"code": 200, "msg": "Updated", "data": inbound})
		})

		authGroup.DELETE("/inbounds/:id", func(c *gin.Context) {
			id := c.Param("id")
			if err := config.DB.Delete(&model.Inbound{}, id).Error; err != nil {
				c.JSON(500, gin.H{"code": 500, "msg": err.Error()})
				return
			}
			c.JSON(200, gin.H{"code": 200, "msg": "Deleted"})
		})

		// ======================================
		// Client CRUD
		// ======================================
		authGroup.GET("/clients", func(c *gin.Context) {
			var clients []model.Client
			query := config.DB
			if inboundID := c.Query("inbound_id"); inboundID != "" {
				query = query.Where("inbound_id = ?", inboundID)
			}
			if err := query.Find(&clients).Error; err != nil {
				c.JSON(500, gin.H{"code": 500, "msg": err.Error()})
				return
			}
			c.JSON(200, gin.H{"code": 200, "msg": "Success", "data": clients})
		})

		authGroup.POST("/clients", func(c *gin.Context) {
			var client model.Client
			if err := c.ShouldBindJSON(&client); err != nil {
				c.JSON(400, gin.H{"code": 400, "msg": "Invalid parameters"})
				return
			}
			// Auto-generate UUID if not provided
			if client.UUID == "" {
				b := make([]byte, 16)
				rand.Read(b)
				client.UUID = fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
					b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
			}
			if err := config.DB.Create(&client).Error; err != nil {
				c.JSON(500, gin.H{"code": 500, "msg": err.Error()})
				return
			}
			c.JSON(200, gin.H{"code": 200, "msg": "Created", "data": client})
		})

		authGroup.PUT("/clients/:id", func(c *gin.Context) {
			id := c.Param("id")
			var client model.Client
			if err := config.DB.First(&client, id).Error; err != nil {
				c.JSON(404, gin.H{"code": 404, "msg": "Client not found"})
				return
			}
			if err := c.ShouldBindJSON(&client); err != nil {
				c.JSON(400, gin.H{"code": 400, "msg": "Invalid parameters"})
				return
			}
			if err := config.DB.Save(&client).Error; err != nil {
				c.JSON(500, gin.H{"code": 500, "msg": err.Error()})
				return
			}
			c.JSON(200, gin.H{"code": 200, "msg": "Updated", "data": client})
		})

		authGroup.DELETE("/clients/:id", func(c *gin.Context) {
			id := c.Param("id")
			if err := config.DB.Delete(&model.Client{}, id).Error; err != nil {
				c.JSON(500, gin.H{"code": 500, "msg": err.Error()})
				return
			}
			c.JSON(200, gin.H{"code": 200, "msg": "Deleted"})
		})

		// ======================================
		// Routing Rule CRUD
		// ======================================
		authGroup.GET("/routing-rules", func(c *gin.Context) {
			var rules []model.RoutingRule
			if err := config.DB.Find(&rules).Error; err != nil {
				c.JSON(500, gin.H{"code": 500, "msg": err.Error()})
				return
			}
			c.JSON(200, gin.H{"code": 200, "msg": "Success", "data": rules})
		})

		authGroup.POST("/routing-rules", func(c *gin.Context) {
			var rule model.RoutingRule
			if err := c.ShouldBindJSON(&rule); err != nil {
				c.JSON(400, gin.H{"code": 400, "msg": "Invalid parameters"})
				return
			}
			if err := config.DB.Create(&rule).Error; err != nil {
				c.JSON(500, gin.H{"code": 500, "msg": err.Error()})
				return
			}
			c.JSON(200, gin.H{"code": 200, "msg": "Created", "data": rule})
		})

		authGroup.PUT("/routing-rules/:id", func(c *gin.Context) {
			id := c.Param("id")
			var rule model.RoutingRule
			if err := config.DB.First(&rule, id).Error; err != nil {
				c.JSON(404, gin.H{"code": 404, "msg": "Routing rule not found"})
				return
			}
			if err := c.ShouldBindJSON(&rule); err != nil {
				c.JSON(400, gin.H{"code": 400, "msg": "Invalid parameters"})
				return
			}
			if err := config.DB.Save(&rule).Error; err != nil {
				c.JSON(500, gin.H{"code": 500, "msg": err.Error()})
				return
			}
			c.JSON(200, gin.H{"code": 200, "msg": "Updated", "data": rule})
		})

		authGroup.DELETE("/routing-rules/:id", func(c *gin.Context) {
			id := c.Param("id")
			if err := config.DB.Delete(&model.RoutingRule{}, id).Error; err != nil {
				c.JSON(500, gin.H{"code": 500, "msg": err.Error()})
				return
			}
			c.JSON(200, gin.H{"code": 200, "msg": "Deleted"})
		})

		// ======================================
		// Firewall Management
		// ======================================
		authGroup.GET("/firewall/rules", func(c *gin.Context) {
			rules, err := firewall.ListRules()
			if err != nil {
				c.JSON(500, gin.H{"code": 500, "msg": err.Error()})
				return
			}
			c.JSON(200, gin.H{"code": 200, "msg": "Success", "data": rules})
		})

		authGroup.POST("/firewall/rules", func(c *gin.Context) {
			var req struct {
				Protocol string `json:"protocol"`
				Port     string `json:"port"`
				Action   string `json:"action"`
				Source   string `json:"source"`
				Comment  string `json:"comment"`
			}
			if err := c.ShouldBindJSON(&req); err != nil {
				c.JSON(400, gin.H{"code": 400, "msg": "Invalid parameters"})
				return
			}
			if err := firewall.AddRule(req.Protocol, req.Port, req.Action, req.Source, req.Comment); err != nil {
				c.JSON(500, gin.H{"code": 500, "msg": err.Error()})
				return
			}
			c.JSON(200, gin.H{"code": 200, "msg": "Rule added"})
		})

		authGroup.DELETE("/firewall/rules", func(c *gin.Context) {
			var req struct {
				Num string `json:"num"`
			}
			if err := c.ShouldBindJSON(&req); err != nil {
				c.JSON(400, gin.H{"code": 400, "msg": "Invalid parameters"})
				return
			}
			if err := firewall.DeleteRule(req.Num); err != nil {
				c.JSON(500, gin.H{"code": 500, "msg": err.Error()})
				return
			}
			c.JSON(200, gin.H{"code": 200, "msg": "Rule deleted"})
		})

		// ======================================
		// Cron Job Management
		// ======================================
		authGroup.GET("/cron/jobs", func(c *gin.Context) {
			jobs, err := sched.ListJobs()
			if err != nil {
				c.JSON(500, gin.H{"code": 500, "msg": err.Error()})
				return
			}
			c.JSON(200, gin.H{"code": 200, "msg": "Success", "data": jobs})
		})

		authGroup.POST("/cron/jobs", func(c *gin.Context) {
			var job model.CronJob
			if err := c.ShouldBindJSON(&job); err != nil {
				c.JSON(400, gin.H{"code": 400, "msg": "Invalid parameters"})
				return
			}
			id, err := sched.AddJob(job)
			if err != nil {
				c.JSON(500, gin.H{"code": 500, "msg": err.Error()})
				return
			}
			job.ID = id
			c.JSON(200, gin.H{"code": 200, "msg": "Job created", "data": job})
		})

		authGroup.DELETE("/cron/jobs/:id", func(c *gin.Context) {
			id := c.Param("id")
			var jobID uint
			fmt.Sscanf(id, "%d", &jobID)
			if err := sched.RemoveJob(jobID); err != nil {
				c.JSON(500, gin.H{"code": 500, "msg": err.Error()})
				return
			}
			c.JSON(200, gin.H{"code": 200, "msg": "Job deleted"})
		})

		// ======================================
		// Proxy Core Management
		// ======================================
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
