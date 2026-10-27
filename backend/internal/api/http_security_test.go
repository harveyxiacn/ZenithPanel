package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestLoopbackOriginUsesExactHostname(t *testing.T) {
	tests := []struct {
		origin string
		want   bool
	}{
		{"http://localhost:5173", true},
		{"https://127.0.0.1", true},
		{"http://[::1]:5173", true},
		{"http://localhost.evil.example", false},
		{"javascript://localhost", false},
		{"not a url", false},
	}
	for _, tc := range tests {
		if got := isLoopbackOrigin(tc.origin); got != tc.want {
			t.Errorf("isLoopbackOrigin(%q)=%v, want %v", tc.origin, got, tc.want)
		}
	}
}

func TestConfigureTrustedProxiesDefaultsToRemoteAddr(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	if err := ConfigureTrustedProxies(r, ""); err != nil {
		t.Fatal(err)
	}
	r.GET("/ip", func(c *gin.Context) { c.String(http.StatusOK, c.ClientIP()) })
	req := httptest.NewRequest(http.MethodGet, "/ip", nil)
	req.RemoteAddr = "203.0.113.5:1234"
	req.Header.Set("X-Forwarded-For", "127.0.0.1")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Body.String() != "203.0.113.5" {
		t.Fatalf("spoofed forwarded IP was trusted: %q", rec.Body.String())
	}
}

func TestConfigureTrustedProxiesRejectsInvalidInput(t *testing.T) {
	if err := ConfigureTrustedProxies(gin.New(), "not-an-ip"); err == nil {
		t.Fatal("expected invalid trusted proxy configuration to fail")
	}
}

func TestSecurityHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(securityHeaders())
	r.GET("/", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	for _, name := range []string{"Content-Security-Policy", "Permissions-Policy", "X-Content-Type-Options"} {
		if rec.Header().Get(name) == "" {
			t.Errorf("missing security header %s", name)
		}
	}
}
