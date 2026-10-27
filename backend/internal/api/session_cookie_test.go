package api

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/api/middleware"
)

func TestSessionCookieIsHttpOnlyStrictAndSecureOnHTTPS(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest("GET", "https://panel.example/api/v1/login", nil)
	setSessionCookie(ctx, "secret", time.Hour)
	header := recorder.Header().Get("Set-Cookie")
	for _, required := range []string{middleware.SessionCookieName + "=secret", "HttpOnly", "SameSite=Strict", "Secure", "Path=/"} {
		if !strings.Contains(header, required) {
			t.Fatalf("Set-Cookie %q missing %q", header, required)
		}
	}
}

func TestForwardedHTTPSRequiresConfiguredProxyPeer(t *testing.T) {
	gin.SetMode(gin.TestMode)
	if err := ConfigureTrustedProxies(gin.New(), "10.0.0.0/8"); err != nil {
		t.Fatal(err)
	}
	for _, testCase := range []struct {
		remote string
		secure bool
	}{
		{remote: "203.0.113.5:1234", secure: false},
		{remote: "10.1.2.3:1234", secure: true},
	} {
		recorder := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(recorder)
		ctx.Request = httptest.NewRequest("GET", "http://panel.example/api/v1/login", nil)
		ctx.Request.RemoteAddr = testCase.remote
		ctx.Request.Header.Set("X-Forwarded-Proto", "https")
		setSessionCookie(ctx, "secret", time.Hour)
		gotSecure := strings.Contains(recorder.Header().Get("Set-Cookie"), "Secure")
		if gotSecure != testCase.secure {
			t.Errorf("remote %s secure=%v, want %v", testCase.remote, gotSecure, testCase.secure)
		}
	}
}
