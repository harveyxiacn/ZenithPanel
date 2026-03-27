package api

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/subtle"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image/png"
	"io"
	"log"
	"net/http"
	"os"

	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
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
	sysopt "github.com/harveyxiacn/ZenithPanel/backend/internal/service/system"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/service/terminal"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/service/updater"
	"github.com/pquerna/otp/totp"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/time/rate"
	"gorm.io/gorm"
)

// fsSandboxRoot restricts file manager operations to /home to prevent
// unauthorized access to system files, credentials, and configs.
var fsSandboxRoot = "/home"

// ipRateLimiters provides per-IP rate limiting to prevent brute-force attacks.
// Each IP gets its own limiter (max 5 req/sec for auth endpoints).
var ipRateLimiters = struct {
	sync.RWMutex
	m map[string]*rate.Limiter
}{m: make(map[string]*rate.Limiter)}

func getIPLimiter(ip string) *rate.Limiter {
	ipRateLimiters.RLock()
	limiter, exists := ipRateLimiters.m[ip]
	ipRateLimiters.RUnlock()
	if exists {
		return limiter
	}
	ipRateLimiters.Lock()
	limiter = rate.NewLimiter(rate.Every(time.Second), 5)
	ipRateLimiters.m[ip] = limiter
	ipRateLimiters.Unlock()
	return limiter
}

// subRateLimiters provides per-IP rate limiting for subscription endpoints.
// Burst 3, refill 1 every 6 seconds (~10/min per IP).
var subRateLimiters = &sync.Map{}

func getSubLimiter(ip string) *rate.Limiter {
	if v, ok := subRateLimiters.Load(ip); ok {
		return v.(*rate.Limiter)
	}
	l := rate.NewLimiter(rate.Every(6*time.Second), 3)
	subRateLimiters.Store(ip, l)
	return l
}

// loginFailures tracks consecutive login failures per IP for lockout.
var loginFailures = struct {
	sync.RWMutex
	m map[string]*failureRecord
}{m: make(map[string]*failureRecord)}

type failureRecord struct {
	count    int
	lockedAt time.Time
}

const maxLoginFailures = 5
const lockoutDuration = 15 * time.Minute

func checkIPLockout(ip string) (bool, time.Duration) {
	loginFailures.RLock()
	rec, exists := loginFailures.m[ip]
	loginFailures.RUnlock()
	if !exists {
		return false, 0
	}
	if rec.count >= maxLoginFailures {
		remaining := lockoutDuration - time.Since(rec.lockedAt)
		if remaining > 0 {
			return true, remaining
		}
		// Lockout expired, clear
		loginFailures.Lock()
		delete(loginFailures.m, ip)
		loginFailures.Unlock()
	}
	return false, 0
}

func recordLoginFailure(ip string) {
	loginFailures.Lock()
	defer loginFailures.Unlock()
	rec, exists := loginFailures.m[ip]
	if !exists {
		loginFailures.m[ip] = &failureRecord{count: 1, lockedAt: time.Now()}
		return
	}
	rec.count++
	rec.lockedAt = time.Now()
}

func clearLoginFailures(ip string) {
	loginFailures.Lock()
	delete(loginFailures.m, ip)
	loginFailures.Unlock()
}

// tryRecoveryCode checks if the provided code matches any stored recovery code.
// If matched, the used code is removed from the database.
func tryRecoveryCode(admin *model.AdminUser, code string) bool {
	if admin.RecoveryCodes == "" {
		return false
	}
	var codes []string
	if err := json.Unmarshal([]byte(admin.RecoveryCodes), &codes); err != nil {
		return false
	}
	for i, stored := range codes {
		if err := bcrypt.CompareHashAndPassword([]byte(stored), []byte(code)); err == nil {
			// Remove used code
			codes = append(codes[:i], codes[i+1:]...)
			updated, _ := json.Marshal(codes)
			admin.RecoveryCodes = string(updated)
			config.DB.Save(admin)
			return true
		}
	}
	return false
}

// startLockoutCleanup periodically removes expired lockout entries.
func startLockoutCleanup() {
	go func() {
		ticker := time.NewTicker(30 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			loginFailures.Lock()
			for ip, rec := range loginFailures.m {
				if time.Since(rec.lockedAt) >= lockoutDuration {
					delete(loginFailures.m, ip)
				}
			}
			loginFailures.Unlock()
		}
	}()
}

// isPathSafe ensures the resolved path stays within the sandbox root and is not a symlink.
func isPathSafe(userPath string) (string, bool) {
	cleaned := filepath.Clean(userPath)
	abs, err := filepath.Abs(cleaned)
	if err != nil {
		return "", false
	}
	if !strings.HasPrefix(abs, fsSandboxRoot) {
		return "", false
	}
	// Resolve symlinks to prevent sandbox escape via symlink chains.
	// For paths that don't exist yet (write ops), resolve the parent instead.
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		parent := filepath.Dir(abs)
		resolvedParent, err := filepath.EvalSymlinks(parent)
		if err != nil {
			return "", false
		}
		resolved = filepath.Join(resolvedParent, filepath.Base(abs))
	}
	if !strings.HasPrefix(resolved, fsSandboxRoot) {
		return "", false
	}
	// Reject explicit symlink entries
	if info, err := os.Lstat(abs); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", false
	}
	return abs, true
}

// dockerIDRe validates Docker container IDs (hex strings, 12-64 chars).
var dockerIDRe = regexp.MustCompile(`^[a-f0-9]{12,64}$`)

// isValidContainerID ensures the parameter is a valid Docker container ID or name.
func isValidContainerID(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	// Allow hex IDs and container names (alphanumeric, hyphens, underscores, dots)
	if dockerIDRe.MatchString(id) {
		return true
	}
	for _, c := range id {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' || c == '.') {
			return false
		}
	}
	return true
}

// parseUintID safely parses a URL parameter as a uint to prevent GORM injection.
func parseUintID(c *gin.Context) (uint, bool) {
	n, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || n == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "Invalid ID"})
		return 0, false
	}
	return uint(n), true
}

type inboundPayload struct {
	Tag            *string                      `json:"tag"`
	Protocol       *string                      `json:"protocol"`
	Listen         *string                      `json:"listen"`
	Port           *int                         `json:"port"`
	Network        *string                      `json:"network"`
	Settings       *string                      `json:"settings"`
	Stream         *string                      `json:"stream"`
	StreamSettings *string                      `json:"streamSettings"`
	ClientStats    *[]threeXUIClientStatPayload `json:"clientStats"`
	Enable         *bool                        `json:"enable"`
	Remark         *string                      `json:"remark"`
}

type clientPayload struct {
	InboundID        *uint   `json:"inbound_id"`
	InboundIDLegacy  *uint   `json:"inboundId"`
	Email            *string `json:"email"`
	UUID             *string `json:"uuid"`
	Enable           *bool   `json:"enable"`
	Total            *int64  `json:"total"`
	TrafficLimit     *int64  `json:"traffic_limit"`
	ExpiryTime       *int64  `json:"expiry_time"`
	ExpiryTimeLegacy *int64  `json:"expiryTime"`
	Remark           *string `json:"remark"`
}

func applyInboundPayload(target *model.Inbound, payload inboundPayload) {
	if payload.Tag != nil {
		target.Tag = strings.TrimSpace(*payload.Tag)
	}
	if payload.Protocol != nil {
		target.Protocol = strings.TrimSpace(*payload.Protocol)
	}
	if payload.Listen != nil {
		target.Listen = strings.TrimSpace(*payload.Listen)
	}
	if payload.Port != nil {
		target.Port = *payload.Port
	}
	if payload.Network != nil {
		target.Network = strings.TrimSpace(*payload.Network)
	}
	if payload.Settings != nil {
		target.Settings = strings.TrimSpace(*payload.Settings)
	}
	switch {
	case payload.Stream != nil:
		target.Stream = strings.TrimSpace(*payload.Stream)
	case payload.StreamSettings != nil:
		target.Stream = strings.TrimSpace(*payload.StreamSettings)
	}
	if payload.Enable != nil {
		target.Enable = *payload.Enable
	}
	if payload.Remark != nil {
		target.Remark = strings.TrimSpace(*payload.Remark)
	}
}

func validateInbound(target model.Inbound) string {
	if strings.TrimSpace(target.Tag) == "" {
		return "Tag is required"
	}
	if strings.TrimSpace(target.Protocol) == "" {
		return "Protocol is required"
	}
	if target.Port <= 0 || target.Port > 65535 {
		return "Port must be between 1 and 65535"
	}
	return ""
}

func applyClientPayload(target *model.Client, payload clientPayload) {
	switch {
	case payload.InboundID != nil:
		target.InboundID = *payload.InboundID
	case payload.InboundIDLegacy != nil:
		target.InboundID = *payload.InboundIDLegacy
	}
	if payload.Email != nil {
		target.Email = strings.TrimSpace(*payload.Email)
	}
	if payload.UUID != nil {
		target.UUID = strings.TrimSpace(*payload.UUID)
	}
	if payload.Enable != nil {
		target.Enable = *payload.Enable
	}
	switch {
	case payload.Total != nil:
		target.Total = *payload.Total
	case payload.TrafficLimit != nil:
		target.Total = *payload.TrafficLimit
	}
	switch {
	case payload.ExpiryTime != nil:
		target.ExpiryTime = *payload.ExpiryTime
	case payload.ExpiryTimeLegacy != nil:
		target.ExpiryTime = *payload.ExpiryTimeLegacy
	}
	if payload.Remark != nil {
		target.Remark = strings.TrimSpace(*payload.Remark)
	}
}

func validateClient(target model.Client) string {
	if target.InboundID == 0 {
		return "Inbound is required"
	}
	if strings.TrimSpace(target.Email) == "" {
		return "Email is required"
	}
	return ""
}

func ensureInboundExists(inboundID uint) bool {
	var count int64
	config.DB.Model(&model.Inbound{}).Where("id = ?", inboundID).Count(&count)
	return count > 0
}

func isClientInboundEmailConflict(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "clients.inbound_id, clients.email") ||
		strings.Contains(msg, "idx_clients_inbound_email")
}

func validateRoutingRule(target model.RoutingRule) string {
	if strings.TrimSpace(target.OutboundTag) == "" {
		return "Outbound tag is required"
	}
	if target.Domain == "" && target.IP == "" && target.Port == "" {
		return "At least one of domain, IP, or port is required"
	}
	return ""
}

func findDuplicateRoutingRule(target model.RoutingRule, excludeID uint) (*model.RoutingRule, error) {
	var rules []model.RoutingRule
	if err := config.DB.Find(&rules).Error; err != nil {
		return nil, err
	}

	signature := proxy.RoutingRuleSignature(target)
	for _, rule := range rules {
		if excludeID != 0 && rule.ID == excludeID {
			continue
		}
		if proxy.RoutingRuleSignature(rule) == signature {
			match := rule
			return &match, nil
		}
	}

	return nil, nil
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

	// Limit request body size to 10 MB to prevent resource exhaustion
	r.Use(func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 10<<20)
		c.Next()
	})

	// Apply Setup Guard Globally
	r.Use(middleware.SetupGuardMiddleware())

	// CORS Middleware — in production the frontend is embedded (same-origin),
	// so cross-origin requests are only allowed for development (localhost).
	r.Use(cors.New(cors.Config{
		AllowOriginFunc: func(origin string) bool {
			if gin.Mode() != gin.ReleaseMode {
				return true // allow all in dev mode
			}
			// In production, only allow same-host origins (e.g. http://host:port)
			return strings.HasPrefix(origin, "http://localhost") || strings.HasPrefix(origin, "http://127.0.0.1")
		},
		AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Accept", "Authorization"},
		ExposeHeaders:    []string{"Content-Length"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	}))

	// Embedded Static Files
	staticFS := GetStaticAssets()

	// Serve static files and SPA fallback via NoRoute.
	// Try the exact file first (e.g. /assets/index-xxx.js, /vite.svg),
	// then fall back to index.html for client-side routing.
	r.NoRoute(func(c *gin.Context) {
		path := c.Request.URL.Path
		if strings.HasPrefix(path, "/api") {
			c.JSON(http.StatusNotFound, gin.H{"code": 404, "msg": "Endpoint not found"})
			return
		}
		// Try to serve the exact static file
		if f, err := staticFS.Open(path); err == nil {
			f.Close()
			c.FileFromFS(path, staticFS)
			return
		}
		// SPA fallback: serve index.html
		c.FileFromFS("/", staticFS)
	})

	// ======================================
	// Setup Wizard APIs
	// ======================================
	setupGroup := r.Group("/api/setup")
	{
		setupGroup.POST("/login", func(c *gin.Context) {
			// Per-IP rate limiting on setup login
			if !getIPLimiter(c.ClientIP()).Allow() {
				c.JSON(http.StatusTooManyRequests, gin.H{"code": 429, "msg": "Too many attempts, please try again later"})
				return
			}
			var req struct {
				Password string `json:"password"`
			}
			if err := c.ShouldBindJSON(&req); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "Invalid parameters"})
				return
			}
			cfg := config.GetConfig()
			// Constant-time comparison to prevent timing attacks on the one-time password
			if subtle.ConstantTimeCompare([]byte(req.Password), []byte(cfg.SetupOneTimeToken)) != 1 {
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
				Username  string `json:"username"`
				Password  string `json:"password"`
				PanelPath string `json:"panel_path"`
			}
			if err := c.ShouldBindJSON(&req); err != nil || req.Username == "" || req.Password == "" {
				c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "Username and password are required"})
				return
			}
			if len(req.Username) < 3 || len(req.Username) > 32 {
				c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "Username must be 3-32 characters"})
				return
			}
			if len(req.Password) < 8 {
				c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "Password must be at least 8 characters"})
				return
			}

			// Hash password with bcrypt
			hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "Failed to hash password"})
				return
			}

			// Wrap admin creation + setup completion in a transaction to prevent
			// partial state (admin exists but setup not marked complete).
			err = config.DB.Transaction(func(tx *gorm.DB) error {
				admin := model.AdminUser{
					Username:     req.Username,
					PasswordHash: string(hash),
				}
				if err := tx.Create(&admin).Error; err != nil {
					return err
				}
				if err := tx.Where("`key` = ?", "setup_complete").
					Assign(model.Setting{Key: "setup_complete", Value: "true"}).
					FirstOrCreate(&model.Setting{}).Error; err != nil {
					return err
				}
				// Persist custom panel path if provided
				if req.PanelPath != "" {
					return tx.Where("`key` = ?", "panel_path").
						Assign(model.Setting{Key: "panel_path", Value: req.PanelPath}).
						FirstOrCreate(&model.Setting{}).Error
				}
				return nil
			})
			if err != nil {
				log.Printf("Setup complete error: %v", err)
				c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "Failed to complete setup"})
				return
			}
			cfg := config.GetConfig()
			cfg.IsSetupComplete = true
			if req.PanelPath != "" {
				cfg.PanelPrefix = req.PanelPath
			}

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
			// IP lockout check (5 consecutive failures = 15 min lockout)
			if locked, remaining := checkIPLockout(c.ClientIP()); locked {
				mins := int(remaining.Minutes()) + 1
				c.JSON(http.StatusTooManyRequests, gin.H{"code": 429, "msg": fmt.Sprintf("Too many failed attempts. Try again in %d minutes.", mins), "data": gin.H{"locked": true, "minutes": mins}})
				return
			}

			// Per-IP rate limiting
			if !getIPLimiter(c.ClientIP()).Allow() {
				c.JSON(http.StatusTooManyRequests, gin.H{"code": 429, "msg": "Too many login attempts, please try again later"})
				return
			}

			var req struct {
				Username string `json:"username"`
				Password string `json:"password"`
				TOTPCode string `json:"totp_code"`
			}
			if err := c.ShouldBindJSON(&req); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "Invalid parameters"})
				return
			}

			// Find admin user in DB
			var admin model.AdminUser
			if err := config.DB.Where("username = ?", req.Username).First(&admin).Error; err != nil {
				recordLoginFailure(c.ClientIP())
				c.JSON(http.StatusUnauthorized, gin.H{"code": 401, "msg": "Invalid username or password"})
				return
			}

			// Verify bcrypt password
			if err := bcrypt.CompareHashAndPassword([]byte(admin.PasswordHash), []byte(req.Password)); err != nil {
				recordLoginFailure(c.ClientIP())
				c.JSON(http.StatusUnauthorized, gin.H{"code": 401, "msg": "Invalid username or password"})
				return
			}

			// 2FA check
			if admin.TOTPEnabled && admin.TOTPSecret != "" {
				if req.TOTPCode == "" {
					// Password OK but 2FA code needed — don't issue token yet
					c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "2FA required", "data": gin.H{"requires_2fa": true}})
					return
				}
				// Validate TOTP code
				valid := totp.Validate(req.TOTPCode, admin.TOTPSecret)
				if !valid {
					// Try recovery codes
					valid = tryRecoveryCode(&admin, req.TOTPCode)
				}
				if !valid {
					recordLoginFailure(c.ClientIP())
					c.JSON(http.StatusUnauthorized, gin.H{"code": 401, "msg": "Invalid 2FA code"})
					return
				}
			}

			clearLoginFailures(c.ClientIP())

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
		apiGroup.GET("/sub/:uuid", func(c *gin.Context) {
			ip := c.ClientIP()
			if !getSubLimiter(ip).Allow() {
				c.Status(429)
				return
			}
			sub.GenerateSubscription(c)
		})
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
					log.Printf("FS list error: %v", err)
					c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "Failed to list directory"})
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
					log.Printf("FS read error: %v", err)
					c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "Failed to read file"})
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
					log.Printf("FS write error: %v", err)
					c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "Failed to write file"})
					return
				}
				c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "File saved"})
			})
		}

		// Diagnostics
		authGroup.GET("/diagnostics/network", func(c *gin.Context) {
			output, err := diagnostic.RunNetworkDiagnostic()
			if err != nil {
				log.Printf("Diagnostic error: %v", err)
				c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "Diagnostic failed", "data": output})
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
				log.Printf("DB error listing inbounds: %v (DB ptr: %v)", err, config.DB)
				c.JSON(500, gin.H{"code": 500, "msg": fmt.Sprintf("DB error: %v", err)})
				return
			}
			c.JSON(200, gin.H{"code": 200, "msg": "Success", "data": inbounds})
		})

		authGroup.POST("/inbounds/import-3xui", func(c *gin.Context) {
			body, err := io.ReadAll(c.Request.Body)
			if err != nil {
				c.JSON(400, gin.H{"code": 400, "msg": "Failed to read request body"})
				return
			}

			items, err := parseThreeXUIImportRequest(body)
			if err != nil {
				c.JSON(400, gin.H{"code": 400, "msg": fmt.Sprintf("Invalid 3x-ui payload: %v", err)})
				return
			}

			results := make([]map[string]interface{}, 0, len(items))
			successCount := 0
			for _, item := range items {
				inbound, importedUsers, err := func() (model.Inbound, int, error) {
					var created model.Inbound
					importedUsers := 0
					err := config.DB.Transaction(func(tx *gorm.DB) error {
						var txErr error
						created, importedUsers, txErr = importThreeXUIInbound(tx, item)
						return txErr
					})
					return created, importedUsers, err
				}()
				if err != nil {
					results = append(results, map[string]interface{}{
						"success":       false,
						"source_tag":    strings.TrimSpace(item.Tag),
						"source_remark": strings.TrimSpace(item.Remark),
						"error":         err.Error(),
					})
					continue
				}

				successCount++
				recordAudit(c, "inbound.import_3xui", inbound.Tag)
				results = append(results, map[string]interface{}{
					"success":        true,
					"inbound_id":     inbound.ID,
					"imported_tag":   inbound.Tag,
					"source_tag":     strings.TrimSpace(item.Tag),
					"source_remark":  strings.TrimSpace(item.Remark),
					"imported_users": importedUsers,
				})
			}

			c.JSON(200, gin.H{
				"code": 200,
				"msg":  fmt.Sprintf("Imported %d/%d inbound(s)", successCount, len(items)),
				"data": results,
			})
		})

		authGroup.POST("/inbounds", func(c *gin.Context) {
			var payload inboundPayload
			if err := c.ShouldBindJSON(&payload); err != nil {
				log.Printf("Inbound bind error: %v", err)
				c.JSON(400, gin.H{"code": 400, "msg": fmt.Sprintf("Invalid parameters: %v", err)})
				return
			}
			inbound := model.Inbound{Enable: true}
			applyInboundPayload(&inbound, payload)
			if msg := validateInbound(inbound); msg != "" {
				c.JSON(400, gin.H{"code": 400, "msg": msg})
				return
			}
			if inbound.Settings == "" {
				inbound.Settings = "{}"
			}
			if inbound.Stream == "" {
				inbound.Stream = "{}"
			}
			err := config.DB.Transaction(func(tx *gorm.DB) error {
				if err := tx.Create(&inbound).Error; err != nil {
					return err
				}
				return syncImportedInboundClients(tx, inbound, payload)
			})
			if err != nil {
				log.Printf("DB error creating inbound: %v", err)
				if strings.Contains(err.Error(), "UNIQUE constraint") {
					c.JSON(409, gin.H{"code": 409, "msg": fmt.Sprintf("Tag '%s' already exists", inbound.Tag)})
					return
				}
				c.JSON(500, gin.H{"code": 500, "msg": fmt.Sprintf("DB error: %v", err)})
				return
			}
			c.JSON(200, gin.H{"code": 200, "msg": "Created", "data": inbound})
			recordAudit(c, "inbound.create", inbound.Tag)
		})

		authGroup.PUT("/inbounds/:id", func(c *gin.Context) {
			id, ok := parseUintID(c)
			if !ok {
				return
			}
			var inbound model.Inbound
			if err := config.DB.First(&inbound, "id = ?", id).Error; err != nil {
				c.JSON(404, gin.H{"code": 404, "msg": "Inbound not found"})
				return
			}
			var payload inboundPayload
			if err := c.ShouldBindJSON(&payload); err != nil {
				c.JSON(400, gin.H{"code": 400, "msg": "Invalid parameters"})
				return
			}
			applyInboundPayload(&inbound, payload)
			if msg := validateInbound(inbound); msg != "" {
				c.JSON(400, gin.H{"code": 400, "msg": msg})
				return
			}
			if inbound.Settings == "" {
				inbound.Settings = "{}"
			}
			if inbound.Stream == "" {
				inbound.Stream = "{}"
			}
			err := config.DB.Transaction(func(tx *gorm.DB) error {
				if err := tx.Save(&inbound).Error; err != nil {
					return err
				}
				return syncImportedInboundClients(tx, inbound, payload)
			})
			if err != nil {
				c.JSON(500, gin.H{"code": 500, "msg": "Failed to update inbound"})
				return
			}
			c.JSON(200, gin.H{"code": 200, "msg": "Updated", "data": inbound})
			recordAudit(c, "inbound.update", inbound.Tag)
		})

		authGroup.DELETE("/inbounds/:id", func(c *gin.Context) {
			id, ok := parseUintID(c)
			if !ok {
				return
			}
			var inbound model.Inbound
			if err := config.DB.First(&inbound, "id = ?", id).Error; err != nil {
				c.JSON(404, gin.H{"code": 404, "msg": "Inbound not found"})
				return
			}
			if err := config.DB.Transaction(func(tx *gorm.DB) error {
				if err := tx.Delete(&model.Client{}, "inbound_id = ?", id).Error; err != nil {
					return err
				}
				return tx.Delete(&model.Inbound{}, "id = ?", id).Error
			}); err != nil {
				c.JSON(500, gin.H{"code": 500, "msg": "Failed to delete inbound"})
				return
			}
			c.JSON(200, gin.H{"code": 200, "msg": "Deleted"})
			recordAudit(c, "inbound.delete", fmt.Sprintf("id=%d", id))
		})

		authGroup.GET("/inbounds/:id/export-3xui", func(c *gin.Context) {
			id, ok := parseUintID(c)
			if !ok {
				return
			}

			var inbound model.Inbound
			if err := config.DB.First(&inbound, "id = ?", id).Error; err != nil {
				c.JSON(404, gin.H{"code": 404, "msg": "Inbound not found"})
				return
			}

			var clients []model.Client
			if err := config.DB.Where("inbound_id = ?", inbound.ID).Order("id ASC").Find(&clients).Error; err != nil {
				c.JSON(500, gin.H{"code": 500, "msg": "Failed to load inbound clients"})
				return
			}

			exported, err := buildThreeXUIInboundExport(inbound, clients)
			if err != nil {
				c.JSON(500, gin.H{"code": 500, "msg": fmt.Sprintf("Failed to export 3x-ui payload: %v", err)})
				return
			}

			c.JSON(200, gin.H{"code": 200, "msg": "Success", "data": exported})
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
				log.Printf("DB error listing clients: %v", err)
				c.JSON(500, gin.H{"code": 500, "msg": "Failed to list clients"})
				return
			}
			c.JSON(200, gin.H{"code": 200, "msg": "Success", "data": clients})
		})

		authGroup.POST("/clients", func(c *gin.Context) {
			var payload clientPayload
			if err := c.ShouldBindJSON(&payload); err != nil {
				c.JSON(400, gin.H{"code": 400, "msg": "Invalid parameters"})
				return
			}
			client := model.Client{Enable: true}
			applyClientPayload(&client, payload)
			if msg := validateClient(client); msg != "" {
				c.JSON(400, gin.H{"code": 400, "msg": msg})
				return
			}
			if !ensureInboundExists(client.InboundID) {
				c.JSON(400, gin.H{"code": 400, "msg": "Inbound not found"})
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
				log.Printf("DB error creating client: %v", err)
				if isClientInboundEmailConflict(err) {
					c.JSON(409, gin.H{"code": 409, "msg": fmt.Sprintf("Email '%s' already exists on this inbound", client.Email)})
					return
				}
				c.JSON(500, gin.H{"code": 500, "msg": "Failed to create client"})
				return
			}
			c.JSON(200, gin.H{"code": 200, "msg": "Created", "data": client})
			recordAudit(c, "client.create", client.Email)
		})

		authGroup.PUT("/clients/:id", func(c *gin.Context) {
			id, ok := parseUintID(c)
			if !ok {
				return
			}
			var client model.Client
			if err := config.DB.First(&client, "id = ?", id).Error; err != nil {
				c.JSON(404, gin.H{"code": 404, "msg": "Client not found"})
				return
			}
			var payload clientPayload
			if err := c.ShouldBindJSON(&payload); err != nil {
				c.JSON(400, gin.H{"code": 400, "msg": "Invalid parameters"})
				return
			}
			applyClientPayload(&client, payload)
			if msg := validateClient(client); msg != "" {
				c.JSON(400, gin.H{"code": 400, "msg": msg})
				return
			}
			if !ensureInboundExists(client.InboundID) {
				c.JSON(400, gin.H{"code": 400, "msg": "Inbound not found"})
				return
			}
			if client.UUID == "" {
				b := make([]byte, 16)
				rand.Read(b)
				client.UUID = fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
					b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
			}
			if err := config.DB.Save(&client).Error; err != nil {
				if isClientInboundEmailConflict(err) {
					c.JSON(409, gin.H{"code": 409, "msg": fmt.Sprintf("Email '%s' already exists on this inbound", client.Email)})
					return
				}
				c.JSON(500, gin.H{"code": 500, "msg": "Failed to update client"})
				return
			}
			c.JSON(200, gin.H{"code": 200, "msg": "Updated", "data": client})
		})

		authGroup.DELETE("/clients/:id", func(c *gin.Context) {
			id, ok := parseUintID(c)
			if !ok {
				return
			}
			if err := config.DB.Delete(&model.Client{}, "id = ?", id).Error; err != nil {
				c.JSON(500, gin.H{"code": 500, "msg": "Failed to delete client"})
				return
			}
			c.JSON(200, gin.H{"code": 200, "msg": "Deleted"})
			recordAudit(c, "client.delete", fmt.Sprintf("id=%d", id))
		})

		// ======================================
		// Routing Rule CRUD
		// ======================================
		authGroup.GET("/routing-rules", func(c *gin.Context) {
			var rules []model.RoutingRule
			if err := config.DB.Find(&rules).Error; err != nil {
				log.Printf("DB error listing rules: %v", err)
				c.JSON(500, gin.H{"code": 500, "msg": "Failed to list routing rules"})
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
			rule = proxy.NormalizeRoutingRule(rule)
			if msg := validateRoutingRule(rule); msg != "" {
				c.JSON(400, gin.H{"code": 400, "msg": msg})
				return
			}
			duplicate, err := findDuplicateRoutingRule(rule, 0)
			if err != nil {
				log.Printf("DB error checking duplicate rule: %v", err)
				c.JSON(500, gin.H{"code": 500, "msg": "Failed to create rule"})
				return
			}
			if duplicate != nil {
				c.JSON(409, gin.H{"code": 409, "msg": "Routing rule already exists", "data": duplicate})
				return
			}
			if err := config.DB.Create(&rule).Error; err != nil {
				log.Printf("DB error creating rule: %v", err)
				c.JSON(500, gin.H{"code": 500, "msg": "Failed to create rule"})
				return
			}
			c.JSON(200, gin.H{"code": 200, "msg": "Created", "data": rule})
		})

		authGroup.PUT("/routing-rules/:id", func(c *gin.Context) {
			id, ok := parseUintID(c)
			if !ok {
				return
			}
			var rule model.RoutingRule
			if err := config.DB.First(&rule, "id = ?", id).Error; err != nil {
				c.JSON(404, gin.H{"code": 404, "msg": "Routing rule not found"})
				return
			}
			if err := c.ShouldBindJSON(&rule); err != nil {
				c.JSON(400, gin.H{"code": 400, "msg": "Invalid parameters"})
				return
			}
			rule = proxy.NormalizeRoutingRule(rule)
			if msg := validateRoutingRule(rule); msg != "" {
				c.JSON(400, gin.H{"code": 400, "msg": msg})
				return
			}
			duplicate, err := findDuplicateRoutingRule(rule, id)
			if err != nil {
				log.Printf("DB error checking duplicate rule: %v", err)
				c.JSON(500, gin.H{"code": 500, "msg": "Failed to update rule"})
				return
			}
			if duplicate != nil {
				c.JSON(409, gin.H{"code": 409, "msg": "Routing rule already exists", "data": duplicate})
				return
			}
			if err := config.DB.Save(&rule).Error; err != nil {
				c.JSON(500, gin.H{"code": 500, "msg": "Failed to update rule"})
				return
			}
			c.JSON(200, gin.H{"code": 200, "msg": "Updated", "data": rule})
		})

		authGroup.DELETE("/routing-rules/:id", func(c *gin.Context) {
			id, ok := parseUintID(c)
			if !ok {
				return
			}
			if err := config.DB.Delete(&model.RoutingRule{}, "id = ?", id).Error; err != nil {
				c.JSON(500, gin.H{"code": 500, "msg": "Failed to delete rule"})
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
				log.Printf("Firewall list error: %v", err)
				c.JSON(500, gin.H{"code": 500, "msg": "Failed to list firewall rules"})
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
				log.Printf("Firewall add error: %v", err)
				c.JSON(500, gin.H{"code": 500, "msg": "Failed to add firewall rule"})
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
				log.Printf("Firewall delete error: %v", err)
				c.JSON(500, gin.H{"code": 500, "msg": "Failed to delete firewall rule"})
				return
			}
			c.JSON(200, gin.H{"code": 200, "msg": "Rule deleted"})
		})

		// Cloudflare Protection
		authGroup.GET("/firewall/cloudflare/status", func(c *gin.Context) {
			port := config.GetSetting("port")
			enabled := firewall.IsCloudflareProtected(port)
			c.JSON(200, gin.H{"code": 200, "data": gin.H{"enabled": enabled, "port": port}})
		})

		authGroup.POST("/firewall/cloudflare/enable", func(c *gin.Context) {
			port := config.GetSetting("port")
			if err := firewall.ApplyCloudflareProtection(port); err != nil {
				log.Printf("Cloudflare protection error: %v", err)
				c.JSON(500, gin.H{"code": 500, "msg": fmt.Sprintf("Failed: %v", err)})
				return
			}
			c.JSON(200, gin.H{"code": 200, "msg": "Cloudflare protection enabled"})
		})

		authGroup.POST("/firewall/cloudflare/disable", func(c *gin.Context) {
			port := config.GetSetting("port")
			firewall.RemoveCloudflareProtection(port)
			c.JSON(200, gin.H{"code": 200, "msg": "Cloudflare protection disabled"})
		})

		// ======================================
		// Cron Job Management
		// ======================================
		authGroup.GET("/cron/jobs", func(c *gin.Context) {
			jobs, err := sched.ListJobs()
			if err != nil {
				log.Printf("Cron list error: %v", err)
				c.JSON(500, gin.H{"code": 500, "msg": "Failed to list cron jobs"})
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
			if len(job.Command) > 1000 {
				c.JSON(400, gin.H{"code": 400, "msg": "Command too long (max 1000 characters)"})
				return
			}
			id, err := sched.AddJob(job)
			if err != nil {
				log.Printf("Cron add error: %v", err)
				c.JSON(500, gin.H{"code": 500, "msg": "Failed to create cron job"})
				return
			}
			job.ID = id
			c.JSON(200, gin.H{"code": 200, "msg": "Job created", "data": job})
		})

		authGroup.DELETE("/cron/jobs/:id", func(c *gin.Context) {
			id, ok := parseUintID(c)
			if !ok {
				return
			}
			if err := sched.RemoveJob(id); err != nil {
				log.Printf("Cron delete error: %v", err)
				c.JSON(500, gin.H{"code": 500, "msg": "Failed to delete cron job"})
				return
			}
			c.JSON(200, gin.H{"code": 200, "msg": "Job deleted"})
		})

		// ======================================
		// Proxy Core Management
		// ======================================
		proxyGroup := authGroup.Group("/proxy")
		{
			proxyGroup.GET("/status", func(c *gin.Context) {
				var enabledInbounds int64
				var enabledClients int64
				var enabledRules int64

				config.DB.Model(&model.Inbound{}).Where("enable = ?", true).Count(&enabledInbounds)
				config.DB.Model(&model.Client{}).
					Joins("JOIN inbounds ON inbounds.id = clients.inbound_id AND inbounds.deleted_at IS NULL").
					Where("clients.enable = ? AND inbounds.enable = ?", true, true).
					Count(&enabledClients)
				config.DB.Model(&model.RoutingRule{}).Where("enable = ?", true).Count(&enabledRules)

				data := gin.H{
					"xray_running":     xm.Status(),
					"singbox_running":  sm.Status(),
					"enabled_inbounds": enabledInbounds,
					"enabled_clients":  enabledClients,
					"enabled_rules":    enabledRules,
				}
				if !xm.Status() {
					if e := xm.LastError(); e != "" {
						data["xray_last_error"] = e
					}
				}
				if !sm.Status() {
					if e := sm.LastError(); e != "" {
						data["singbox_last_error"] = e
					}
				}
				c.JSON(http.StatusOK, gin.H{
					"code": 200,
					"msg":  "Success",
					"data": data,
				})
			})

			proxyGroup.POST("/apply", func(c *gin.Context) {
				engine := strings.ToLower(strings.TrimSpace(c.DefaultQuery("engine", "xray")))

				switch engine {
				case "", "xray":
					if err := xm.Restart(); err != nil {
						log.Printf("Xray apply error: %v", err)
						c.JSON(http.StatusInternalServerError, gin.H{
							"code": 500,
							"msg":  fmt.Sprintf("Failed to apply Xray config: %v", err),
						})
						return
					}
					msg := "Xray configuration applied successfully"
					if skipped := xm.SkippedProtocols(); len(skipped) > 0 {
						msg += fmt.Sprintf(" (skipped %d inbounds not supported by Xray: %s — use Sing-box engine for these)",
							len(skipped), strings.Join(skipped, ", "))
					}
					c.JSON(http.StatusOK, gin.H{
						"code": 200,
						"msg":  msg,
					})
					recordAudit(c, "proxy.apply", engine)
				case "singbox", "sing-box":
					if err := sm.Restart(); err != nil {
						log.Printf("Sing-box apply error: %v", err)
						c.JSON(http.StatusInternalServerError, gin.H{
							"code": 500,
							"msg":  fmt.Sprintf("Failed to apply Sing-box config: %v", err),
						})
						return
					}
					c.JSON(http.StatusOK, gin.H{
						"code": 200,
						"msg":  "Sing-box configuration applied successfully",
					})
					recordAudit(c, "proxy.apply", engine)
				default:
					c.JSON(http.StatusBadRequest, gin.H{
						"code": 400,
						"msg":  "Unsupported proxy engine",
					})
				}
			})

			proxyGroup.GET("/config/xray", func(c *gin.Context) {
				cfg, err := xm.GenerateConfig()
				if err != nil {
					log.Printf("Xray config error: %v", err)
					c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "Failed to generate Xray config"})
					return
				}
				c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "Success", "data": cfg})
			})

			proxyGroup.GET("/config/singbox", func(c *gin.Context) {
				cfg, err := sm.GenerateConfig()
				if err != nil {
					log.Printf("Singbox config error: %v", err)
					c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "Failed to generate Sing-box config"})
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
					log.Printf("Cert issue error: %v", err)
					c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "Failed to issue certificate"})
					return
				}

				c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "Certificate issued successfully"})
			})

			// Generate X25519 keypair for VLESS Reality
			proxyGroup.POST("/generate-reality-keys", func(c *gin.Context) {
				curve := ecdh.X25519()
				priv, err := curve.GenerateKey(rand.Reader)
				if err != nil {
					log.Printf("Reality key gen error: %v", err)
					c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "Failed to generate keys"})
					return
				}
				shortIdBytes := make([]byte, 4)
				rand.Read(shortIdBytes)
				c.JSON(http.StatusOK, gin.H{
					"code": 200,
					"msg":  "Success",
					"data": gin.H{
						"private_key": base64.RawURLEncoding.EncodeToString(priv.Bytes()),
						"public_key":  base64.RawURLEncoding.EncodeToString(priv.PublicKey().Bytes()),
						"short_id":    fmt.Sprintf("%x", shortIdBytes),
					},
				})
			})

			// Test outbound connectivity and show exit IP
			proxyGroup.POST("/test-connection", func(c *gin.Context) {
				if !xm.Status() {
					c.JSON(200, gin.H{"code": 200, "data": gin.H{"success": false, "error": "Xray is not running"}})
					return
				}

				ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
				defer cancel()

				req, err := http.NewRequestWithContext(ctx, "GET", "https://ipinfo.io/json", nil)
				if err != nil {
					c.JSON(200, gin.H{"code": 200, "data": gin.H{"success": false, "error": "Request build failed"}})
					return
				}
				req.Header.Set("User-Agent", "ZenithPanel/1.0")
				resp, err := http.DefaultClient.Do(req)
				if err != nil {
					c.JSON(200, gin.H{"code": 200, "data": gin.H{"success": false, "error": fmt.Sprintf("Connection failed: %v", err)}})
					return
				}
				defer resp.Body.Close()

				var result map[string]interface{}
				if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
					c.JSON(200, gin.H{"code": 200, "data": gin.H{"success": false, "error": "Failed to parse response"}})
					return
				}
				c.JSON(200, gin.H{"code": 200, "data": gin.H{
					"success": true,
					"ip":      result["ip"],
					"country": result["country"],
					"org":     result["org"],
				}})
			})
		}

		// ======================================
		// Admin Password Change
		// ======================================
		authGroup.POST("/admin/change-password", func(c *gin.Context) {
			var req struct {
				OldPassword string `json:"old_password"`
				NewPassword string `json:"new_password"`
			}
			if err := c.ShouldBindJSON(&req); err != nil || req.OldPassword == "" || req.NewPassword == "" {
				c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "Old and new passwords are required"})
				return
			}
			if len(req.NewPassword) < 8 {
				c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "New password must be at least 8 characters"})
				return
			}

			username, _ := c.Get("username")
			var admin model.AdminUser
			if err := config.DB.Where("username = ?", username).First(&admin).Error; err != nil {
				c.JSON(http.StatusNotFound, gin.H{"code": 404, "msg": "Admin user not found"})
				return
			}
			if err := bcrypt.CompareHashAndPassword([]byte(admin.PasswordHash), []byte(req.OldPassword)); err != nil {
				c.JSON(http.StatusUnauthorized, gin.H{"code": 401, "msg": "Old password is incorrect"})
				return
			}
			hash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "Failed to hash password"})
				return
			}
			admin.PasswordHash = string(hash)
			if err := config.DB.Save(&admin).Error; err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "Failed to update password"})
				return
			}
			c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "Password changed successfully"})
			recordAudit(c, "admin.password_change", "")
		})

		// ======================================
		// OTA Update
		// ======================================
		authGroup.GET("/system/update/check", func(c *gin.Context) {
			info, err := updater.CheckForUpdate(c.Request.Context())
			if err != nil {
				log.Printf("Update check error: %v", err)
				c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "Update check failed"})
				return
			}
			c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "Success", "data": info})
		})

		authGroup.POST("/system/update/apply", func(c *gin.Context) {
			if err := updater.PerformUpdate(c.Request.Context()); err != nil {
				log.Printf("Update apply error: %v", err)
				c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": fmt.Sprintf("Update failed: %v", err)})
				return
			}
			c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "Update applied. Panel will restart in a few seconds."})
		})

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

		// ======================================
		// Two-Factor Authentication (TOTP)
		// ======================================
		authGroup.GET("/admin/2fa/status", func(c *gin.Context) {
			username, _ := c.Get("username")
			var admin model.AdminUser
			if err := config.DB.Where("username = ?", username).First(&admin).Error; err != nil {
				c.JSON(http.StatusNotFound, gin.H{"code": 404, "msg": "Admin not found"})
				return
			}
			c.JSON(http.StatusOK, gin.H{"code": 200, "data": gin.H{"enabled": admin.TOTPEnabled}})
		})

		authGroup.POST("/admin/2fa/setup", func(c *gin.Context) {
			username, _ := c.Get("username")
			var admin model.AdminUser
			if err := config.DB.Where("username = ?", username).First(&admin).Error; err != nil {
				c.JSON(http.StatusNotFound, gin.H{"code": 404, "msg": "Admin not found"})
				return
			}
			if admin.TOTPEnabled {
				c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "2FA is already enabled"})
				return
			}

			key, err := totp.Generate(totp.GenerateOpts{
				Issuer:      "ZenithPanel",
				AccountName: admin.Username,
			})
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "Failed to generate TOTP secret"})
				return
			}

			// Generate QR code as base64 PNG
			img, err := key.Image(200, 200)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "Failed to generate QR code"})
				return
			}
			var buf bytes.Buffer
			if err := png.Encode(&buf, img); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "Failed to encode QR"})
				return
			}
			qrBase64 := base64.StdEncoding.EncodeToString(buf.Bytes())

			// Generate 8 recovery codes
			recoveryCodes := make([]string, 8)
			recoveryHashes := make([]string, 8)
			for i := 0; i < 8; i++ {
				b := make([]byte, 4)
				rand.Read(b)
				code := hex.EncodeToString(b)
				recoveryCodes[i] = code
				hash, _ := bcrypt.GenerateFromPassword([]byte(code), bcrypt.DefaultCost)
				recoveryHashes[i] = string(hash)
			}
			hashesJSON, _ := json.Marshal(recoveryHashes)

			// Save secret + recovery codes (not yet enabled)
			admin.TOTPSecret = key.Secret()
			admin.RecoveryCodes = string(hashesJSON)
			if err := config.DB.Save(&admin).Error; err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "Failed to save"})
				return
			}

			c.JSON(http.StatusOK, gin.H{"code": 200, "data": gin.H{
				"secret":         key.Secret(),
				"qr_base64":      qrBase64,
				"recovery_codes": recoveryCodes,
			}})
		})

		authGroup.POST("/admin/2fa/verify", func(c *gin.Context) {
			var req struct {
				Code string `json:"code"`
			}
			if err := c.ShouldBindJSON(&req); err != nil || req.Code == "" {
				c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "Code is required"})
				return
			}
			username, _ := c.Get("username")
			var admin model.AdminUser
			if err := config.DB.Where("username = ?", username).First(&admin).Error; err != nil {
				c.JSON(http.StatusNotFound, gin.H{"code": 404, "msg": "Admin not found"})
				return
			}
			if admin.TOTPSecret == "" {
				c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "Run 2FA setup first"})
				return
			}
			if !totp.Validate(req.Code, admin.TOTPSecret) {
				c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "Invalid code"})
				return
			}
			admin.TOTPEnabled = true
			config.DB.Save(&admin)
			c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "2FA enabled successfully"})
			recordAudit(c, "admin.2fa_enable", "")
		})

		authGroup.POST("/admin/2fa/disable", func(c *gin.Context) {
			var req struct {
				Password string `json:"password"`
			}
			if err := c.ShouldBindJSON(&req); err != nil || req.Password == "" {
				c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "Password is required"})
				return
			}
			username, _ := c.Get("username")
			var admin model.AdminUser
			if err := config.DB.Where("username = ?", username).First(&admin).Error; err != nil {
				c.JSON(http.StatusNotFound, gin.H{"code": 404, "msg": "Admin not found"})
				return
			}
			if err := bcrypt.CompareHashAndPassword([]byte(admin.PasswordHash), []byte(req.Password)); err != nil {
				c.JSON(http.StatusUnauthorized, gin.H{"code": 401, "msg": "Password is incorrect"})
				return
			}
			admin.TOTPEnabled = false
			admin.TOTPSecret = ""
			admin.RecoveryCodes = ""
			config.DB.Save(&admin)
			c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "2FA disabled"})
			recordAudit(c, "admin.2fa_disable", "")
		})

		// ======================================
		// Access Configuration
		// ======================================
		authGroup.GET("/admin/access", func(c *gin.Context) {
			panelPath := config.GetSetting("panel_path")
			port := config.GetSetting("port")
			c.JSON(http.StatusOK, gin.H{"code": 200, "data": gin.H{
				"panel_path": panelPath,
				"port":       port,
			}})
		})

		authGroup.PUT("/admin/access", func(c *gin.Context) {
			var req struct {
				PanelPath *string `json:"panel_path"`
				Port      *string `json:"port"`
			}
			if err := c.ShouldBindJSON(&req); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "Invalid request"})
				return
			}
			changed := false
			if req.PanelPath != nil {
				config.SetSetting("panel_path", *req.PanelPath)
				changed = true
			}
			if req.Port != nil && *req.Port != "" {
				// Validate port is a number in range
				p, err := strconv.Atoi(*req.Port)
				if err != nil || p < 1 || p > 65535 {
					c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "Port must be 1-65535"})
					return
				}
				config.SetSetting("port", *req.Port)
				changed = true
			}
			msg := "Settings saved."
			if changed {
				msg = "Settings saved. Restart panel to apply changes."
			}
			c.JSON(http.StatusOK, gin.H{"code": 200, "msg": msg})
		})

		authGroup.POST("/admin/restart", func(c *gin.Context) {
			port := config.GetSetting("port")
			go func() {
				if err := updater.RestartSelf(context.Background(), port); err != nil {
					log.Printf("Restart failed: %v", err)
				}
			}()
			c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "Panel restarting with new configuration..."})
		})

		// ======================================
		// TLS/HTTPS Certificate Management
		// ======================================
		authGroup.GET("/admin/tls/status", func(c *gin.Context) {
			certPath := config.GetSetting("tls_cert_path")
			keyPath := config.GetSetting("tls_key_path")
			enabled := certPath != "" && keyPath != ""
			c.JSON(http.StatusOK, gin.H{"code": 200, "data": gin.H{
				"enabled":   enabled,
				"cert_path": certPath,
				"key_path":  keyPath,
			}})
		})

		authGroup.POST("/admin/tls/upload", func(c *gin.Context) {
			certFile, err := c.FormFile("cert")
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "Certificate file is required"})
				return
			}
			keyFile, err := c.FormFile("key")
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "Key file is required"})
				return
			}

			// Read file contents
			cf, _ := certFile.Open()
			defer cf.Close()
			certData, _ := io.ReadAll(cf)
			kf, _ := keyFile.Open()
			defer kf.Close()
			keyData, _ := io.ReadAll(kf)

			// Validate TLS pair
			if _, err := tls.X509KeyPair(certData, keyData); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": fmt.Sprintf("Invalid certificate/key pair: %v", err)})
				return
			}

			// Save to data/tls/
			tlsDir := "data/tls"
			os.MkdirAll(tlsDir, 0700)
			certDst := filepath.Join(tlsDir, "cert.pem")
			keyDst := filepath.Join(tlsDir, "key.pem")
			if err := os.WriteFile(certDst, certData, 0600); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "Failed to save certificate"})
				return
			}
			if err := os.WriteFile(keyDst, keyData, 0600); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "Failed to save key"})
				return
			}

			config.SetSetting("tls_cert_path", certDst)
			config.SetSetting("tls_key_path", keyDst)

			c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "TLS certificates uploaded. Restart panel to enable HTTPS."})
		})

		authGroup.DELETE("/admin/tls", func(c *gin.Context) {
			os.Remove("data/tls/cert.pem")
			os.Remove("data/tls/key.pem")
			config.SetSetting("tls_cert_path", "")
			config.SetSetting("tls_key_path", "")
			c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "TLS disabled. Restart panel to apply."})
		})

		// ======================================
		// Audit Log
		// ======================================
		authGroup.GET("/admin/audit-log", func(c *gin.Context) {
			limit := 50
			offset := 0
			if v := c.Query("limit"); v != "" {
				fmt.Sscanf(v, "%d", &limit)
			}
			if v := c.Query("offset"); v != "" {
				fmt.Sscanf(v, "%d", &offset)
			}
			if limit > 200 {
				limit = 200
			}
			var logs []model.AuditLog
			var total int64
			config.DB.Model(&model.AuditLog{}).Count(&total)
			config.DB.Order("created_at desc").Limit(limit).Offset(offset).Find(&logs)
			c.JSON(200, gin.H{"code": 200, "data": logs, "total": total})
		})

		// ======================================
		// JWT Refresh
		// ======================================
		authGroup.POST("/auth/refresh", func(c *gin.Context) {
			username, _ := c.Get("username")
			userID, _ := c.Get("user_id")
			usernameStr, _ := username.(string)
			userIDStr, _ := userID.(string)

			token, err := jwtutil.GenerateToken(userIDStr, usernameStr, 24*time.Hour)
			if err != nil {
				c.JSON(500, gin.H{"code": 500, "msg": "Token generation failed"})
				return
			}
			c.JSON(200, gin.H{"code": 200, "token": token})
		})
	}

	// Start background lockout cleanup
	startLockoutCleanup()
}
