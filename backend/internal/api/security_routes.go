package api

import (
	"bytes"
	"context"
	"crypto/rand"
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
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/config"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/model"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/pkg/jwtutil"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/service/notify"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/service/updater"
	"github.com/pquerna/otp/totp"
	"golang.org/x/crypto/bcrypt"
)

func registerSecurityRoutes(authGroup *gin.RouterGroup) {
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
		result := config.DB.Model(&model.AdminUser{}).Where("id = ? AND session_version = ? AND totp_enabled = ?", admin.ID, admin.SessionVersion, false).Updates(map[string]any{"totp_secret": admin.TOTPSecret, "recovery_codes": admin.RecoveryCodes})
		if result.Error != nil {
			c.JSON(500, gin.H{"code": 500, "msg": "Failed to save"})
			return
		}
		if result.RowsAffected != 1 {
			c.JSON(409, gin.H{"code": 409, "msg": "Security settings changed; sign in and try again"})
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
		if err := updateAdminSecurity(c.Request.Context(), admin, map[string]any{"totp_enabled": true}); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "Failed to enable 2FA"})
			return
		}
		clearSessionCookie(c)
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
		if err := updateAdminSecurity(c.Request.Context(), admin, map[string]any{"totp_enabled": false, "totp_secret": "", "recovery_codes": ""}); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "Failed to disable 2FA"})
			return
		}
		clearSessionCookie(c)
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
		if !strings.HasPrefix(c.GetString("principal"), "admin:") {
			c.JSON(http.StatusForbidden, gin.H{"code": 403, "msg": "JWT administrator session required"})
			return
		}
		username, _ := c.Get("username")
		userID, _ := c.Get("user_id")
		usernameStr, _ := username.(string)
		userIDStr, _ := userID.(string)
		sessionVersion := c.GetUint64("session_version")
		loggedInAt := time.Unix(c.GetInt64("logged_in_at"), 0)
		remaining := time.Until(loggedInAt.Add(jwtutil.MaxSessionAge))
		if remaining <= 0 {
			clearSessionCookie(c)
			c.JSON(http.StatusUnauthorized, gin.H{"code": 401, "msg": "Session maximum lifetime reached; sign in again"})
			return
		}
		lifetime := 24 * time.Hour
		if remaining < lifetime {
			lifetime = remaining
		}

		token, err := jwtutil.GenerateSessionToken(userIDStr, usernameStr, sessionVersion, loggedInAt, lifetime)
		if err != nil {
			c.JSON(500, gin.H{"code": 500, "msg": "Token generation failed"})
			return
		}
		setSessionCookie(c, token, lifetime)
		c.JSON(200, gin.H{"code": 200, "msg": "Session refreshed"})
	})

	// Revoke every browser session for the current administrator, including a
	// stolen Bearer JWT. API tokens are separate credentials and are untouched.
	authGroup.POST("/auth/logout-all", func(c *gin.Context) {
		if !strings.HasPrefix(c.GetString("principal"), "admin:") {
			c.JSON(http.StatusForbidden, gin.H{"code": 403, "msg": "JWT administrator session required"})
			return
		}
		userID := c.GetString("user_id")
		var admin model.AdminUser
		if err := config.DB.First(&admin, userID).Error; err != nil {
			c.JSON(http.StatusNotFound, gin.H{"code": 404, "msg": "Admin not found"})
			return
		}
		if err := updateAdminSecurity(c.Request.Context(), admin, map[string]any{}); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "Failed to revoke sessions"})
			return
		}
		clearSessionCookie(c)
		recordAudit(c, "auth.logout_all", "")
		c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "All sessions revoked"})
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

}
