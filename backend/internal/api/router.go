package api

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/api/middleware"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/config"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/docker"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/model"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/pkg/cpuinfo"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/pkg/jwtutil"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/service/adblock"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/service/cert"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/service/diagnostic"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/service/firewall"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/service/monitor"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/service/proxy"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/service/scheduler"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/service/sub"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/service/terminal"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/service/traffic"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/service/webserver"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/version"
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
	var count int
	var lockedAt time.Time
	if exists {
		// Copy under the lock: recordLoginFailure mutates *rec concurrently.
		count, lockedAt = rec.count, rec.lockedAt
	}
	loginFailures.RUnlock()
	if !exists {
		return false, 0
	}
	if count >= maxLoginFailures {
		remaining := lockoutDuration - time.Since(lockedAt)
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
			result := config.DB.Model(&model.AdminUser{}).Where("id = ? AND session_version = ? AND recovery_codes = ?", admin.ID, admin.SessionVersion, admin.RecoveryCodes).Update("recovery_codes", string(updated))
			if result.Error != nil || result.RowsAffected != 1 {
				return false
			}
			admin.RecoveryCodes = string(updated)
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
	ServerAddressIPv4   *string                      `json:"server_address_ipv4"`
	ServerAddressIPv6   *string                      `json:"server_address_ipv6"`
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
	if payload.ServerAddressIPv4 != nil {
		target.ServerAddressIPv4 = normalizeInboundLiteralIP(*payload.ServerAddressIPv4)
	}
	if payload.ServerAddressIPv6 != nil {
		target.ServerAddressIPv6 = normalizeInboundLiteralIP(*payload.ServerAddressIPv6)
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

// normalizeInboundLiteralIP accepts cosmetic brackets and whitespace around a
// literal address, then stores net.IP's canonical spelling. It deliberately
// leaves malformed input intact so validation rejects it rather than silently
// treating a supplied invalid value as an empty optional field.
func normalizeInboundLiteralIP(raw string) string {
	value := strings.TrimSpace(raw)
	candidate := value
	if len(value) >= 2 && value[0] == '[' && value[len(value)-1] == ']' {
		candidate = strings.TrimSpace(value[1 : len(value)-1])
	}
	if ip := net.ParseIP(candidate); ip != nil {
		return ip.String()
	}
	return value
}

func validateInboundLiteralIP(field, value string, ipv4 bool) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	ip := net.ParseIP(value)
	if ip == nil {
		return fmt.Sprintf("%s must be a literal IP address without a port, zone, or hostname", field)
	}
	if ip.IsUnspecified() || ip.IsMulticast() {
		return fmt.Sprintf("%s must be a dialable unicast address", field)
	}
	if ipv4 && ip.To4() == nil {
		return fmt.Sprintf("%s must be an IPv4 address", field)
	}
	if !ipv4 && ip.To4() != nil {
		return fmt.Sprintf("%s must be an IPv6 address", field)
	}
	return ""
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
	if msg := validateInboundLiteralIP("server_address_ipv4", target.ServerAddressIPv4, true); msg != "" {
		return msg
	}
	if msg := validateInboundLiteralIP("server_address_ipv6", target.ServerAddressIPv6, false); msg != "" {
		return msg
	}
	// Port conflict check — exclude self (target.ID == 0 on create, non-zero on update)
	var conflict model.Inbound
	if res := config.DB.Where("port = ? AND id != ? AND deleted_at IS NULL", target.Port, target.ID).First(&conflict); res.Error == nil {
		return fmt.Sprintf("Port %d is already used by inbound '%s'", target.Port, conflict.Tag)
	}
	if strings.TrimSpace(target.ServerAddress) == "" && strings.TrimSpace(target.ServerAddressIPv4) == "" && strings.TrimSpace(target.ServerAddressIPv6) == "" && !inboundHasDerivedPublicHost(target.Stream) {
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
func SetupRoutes(r *gin.Engine, dm *docker.Manager, xm *proxy.XrayManager, sm *proxy.SingboxManager, sched *scheduler.Scheduler, tm *traffic.Monitor, eg *traffic.EgressCollector) {

	// Browser hardening and strict same/loopback-origin CORS policy.
	r.Use(securityHeaders())

	// Limit request body size to 10 MB to prevent resource exhaustion
	r.Use(func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 10<<20)
		c.Next()
	})

	// IP Whitelist — applied before setup guard so non-whitelisted IPs see 404
	r.Use(middleware.IPWhitelistMiddleware())

	// Apply Setup Guard Globally
	r.Use(middleware.SetupGuardMiddleware())

	// The embedded UI is same-origin. Cross-origin access is limited to exact
	// loopback hosts for local Vite development.
	r.Use(panelCORS())

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
			// Once setup has completed (or after any restart post-setup) the
			// one-time token is empty; without this guard an empty password
			// would match it and mint an admin-equivalent JWT.
			if cfg.IsSetupComplete || cfg.SetupOneTimeToken == "" {
				c.JSON(http.StatusNotFound, gin.H{"code": 404, "msg": "Endpoint not found"})
				return
			}
			// Constant-time comparison to prevent timing attacks on the one-time password
			if subtle.ConstantTimeCompare([]byte(req.Password), []byte(cfg.SetupOneTimeToken)) != 1 {
				c.JSON(http.StatusUnauthorized, gin.H{"code": 401, "msg": "Invalid one-time password"})
				return
			}

			// Setup tokens are purpose-bound and cannot access /api/v1.
			token, err := jwtutil.GenerateSetupToken(time.Minute * 30)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "Failed to generate token"})
				return
			}
			setSessionCookie(c, token, 30*time.Minute)
			c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "Setup login successful"})
		})

		setupGroup.POST("/complete", middleware.JWTAuthMiddleware(), func(c *gin.Context) {
			if config.GetConfig().IsSetupComplete || c.GetString("user_id") != middleware.SetupAdminUserID {
				c.JSON(http.StatusNotFound, gin.H{"code": 404, "msg": "Endpoint not found"})
				return
			}
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
			cfg.SetupOneTimeToken = "" // one-time: never usable again
			if req.PanelPath != "" {
				cfg.PanelPrefix = req.PanelPath
			}
			clearSessionCookie(c)
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
			ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
			defer cancel()
			if sqlDB, err := config.ReadDB().DB(); err != nil || sqlDB.PingContext(ctx) != nil {
				dbStatus = "error"
			}

			health := gin.H{
				"status":  "ok",
				"proxy":   proxyStatus,
				"db":      dbStatus,
				"version": version.Version,
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
		// WebSocket authentication is a one-time ticket consumed by the handler;
		// browser session JWTs never appear in URLs or proxy logs.
		apiGroup.GET("/terminal", terminal.HandleTerminalWebSocket)

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

			token, err := jwtutil.GenerateSessionToken(
				strconv.Itoa(int(admin.ID)),
				admin.Username,
				admin.SessionVersion,
				time.Now(),
				time.Hour*24,
			)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "Failed to generate token"})
				return
			}
			setSessionCookie(c, token, 24*time.Hour)
			c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "Login success", "data": gin.H{"user": gin.H{"id": admin.ID, "username": admin.Username}}})
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
	authGroup.Use(middleware.ScopeAuthorizationMiddleware())
	{
		authGroup.GET("/auth/session", func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "Success", "data": gin.H{
				"principal": c.GetString("principal"),
				"username":  c.GetString("username"),
			}})
		})
		authGroup.POST("/auth/logout", func(c *gin.Context) {
			clearSessionCookie(c)
			c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "Logged out"})
		})
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

		// CPU profile + proxy-relevant advice (hardware AES, hidden AES-NI…).
		authGroup.GET("/system/cpu", func(c *gin.Context) {
			info := cpuinfo.Detect()
			c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "Success", "data": gin.H{
				"cpu":    info,
				"advice": info.Advice(),
			}})
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
			tm.MarkViewed() // keep UI-only sampling on while someone is watching
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
			tm.MarkViewed()
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

		// Egress logger — per-(instance, user, destination) traffic history with
		// time-series, summaries, coverage badges, and config. Handlers live in
		// traffic_egress.go to keep this file readable.
		registerEgressRoutes(authGroup, eg)

		registerDockerRoutes(authGroup, dm)
		registerTerminalFileRoutes(authGroup)

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
			// Hard delete: a soft-deleted row keeps its tag in the unique
			// index, so an inbound could never be recreated under its name.
			if err := config.DB.Transaction(func(tx *gorm.DB) error {
				if err := tx.Unscoped().Delete(&model.Client{}, "inbound_id = ?", id).Error; err != nil {
					return err
				}
				return tx.Unscoped().Delete(&model.Inbound{}, "id = ?", id).Error
			}); err != nil {
				c.JSON(500, gin.H{"code": 500, "msg": "Failed to delete inbound"})
				return
			}
			sub.InvalidateSubCache()
			EnsurePanelPortsOpen() // close the deleted node's port
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
			if err := config.DB.Unscoped().Delete(&model.Client{}, "id = ?", id).Error; err != nil {
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
				res := config.DB.Unscoped().Delete(&model.Client{}, "id IN ?", req.IDs)
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
			if err := config.DB.Unscoped().Delete(&model.Outbound{}, "id = ?", id).Error; err != nil {
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
			family := c.DefaultQuery("family", "ipv4")
			if family != "ipv4" && family != "ipv6" {
				c.JSON(400, gin.H{"code": 400, "msg": "family must be ipv4 or ipv6"})
				return
			}
			rules, err := firewall.ListRulesForFamily(family)
			if err != nil {
				log.Printf("Firewall list error: %v", err)
				c.JSON(500, gin.H{"code": 500, "msg": "Failed to list firewall rules"})
				return
			}
			c.JSON(200, gin.H{"code": 200, "msg": "Success", "data": rules})
		})

		authGroup.POST("/firewall/rules", func(c *gin.Context) {
			var req struct {
				Family   string `json:"family"`
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
			if req.Family != "" && req.Family != "ipv4" && req.Family != "ipv6" {
				c.JSON(400, gin.H{"code": 400, "msg": "family must be ipv4 or ipv6"})
				return
			}
			if err := firewall.AddRuleForFamily(req.Family, req.Protocol, req.Port, req.Action, req.Source, req.Comment); err != nil {
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

		registerProxyRoutes(authGroup, xm, sm)
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
			if err := updateAdminSecurity(c.Request.Context(), admin, map[string]any{"password_hash": string(hash)}); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "Failed to update password"})
				return
			}
			clearSessionCookie(c)
			c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "Password changed successfully"})
			recordAudit(c, "admin.password_change", "")
		})

		// ======================================
		// OTA Update
		// ======================================
		registerUpdateRoutes(authGroup)

		registerSystemOptimizationRoutes(authGroup)
		registerSecurityRoutes(authGroup)
		// ======================================
		// Backup / Restore
		// ======================================
		registerBackupRoutes(authGroup, sched, xm, sm)

		registerSiteRoutes(authGroup, func(ctx context.Context) error { return proxy.ApplyRuntime(ctx, xm, sm, "auto") })

		// Public subscription listener (admin-scoped settings).
		registerSubscriptionServerRoutes(authGroup, func() string { return config.GetSetting("port") })

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
			// The external runner is restarted only through ApplyRuntime so the
			// config write, per-engine markers, and post-restart probes remain a
			// single verified transition. Direct manager Restart would only report
			// an unsupported local operation.
			if proxy.ExternalRuntimeEnabled() {
				ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Minute)
				err := proxy.ApplyRuntime(ctx, xm, sm, "auto")
				cancel()
				if err != nil {
					c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "Ad-block setting saved but external proxy update failed: " + err.Error()})
					return
				}
				recordAudit(c, "adblock.toggle", val)
				c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "Ad-block " + val, "data": gin.H{"enabled": req.Enabled}})
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
		registerMetricsRoute(authGroup, xm, sm, eg)
	}

	// Start background lockout cleanup and rate-limiter map GC
	startLockoutCleanup()
	startLimiterCleanup()
}
