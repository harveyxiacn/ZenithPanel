package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/api/middleware"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/config"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/model"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/pkg/jwtutil"
	"gorm.io/gorm"
)

func TestNormalizeUsageProfileDefaultsToMixed(t *testing.T) {
	cases := map[string]string{
		"":                "mixed",
		"personal_proxy":  "personal_proxy",
		"vps_ops":         "vps_ops",
		"mixed":           "mixed",
		"weird":           "mixed",
		" PERSONAL_PROXY": "personal_proxy",
		"VPS_OPS ":        "vps_ops",
	}

	for input, want := range cases {
		if got := normalizeUsageProfile(input); got != want {
			t.Fatalf("normalizeUsageProfile(%q) = %q, want %q", input, got, want)
		}
	}
}

// setupLogin exchanges the one-time password for a setup-wizard JWT.
func setupLogin(t *testing.T, router *gin.Engine, password string) (int, string) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"password": password})
	req := httptest.NewRequest(http.MethodPost, "/api/setup/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	for _, cookie := range rec.Result().Cookies() {
		if cookie.Name == middleware.SessionCookieName {
			return rec.Code, cookie.Value
		}
	}
	return rec.Code, ""
}

func TestSetupLoginRejectedAfterSetupComplete(t *testing.T) {
	router, _ := setupRouterValidationTestServer(t, true)

	// Simulates a restart after setup: the one-time token is empty in memory.
	config.GetConfig().SetupOneTimeToken = ""
	if code, token := setupLogin(t, router, ""); code == http.StatusOK || token != "" {
		t.Fatalf("empty-password setup login after setup must fail, got %d token=%q", code, token)
	}
}

func TestSetupTokenCannotCallProtectedAPI(t *testing.T) {
	router, _ := setupRouterValidationTestServer(t, false)

	code, setupToken := setupLogin(t, router, "one-time-test-token")
	if code != http.StatusOK || setupToken == "" {
		t.Fatalf("setup login failed: %d", code)
	}
	// Pretend setup finished so the setup guard lets /api/v1 through.
	config.GetConfig().IsSetupComplete = true

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/access", nil)
	req.Header.Set("Authorization", "Bearer "+setupToken)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("setup token on /api/v1 should be 401, got %d", rec.Code)
	}
}

func TestSetupCompleteRequiresSetupToken(t *testing.T) {
	router, adminToken := setupRouterValidationTestServer(t, false)

	req := httptest.NewRequest(http.MethodPost, "/api/setup/complete", bytes.NewBufferString(`{"username":"adminuser","password":"password123"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+adminToken)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code == http.StatusOK {
		t.Fatalf("setup/complete must only accept setup-wizard tokens")
	}
}

func TestApplySetupCompletePersistsUsageProfile(t *testing.T) {
	router, _ := setupRouterValidationTestServer(t, false)
	token, err := jwtutil.GenerateSetupToken(time.Hour)
	if err != nil {
		t.Fatalf("generate setup token: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/setup/complete", bytes.NewBufferString(`{
		"username":"adminuser",
		"password":"password123",
		"usage_profile":"personal_proxy"
	}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if got := config.GetSetting("usage_profile"); got != "personal_proxy" {
		t.Fatalf("expected usage_profile=personal_proxy, got %q", got)
	}
	if code, _ := setupLogin(t, router, "one-time-test-token"); code == http.StatusOK {
		t.Fatalf("one-time password must be unusable after setup completes")
	}
}

func TestAdminAccessConfigRoundTripsUsageProfile(t *testing.T) {
	router, token := setupRouterValidationTestServer(t, true)

	if err := config.SetSetting("usage_profile", "not-a-profile"); err != nil {
		t.Fatalf("seed usage_profile: %v", err)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/admin/access", nil)
	getReq.Header.Set("Authorization", "Bearer "+token)
	getRec := httptest.NewRecorder()
	router.ServeHTTP(getRec, getReq)

	if getRec.Code != http.StatusOK {
		t.Fatalf("expected GET status 200, got %d body=%s", getRec.Code, getRec.Body.String())
	}
	var getResp struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(getRec.Body.Bytes(), &getResp); err != nil {
		t.Fatalf("decode GET response: %v", err)
	}
	if got, _ := getResp.Data["usage_profile"].(string); got != "mixed" {
		t.Fatalf("expected normalized usage_profile=mixed from GET, got %#v", getResp.Data["usage_profile"])
	}

	putReq := httptest.NewRequest(http.MethodPut, "/api/v1/admin/access", bytes.NewBufferString(`{
		"usage_profile":"VPS_OPS"
	}`))
	putReq.Header.Set("Content-Type", "application/json")
	putReq.Header.Set("Authorization", "Bearer "+token)
	putRec := httptest.NewRecorder()
	router.ServeHTTP(putRec, putReq)

	if putRec.Code != http.StatusOK {
		t.Fatalf("expected PUT status 200, got %d body=%s", putRec.Code, putRec.Body.String())
	}
	if got := config.GetSetting("usage_profile"); got != "vps_ops" {
		t.Fatalf("expected persisted usage_profile=vps_ops after PUT, got %q", got)
	}
}

func TestValidateInboundRequiresServerAddressWhenNoSafePublicHostExists(t *testing.T) {
	inbound := model.Inbound{
		Tag:      "vless-reality",
		Protocol: "vless",
		Port:     443,
		Settings: `{"decryption":"none","flow":"xtls-rprx-vision"}`,
		Stream: `{
			"network":"tcp",
			"security":"reality",
			"realitySettings":{
				"target":"www.microsoft.com:443",
				"serverNames":["www.microsoft.com"],
				"privateKey":"priv",
				"shortIds":["ab"]
			}
		}`,
	}

	if msg := validateInbound(inbound); msg == "" {
		t.Fatal("expected validation error when inbound has no explicit or derivable public host")
	}
}

func TestValidateInboundAllowsTLSDerivedPublicHostWithoutServerAddress(t *testing.T) {
	inbound := model.Inbound{
		Tag:      "trojan-tls",
		Protocol: "trojan",
		Port:     443,
		Settings: `{}`,
		Stream: `{
			"network":"tcp",
			"security":"tls",
			"tlsSettings":{
				"serverName":"edge.example.com"
			}
		}`,
	}

	if msg := validateInbound(inbound); msg != "" {
		t.Fatalf("expected inbound with TLS serverName to pass validation, got %q", msg)
	}
}

func TestValidateInboundRejectsNonIPListenAddress(t *testing.T) {
	inbound := model.Inbound{
		Tag:           "vless-reality",
		Protocol:      "vless",
		Listen:        "Listen",
		ServerAddress: "vpn.example.com",
		Port:          8443,
		Settings:      `{"decryption":"none"}`,
	}

	if msg := validateInbound(inbound); msg == "" {
		t.Fatal("expected validation error for non-IP listen address")
	}
}

func TestValidateInboundAllowsIPAddressListenAddress(t *testing.T) {
	inbound := model.Inbound{
		Tag:           "vless-reality",
		Protocol:      "vless",
		Listen:        "0.0.0.0",
		ServerAddress: "vpn.example.com",
		Port:          8443,
		Settings:      `{"decryption":"none"}`,
	}

	if msg := validateInbound(inbound); msg != "" {
		t.Fatalf("expected valid IP listen address to pass validation, got %q", msg)
	}
}

func TestValidateInboundTrimsListenAddressBeforeValidation(t *testing.T) {
	inbound := model.Inbound{
		Tag:           "vless-reality",
		Protocol:      "vless",
		Listen:        "  127.0.0.1  ",
		ServerAddress: "vpn.example.com",
		Port:          8443,
		Settings:      `{"decryption":"none"}`,
	}

	if msg := validateInbound(inbound); msg != "" {
		t.Fatalf("expected trimmed listen address to pass validation, got %q", msg)
	}
}

func TestInboundAPIPersistsDualStackLiteralAddresses(t *testing.T) {
	router, token := setupRouterValidationTestServer(t, true)

	create := httptest.NewRequest(http.MethodPost, "/api/v1/inbounds", bytes.NewBufferString(`{
		"tag":"dual-stack", "protocol":"vless", "port":24443, "network":"tcp",
		"server_address":"legacy.example.test",
		"server_address_ipv4":" [203.0.113.9] ",
		"server_address_ipv6":" [2001:0DB8:0:0:0:0:0:9] ",
		"settings":"{}", "stream":"{}", "enable":true
	}`))
	create.Header.Set("Content-Type", "application/json")
	create.Header.Set("Authorization", "Bearer "+token)
	createRec := httptest.NewRecorder()
	router.ServeHTTP(createRec, create)
	if createRec.Code != http.StatusOK {
		t.Fatalf("create inbound: status=%d body=%s", createRec.Code, createRec.Body.String())
	}

	var created struct {
		Data model.Inbound `json:"data"`
	}
	if err := json.Unmarshal(createRec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if created.Data.ServerAddressIPv4 != "203.0.113.9" || created.Data.ServerAddressIPv6 != "2001:db8::9" {
		t.Fatalf("create did not normalize dual-stack endpoints: %#v", created.Data)
	}

	var persisted model.Inbound
	if err := config.DB.First(&persisted, created.Data.ID).Error; err != nil {
		t.Fatalf("load persisted inbound: %v", err)
	}
	if persisted.ServerAddressIPv4 != "203.0.113.9" || persisted.ServerAddressIPv6 != "2001:db8::9" {
		t.Fatalf("persisted dual-stack endpoints = %#v", persisted)
	}

	update := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/v1/inbounds/%d", created.Data.ID), bytes.NewBufferString(`{"server_address_ipv6":" [2001:db8::10] "}`))
	update.Header.Set("Content-Type", "application/json")
	update.Header.Set("Authorization", "Bearer "+token)
	updateRec := httptest.NewRecorder()
	router.ServeHTTP(updateRec, update)
	if updateRec.Code != http.StatusOK {
		t.Fatalf("update inbound: status=%d body=%s", updateRec.Code, updateRec.Body.String())
	}
	if err := config.DB.First(&persisted, created.Data.ID).Error; err != nil {
		t.Fatalf("reload updated inbound: %v", err)
	}
	if persisted.ServerAddress != "legacy.example.test" || persisted.ServerAddressIPv4 != "203.0.113.9" || persisted.ServerAddressIPv6 != "2001:db8::10" {
		t.Fatalf("update failed to preserve/update addresses: %#v", persisted)
	}
}

func TestValidateInboundRejectsInvalidDualStackLiteralAddresses(t *testing.T) {
	router, token := setupRouterValidationTestServer(t, true)
	cases := []struct {
		name string
		v4   string
		v6   string
	}{
		{name: "ipv4 hostname", v4: "vpn.example.test"},
		{name: "ipv4 port", v4: "203.0.113.9:443"},
		{name: "ipv4 unspecified", v4: "0.0.0.0"},
		{name: "ipv4 multicast", v4: "224.0.0.1"},
		{name: "ipv4 wrong family", v4: "2001:db8::9"},
		{name: "ipv6 zone", v6: "fe80::1%eth0"},
		{name: "ipv6 port", v6: "[2001:db8::9]:443"},
		{name: "ipv6 unspecified", v6: "::"},
		{name: "ipv6 multicast", v6: "ff02::1"},
		{name: "ipv6 wrong family", v6: "203.0.113.9"},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body, err := json.Marshal(map[string]any{
				"tag":                 fmt.Sprintf("invalid-dual-stack-%d", i),
				"protocol":            "vless",
				"port":                25443 + i,
				"server_address":      "legacy.example.test",
				"server_address_ipv4": tc.v4,
				"server_address_ipv6": tc.v6,
				"settings":            "{}",
				"stream":              "{}",
			})
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodPost, "/api/v1/inbounds", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+token)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected API rejection for %#v, status=%d body=%s", tc, rec.Code, rec.Body.String())
			}
		})
	}
}

func setupRouterValidationTestServer(t *testing.T, setupComplete bool) (*gin.Engine, string) {
	t.Helper()

	db := setupRouterValidationTestDB(t)
	config.DB = db

	cfg := config.GetConfig()
	cfg.IsSetupComplete = setupComplete
	cfg.PanelPrefix = "/"
	cfg.SetupURLSuffix = "testsuffix"
	cfg.SetupOneTimeToken = "one-time-test-token"

	if setupComplete {
		if err := db.Create(&model.AdminUser{Username: "admin", PasswordHash: "test-hash"}).Error; err != nil {
			t.Fatal(err)
		}
	}
	jwtutil.InitSecret([]byte("0123456789abcdef0123456789abcdef"))
	token, err := jwtutil.GenerateToken("1", "admin", time.Hour)
	if err != nil {
		t.Fatalf("generate jwt token: %v", err)
	}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	SetupRoutes(router, nil, nil, nil, nil, nil, nil)
	return router, token
}

func setupRouterValidationTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	dsn := fmt.Sprintf("file:router_validation_%d?mode=memory&cache=shared", time.Now().UnixNano())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}

	if err := db.AutoMigrate(&model.Setting{}, &model.AdminUser{}, &model.AuditLog{}, &model.Inbound{}, &model.Client{}); err != nil {
		t.Fatalf("migrate db: %v", err)
	}

	return db
}
