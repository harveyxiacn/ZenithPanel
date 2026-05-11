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

func TestApplySetupCompletePersistsUsageProfile(t *testing.T) {
	router, token := setupRouterValidationTestServer(t, false)

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

func setupRouterValidationTestServer(t *testing.T, setupComplete bool) (*gin.Engine, string) {
	t.Helper()

	db := setupRouterValidationTestDB(t)
	config.DB = db

	cfg := config.GetConfig()
	cfg.IsSetupComplete = setupComplete
	cfg.PanelPrefix = "/"
	cfg.SetupURLSuffix = "testsuffix"
	cfg.SetupOneTimeToken = "one-time-test-token"

	jwtutil.InitSecret([]byte("0123456789abcdef0123456789abcdef"))
	token, err := jwtutil.GenerateToken("1", "admin", time.Hour)
	if err != nil {
		t.Fatalf("generate jwt token: %v", err)
	}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	SetupRoutes(router, nil, nil, nil, nil, nil)
	return router, token
}

func setupRouterValidationTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	dsn := fmt.Sprintf("file:router_validation_%d?mode=memory&cache=shared", time.Now().UnixNano())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}

	if err := db.AutoMigrate(&model.Setting{}, &model.AdminUser{}, &model.AuditLog{}); err != nil {
		t.Fatalf("migrate db: %v", err)
	}

	return db
}
