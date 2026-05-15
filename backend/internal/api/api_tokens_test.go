package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
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

// apiTokenTestEngine returns a minimal gin engine with the AuthMiddleware
// and api_token routes mounted. Database state is an in-memory SQLite scoped
// to the test, so concurrent runs don't collide on the global config.DB.
func apiTokenTestEngine(t *testing.T) *gin.Engine {
	t.Helper()
	dsn := fmt.Sprintf("file:apitokens_%d?mode=memory&cache=shared", time.Now().UnixNano())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	// Migrate every table any handler in the api package might touch — the
	// global config.DB is shared across tests in this package, so leaving
	// tables out causes adjacent tests to fail with "no such table".
	if err := db.AutoMigrate(
		&model.ApiToken{},
		&model.AuditLog{},
		&model.Inbound{},
		&model.Client{},
		&model.RoutingRule{},
		&model.Setting{},
		&model.AdminUser{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	prevDB := config.DB
	config.DB = db
	t.Cleanup(func() { config.DB = prevDB })
	jwtutil.InitSecret([]byte("0123456789abcdef0123456789abcdef"))

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(TrustedLocalFromContext())
	authGroup := r.Group("/api/v1")
	authGroup.Use(middleware.AuthMiddleware())
	registerAPITokenRoutes(authGroup)
	return r
}

// fireRequest is a tiny helper that builds an httptest request, optionally
// stamps trusted_local via context, and returns the response recorder. The
// body string is only sent as JSON when it parses as JSON — otherwise it
// flows through as the raw bytes so we can also exercise the "bad input"
// branches of the handler.
func fireRequest(r *gin.Engine, method, path, body, bearer string, trusted bool) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	if trusted {
		req = req.WithContext(context.WithValue(req.Context(), trustedLocalCtxKey, true))
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// trustedLocalCtxKey is a duplicate of the api package's unexported context
// key. We can't reuse the original symbol from a different file in the same
// package via `_test.go` because the value type must be identical. It is
// — both versions use the same private `ctxKey` type defined in unix_socket.go,
// and the test file lives in the api package so it can read it directly.
// (Kept here as documentation; the helper above uses the real symbol.)

// TestBootstrapMintsTokenOverUnixOnly verifies the local-only bootstrap path.
// Unauthenticated requests (no Bearer, no trusted_local) are stopped by the
// AuthMiddleware before they reach the handler — that's a 401. A request
// authenticated via *another* token but not on the unix socket reaches the
// handler and gets the in-handler 403 check. The unix-socket path issues
// a fresh token with full scope.
func TestBootstrapMintsTokenOverUnixOnly(t *testing.T) {
	r := apiTokenTestEngine(t)

	// HTTP, no auth → AuthMiddleware 401
	rec := fireRequest(r, "POST", "/api/v1/admin/api-tokens/bootstrap", "", "", false)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("HTTP no-auth bootstrap: expected 401, got %d (%s)", rec.Code, rec.Body.String())
	}

	// Unix-socket path → 200 with a fresh token
	rec = fireRequest(r, "POST", "/api/v1/admin/api-tokens/bootstrap", "", "", true)
	if rec.Code != http.StatusOK {
		t.Fatalf("unix bootstrap: expected 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	var resp struct {
		Code int `json:"code"`
		Data struct {
			Token string `json:"token"`
			Name  string `json:"name"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("parse body: %v", err)
	}
	if !strings.HasPrefix(resp.Data.Token, "ztk_") {
		t.Errorf("expected ztk_-prefixed token, got %q", resp.Data.Token)
	}
	if !strings.HasPrefix(resp.Data.Name, "local-root-") {
		t.Errorf("bootstrap name: got %q", resp.Data.Name)
	}

	// Reusing the just-minted token to call bootstrap over a non-unix-socket
	// path should be blocked by the in-handler check with a clear 403.
	rec = fireRequest(r, "POST", "/api/v1/admin/api-tokens/bootstrap", "", resp.Data.Token, false)
	if rec.Code != http.StatusForbidden {
		t.Errorf("token-auth HTTP bootstrap: expected 403, got %d (%s)", rec.Code, rec.Body.String())
	}
}

// TestCreateListRevokeRoundTrip drives the full token lifecycle: bootstrap
// to mint a credential, create a named token, list it, revoke it, list
// again to confirm the revoked flag flipped.
func TestCreateListRevokeRoundTrip(t *testing.T) {
	r := apiTokenTestEngine(t)

	// Bootstrap a token so subsequent calls can authenticate as a token
	// principal (not as local-root). Tokens minted via bootstrap have * scope.
	rec := fireRequest(r, "POST", "/api/v1/admin/api-tokens/bootstrap", "", "", true)
	if rec.Code != 200 {
		t.Fatalf("bootstrap: %d %s", rec.Code, rec.Body.String())
	}
	var boot struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &boot)

	// Create a named token with limited scopes.
	body, _ := json.Marshal(map[string]any{"name": "ci-runner", "scopes": "read,write"})
	rec = fireRequest(r, "POST", "/api/v1/admin/api-tokens", string(body), boot.Data.Token, false)
	if rec.Code != 200 {
		t.Fatalf("create token: %d %s", rec.Code, rec.Body.String())
	}

	// List should now include the new token.
	rec = fireRequest(r, "GET", "/api/v1/admin/api-tokens", "", boot.Data.Token, false)
	if rec.Code != 200 {
		t.Fatalf("list: %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"ci-runner"`) {
		t.Errorf("ci-runner missing from list: %s", rec.Body.String())
	}

	// Revoke by id (we parse it back out of the list).
	var listed struct {
		Data []struct {
			ID   uint   `json:"id"`
			Name string `json:"name"`
		} `json:"data"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &listed)
	var targetID uint
	for _, row := range listed.Data {
		if row.Name == "ci-runner" {
			targetID = row.ID
		}
	}
	if targetID == 0 {
		t.Fatalf("could not find ci-runner row in %s", rec.Body.String())
	}
	rec = fireRequest(r, "DELETE", fmt.Sprintf("/api/v1/admin/api-tokens/%d", targetID), "", boot.Data.Token, false)
	if rec.Code != 200 {
		t.Fatalf("revoke: %d %s", rec.Code, rec.Body.String())
	}

	// After revoke, the row is still listed but `revoked=true`.
	rec = fireRequest(r, "GET", "/api/v1/admin/api-tokens", "", boot.Data.Token, false)
	var listed2 struct {
		Data []struct {
			Name    string `json:"name"`
			Revoked bool   `json:"revoked"`
		} `json:"data"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &listed2)
	for _, row := range listed2.Data {
		if row.Name == "ci-runner" && !row.Revoked {
			t.Errorf("expected ci-runner revoked=true")
		}
	}
}

// TestCreateRejectsBadName guards the name regex and the scopes default.
// Uses the unix-socket marker rather than a bootstrap token so it doesn't
// depend on neighbour-test bootstrap timing (which used to flake under -count
// when the in-suite DB pointer races got squeezed inside a single nanosecond).
func TestCreateRejectsBadName(t *testing.T) {
	r := apiTokenTestEngine(t)
	for _, bad := range []string{"", "with spaces", "with/slash", strings.Repeat("a", 65)} {
		body, _ := json.Marshal(map[string]string{"name": bad})
		rec := fireRequest(r, "POST", "/api/v1/admin/api-tokens", string(body), "", true)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("name=%q: expected 400, got %d (%s)", bad, rec.Code, rec.Body.String())
		}
	}
}

// TestCreateDuplicateNameReturns409 pins the unique-name constraint behavior.
// Uses the unix-socket auth path for stability under -count repeated runs.
func TestCreateDuplicateNameReturns409(t *testing.T) {
	r := apiTokenTestEngine(t)
	body := `{"name":"dup-test"}`
	rec := fireRequest(r, "POST", "/api/v1/admin/api-tokens", body, "", true)
	if rec.Code != 200 {
		t.Fatalf("first create: %d (%s)", rec.Code, rec.Body.String())
	}
	rec = fireRequest(r, "POST", "/api/v1/admin/api-tokens", body, "", true)
	if rec.Code != http.StatusConflict {
		t.Errorf("duplicate name: expected 409, got %d (%s)", rec.Code, rec.Body.String())
	}
}

// TestCreateBodyIsJSON guards the request decoder. A garbage body should
// land as 400, not crash the handler. We authenticate via the
// trusted_local marker rather than a bootstrap token so this test stays
// independent of any one-second-resolution token-name collision across
// neighbour tests in the same package.
func TestCreateBodyIsJSON(t *testing.T) {
	r := apiTokenTestEngine(t)
	rec := fireRequest(r, "POST", "/api/v1/admin/api-tokens", "nope", "", true)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for non-JSON body, got %d (%s)", rec.Code, rec.Body.String())
	}
}
