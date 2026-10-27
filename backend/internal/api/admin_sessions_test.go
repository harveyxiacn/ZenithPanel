package api

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/harveyxiacn/ZenithPanel/backend/internal/config"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/model"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/pkg/jwtutil"
	"golang.org/x/crypto/bcrypt"
)

func TestPasswordChangeRevokesBearerCookieAndRefresh(t *testing.T) {
	router, token := setupRouterValidationTestServer(t, true)
	hash, _ := bcrypt.GenerateFromPassword([]byte("old-password"), bcrypt.MinCost)
	if err := config.DB.Model(&model.AdminUser{}).Where("username = ?", "admin").Update("password_hash", string(hash)).Error; err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/change-password", bytes.NewBufferString(`{"old_password":"old-password","new_password":"new-password"}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("change password: %d %s", w.Code, w.Body.String())
	}
	for _, cookie := range []bool{false, true} {
		for _, path := range []string{"/api/v1/admin/access", "/api/v1/auth/refresh"} {
			method := http.MethodGet
			if path == "/api/v1/auth/refresh" {
				method = http.MethodPost
			}
			req = httptest.NewRequest(method, path, nil)
			if cookie {
				req.AddCookie(&http.Cookie{Name: "zenith_session", Value: token})
				req.Header.Set("Origin", "http://example.com")
			} else {
				req.Header.Set("Authorization", "Bearer "+token)
			}
			w = httptest.NewRecorder()
			router.ServeHTTP(w, req)
			if w.Code != 401 {
				t.Fatalf("old cookie=%v %s: %d %s", cookie, path, w.Code, w.Body.String())
			}
		}
	}
	var admin model.AdminUser
	if err := config.DB.First(&admin).Error; err != nil {
		t.Fatal(err)
	}
	fresh, err := jwtutil.GenerateSessionToken("1", "admin", admin.SessionVersion, time.Now(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	req = httptest.NewRequest(http.MethodGet, "/api/v1/admin/access", nil)
	req.Header.Set("Authorization", "Bearer "+fresh)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("fresh session: %d %s", w.Code, w.Body.String())
	}
}

func TestSecurityVersionCannotBeOverwrittenByConcurrentChange(t *testing.T) {
	setupRouterValidationTestServer(t, true)
	var admin model.AdminUser
	if err := config.DB.First(&admin).Error; err != nil {
		t.Fatal(err)
	}
	if err := updateAdminSecurity(context.Background(), admin, map[string]any{"password_hash": "first"}); err != nil {
		t.Fatal(err)
	}
	if err := updateAdminSecurity(context.Background(), admin, map[string]any{"password_hash": "stale"}); err == nil {
		t.Fatal("stale security write succeeded")
	}
	var current model.AdminUser
	config.DB.First(&current, admin.ID)
	if current.SessionVersion != admin.SessionVersion+1 || current.PasswordHash != "first" {
		t.Fatalf("stale update overwrote security state: %+v", current)
	}
}

func TestLogoutAllRevokesCurrentSession(t *testing.T) {
	router, token := setupRouterValidationTestServer(t, true)
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout-all", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		want := 200
		if i == 1 {
			want = 401
		}
		if w.Code != want {
			t.Fatalf("logout attempt %d: %d %s", i, w.Code, w.Body.String())
		}
	}
}

func TestDisablingTwoFactorRevokesExistingSession(t *testing.T) {
	router, token := setupRouterValidationTestServer(t, true)
	hash, _ := bcrypt.GenerateFromPassword([]byte("password-for-test"), bcrypt.MinCost)
	if err := config.DB.Model(&model.AdminUser{}).Where("username = ?", "admin").Updates(map[string]any{"password_hash": string(hash), "totp_enabled": true, "totp_secret": "test-secret"}).Error; err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/2fa/disable", bytes.NewBufferString(`{"password":"password-for-test"}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("disable: %d %s", w.Code, w.Body.String())
	}
	req = httptest.NewRequest(http.MethodGet, "/api/v1/auth/session", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != 401 {
		t.Fatalf("old session: %d", w.Code)
	}
}

func TestReadinessRejectsEnabledInboundWhenEnginesAreStopped(t *testing.T) {
	router, token := setupRouterValidationTestServer(t, true)
	if err := config.DB.AutoMigrate(&model.Inbound{}); err != nil {
		t.Fatal(err)
	}
	check := func(want int) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/proxy/readiness", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != want {
			t.Fatalf("ready: %d %s", w.Code, w.Body.String())
		}
	}
	check(200)
	if err := config.DB.Create(&model.Inbound{Tag: "must-run", Protocol: "vless", Port: 4443, Enable: true, Settings: "{}", Stream: "{}"}).Error; err != nil {
		t.Fatal(err)
	}
	check(503)
}
