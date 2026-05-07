package main

import (
	"context"
	cryptotls "crypto/tls"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/api"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/config"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/core/setup"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/docker"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/model"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/pkg/jwtutil"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/service/notify"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/service/proxy"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/service/scheduler"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/service/webserver"
)

func main() {
	// Initialize the application
	log.Println("ZenithPanel server starting...")

	// 1. Initialize Database (store in data/ so Docker volumes persist it across updates)
	dbPath := "data/zenith.db"
	os.MkdirAll("data", 0700)
	// Migrate old file locations if they exist (pre-v1.1 stored them outside data/)
	migrateFile := func(oldPath, newPath string) {
		if _, err := os.Stat(oldPath); err == nil {
			if _, err := os.Stat(newPath); os.IsNotExist(err) {
				log.Printf("Migrating %s -> %s", oldPath, newPath)
				if err := os.Rename(oldPath, newPath); err != nil {
					if data, err := os.ReadFile(oldPath); err == nil {
						os.WriteFile(newPath, data, 0600)
					}
				}
			}
		}
	}
	migrateFile("zenith.db", dbPath)
	migrateFile("xray_config.json", "data/xray_config.json")
	migrateFile("/opt/zenithpanel/xray_config.json", "data/xray_config.json")
	config.InitDB(dbPath)
	if removed, err := proxy.CleanupDuplicateRoutingRules(); err != nil {
		log.Printf("Warning: Failed to clean duplicate routing rules: %v", err)
	} else if removed > 0 {
		log.Printf("Cleaned up %d duplicate routing rule(s)", removed)
	}

	// 2. Initialize JWT Secret from persistent storage
	secret := config.EnsureJWTSecret()
	jwtutil.InitSecret(secret)

	// 3. Execute Setup Initialization (check persistent state)
	setup.InitSetup()

	// 4. Initialize Managers
	dm, err := docker.NewManager()
	if err != nil {
		log.Printf("Warning: Docker manager init failed: %v", err)
	}
	xm := proxy.NewXrayManager()
	sm := proxy.NewSingboxManager()

	var enabledInboundCount int64
	if err := config.DB.Model(&model.Inbound{}).Where("enable = ?", true).Count(&enabledInboundCount).Error; err != nil {
		log.Printf("Warning: Failed to count enabled inbounds: %v", err)
	} else if enabledInboundCount > 0 {
		if err := xm.Start(); err != nil {
			log.Printf("Warning: Failed to auto-start Xray: %v", err)
		} else {
			log.Printf("Xray auto-started with %d enabled inbound(s)", enabledInboundCount)
		}
	}

	// 5. Initialize Cron Scheduler
	sched := scheduler.NewScheduler()
	if err := sched.LoadFromDB(); err != nil {
		log.Printf("Warning: Failed to load cron jobs: %v", err)
	}
	sched.Start()

	// 5a. Initialize built-in web server (Sites / reverse proxy)
	webserver.Init(config.DB)
	if err := webserver.Get().Start(); err != nil {
		log.Printf("Warning: built-in web server failed to start: %v", err)
	}

	// 5b. Start background notification checker (every 6 hours)
	go func() {
		ticker := time.NewTicker(6 * time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			notify.RunClientChecks(config.DB)
		}
	}()

	// 6. Create a new Gin router.
	// Release mode disables per-request debug printing; a custom logger skips
	// hot paths (monitor polling, subscription fetches) to keep stdout quiet on
	// low-spec VPS where disk I/O from logs is noticeable.
	if os.Getenv("GIN_MODE") == "" {
		gin.SetMode(gin.ReleaseMode)
	}
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(gin.LoggerWithConfig(gin.LoggerConfig{
		SkipPaths: []string{
			"/api/v1/system/monitor",
			"/api/v1/proxy/status",
		},
		Formatter: func(p gin.LogFormatterParams) string {
			// Skip subscription endpoint access logs (path starts with /api/v1/sub/)
			if strings.HasPrefix(p.Path, "/api/v1/sub/") {
				return ""
			}
			return fmt.Sprintf("[%s] %3d | %6v | %s %s\n",
				p.TimeStamp.Format("2006-01-02 15:04:05"),
				p.StatusCode, p.Latency, p.Method, p.Path)
		},
	}))

	// 7. Setup API routes
	api.SetupRoutes(r, dm, xm, sm, sched)

	// Resolve listen port (random on first run, persisted in DB)
	port := config.EnsurePort()

	// Print setup wizard URL now that port is known
	setup.PrintSetupIfPending(port)

	// Define HTTP Server
	srv := &http.Server{
		Addr:    ":" + port,
		Handler: r,
	}

	// 8. Run the server in a goroutine so it doesn't block
	go func() {
		certPath := config.GetSetting("tls_cert_path")
		keyPath := config.GetSetting("tls_key_path")
		if certPath != "" && keyPath != "" {
			// Verify cert files exist and are valid before attempting TLS
			certData, certErr := os.ReadFile(certPath)
			keyData, keyErr := os.ReadFile(keyPath)
			if certErr != nil || keyErr != nil {
				log.Printf("TLS cert/key files not found (cert: %v, key: %v), falling back to HTTP...", certErr, keyErr)
				config.SetSetting("tls_cert_path", "")
				config.SetSetting("tls_key_path", "")
			} else if _, err := cryptotls.X509KeyPair(certData, keyData); err != nil {
				log.Printf("TLS cert/key invalid (%v), falling back to HTTP...", err)
				config.SetSetting("tls_cert_path", "")
				config.SetSetting("tls_key_path", "")
			} else {
				log.Printf("ZenithPanel listening on https://0.0.0.0:%s", port)
				if err := srv.ListenAndServeTLS(certPath, keyPath); err != nil && err != http.ErrServerClosed {
					log.Printf("TLS listen failed (%v), falling back to HTTP...", err)
				} else {
					return
				}
				// Create a fresh server for HTTP fallback
				srv = &http.Server{Addr: ":" + port, Handler: r}
			}
		}
		log.Printf("ZenithPanel listening on http://0.0.0.0:%s", port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Failed to start server: %v", err)
		}
	}()

	// 8. Graceful Shutdown: Wait for interrupt signal
	quit := make(chan os.Signal, 1)
	// kill (no param) default send syscall.SIGTERM
	// kill -2 is syscall.SIGINT
	// kill -9 is syscall.SIGKILL but can't be caught, so don't need to add it
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("Shutting down server...")
	sched.Stop()

	// The context is used to inform the server it has 5 seconds to finish
	// the request it is currently handling
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Fatal("Server forced to shutdown:", err)
	}

	log.Println("Server exiting")
}
