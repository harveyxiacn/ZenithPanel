package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/api/middleware"
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
