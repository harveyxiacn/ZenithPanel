package main

import (
	"context"
	cryptotls "crypto/tls"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/api"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/cli"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/config"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/core/setup"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/docker"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/model"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/pkg/jwtutil"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/service/monitor"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/service/notify"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/service/proxy"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/service/scheduler"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/service/sub"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/service/traffic"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/service/webserver"
)

func main() {
	// CLI mode: when invoked as `zenithpanel ctl …` (or via the zenithctl
	// symlink), short-circuit to the headless command tree. The CLI never
	// touches DB, scheduler, or proxy managers — it only speaks HTTP.
	if shouldRunCLI() {
		os.Exit(cli.Run(cliArgs()))
	}

	// Initialize the application
	log.Println("ZenithPanel server starting...")

	// 1. Initialize Database (store in data/ so Docker volumes persist it across updates)
	dbPath := "data/zenith.db"
	if err := os.MkdirAll("data", 0700); err != nil {
		log.Fatalf("Failed to create data directory: %v", err)
	}
	// Migrate old file locations if they exist (pre-v1.1 stored them outside data/)
	migrateFile := func(oldPath, newPath string) {
		if _, err := os.Stat(oldPath); err == nil {
			if _, err := os.Stat(newPath); os.IsNotExist(err) {
				log.Printf("Migrating %s -> %s", oldPath, newPath)
				if err := os.Rename(oldPath, newPath); err != nil {
					if data, err := os.ReadFile(oldPath); err == nil {
						if werr := os.WriteFile(newPath, data, 0600); werr != nil {
							log.Printf("migrate: write %s: %v", newPath, werr)
						}
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

	// Auto-start both engines in dual mode so every enabled inbound is
	// reachable immediately after panel boot, regardless of which engine its
	// protocol needs. The partitioner decides whether each engine has work;
	// engines with nothing to do are left stopped.
	var enabled []model.Inbound
	if err := config.DB.Where("enable = ?", true).Find(&enabled).Error; err != nil {
		log.Printf("Warning: Failed to list enabled inbounds: %v", err)
	} else if len(enabled) > 0 {
		wantXray := false
		wantSingbox := false
		for _, in := range enabled {
			if proxy.IsXraySupported(in.Protocol) {
				wantXray = true
			} else {
				wantSingbox = true
			}
		}
		xm.SetDualMode(true)
		sm.SetDualMode(true)
		if wantXray {
			if err := xm.Start(); err != nil {
				log.Printf("Warning: Failed to auto-start Xray: %v", err)
			} else {
				log.Printf("Xray auto-started in dual mode")
			}
		}
		if wantSingbox {
			if err := sm.Start(); err != nil {
				log.Printf("Warning: Failed to auto-start Sing-box: %v", err)
			} else {
				log.Printf("Sing-box auto-started in dual mode")
			}
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
			notify.RunCertCheck(config.DB)
		}
	}()

	// 5c. Daily traffic reset goroutine (runs just after midnight local time).
	// Resets up_load/down_load for clients whose reset_day matches today.
	go func() {
		for {
			now := time.Now()
			next := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 1, 0, 0, now.Location())
			time.Sleep(time.Until(next))
			if affected := proxy.RunDailyTrafficReset(config.DB); affected > 0 {
				sub.InvalidateSubCache()
			}
		}
	}()

	// 5d. Hourly network-history persistence goroutine. Snapshots the current
	// network sample into the NetworkMetric table so the dashboard graph
	// survives panel restarts. Older-than-30-days rows are pruned in-place.
	go func() {
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			monitor.PersistHourlySnapshot(config.DB)
		}
	}()

	// 5e. Telegram bot lifecycle: watches the enable flag every 30s and
	// starts/stops the long-polling goroutine accordingly. Splitting the
	// lifecycle from the bot itself keeps the panel responsive to settings
	// changes without requiring a restart.
	go func() {
		var current *notify.BotPoller
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		// Run an initial check immediately so the bot starts without a 30s delay.
		check := func() {
			enabled := config.GetSetting("notify_telegram_bot_enabled") == "true"
			token := config.GetSetting("notify_telegram_token")
			chatID := config.GetSetting("notify_telegram_chat_id")
			if enabled && token != "" && chatID != "" {
				if current == nil {
					current = notify.NewBotPoller(token, chatID, config.DB, sub.InvalidateSubCache)
					current.Start()
				}
			} else if current != nil {
				current.Stop()
				current = nil
			}
		}
		check()
		for range ticker.C {
			check()
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
	// Promotes the unix-socket request-context marker into c.Set("trusted_local").
	// No-op for TCP requests; safe to mount globally.
	r.Use(api.TrustedLocalFromContext())
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

	// 6a. Traffic monitor — samples Clash API connections and OS sockets in a
	// background goroutine so the /traffic/live endpoint is just a memory read.
	trafficCtx, cancelTraffic := context.WithCancel(context.Background())
	defer cancelTraffic()
	tm := traffic.NewMonitor(sm)
	tm.Start(trafficCtx)

	// 6b. Traffic accountant — every 30 s, drain sing-box byte deltas (already
	// captured for the live-rate view) and query Xray's StatsService over the
	// internal API inbound; add both into Client.UpLoad/DownLoad so the
	// per-user cumulative columns reflect actually-flowed bytes across engines.
	tacct := traffic.NewAccountant(config.DB, xm, sm, tm.ProxyAggregator())
	tacct.Start(trafficCtx)

	// 7. Setup API routes
	api.SetupRoutes(r, dm, xm, sm, sched, tm)

	// Resolve listen port (random on first run, persisted in DB)
	port := config.EnsurePort()

	// Print setup wizard URL now that port is known
	setup.PrintSetupIfPending(port)

	// Define HTTP Server
	srv := &http.Server{
		Addr:    ":" + port,
		Handler: r,
	}

	// 7a. Unix domain socket listener for the in-host CLI (`zenithctl`). The
	// socket lives under /run/ (or $XDG_RUNTIME_DIR fallback) with mode 0600;
	// connections through it carry a request-context marker that the auth
	// middleware reads as `trusted_local`, so root on the host doesn't need a
	// token. Linux only — Windows and macOS skip this gracefully.
	if runtime.GOOS == "linux" {
		go startUnixSocketServer(r)
	}

	// 8. Run the server in a goroutine so it doesn't block
	go func() {
		certPath := config.GetSetting("tls_cert_path")
		keyPath := config.GetSetting("tls_key_path")
		if certPath != "" && keyPath != "" {
			// Verify cert files exist and are valid before attempting TLS
			certData, certErr := os.ReadFile(certPath)
			keyData, keyErr := os.ReadFile(keyPath)
			// On any kind of cert misconfiguration, clear the persisted paths
			// so the next boot starts on plain HTTP without retrying the same
			// broken cert. Logging the persistence failure is the most we can
			// do — losing the cert pointer is recoverable next boot.
			clearTLSPaths := func(reason string) {
				log.Printf("TLS clear reason: %s", reason)
				if err := config.SetSetting("tls_cert_path", ""); err != nil {
					log.Printf("clear tls_cert_path: %v", err)
				}
				if err := config.SetSetting("tls_key_path", ""); err != nil {
					log.Printf("clear tls_key_path: %v", err)
				}
			}
			if certErr != nil || keyErr != nil {
				clearTLSPaths(fmt.Sprintf("files not found (cert: %v, key: %v)", certErr, keyErr))
			} else if _, err := cryptotls.X509KeyPair(certData, keyData); err != nil {
				clearTLSPaths(fmt.Sprintf("invalid pair: %v", err))
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

	// Best-effort cleanup of the unix socket so the next start binds fresh.
	if runtime.GOOS == "linux" {
		_ = api.RemoveExistingSocket(api.LocalSocketPath)
	}

	log.Println("Server exiting")
}

// shouldRunCLI is true when argv signals headless mode:
//
//	zenithpanel ctl …          (subcommand on the main binary)
//	zenithctl                  (symlink — argv[0] basename equals "zenithctl")
func shouldRunCLI() bool {
	if base := filepathBase(os.Args[0]); base == "zenithctl" || base == "zenithctl.exe" {
		return true
	}
	return len(os.Args) >= 2 && os.Args[1] == "ctl"
}

// cliArgs strips the `ctl` subcommand when invoked through the main binary so
// the CLI parser sees a uniform argv regardless of entry point.
func cliArgs() []string {
	if len(os.Args) >= 2 && os.Args[1] == "ctl" {
		out := make([]string, 0, len(os.Args)-1)
		out = append(out, os.Args[0])
		out = append(out, os.Args[2:]...)
		return out
	}
	return os.Args
}

// filepathBase is a tiny path.Base() without an extra import.
func filepathBase(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' || p[i] == '\\' {
			return p[i+1:]
		}
	}
	return p
}

// startUnixSocketServer binds /run/zenithpanel.sock and serves the same gin
// engine wrapped in EngineWithLocalTrust. Errors are logged but never fatal —
// the panel keeps working on TCP if the socket can't be created (e.g. /run
// is read-only, or we're not root).
func startUnixSocketServer(engine http.Handler) {
	path := api.LocalSocketPath
	if err := api.RemoveExistingSocket(path); err != nil {
		log.Printf("unix socket: cleanup failed (%v); continuing", err)
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		// Fall back to $XDG_RUNTIME_DIR if /run isn't writable.
		if alt := os.Getenv("XDG_RUNTIME_DIR"); alt != "" {
			path = alt + "/zenithpanel.sock"
			_ = api.RemoveExistingSocket(path)
			if ln2, err2 := net.Listen("unix", path); err2 == nil {
				ln, err = ln2, nil
			}
		}
		if err != nil {
			log.Printf("unix socket: listen failed (%v); CLI on-host access unavailable", err)
			return
		}
	}
	if err := os.Chmod(path, 0600); err != nil {
		log.Printf("unix socket: chmod 0600 failed (%v); refusing to serve to avoid exposing it", err)
		ln.Close()
		return
	}
	log.Printf("Unix socket listening on %s", path)
	srv := &http.Server{Handler: api.EngineWithLocalTrust(engine)}
	if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
		log.Printf("unix socket: serve ended (%v)", err)
	}
}
