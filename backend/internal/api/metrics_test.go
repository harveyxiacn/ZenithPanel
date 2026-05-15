package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/api/middleware"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/config"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/model"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/service/proxy"
)

// TestMetricsEndpointShape confirms the response is Prometheus-flavored text
// (one HELP, one TYPE, one value per metric), reachable via the same auth
// path as everything else under /api/v1, and includes the labeled tokens
// gauge.
//
// We mount the metrics handler under /api/v2/metrics on a side group so it
// doesn't collide with the api-tokens routes the fixture already registered
// under /api/v1.
func TestMetricsEndpointShape(t *testing.T) {
	r := apiTokenTestEngine(t)

	xm := proxy.NewXrayManager()
	sm := proxy.NewSingboxManager()
	gin.SetMode(gin.TestMode)
	side := r.Group("/api/v2")
	side.Use(middleware.AuthMiddleware())
	registerMetricsRoute(side, xm, sm)

	rec := fireRequest(r, "GET", "/api/v2/metrics", "", "", true)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()

	// Required metric names + labeled tokens series.
	wanted := []string{
		"zenithpanel_uptime_seconds",
		"zenithpanel_xray_running",
		"zenithpanel_singbox_running",
		"zenithpanel_enabled_inbounds",
		`zenithpanel_api_tokens{state="active"}`,
		`zenithpanel_api_tokens{state="revoked"}`,
	}
	// The fixture's DB has no inbounds/clients so the client-traffic series
	// is allowed to be absent. The pure-format invariants above still hold.
	for _, name := range wanted {
		if !strings.Contains(body, name) {
			t.Errorf("metric %q missing from response:\n%s", name, body)
		}
	}
	if !strings.Contains(body, "# HELP zenithpanel_uptime_seconds") {
		t.Error("expected HELP line for uptime metric")
	}
	if !strings.Contains(body, "# TYPE zenithpanel_uptime_seconds gauge") {
		t.Error("expected TYPE line for uptime metric")
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("Content-Type should be text/plain, got %q", ct)
	}
}

// TestMetricsClientTrafficSeries seeds a couple of enabled clients on an
// enabled inbound and asserts the per-client traffic counter shows up with
// the right labels and direction split.
func TestMetricsClientTrafficSeries(t *testing.T) {
	r := apiTokenTestEngine(t)

	// Seed: one inbound, two clients with non-zero traffic.
	ib := model.Inbound{Tag: "edge-1", Protocol: "vless", Port: 4443, Settings: "{}", Stream: "{}", Enable: true, ServerAddress: "127.0.0.1"}
	if err := config.DB.Create(&ib).Error; err != nil {
		t.Fatalf("seed inbound: %v", err)
	}
	c1 := model.Client{InboundID: ib.ID, Email: "alice@t", UUID: "u1", Enable: true, UpLoad: 1024, DownLoad: 4096}
	c2 := model.Client{InboundID: ib.ID, Email: "bob@t", UUID: "u2", Enable: true, UpLoad: 0, DownLoad: 0}
	if err := config.DB.Create(&c1).Error; err != nil {
		t.Fatalf("seed client1: %v", err)
	}
	if err := config.DB.Create(&c2).Error; err != nil {
		t.Fatalf("seed client2: %v", err)
	}

	xm := proxy.NewXrayManager()
	sm := proxy.NewSingboxManager()
	gin.SetMode(gin.TestMode)
	side := r.Group("/api/v3")
	side.Use(middleware.AuthMiddleware())
	registerMetricsRoute(side, xm, sm)

	rec := fireRequest(r, "GET", "/api/v3/metrics", "", "", true)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body := rec.Body.String()

	// HELP/TYPE for the counter, and both directions per client should show.
	for _, expected := range []string{
		"# HELP zenithpanel_client_traffic_bytes",
		"# TYPE zenithpanel_client_traffic_bytes counter",
		`zenithpanel_client_traffic_bytes{email="alice@t",inbound="edge-1",direction="up"} 1024`,
		`zenithpanel_client_traffic_bytes{email="alice@t",inbound="edge-1",direction="down"} 4096`,
		`zenithpanel_client_traffic_bytes{email="bob@t",inbound="edge-1",direction="up"} 0`,
	} {
		if !strings.Contains(body, expected) {
			t.Errorf("missing line %q in body:\n%s", expected, body)
		}
	}
}
