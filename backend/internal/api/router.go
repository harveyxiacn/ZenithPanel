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
	"errors"
	"fmt"
	"image/png"
	"io"
	"log"
	"net"
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
	"github.com/harveyxiacn/ZenithPanel/backend/internal/service/adblock"
	backupsvc "github.com/harveyxiacn/ZenithPanel/backend/internal/service/backup"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/service/cert"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/service/diagnostic"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/service/firewall"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/service/fs"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/service/monitor"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/service/notify"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/service/proxy"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/service/scheduler"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/service/sub"
	sysopt "github.com/harveyxiacn/ZenithPanel/backend/internal/service/system"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/service/terminal"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/service/traffic"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/service/updater"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/service/webserver"
	"github.com/pquerna/otp/totp"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/time/rate"
	"gorm.io/gorm"
)

// fsSandboxRoot restricts file manager operations to /home to prevent
// unauthorized access to system files, credentials, and configs.
var fsSandboxRoot = "/home"

// networkCheckDo is the HTTP transport used by the server network check endpoint.
// Replaced in tests to avoid real outbound calls.
var networkCheckDo = func(req *http.Request) (*http.Response, error) {
	return http.DefaultClient.Do(req)
}

// webserverReload triggers a hot-reload of the built-in web server if it is running.
func webserverReload() {
	if m := webserver.Get(); m != nil {
		if err := m.Reload(); err != nil {
			log.Printf("webserver reload: %v", err)
		}
	}
}

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

// startLimiterCleanup removes idle per-IP rate limiters so the maps don't
// accumulate indefinitely. A limiter is considered idle when its token bucket
// is full, which means the IP hasn't issued a request recently.
func startLimiterCleanup() {
	go func() {
		ticker := time.NewTicker(10 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			ipRateLimiters.Lock()
			for ip, l := range ipRateLimiters.m {
				if l.Tokens() >= 4.9 {
					delete(ipRateLimiters.m, ip)
				}
			}
			ipRateLimiters.Unlock()

			subRateLimiters.Range(func(k, v any) bool {
				l := v.(*rate.Limiter)
				if l.Tokens() >= 2.9 {
					subRateLimiters.Delete(k)
				}
				return true
			})
		}
	}()
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
	Tag                 *string                      `json:"tag"`
	Protocol            *string                      `json:"protocol"`
	Listen              *string                      `json:"listen"`
	ServerAddress       *string                      `json:"server_address"`
	ServerAddressLegacy *string                      `json:"serverAddress"`
	Port                *int                         `json:"port"`
	Network             *string                      `json:"network"`
	Settings            *string                      `json:"settings"`
	Stream              *string                      `json:"stream"`
	StreamSettings      *string                      `json:"streamSettings"`
	ClientStats         *[]threeXUIClientStatPayload `json:"clientStats"`
	Enable              *bool                        `json:"enable"`
	Remark              *string                      `json:"remark"`
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
	SpeedLimit       *int64  `json:"speed_limit"`
	ResetDay         *int    `json:"reset_day"`
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
	switch {
	case payload.ServerAddress != nil:
		target.ServerAddress = strings.TrimSpace(*payload.ServerAddress)
	case payload.ServerAddressLegacy != nil:
		target.ServerAddress = strings.TrimSpace(*payload.ServerAddressLegacy)
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

// setSetting wraps config.SetSetting with a logged failure path. The handlers
// that call it are already past their validation — they accepted the user's
// request — so a SQLite write failure shouldn't 500 the response, but it
// must be surfaced to whoever is tailing the logs (almost always a sign
// that the panel data directory is read-only or out of space).
func setSetting(key, value string) {
	if err := config.SetSetting(key, value); err != nil {
		log.Printf("config.SetSetting(%q): %v", key, err)
	}
}

// partitionInboundEngines splits a list of enabled inbounds into the two
// engines that will serve them in dual-engine mode. Used by the proxy/apply
// endpoint and by startup auto-recovery to decide which engines to spin up.
//
// The rule is simple and stable: anything Xray can natively serve goes to
// Xray; everything else (Hysteria2, TUIC, future QUIC-only protocols) goes
// to Sing-box. We never split a single protocol across engines, so port
// validation in validateInbound is sufficient — no two enabled inbounds can
// land on the same port.
func partitionInboundEngines(enabled []model.Inbound) (wantXray, wantSingbox bool) {
	for _, in := range enabled {
		if proxy.IsXraySupported(in.Protocol) {
			wantXray = true
		} else {
			wantSingbox = true
		}
		if wantXray && wantSingbox {
			break
		}
	}
	return
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
	if listen := strings.TrimSpace(target.Listen); listen != "" && net.ParseIP(listen) == nil {
		return "Listen must be blank or a valid IP address"
	}
	// Port conflict check — exclude self (target.ID == 0 on create, non-zero on update)
	var conflict model.Inbound
	if res := config.DB.Where("port = ? AND id != ? AND deleted_at IS NULL", target.Port, target.ID).First(&conflict); res.Error == nil {
		return fmt.Sprintf("Port %d is already used by inbound '%s'", target.Port, conflict.Tag)
	}
	if strings.TrimSpace(target.ServerAddress) == "" && !inboundHasDerivedPublicHost(target.Stream) {
		return "Fill 'Public Host / IP' or a stream host (TLS serverName, WebSocket Host, HTTP/2 host, or HTTPUpgrade host) — clients need this to reach the proxy."
	}
	// Trojan requires TLS or Reality — validate early so Sing-box doesn't crash on start.
	if target.Protocol == "trojan" {
		if msg := validateTrojanTLS(target.Stream); msg != "" {
			return msg
		}
	}
	return ""
}

// validateTrojanTLS checks that a Trojan inbound has TLS or Reality stream security.
func validateTrojanTLS(streamJSON string) string {
	if streamJSON == "" || streamJSON == "{}" {
		return "Trojan inbound requires TLS or Reality stream security (stream is not configured)"
	}
	var stream map[string]any
	if err := json.Unmarshal([]byte(streamJSON), &stream); err != nil {
		return "Trojan inbound has invalid stream JSON"
	}
	sec, _ := stream["security"].(string)
	if sec != "tls" && sec != "reality" {
		return fmt.Sprintf("Trojan inbound requires TLS or Reality security, got %q", sec)
	}
	return ""
}

func normalizeUsageProfile(raw string) string {
	switch strings.TrimSpace(strings.ToLower(raw)) {
	case "personal_proxy":
		return "personal_proxy"
	case "vps_ops":
		return "vps_ops"
	case "mixed":
		return "mixed"
	default:
		return "mixed"
	}
}

func inboundHasDerivedPublicHost(streamJSON string) bool {
	if strings.TrimSpace(streamJSON) == "" || strings.TrimSpace(streamJSON) == "{}" {
		return false
	}

	var stream map[string]any
	if err := json.Unmarshal([]byte(streamJSON), &stream); err != nil {
		return false
	}

	if tlsSettings, ok := stream["tlsSettings"].(map[string]any); ok {
		if serverName, ok := tlsSettings["serverName"].(string); ok && strings.TrimSpace(serverName) != "" {
			return true
		}
	}

	if wsSettings, ok := stream["wsSettings"].(map[string]any); ok {
		if headers, ok := wsSettings["headers"].(map[string]any); ok {
			if host, ok := headers["Host"].(string); ok && strings.TrimSpace(host) != "" {
				return true
			}
		}
	}

	if httpSettings, ok := stream["httpSettings"].(map[string]any); ok {
		if hosts, ok := httpSettings["host"].([]any); ok {
			for _, host := range hosts {
				if hostStr, ok := host.(string); ok && strings.TrimSpace(hostStr) != "" {
					return true
				}
			}
		}
	}

	if httpUpgradeSettings, ok := stream["httpupgradeSettings"].(map[string]any); ok {
		if host, ok := httpUpgradeSettings["host"].(string); ok && strings.TrimSpace(host) != "" {
			return true
		}
	}

	return false
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
	if payload.SpeedLimit != nil {
		target.SpeedLimit = *payload.SpeedLimit
	}
	if payload.ResetDay != nil {
		day := *payload.ResetDay
		if day < 0 {
			day = 0
		}
		if day > 28 {
			day = 28
		}
		target.ResetDay = day
	}
	if payload.Remark != nil {
		target.Remark = strings.TrimSpace(*payload.Remark)
	}
}

// clientUUIDPattern permits UUIDs, hex strings, and simple base64-ish passwords.
// Disallows characters that would ambiguate share-link parsing (@ ends userinfo,
// # starts a fragment, ?/& split query, whitespace can't travel in a URI).
var clientUUIDPattern = regexp.MustCompile(`^[A-Za-z0-9._\-+=/]{1,128}$`)

func validateClient(target model.Client) string {
	if target.InboundID == 0 {
		return "Inbound is required"
	}
	if strings.TrimSpace(target.Email) == "" {
		return "Email is required"
	}
	// Empty UUID is accepted — the POST /clients handler autogenerates one.
	// But if the caller supplied a UUID, it has to be safe to embed in a share link.
	if uuid := strings.TrimSpace(target.UUID); uuid != "" && !clientUUIDPattern.MatchString(uuid) {
		return "UUID contains characters that would break subscription links (allowed: A-Z a-z 0-9 . _ - + = /)"
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
func SetupRoutes(r *gin.Engine, dm *docker.Manager, xm *proxy.XrayManager, sm *proxy.SingboxManager, sched *scheduler.Scheduler, tm *traffic.Monitor) {

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

	// IP Whitelist — applied before setup guard so non-whitelisted IPs see 404
	r.Use(middleware.IPWhitelistMiddleware())

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
				Username     string `json:"username"`
				Password     string `json:"password"`
				PanelPath    string `json:"panel_path"`
				UsageProfile string `json:"usage_profile"`
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
				usageProfile := normalizeUsageProfile(req.UsageProfile)
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
				if err := tx.Where("`key` = ?", "usage_profile").
					Assign(model.Setting{Key: "usage_profile", Value: usageProfile}).
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

		// Health check — unauthenticated, suitable for external monitors (UptimeRobot, Grafana)
		apiGroup.GET("/health", func(c *gin.Context) {
			proxyStatus := "stopped"
			if xm.Status() || sm.Status() {
				proxyStatus = "running"
			}

			dbStatus := "ok"
			if sqlDB, err := config.DB.DB(); err != nil || sqlDB.Ping() != nil {
				dbStatus = "error"
			}

			health := gin.H{
				"status": "ok",
				"proxy":  proxyStatus,
				"db":     dbStatus,
			}

			// Engine-level uptime helps external monitors distinguish "always
			// running" from "just restarted N seconds ago" (i.e. flapping).
			// Zero indicates the engine isn't currently running.
			health["xray_uptime_seconds"] = int(xm.Uptime().Seconds())
			health["singbox_uptime_seconds"] = int(sm.Uptime().Seconds())

			// last_apply_unix is the wall-clock time of the most recent
			// proxy/apply. Set on apply, surfaced here so a config that
			// got stuck pre-apply is visible.
			if raw := config.GetSetting("last_apply_unix"); raw != "" {
				if v, err := strconv.ParseInt(raw, 10, 64); err == nil && v > 0 {
					health["last_apply_unix"] = v
				}
			}

			if stats, err := monitor.GetSystemStats(); err == nil {
				health["uptime_seconds"] = stats.UptimeSeconds
				health["disk_free_gb"] = float64(stats.DiskTotal-stats.DiskUsed) / (1024 * 1024 * 1024)
			}

			// Include cert expiry if TLS is configured
			certPath := config.GetSetting("tls_cert_path")
			keyPath := config.GetSetting("tls_key_path")
			if certPath != "" && keyPath != "" {
				if expiry, err := cert.ValidatePair(certPath, keyPath); err == nil {
					health["cert_expires_in_days"] = int(time.Until(expiry).Hours() / 24)
				}
			}

			statusCode := http.StatusOK
			if dbStatus == "error" {
				health["status"] = "degraded"
				statusCode = http.StatusServiceUnavailable
			}

			c.JSON(statusCode, health)
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
	authGroup.Use(middleware.AuthMiddleware())
	{
		authGroup.GET("/system/monitor", func(c *gin.Context) {
			stats, err := monitor.GetSystemStats()
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "Failed to get system stats"})
				return
			}
			// Record network sample for the history ring buffer on every poll
			monitor.RecordNetworkSample(stats.NetIn, stats.NetOut)
			c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "Success", "data": stats})
		})

		authGroup.GET("/system/network-history", func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "Success", "data": monitor.GetNetworkHistory()})
		})

		// Traffic Observer — who's moving bytes right now (proxy users + OS processes).
		// Reads come from the shared in-process traffic.Monitor; no syscalls in
		// the handler. tm is nil under unit tests, so each handler guards for it.
		authGroup.GET("/traffic/live", func(c *gin.Context) {
			if tm == nil {
				c.JSON(http.StatusServiceUnavailable, gin.H{"code": 503, "msg": "Traffic monitor not initialized"})
				return
			}
			c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "Success", "data": tm.Latest()})
		})
		authGroup.GET("/traffic/history", func(c *gin.Context) {
			if tm == nil {
				c.JSON(http.StatusServiceUnavailable, gin.H{"code": 503, "msg": "Traffic monitor not initialized"})
				return
			}
			secs := 120
			if q := strings.TrimSpace(c.Query("seconds")); q != "" {
				if v, err := strconv.Atoi(q); err == nil && v > 0 && v <= 600 {
					secs = v
				}
			}
			c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "Success", "data": tm.History(secs)})
		})

		// Extended network history (persisted hourly snapshots). Default window: 7 days.
		authGroup.GET("/system/network-history/extended", func(c *gin.Context) {
			since := time.Now().AddDate(0, 0, -7).Unix()
			if q := strings.TrimSpace(c.Query("since")); q != "" {
				if v, err := strconv.ParseInt(q, 10, 64); err == nil && v > 0 {
					since = v
				}
			}
			data := monitor.GetPersistedHistory(config.DB, since)
			c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "Success", "data": data})
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
				if errors.Is(err, diagnostic.ErrDiagnosticScriptUnavailable) {
					c.JSON(http.StatusServiceUnavailable, gin.H{"code": 503, "msg": "Diagnostic script is unavailable in this deployment", "data": output})
					return
				}
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

			results := make([]map[string]any, 0, len(items))
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
					results = append(results, map[string]any{
						"success":       false,
						"source_tag":    strings.TrimSpace(item.Tag),
						"source_remark": strings.TrimSpace(item.Remark),
						"error":         err.Error(),
					})
					continue
				}

				successCount++
				recordAudit(c, "inbound.import_3xui", inbound.Tag)
				results = append(results, map[string]any{
					"success":        true,
					"inbound_id":     inbound.ID,
					"imported_tag":   inbound.Tag,
					"source_tag":     strings.TrimSpace(item.Tag),
					"source_remark":  strings.TrimSpace(item.Remark),
					"imported_users": importedUsers,
				})
			}

			if successCount > 0 {
				sub.InvalidateSubCache()
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
			sub.InvalidateSubCache()
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
			sub.InvalidateSubCache()
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
			sub.InvalidateSubCache()
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
			sub.InvalidateSubCache()
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
			sub.InvalidateSubCache()
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
			sub.InvalidateSubCache()
			c.JSON(200, gin.H{"code": 200, "msg": "Deleted"})
			recordAudit(c, "client.delete", fmt.Sprintf("id=%d", id))
		})

		// Bulk client operations: delete, enable, disable, reset_traffic by IDs
		authGroup.POST("/clients/bulk", func(c *gin.Context) {
			var req struct {
				Action string `json:"action"`
				IDs    []uint `json:"ids"`
			}
			if err := c.ShouldBindJSON(&req); err != nil {
				c.JSON(400, gin.H{"code": 400, "msg": "Invalid parameters"})
				return
			}
			if len(req.IDs) == 0 {
				c.JSON(400, gin.H{"code": 400, "msg": "ids must not be empty"})
				return
			}

			var affected int64
			switch req.Action {
			case "delete":
				res := config.DB.Delete(&model.Client{}, "id IN ?", req.IDs)
				if res.Error != nil {
					c.JSON(500, gin.H{"code": 500, "msg": "Bulk delete failed"})
					return
				}
				affected = res.RowsAffected
			case "enable":
				res := config.DB.Model(&model.Client{}).Where("id IN ?", req.IDs).Update("enable", true)
				if res.Error != nil {
					c.JSON(500, gin.H{"code": 500, "msg": "Bulk enable failed"})
					return
				}
				affected = res.RowsAffected
			case "disable":
				res := config.DB.Model(&model.Client{}).Where("id IN ?", req.IDs).Update("enable", false)
				if res.Error != nil {
					c.JSON(500, gin.H{"code": 500, "msg": "Bulk disable failed"})
					return
				}
				affected = res.RowsAffected
			case "reset_traffic":
				res := config.DB.Model(&model.Client{}).Where("id IN ?", req.IDs).Updates(map[string]any{"up_load": 0, "down_load": 0})
				if res.Error != nil {
					c.JSON(500, gin.H{"code": 500, "msg": "Bulk reset failed"})
					return
				}
				affected = res.RowsAffected
			default:
				c.JSON(400, gin.H{"code": 400, "msg": "action must be one of: delete, enable, disable, reset_traffic"})
				return
			}

			sub.InvalidateSubCache()
			recordAudit(c, "client.bulk_"+req.Action, fmt.Sprintf("affected=%d", affected))
			c.JSON(200, gin.H{"code": 200, "msg": "Success", "data": gin.H{"affected": affected}})
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
		// Outbound Management (WARP, SOCKS5, HTTP)
		// ======================================
		authGroup.GET("/outbounds", func(c *gin.Context) {
			var outbounds []model.Outbound
			if err := config.DB.Find(&outbounds).Error; err != nil {
				c.JSON(500, gin.H{"code": 500, "msg": "Failed to list outbounds"})
				return
			}
			c.JSON(200, gin.H{"code": 200, "msg": "Success", "data": outbounds})
		})

		authGroup.POST("/outbounds", func(c *gin.Context) {
			var payload struct {
				Tag         string `json:"tag"`
				Protocol    string `json:"protocol"`
				Config      string `json:"config"`
				Description string `json:"description"`
				Enable      *bool  `json:"enable"`
			}
			if err := c.ShouldBindJSON(&payload); err != nil {
				c.JSON(400, gin.H{"code": 400, "msg": "Invalid parameters"})
				return
			}
			if payload.Tag == "" || payload.Protocol == "" {
				c.JSON(400, gin.H{"code": 400, "msg": "tag and protocol are required"})
				return
			}
			enable := true
			if payload.Enable != nil {
				enable = *payload.Enable
			}
			ob := model.Outbound{
				Tag:         payload.Tag,
				Protocol:    payload.Protocol,
				Config:      payload.Config,
				Description: payload.Description,
				Enable:      enable,
			}
			if err := config.DB.Create(&ob).Error; err != nil {
				if strings.Contains(err.Error(), "UNIQUE") {
					c.JSON(409, gin.H{"code": 409, "msg": "Outbound tag already exists"})
					return
				}
				c.JSON(500, gin.H{"code": 500, "msg": "Failed to create outbound"})
				return
			}
			recordAudit(c, "outbound.create", ob.Tag)
			c.JSON(200, gin.H{"code": 200, "msg": "Created", "data": ob})
		})

		authGroup.PUT("/outbounds/:id", func(c *gin.Context) {
			id, ok := parseUintID(c)
			if !ok {
				return
			}
			var ob model.Outbound
			if err := config.DB.First(&ob, "id = ?", id).Error; err != nil {
				c.JSON(404, gin.H{"code": 404, "msg": "Outbound not found"})
				return
			}
			var payload struct {
				Tag         string `json:"tag"`
				Protocol    string `json:"protocol"`
				Config      string `json:"config"`
				Description string `json:"description"`
				Enable      *bool  `json:"enable"`
			}
			if err := c.ShouldBindJSON(&payload); err != nil {
				c.JSON(400, gin.H{"code": 400, "msg": "Invalid parameters"})
				return
			}
			if payload.Tag != "" {
				ob.Tag = payload.Tag
			}
			if payload.Protocol != "" {
				ob.Protocol = payload.Protocol
			}
			if payload.Config != "" {
				ob.Config = payload.Config
			}
			ob.Description = payload.Description
			if payload.Enable != nil {
				ob.Enable = *payload.Enable
			}
			if err := config.DB.Save(&ob).Error; err != nil {
				c.JSON(500, gin.H{"code": 500, "msg": "Failed to update outbound"})
				return
			}
			recordAudit(c, "outbound.update", ob.Tag)
			c.JSON(200, gin.H{"code": 200, "msg": "Updated", "data": ob})
		})

		authGroup.DELETE("/outbounds/:id", func(c *gin.Context) {
			id, ok := parseUintID(c)
			if !ok {
				return
			}
			if err := config.DB.Delete(&model.Outbound{}, "id = ?", id).Error; err != nil {
				c.JSON(500, gin.H{"code": 500, "msg": "Failed to delete outbound"})
				return
			}
			recordAudit(c, "outbound.delete", fmt.Sprintf("id=%d", id))
			c.JSON(200, gin.H{"code": 200, "msg": "Deleted"})
		})

		// Fetch WARP WireGuard credentials from Cloudflare
		authGroup.POST("/outbounds/warp/fetch", func(c *gin.Context) {
			var payload struct {
				AccountID string `json:"account_id"`
				Token     string `json:"token"`
			}
			if err := c.ShouldBindJSON(&payload); err != nil || payload.AccountID == "" || payload.Token == "" {
				c.JSON(400, gin.H{"code": 400, "msg": "account_id and token are required"})
				return
			}
			warpCfg, err := proxy.FetchWARPConfig(payload.AccountID, payload.Token)
			if err != nil {
				log.Printf("WARP fetch error: %v", err)
				c.JSON(500, gin.H{"code": 500, "msg": fmt.Sprintf("WARP API error: %v", err)})
				return
			}
			c.JSON(200, gin.H{"code": 200, "msg": "Success", "data": warpCfg})
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
			var rule firewall.Rule
			if err := c.ShouldBindJSON(&rule); err != nil {
				c.JSON(400, gin.H{"code": 400, "msg": "Invalid parameters"})
				return
			}
			if err := firewall.DeleteRule(rule); err != nil {
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
			if strings.TrimSpace(job.Schedule) == "" {
				c.JSON(400, gin.H{"code": 400, "msg": "Schedule is required"})
				return
			}
			if len(job.Command) > 1000 {
				c.JSON(400, gin.H{"code": 400, "msg": "Command too long (max 1000 characters)"})
				return
			}
			if err := scheduler.ValidateSchedule(job.Schedule); err != nil {
				c.JSON(400, gin.H{"code": 400, "msg": fmt.Sprintf("Invalid cron schedule: %v", err)})
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

				dualMode := xm.IsDualMode() || sm.IsDualMode()
				data := gin.H{
					"xray_running":     xm.Status(),
					"singbox_running":  sm.Status(),
					"enabled_inbounds": enabledInbounds,
					"enabled_clients":  enabledClients,
					"enabled_rules":    enabledRules,
					"dual_mode":        dualMode,
				}
				if !xm.Status() {
					if e := xm.LastError(); e != "" {
						data["xray_last_error"] = e
					}
				} else if skipped := xm.SkippedProtocols(); len(skipped) > 0 {
					// In dual mode these aren't "skipped" — they're being served
					// by Sing-box. Rename the field so the UI doesn't show a
					// scary warning when both engines cooperate as designed.
					if dualMode {
						data["xray_handed_off_to_singbox"] = skipped
					} else {
						data["xray_skipped_protocols"] = skipped
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
				// Default to "auto" (dual-engine partition). Explicit engine=xray
				// or engine=singbox preserves the legacy single-engine override
				// used by the Web UI's per-engine apply buttons.
				engine := strings.ToLower(strings.TrimSpace(c.DefaultQuery("engine", "auto")))

				switch engine {
				case "auto", "":
					// Partition enabled inbounds: Xray handles everything it can
					// (VLESS / VMess / Trojan / SS); Sing-box handles the rest
					// (Hysteria2 / TUIC / any future singbox-exclusive protocol).
					// Both engines run concurrently so all protocols stay reachable
					// at the same time.
					var enabledInbounds []model.Inbound
					config.DB.Where("enable = ?", true).Find(&enabledInbounds)
					wantXray, wantSingbox := partitionInboundEngines(enabledInbounds)

					xm.SetDualMode(true)
					sm.SetDualMode(true)

					if err := xm.Stop(); err != nil {
						log.Printf("Xray pre-stop (auto): %v", err)
					}
					if err := sm.Stop(); err != nil {
						log.Printf("Sing-box pre-stop (auto): %v", err)
					}

					var xrayErr, singboxErr error
					if wantXray {
						if err := xm.Start(); err != nil {
							xrayErr = err
							log.Printf("Xray start (auto): %v", err)
						}
					}
					if wantSingbox {
						if err := sm.Start(); err != nil {
							singboxErr = err
							log.Printf("Sing-box start (auto): %v", err)
						}
					}

					data := gin.H{
						"xray_running":    xm.Status(),
						"singbox_running": sm.Status(),
						"mode":            "auto",
					}
					var msgs []string
					if wantXray {
						if xrayErr != nil {
							msgs = append(msgs, fmt.Sprintf("Xray failed: %v", xrayErr))
						} else {
							msgs = append(msgs, "Xray applied")
						}
					}
					if wantSingbox {
						if singboxErr != nil {
							msgs = append(msgs, fmt.Sprintf("Sing-box failed: %v", singboxErr))
						} else {
							msgs = append(msgs, "Sing-box applied")
						}
					}
					if !wantXray && !wantSingbox {
						msgs = append(msgs, "No enabled inbounds; both engines stopped")
					}
					status := http.StatusOK
					if xrayErr != nil || singboxErr != nil {
						status = http.StatusInternalServerError
					}
					c.JSON(status, gin.H{
						"code": status,
						"msg":  strings.Join(msgs, "; "),
						"data": data,
					})
					setSetting("last_apply_unix", strconv.FormatInt(time.Now().Unix(), 10))
					recordAudit(c, "proxy.apply", engine)
				case "xray":
					// Stop Sing-box first to free any ports it holds before Xray binds them.
					if sm.Status() {
						if err := sm.Stop(); err != nil {
							log.Printf("Sing-box stop (before Xray start): %v", err)
						}
					}
					xm.SetDualMode(false)
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
						"code":              200,
						"msg":               msg,
						"skipped_protocols": xm.SkippedProtocols(),
					})
					setSetting("last_apply_unix", strconv.FormatInt(time.Now().Unix(), 10))
					recordAudit(c, "proxy.apply", engine)
				case "singbox", "sing-box":
					// Stop Xray first to free any ports it holds before Sing-box binds them.
					if xm.Status() {
						if err := xm.Stop(); err != nil {
							log.Printf("Xray stop (before Sing-box start): %v", err)
						}
					}
					sm.SetDualMode(false)
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
					setSetting("last_apply_unix", strconv.FormatInt(time.Now().Unix(), 10))
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

			// Clash API toggle + connections proxy (Sing-box experimental.clash_api)
			proxyGroup.POST("/clash-api/enable", func(c *gin.Context) {
				setSetting("singbox_clash_api_enabled", "true")
				c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "Clash API enabled. Re-apply Sing-box config to take effect."})
			})

			proxyGroup.POST("/clash-api/disable", func(c *gin.Context) {
				setSetting("singbox_clash_api_enabled", "false")
				c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "Clash API disabled. Re-apply Sing-box config to take effect."})
			})

			proxyGroup.GET("/clash-api/status", func(c *gin.Context) {
				enabled := config.GetSetting("singbox_clash_api_enabled") == "true"
				c.JSON(http.StatusOK, gin.H{"code": 200, "data": gin.H{"enabled": enabled}})
			})

			// Live connections — proxies Sing-box's Clash API /connections endpoint.
			proxyGroup.GET("/connections", func(c *gin.Context) {
				if !sm.Status() {
					c.JSON(http.StatusServiceUnavailable, gin.H{"code": 503, "msg": "Sing-box is not running"})
					return
				}
				if config.GetSetting("singbox_clash_api_enabled") != "true" {
					c.JSON(http.StatusServiceUnavailable, gin.H{"code": 503, "msg": "Clash API not enabled — enable it and re-apply Sing-box config"})
					return
				}
				port := config.GetSetting("singbox_clash_api_port")
				if port == "" {
					port = "9090"
				}
				client := &http.Client{Timeout: 3 * time.Second}
				resp, err := client.Get("http://127.0.0.1:" + port + "/connections")
				if err != nil {
					c.JSON(http.StatusServiceUnavailable, gin.H{"code": 503, "msg": "Failed to reach Clash API: " + err.Error()})
					return
				}
				defer resp.Body.Close()
				body, _ := io.ReadAll(io.LimitReader(resp.Body, 5<<20)) // 5 MiB cap
				var data any
				if err := json.Unmarshal(body, &data); err != nil {
					c.JSON(http.StatusBadGateway, gin.H{"code": 502, "msg": "Invalid response from Clash API"})
					return
				}
				c.JSON(http.StatusOK, gin.H{"code": 200, "data": data})
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

				// Use ObtainCert (not IssueCertificate) so we can surface the
				// on-disk paths back to the caller. Operators need them to
				// wire the cert into an inbound's tlsSettings.
				certPath, keyPath, err := cert.ObtainCert(req.Domain, req.Email)
				if err != nil {
					log.Printf("Cert issue error: %v", err)
					c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": fmt.Sprintf("Failed to issue certificate: %v", err)})
					return
				}

				// Stamp the renewal bookkeeping so the auto-renewal ticker
				// knows which (domain, email) pair to retry with.
				setSetting("acme_email", req.Email)

				notAfter, _ := cert.ValidatePair(certPath, keyPath)
				recordAudit(c, "tls.issue", req.Domain)
				c.JSON(http.StatusOK, gin.H{
					"code": 200,
					"msg":  "Certificate issued. Wire the paths into an inbound's tlsSettings or panel TLS upload to put it into use.",
					"data": gin.H{
						"domain":    req.Domain,
						"cert_path": certPath,
						"key_path":  keyPath,
						"not_after": notAfter.Unix(),
					},
				})
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

			// Check server public network — reports the VPS exit IP regardless of proxy state.
			// Inbound connectivity probe — defensive, panel-local check that
			// confirms the engine is actually serving the inbound's port. See
			// docs/cli_api_spec.md §2.5 and service/diagnostic.ProbeInbound for
			// the staged result (not_bound → tcp → tls → ok) the prober returns.
			proxyGroup.GET("/test/:inbound_id", func(c *gin.Context) {
				idStr := c.Param("inbound_id")
				id, err := strconv.ParseUint(idStr, 10, 32)
				if err != nil {
					c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "Invalid inbound id"})
					return
				}
				var in model.Inbound
				if err := config.DB.First(&in, id).Error; err != nil {
					c.JSON(http.StatusNotFound, gin.H{"code": 404, "msg": "Inbound not found"})
					return
				}
				result := diagnostic.ProbeInbound(in)
				c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "ok", "data": result})
			})

			proxyGroup.POST("/test-connection", func(c *gin.Context) {
				ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
				defer cancel()

				req, err := http.NewRequestWithContext(ctx, "GET", "https://ipinfo.io/json", nil)
				if err != nil {
					c.JSON(200, gin.H{"code": 200, "data": gin.H{
						"success": false,
						"scope":   "server_public_network",
						"error":   "Request build failed",
					}})
					return
				}
				req.Header.Set("User-Agent", "ZenithPanel/1.0")
				resp, err := networkCheckDo(req)
				if err != nil {
					c.JSON(200, gin.H{"code": 200, "data": gin.H{
						"success": false,
						"scope":   "server_public_network",
						"error":   fmt.Sprintf("Connection failed: %v", err),
					}})
					return
				}
				defer resp.Body.Close()

				var result map[string]any
				if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
					c.JSON(200, gin.H{"code": 200, "data": gin.H{
						"success": false,
						"scope":   "server_public_network",
						"error":   "Failed to parse response",
					}})
					return
				}
				c.JSON(200, gin.H{"code": 200, "data": gin.H{
					"success": true,
					"scope":   "server_public_network",
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
			usageProfile := normalizeUsageProfile(config.GetSetting("usage_profile"))
			ipWhitelist := config.GetSetting("panel_ip_whitelist")
			c.JSON(http.StatusOK, gin.H{"code": 200, "data": gin.H{
				"panel_path":    panelPath,
				"port":          port,
				"usage_profile": usageProfile,
				"ip_whitelist":  ipWhitelist,
				"your_ip":       c.ClientIP(),
			}})
		})

		authGroup.PUT("/admin/access", func(c *gin.Context) {
			var req struct {
				PanelPath    *string `json:"panel_path"`
				Port         *string `json:"port"`
				UsageProfile *string `json:"usage_profile"`
				IPWhitelist  *string `json:"ip_whitelist"`
			}
			if err := c.ShouldBindJSON(&req); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "Invalid request"})
				return
			}
			changed := false
			if req.PanelPath != nil {
				setSetting("panel_path", *req.PanelPath)
				changed = true
			}
			if req.Port != nil && *req.Port != "" {
				// Validate port is a number in range
				p, err := strconv.Atoi(*req.Port)
				if err != nil || p < 1 || p > 65535 {
					c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "Port must be 1-65535"})
					return
				}
				setSetting("port", *req.Port)
				changed = true
			}
			if req.UsageProfile != nil {
				setSetting("usage_profile", normalizeUsageProfile(*req.UsageProfile))
				changed = true
			}
			if req.IPWhitelist != nil {
				setSetting("panel_ip_whitelist", strings.TrimSpace(*req.IPWhitelist))
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
			if err := os.MkdirAll(tlsDir, 0700); err != nil {
				log.Printf("MkdirAll(%s): %v", tlsDir, err)
				c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "Failed to create TLS directory"})
				return
			}
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

			setSetting("tls_cert_path", certDst)
			setSetting("tls_key_path", keyDst)

			c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "TLS certificates uploaded. Restart panel to enable HTTPS."})
		})

		authGroup.DELETE("/admin/tls", func(c *gin.Context) {
			os.Remove("data/tls/cert.pem")
			os.Remove("data/tls/key.pem")
			setSetting("tls_cert_path", "")
			setSetting("tls_key_path", "")
			c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "TLS disabled. Restart panel to apply."})
		})

		// ======================================
		// Audit Log
		// ======================================
		authGroup.GET("/admin/audit-log", func(c *gin.Context) {
			limit := 50
			offset := 0
			// Sscanf failure leaves the destination at its zero value, which
			// would silently flip limit to 0 (i.e. "return nothing"). Capture
			// the count return so a bad query string falls back to defaults
			// instead of breaking the audit log view.
			if v := c.Query("limit"); v != "" {
				if n, _ := fmt.Sscanf(v, "%d", &limit); n != 1 {
					limit = 50
				}
			}
			if v := c.Query("offset"); v != "" {
				if n, _ := fmt.Sscanf(v, "%d", &offset); n != 1 {
					offset = 0
				}
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

		// ======================================
		// Notification Settings
		// ======================================
		notifyKeys := []string{
			"notify_telegram_token",
			"notify_telegram_chat_id",
			"notify_webhook_url",
			"notify_enable_expiring_soon",
			"notify_enable_expired",
			"notify_enable_traffic_limit",
			"notify_enable_proxy_crashed",
			"notify_enable_cert_expiry",
			"notify_telegram_bot_enabled",
		}

		authGroup.GET("/admin/notify", func(c *gin.Context) {
			result := map[string]string{}
			for _, k := range notifyKeys {
				result[k] = config.GetSetting(k)
			}
			c.JSON(200, gin.H{"code": 200, "msg": "Success", "data": result})
		})

		// DNS Settings — controls how Sing-box/Xray resolve domains for outbound traffic.
		authGroup.GET("/admin/dns", func(c *gin.Context) {
			mode := config.GetSetting("dns_mode")
			if mode == "" {
				mode = "plain"
			}
			c.JSON(200, gin.H{"code": 200, "data": gin.H{
				"dns_mode":      mode,
				"dns_primary":   config.GetSetting("dns_primary"),
				"dns_secondary": config.GetSetting("dns_secondary"),
			}})
		})

		authGroup.PUT("/admin/dns", func(c *gin.Context) {
			var req struct {
				DNSMode      *string `json:"dns_mode"`
				DNSPrimary   *string `json:"dns_primary"`
				DNSSecondary *string `json:"dns_secondary"`
			}
			if err := c.ShouldBindJSON(&req); err != nil {
				c.JSON(400, gin.H{"code": 400, "msg": "Invalid parameters"})
				return
			}
			if req.DNSMode != nil {
				mode := strings.ToLower(strings.TrimSpace(*req.DNSMode))
				if mode != "" && mode != "plain" && mode != "doh" {
					c.JSON(400, gin.H{"code": 400, "msg": "dns_mode must be 'plain' or 'doh'"})
					return
				}
				setSetting("dns_mode", mode)
			}
			if req.DNSPrimary != nil {
				setSetting("dns_primary", strings.TrimSpace(*req.DNSPrimary))
			}
			if req.DNSSecondary != nil {
				setSetting("dns_secondary", strings.TrimSpace(*req.DNSSecondary))
			}
			c.JSON(200, gin.H{"code": 200, "msg": "Saved. Re-apply proxy config to take effect."})
		})

		authGroup.PUT("/admin/notify", func(c *gin.Context) {
			var payload map[string]string
			if err := c.ShouldBindJSON(&payload); err != nil {
				c.JSON(400, gin.H{"code": 400, "msg": "Invalid parameters"})
				return
			}
			allowed := map[string]bool{}
			for _, k := range notifyKeys {
				allowed[k] = true
			}
			for k, v := range payload {
				if allowed[k] {
					setSetting(k, v)
				}
			}
			c.JSON(200, gin.H{"code": 200, "msg": "Saved"})
		})

		authGroup.POST("/admin/notify/test", func(c *gin.Context) {
			var payload struct {
				Channel string `json:"channel"` // "telegram" | "webhook"
				Token   string `json:"token"`
				ChatID  string `json:"chat_id"`
				URL     string `json:"url"`
			}
			if err := c.ShouldBindJSON(&payload); err != nil {
				c.JSON(400, gin.H{"code": 400, "msg": "Invalid parameters"})
				return
			}
			cfg := notify.Config{
				TelegramToken:      payload.Token,
				TelegramChatID:     payload.ChatID,
				WebhookURL:         payload.URL,
				EnableExpiringSoon: true,
				EnableExpired:      true,
				EnableTrafficLimit: true,
				EnableProxyCrashed: true,
			}
			if err := notify.SendTest(cfg); err != nil {
				c.JSON(200, gin.H{"code": 500, "msg": fmt.Sprintf("Test failed: %v", err)})
				return
			}
			c.JSON(200, gin.H{"code": 200, "msg": "Test notification sent"})
		})

		// ======================================
		// Backup / Restore
		// ======================================
		authGroup.GET("/admin/backup/export", func(c *gin.Context) {
			filename := fmt.Sprintf("zenithpanel-backup-%s.zip", time.Now().UTC().Format("20060102-150405"))
			c.Header("Content-Type", "application/zip")
			c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", filename))
			counts, err := backupsvc.Export(c.Writer)
			if err != nil {
				log.Printf("Backup export failed: %v", err)
				// Response body is already being written; trailers are best-effort here.
				c.Status(500)
				return
			}
			recordAudit(c, "backup.export", fmt.Sprintf("items=%v", counts))
		})

		authGroup.POST("/admin/backup/restore", func(c *gin.Context) {
			// Cap body at 16 MB — backups are JSON-in-zip and should stay small.
			const maxBackupBytes = 16 * 1024 * 1024
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBackupBytes)
			body, err := io.ReadAll(c.Request.Body)
			if err != nil {
				c.JSON(400, gin.H{"code": 400, "msg": "Failed to read backup payload (max 16 MB)"})
				return
			}
			if len(body) == 0 {
				c.JSON(400, gin.H{"code": 400, "msg": "Empty backup payload"})
				return
			}
			counts, err := backupsvc.Restore(body)
			if err != nil {
				c.JSON(400, gin.H{"code": 400, "msg": fmt.Sprintf("Restore failed: %v", err)})
				return
			}
			sub.InvalidateSubCache()
			recordAudit(c, "backup.restore", fmt.Sprintf("items=%v", counts))
			c.JSON(200, gin.H{"code": 200, "msg": "Restored", "data": counts})
		})

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
		RegisterDeployRoutes(authGroup)

		// Ad-block toggle: GET reports current state; PUT flips the setting,
		// re-applies the managed routing rule, and triggers a proxy re-apply
		// so both engines pick up the change immediately.
		authGroup.GET("/admin/adblock", func(c *gin.Context) {
			if !middleware.HasScope(c, "admin") {
				c.JSON(http.StatusForbidden, gin.H{"code": 403, "msg": "Scope 'admin' required"})
				return
			}
			c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "ok", "data": gin.H{
				"enabled": adblock.IsEnabled(config.GetSetting),
			}})
		})
		authGroup.PUT("/admin/adblock", func(c *gin.Context) {
			if !middleware.HasScope(c, "admin") {
				c.JSON(http.StatusForbidden, gin.H{"code": 403, "msg": "Scope 'admin' required"})
				return
			}
			var req struct {
				Enabled bool `json:"enabled"`
			}
			if err := c.ShouldBindJSON(&req); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "Invalid parameters"})
				return
			}
			val := "false"
			if req.Enabled {
				val = "true"
			}
			setSetting(adblock.SettingKey, val)
			if err := adblock.Apply(config.DB, req.Enabled); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": err.Error()})
				return
			}
			// Restart whichever engine(s) are running so the new routing rule
			// materializes. Dual mode runs both restarts concurrently — they
			// touch different binaries and config files, and serially they'd
			// double the toggle latency on the slow path. Failures are logged
			// (the UI toggle has already been persisted; next manual apply
			// will pick up the rule).
			restartEngine := func(name string, restart func() error) {
				if err := restart(); err != nil {
					log.Printf("adblock: %s restart failed: %v", name, err)
				}
			}
			switch {
			case xm.IsDualMode() || sm.IsDualMode():
				var wg sync.WaitGroup
				wg.Add(2)
				go func() { defer wg.Done(); restartEngine("xray", xm.Restart) }()
				go func() { defer wg.Done(); restartEngine("singbox", sm.Restart) }()
				wg.Wait()
			case xm.Status():
				restartEngine("xray", xm.Restart)
			case sm.Status():
				restartEngine("singbox", sm.Restart)
			}
			recordAudit(c, "adblock.toggle", val)
			c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "Ad-block " + val, "data": gin.H{"enabled": req.Enabled}})
		})

		// API token CRUD for CLI / headless automation. See docs/cli_api_spec.md.
		registerAPITokenRoutes(authGroup)

		// Prometheus-compatible metrics endpoint (authenticated). See metrics.go.
		registerMetricsRoute(authGroup, xm, sm)
	}

	// Start background lockout cleanup and rate-limiter map GC
	startLockoutCleanup()
	startLimiterCleanup()
}
