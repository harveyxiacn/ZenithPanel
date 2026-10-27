package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestValidateScopeList(t *testing.T) {
	tests := []struct {
		in   string
		want string
		ok   bool
	}{
		{"", ScopeRead, true},
		{"read, write,read", "read,write", true},
		{"infra:read,terminal", "infra:read,terminal", true},
		{"*", "*", true},
		{"*,read", "", false},
		{"reed", "", false},
	}
	for _, tc := range tests {
		got, ok := ValidateScopeList(tc.in)
		if got != tc.want || ok != tc.ok {
			t.Errorf("ValidateScopeList(%q)=(%q,%v), want (%q,%v)", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func TestRequiredScopeMatrix(t *testing.T) {
	tests := []struct{ method, path, want string }{
		{http.MethodGet, "/api/v1/inbounds", ScopeRead},
		{http.MethodPost, "/api/v1/inbounds", ScopeWrite},
		{http.MethodPost, "/api/v1/proxy/apply", ScopeProxyApply},
		{http.MethodGet, "/api/v1/docker/containers", ScopeInfraRead},
		{http.MethodPost, "/api/v1/docker/containers/x/start", ScopeInfraWrite},
		{http.MethodGet, "/api/v1/fs/read", ScopeInfraRead},
		{http.MethodPost, "/api/v1/fs/write", ScopeInfraWrite},
		{http.MethodGet, "/api/v1/terminal", ScopeTerminal},
		{http.MethodPost, "/api/v1/terminal/ticket", ScopeTerminal},
		{http.MethodGet, "/api/v1/admin/backup/export", ScopeAdmin},
		{http.MethodPost, "/api/v1/system/update/apply", ScopeAdmin},
		{http.MethodPost, "/api/v1/proxy/test-connection", ScopeRead},
		{http.MethodOptions, "/api/v1/inbounds", ""},
	}
	for _, tc := range tests {
		if got := requiredScope(tc.method, tc.path); got != tc.want {
			t.Errorf("requiredScope(%s,%s)=%q, want %q", tc.method, tc.path, got, tc.want)
		}
	}
}

func TestScopeAuthorizationMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("scopes", c.GetHeader("X-Test-Scopes"))
		c.Next()
	})
	r.Use(ScopeAuthorizationMiddleware())
	r.GET("/api/v1/inbounds", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	r.GET("/api/v1/terminal", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	r.POST("/api/v1/terminal/ticket", func(c *gin.Context) { c.Status(http.StatusNoContent) })

	request := func(path, scopes string) int {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("X-Test-Scopes", scopes)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec.Code
	}
	if got := request("/api/v1/inbounds", ScopeRead); got != http.StatusNoContent {
		t.Fatalf("read token denied read route: %d", got)
	}
	if got := request("/api/v1/terminal", ScopeRead); got != http.StatusForbidden {
		t.Fatalf("read token reached terminal: %d", got)
	}
	if got := request("/api/v1/terminal", ScopeTerminal); got != http.StatusNoContent {
		t.Fatalf("terminal token denied terminal route: %d", got)
	}
	if got := request("/api/v1/terminal", ScopeAdmin); got != http.StatusNoContent {
		t.Fatalf("admin token denied terminal route: %d", got)
	}

	requestPost := func(scopes string) int {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/terminal/ticket", nil)
		req.Header.Set("X-Test-Scopes", scopes)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec.Code
	}
	if got := requestPost(ScopeWrite); got != http.StatusForbidden {
		t.Fatalf("write token reached terminal ticket route: %d", got)
	}
	if got := requestPost(ScopeTerminal); got != http.StatusNoContent {
		t.Fatalf("terminal token denied ticket route: %d", got)
	}
}
