package api

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/config"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/model"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/service/proxy"
)

// metricsBootTime is captured at package init so `zenithpanel_uptime_seconds`
// is monotonic and survives panel reloads of the route table. (Package vars
// initialize once even when registerMetricsRoute runs multiple times in tests.)
var metricsBootTime = time.Now()

// registerMetricsRoute wires GET /api/v1/metrics, a Prometheus text-format
// endpoint covering the panel-level signals operators care about:
//
//   - zenithpanel_uptime_seconds         — gauge, seconds since boot
//   - zenithpanel_xray_running           — gauge, 0/1
//   - zenithpanel_singbox_running        — gauge, 0/1
//   - zenithpanel_dual_mode              — gauge, 0/1
//   - zenithpanel_enabled_inbounds       — gauge
//   - zenithpanel_enabled_clients        — gauge
//   - zenithpanel_enabled_rules          — gauge
//   - zenithpanel_handed_off_singbox     — gauge, # protocols Sing-box serves
//     in dual mode
//   - zenithpanel_api_tokens{state="…"}  — gauge, by active/revoked
//
// The endpoint requires authentication (token or local-root), matching every
// other /api/v1/* route. Operators wiring Prometheus should mint a read-only
// API token in the panel and configure scrape with `bearer_token`.
func registerMetricsRoute(g *gin.RouterGroup, xm *proxy.XrayManager, sm *proxy.SingboxManager) {
	g.GET("/metrics", func(c *gin.Context) {
		var enabledInbounds, enabledClients, enabledRules int64
		config.DB.Model(&model.Inbound{}).Where("enable = ?", true).Count(&enabledInbounds)
		config.DB.Model(&model.Client{}).
			Joins("JOIN inbounds ON inbounds.id = clients.inbound_id AND inbounds.deleted_at IS NULL").
			Where("clients.enable = ? AND inbounds.enable = ?", true, true).
			Count(&enabledClients)
		config.DB.Model(&model.RoutingRule{}).Where("enable = ?", true).Count(&enabledRules)

		var activeTokens, revokedTokens int64
		config.DB.Model(&model.ApiToken{}).Where("revoked = ?", false).Count(&activeTokens)
		config.DB.Model(&model.ApiToken{}).Where("revoked = ?", true).Count(&revokedTokens)

		xrayRunning := xm.Status()
		singboxRunning := sm.Status()
		dualMode := xm.IsDualMode() || sm.IsDualMode()
		var handedOff int
		if dualMode {
			handedOff = len(xm.SkippedProtocols())
		}

		var b strings.Builder
		write := func(name, help, typ string, value float64) {
			fmt.Fprintf(&b, "# HELP %s %s\n", name, help)
			fmt.Fprintf(&b, "# TYPE %s %s\n", name, typ)
			fmt.Fprintf(&b, "%s %g\n", name, value)
		}
		writeLabeled := func(name, label string, value float64) {
			fmt.Fprintf(&b, "%s{%s} %g\n", name, label, value)
		}

		write("zenithpanel_uptime_seconds",
			"Seconds since the panel process started.",
			"gauge", time.Since(metricsBootTime).Seconds())
		write("zenithpanel_xray_running",
			"1 when the Xray engine is currently running, 0 otherwise.",
			"gauge", b01(xrayRunning))
		write("zenithpanel_singbox_running",
			"1 when the Sing-box engine is currently running, 0 otherwise.",
			"gauge", b01(singboxRunning))
		write("zenithpanel_dual_mode",
			"1 when both engines cooperate on disjoint partitions, 0 in single-engine mode.",
			"gauge", b01(dualMode))
		write("zenithpanel_enabled_inbounds",
			"Number of enabled (not soft-deleted) inbound listeners.",
			"gauge", float64(enabledInbounds))
		write("zenithpanel_enabled_clients",
			"Number of enabled clients across all enabled inbounds.",
			"gauge", float64(enabledClients))
		write("zenithpanel_enabled_rules",
			"Number of enabled routing rules.",
			"gauge", float64(enabledRules))
		write("zenithpanel_handed_off_singbox",
			"Number of inbounds handed off from Xray to Sing-box in dual mode.",
			"gauge", float64(handedOff))

		// Labeled tokens series. Emit HELP/TYPE once, then both label values.
		fmt.Fprintf(&b, "# HELP zenithpanel_api_tokens Number of API tokens, broken down by state.\n")
		fmt.Fprintf(&b, "# TYPE zenithpanel_api_tokens gauge\n")
		writeLabeled("zenithpanel_api_tokens", `state="active"`, float64(activeTokens))
		writeLabeled("zenithpanel_api_tokens", `state="revoked"`, float64(revokedTokens))

		c.Header("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		c.String(http.StatusOK, b.String())
	})
}

func b01(v bool) float64 {
	if v {
		return 1
	}
	return 0
}
