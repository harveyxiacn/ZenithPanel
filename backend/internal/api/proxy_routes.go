package api

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/config"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/model"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/service/cert"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/service/diagnostic"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/service/proxy"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/service/selftest"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/service/sub"
)

func registerProxyRoutes(authGroup *gin.RouterGroup, xm *proxy.XrayManager, sm *proxy.SingboxManager) {
	// ======================================
	// Proxy Core Management
	// ======================================
	proxyGroup := authGroup.Group("/proxy")
	proxyGroup.GET("/readiness", func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
		defer cancel()
		var inbounds []model.Inbound
		if err := config.ReadDB().WithContext(ctx).Where("enable = ?", true).Find(&inbounds).Error; err != nil {
			c.JSON(503, gin.H{"code": 503, "msg": "database unavailable"})
			return
		}
		mode := config.GetSetting("proxy_runtime_mode")
		if mode == "" {
			mode = "auto"
		}
		for _, in := range inbounds {
			if ctx.Err() != nil {
				c.JSON(503, gin.H{"code": 503, "msg": "readiness timed out"})
				return
			}
			xrayRunning := xm != nil && xm.Status()
			singboxRunning := sm != nil && sm.Status()
			needsXray := mode != "singbox" && proxy.IsXraySupported(in.Protocol)
			needsSingbox := mode == "singbox" || (mode == "auto" && !proxy.IsXraySupported(in.Protocol))
			if (needsXray && !xrayRunning) || (needsSingbox && !singboxRunning) {
				c.JSON(503, gin.H{"code": 503, "msg": "required proxy engine is stopped"})
				return
			}
			if mode == "xray" && !proxy.IsXraySupported(in.Protocol) {
				continue
			}
			if probe := diagnostic.ProbeInbound(in); !probe.OK {
				c.JSON(503, gin.H{"code": 503, "msg": fmt.Sprintf("inbound %s: %s", in.Tag, probe.Stage)})
				return
			}
		}
		c.JSON(200, gin.H{"code": 200, "data": gin.H{"ready": true, "checked_inbounds": len(inbounds)}})
	})

	{
		proxyGroup.GET("/status", func(c *gin.Context) {
			var enabledInbounds int64
			var enabledClients int64
			var enabledRules int64

			config.ReadDB().WithContext(c.Request.Context()).Model(&model.Inbound{}).Where("enable = ?", true).Count(&enabledInbounds)
			config.ReadDB().WithContext(c.Request.Context()).Model(&model.Client{}).
				Joins("JOIN inbounds ON inbounds.id = clients.inbound_id AND inbounds.deleted_at IS NULL").
				Where("clients.enable = ? AND inbounds.enable = ?", true, true).
				Count(&enabledClients)
			config.ReadDB().WithContext(c.Request.Context()).Model(&model.RoutingRule{}).Where("enable = ?", true).Count(&enabledRules)

			dualMode := xm.IsDualMode() || sm.IsDualMode()
			data := gin.H{
				"xray_running":     xm.Status(),
				"singbox_running":  sm.Status(),
				"enabled_inbounds": enabledInbounds,
				"enabled_clients":  enabledClients,
				"enabled_rules":    enabledRules,
				"dual_mode":        dualMode,
				"runtime":          proxy.RuntimeStatusFor(xm),
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
			engine := strings.ToLower(strings.TrimSpace(c.DefaultQuery("engine", "auto")))
			if engine != "auto" && engine != "" && engine != "xray" && engine != "singbox" && engine != "sing-box" {
				c.JSON(400, gin.H{"code": 400, "msg": "Unknown engine"})
				return
			}
			ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Minute)
			defer cancel()
			force := c.Query("force") == "1" || strings.EqualFold(c.Query("force"), "true")
			err := proxy.ApplyRuntime(ctx, xm, sm, engine, force)
			status := http.StatusOK
			message := "Proxy configuration applied and listeners verified"
			if proxy.ExternalRuntimeEnabled() {
				message = "Proxy configuration applied and external runner restarted; existing proxy connections were interrupted"
			}
			if err != nil {
				status = http.StatusInternalServerError
				message = err.Error()
			}
			data := gin.H{"runtime": proxy.RuntimeStatusFor(xm)}
			if xm != nil && sm != nil {
				data["xray_running"], data["singbox_running"] = xm.Status(), sm.Status()
				data["mode"] = engine
			}
			recordAudit(c, "proxy.apply", engine+": "+message)
			c.JSON(status, gin.H{"code": status, "msg": message, "data": data})
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
			family := strings.ToLower(strings.TrimSpace(c.Query("family")))
			if family != "" && family != "auto" && family != "ipv4" && family != "ipv6" && family != "4" && family != "6" {
				c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "family must be auto, ipv4, or ipv6"})
				return
			}
			result := diagnostic.ProbeInboundFamily(in, family)
			// A bound listener alone cannot catch bad Reality keys, an invalid
			// TLS chain, or a broken client link. Unless explicitly requested as
			// a quick port probe, exercise the first enabled client's exact
			// subscription URI through the local inbound.
			if result.OK && c.Query("quick") != "1" {
				var client model.Client
				if err := config.ReadDB().WithContext(c.Request.Context()).Where("inbound_id = ? AND enable = ?", in.ID, true).Order("id").First(&client).Error; err != nil {
					result.E2E = "skipped"
					result.Err = "no enabled client — add one to run the end-to-end test"
				} else if link := sub.ShareLink(in, client, sub.ResolveServerAddress(in, subFallbackHost(c))); link == "" {
					result.E2E = "skipped"
				} else if dialHost, _, err := diagnostic.ProbeDialHost(in, family); err != nil {
					// This should be unreachable because ProbeInboundFamily uses the
					// same selector and succeeded above, but avoid silently falling
					// back to IPv4 if future selection rules change.
					result.E2E = "failed"
					result.OK = false
					result.Stage = "address_family"
					result.Err = err.Error()
				} else {
					e2e := selftest.Run(c.Request.Context(), link, selftest.Options{Server: dialHost})
					result.ElapsedMs += e2e.LatencyMs
					result.E2ELatency = e2e.LatencyMs
					if e2e.OK {
						result.E2E = "passed"
						result.ExitIP = e2e.ExitIP
					} else {
						result.E2E = "failed"
						result.OK = false
						result.Stage = "proxy"
						result.Err = e2e.Err
					}
				}
			}
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

}
