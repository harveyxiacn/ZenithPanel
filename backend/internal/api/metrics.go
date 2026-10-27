package api

import (
	"fmt"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/service/traffic"
	"net/http"
	"runtime"
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
func registerMetricsRoute(g *gin.RouterGroup, xm *proxy.XrayManager, sm *proxy.SingboxManager, collectors ...*traffic.EgressCollector) {
	g.GET("/metrics", func(c *gin.Context) {
		var enabledInbounds, enabledClients, enabledRules int64
		config.ReadDB().WithContext(c.Request.Context()).Model(&model.Inbound{}).Where("enable = ?", true).Count(&enabledInbounds)
		config.ReadDB().WithContext(c.Request.Context()).Model(&model.Client{}).
			Joins("JOIN inbounds ON inbounds.id = clients.inbound_id AND inbounds.deleted_at IS NULL").
			Where("clients.enable = ? AND inbounds.enable = ?", true, true).
			Count(&enabledClients)
		config.ReadDB().WithContext(c.Request.Context()).Model(&model.RoutingRule{}).Where("enable = ?", true).Count(&enabledRules)

		var activeTokens, revokedTokens int64
		config.ReadDB().WithContext(c.Request.Context()).Model(&model.ApiToken{}).Where("revoked = ?", false).Count(&activeTokens)
		config.ReadDB().WithContext(c.Request.Context()).Model(&model.ApiToken{}).Where("revoked = ?", true).Count(&revokedTokens)

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

		var memory runtime.MemStats
		runtime.ReadMemStats(&memory)
		write("zenithpanel_heap_alloc_bytes", "Allocated Go heap bytes.", "gauge", float64(memory.HeapAlloc))
		write("zenithpanel_goroutines", "Live Go goroutines.", "gauge", float64(runtime.NumGoroutine()))
		if len(collectors) > 0 && collectors[0] != nil {
			status := collectors[0].Status()
			write("zenithpanel_egress_pending_keys", "Pending destination keys.", "gauge", float64(status.Pending))
			write("zenithpanel_egress_retry_keys", "Destination keys awaiting retry.", "gauge", float64(status.Retry))
			write("zenithpanel_egress_dropped_keys_total", "Destination updates dropped at capacity.", "counter", float64(status.Dropped))
			write("zenithpanel_egress_retries_total", "Failed destination write batches.", "counter", float64(status.Retries))
		}
		write("zenithpanel_uptime_seconds",
			"Seconds since the panel process started.",
			"gauge", time.Since(metricsBootTime).Seconds())
		write("zenithpanel_xray_running",
			"1 when the Xray engine is currently running, 0 otherwise.",
			"gauge", b01(xrayRunning))
		write("zenithpanel_xray_uptime_seconds",
			"Seconds since the Xray engine was last (re)started. 0 if not running.",
			"gauge", xm.Uptime().Seconds())
		write("zenithpanel_singbox_running",
			"1 when the Sing-box engine is currently running, 0 otherwise.",
			"gauge", b01(singboxRunning))
		write("zenithpanel_singbox_uptime_seconds",
			"Seconds since the Sing-box engine was last (re)started. 0 if not running.",
			"gauge", sm.Uptime().Seconds())
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

		// database/sql exposes how often callers had to wait for the single
		// SQLite writer connection. These monotonic values make DB contention
		// visible without parsing SQLITE_BUSY log messages.
		if sqlDB, err := config.DB.DB(); err == nil {
			stats := sqlDB.Stats()
			write("zenithpanel_db_connection_wait_total",
				"Number of waits for the serialized SQLite connection.",
				"counter", float64(stats.WaitCount))
			write("zenithpanel_db_connection_wait_seconds_total",
				"Cumulative seconds spent waiting for the SQLite connection.",
				"counter", stats.WaitDuration.Seconds())
			write("zenithpanel_db_connections_in_use",
				"Current SQLite connections executing or held by callers.",
				"gauge", float64(stats.InUse))
		}

		// Labeled tokens series. Emit HELP/TYPE once, then both label values.
		fmt.Fprintf(&b, "# HELP zenithpanel_api_tokens Number of API tokens, broken down by state.\n")
		fmt.Fprintf(&b, "# TYPE zenithpanel_api_tokens gauge\n")
		writeLabeled("zenithpanel_api_tokens", `state="active"`, float64(activeTokens))
		writeLabeled("zenithpanel_api_tokens", `state="revoked"`, float64(revokedTokens))

		// Per-client traffic counters. Joined against inbounds so we have the
		// inbound tag for the label set — operators usually want to group
		// dashboards by inbound, not by email alone. `direction` separates
		// upload and download as is standard for Prometheus byte counters.
		type clientTrafficRow struct {
			Email    string
			Tag      string
			UpLoad   int64
			DownLoad int64
		}
		var clientRows []clientTrafficRow
		config.DB.
			Table("clients").
			Select("clients.email, inbounds.tag, clients.up_load, clients.down_load").
			Joins("JOIN inbounds ON inbounds.id = clients.inbound_id AND inbounds.deleted_at IS NULL").
			Where("clients.enable = ? AND inbounds.enable = ?", true, true).
			Scan(&clientRows)
		if len(clientRows) > 0 {
			fmt.Fprintf(&b, "# HELP zenithpanel_client_traffic_bytes Cumulative client traffic, in bytes, by inbound and direction.\n")
			fmt.Fprintf(&b, "# TYPE zenithpanel_client_traffic_bytes counter\n")
			for _, r := range clientRows {
				labels := fmt.Sprintf(`email=%q,inbound=%q`, r.Email, r.Tag)
				writeLabeled("zenithpanel_client_traffic_bytes", labels+`,direction="up"`, float64(r.UpLoad))
				writeLabeled("zenithpanel_client_traffic_bytes", labels+`,direction="down"`, float64(r.DownLoad))
			}
		}

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
