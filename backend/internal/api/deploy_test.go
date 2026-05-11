package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/config"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/model"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/pkg/jwtutil"
	"gorm.io/gorm"
)

// setupDeployTestRouter wires the full route tree against an in-memory DB,
// seeding it with the tables Smart Deploy needs. The returned auth token
// opens every handler that requires JWT.
func setupDeployTestRouter(t *testing.T) (*gin.Engine, string, *gorm.DB) {
	t.Helper()

	// Isolate each test with its own shared-cache memory DSN.
	dsn := fmt.Sprintf("file:deploy_api_%d?mode=memory&cache=shared", time.Now().UnixNano())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(
		&model.Setting{}, &model.AdminUser{}, &model.AuditLog{},
		&model.Inbound{}, &model.Deployment{}, &model.DeploymentOp{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	config.DB = db

	cfg := config.GetConfig()
	cfg.IsSetupComplete = true
	cfg.PanelPrefix = "/"

	jwtutil.InitSecret([]byte("0123456789abcdef0123456789abcdef"))
	token, err := jwtutil.GenerateToken("1", "admin", time.Hour)
	if err != nil {
		t.Fatalf("token: %v", err)
	}

	// Reset the lazily-initialized orchestrator so each test gets a fresh
	// one wired to this test's DB.
	deployOrchestratorOnce = sync.Once{}
	deployOrchestrator = nil

	gin.SetMode(gin.TestMode)
	r := gin.New()
	SetupRoutes(r, nil, nil, nil, nil, nil)
	return r, token, db
}

func TestDeployProbeEndpointReturnsSnapshot(t *testing.T) {
	r, token, _ := setupDeployTestRouter(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/deploy/probe", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Code int            `json:"code"`
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Code != 200 {
		t.Errorf("code = %d", resp.Code)
	}
	if _, ok := resp.Data["root_check"]; !ok {
		t.Errorf("probe snapshot missing root_check, got keys=%v", keysOf(resp.Data))
	}
}

func TestDeployPreviewEndpointRejectsUnknownPreset(t *testing.T) {
	r, token, _ := setupDeployTestRouter(t)

	body, _ := json.Marshal(deployRequest{PresetID: "not_a_real_preset"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/deploy/preview", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestDeployPreviewEndpointMissingPresetID(t *testing.T) {
	r, token, _ := setupDeployTestRouter(t)

	body, _ := json.Marshal(deployRequest{})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/deploy/preview", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d (%s)", rec.Code, rec.Body.String())
	}
}

func TestDeployListEndpointReturnsEmpty(t *testing.T) {
	r, token, _ := setupDeployTestRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/deploy", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Code int `json:"code"`
		Data []model.Deployment
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Code != 200 {
		t.Errorf("code = %d", resp.Code)
	}
	if len(resp.Data) != 0 {
		t.Errorf("expected no deployments initially, got %d", len(resp.Data))
	}
}

func TestDeployGetEndpointReturns404WhenMissing(t *testing.T) {
	r, token, _ := setupDeployTestRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/deploy/9999", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestDeployEndpointsRequireAuth(t *testing.T) {
	r, _, _ := setupDeployTestRouter(t)
	// No Authorization header — every endpoint should reject.
	for _, path := range []string{
		"/api/v1/deploy/probe",
		"/api/v1/deploy/preview",
		"/api/v1/deploy/apply",
		"/api/v1/deploy",
		"/api/v1/deploy/1",
		"/api/v1/deploy/1/rollback",
		"/api/v1/deploy/1/clients",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code == http.StatusOK {
			t.Errorf("%s: expected non-200 without auth, got 200", path)
		}
	}
}

func keysOf(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
