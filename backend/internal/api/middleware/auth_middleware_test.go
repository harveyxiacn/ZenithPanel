package middleware

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/config"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/model"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/pkg/apitoken"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/pkg/jwtutil"
	"gorm.io/gorm"
)

// trustedLocalKey duplicates the unexported type used by api.EngineWithLocalTrust.
// We can't reuse the value directly because it's in another package, but we
// can simulate it by passing a fresh context-value the middleware also reads
// — except the middleware reads c.GetBool("trusted_local"), which is set by
// the TrustedLocalFromContext promoter in the api package. For unit tests of
// the auth middleware in isolation, we instead pre-stamp the gin context via
// a wrapper handler that sets c.Set("trusted_local", true). The wrapper is
// what every test below uses to fake the unix-socket path.

func newAuthTestEngine(t *testing.T) (*gin.Engine, *gorm.DB) {
	t.Helper()
	dsn := fmt.Sprintf("file:auth_%d?mode=memory&cache=shared", time.Now().UnixNano())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.ApiToken{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	prevDB := config.DB
	config.DB = db
	t.Cleanup(func() { config.DB = prevDB })
	jwtutil.InitSecret([]byte("test-secret-32-bytes-of-padding!!"))

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/probe", AuthMiddleware(), func(c *gin.Context) {
		c.JSON(200, gin.H{
			"principal": c.GetString("principal"),
			"scopes":    c.GetString("scopes"),
		})
	})
	r.GET("/probe-as-local-root", func(c *gin.Context) {
		c.Set("trusted_local", true)
		c.Next()
	}, AuthMiddleware(), func(c *gin.Context) {
		c.JSON(200, gin.H{
			"principal": c.GetString("principal"),
			"scopes":    c.GetString("scopes"),
		})
	})
	return r, db
}

// TestAuthMissingHeader401 is the negative baseline: a request with no
// Authorization header and no trusted_local marker must 401.
func TestAuthMissingHeader401(t *testing.T) {
	r, _ := newAuthTestEngine(t)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/probe", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("no auth: expected 401, got %d", rec.Code)
	}
}

// TestAuthJWTValid pins the legacy JWT path stays functional after the
// middleware swap.
func TestAuthJWTValid(t *testing.T) {
	r, _ := newAuthTestEngine(t)
	token, err := jwtutil.GenerateToken("42", "harvey", time.Hour)
	if err != nil {
		t.Fatalf("jwt: %v", err)
	}
	req := httptest.NewRequest("GET", "/probe", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("expected 200 for valid JWT, got %d (%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"admin:harvey"`) {
		t.Errorf("expected principal admin:harvey, body=%s", rec.Body.String())
	}
}

// TestAuthAPITokenValid covers the new ztk_ Bearer path.
func TestAuthAPITokenValid(t *testing.T) {
	r, db := newAuthTestEngine(t)
	plain, _, _ := apitoken.Generate()
	sum := sha256.Sum256([]byte(plain))
	row := &model.ApiToken{
		Name:      "ci",
		TokenHash: hex.EncodeToString(sum[:]),
		Scopes:    "read",
	}
	if err := db.Create(row).Error; err != nil {
		t.Fatalf("seed token: %v", err)
	}
	req := httptest.NewRequest("GET", "/probe", nil)
	req.Header.Set("Authorization", "Bearer "+plain)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("expected 200 for valid api token, got %d (%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"token:ci"`) {
		t.Errorf("expected principal token:ci, body=%s", rec.Body.String())
	}
}

// TestAuthAPITokenRevoked confirms a revoked row 401s even if the plaintext
// still hashes to a stored hash.
func TestAuthAPITokenRevoked(t *testing.T) {
	r, db := newAuthTestEngine(t)
	plain, _, _ := apitoken.Generate()
	sum := sha256.Sum256([]byte(plain))
	row := &model.ApiToken{
		Name:      "revoked-one",
		TokenHash: hex.EncodeToString(sum[:]),
		Revoked:   true,
	}
	if err := db.Create(row).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}
	req := httptest.NewRequest("GET", "/probe", nil)
	req.Header.Set("Authorization", "Bearer "+plain)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != 401 {
		t.Errorf("revoked: expected 401, got %d", rec.Code)
	}
}

// TestAuthAPITokenExpired confirms ExpiresAt < now() 401s.
func TestAuthAPITokenExpired(t *testing.T) {
	r, db := newAuthTestEngine(t)
	plain, _, _ := apitoken.Generate()
	sum := sha256.Sum256([]byte(plain))
	row := &model.ApiToken{
		Name:      "expired-one",
		TokenHash: hex.EncodeToString(sum[:]),
		ExpiresAt: time.Now().Add(-time.Hour).Unix(),
	}
	if err := db.Create(row).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}
	req := httptest.NewRequest("GET", "/probe", nil)
	req.Header.Set("Authorization", "Bearer "+plain)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != 401 {
		t.Errorf("expired: expected 401, got %d", rec.Code)
	}
}

// TestAuthTrustedLocalSkipsHeader confirms the unix-socket path doesn't need
// any Authorization header — the in-context marker is enough, and the
// principal lands as local-root with full scope.
func TestAuthTrustedLocalSkipsHeader(t *testing.T) {
	r, _ := newAuthTestEngine(t)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/probe-as-local-root", nil))
	if rec.Code != 200 {
		t.Fatalf("trusted_local: expected 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"local-root"`) {
		t.Errorf("expected principal local-root, body=%s", rec.Body.String())
	}
}

// TestHasScopeMatrix pins the scope-check helper. Wildcard always wins; a
// specific listed scope grants only that one; empty string denies.
func TestHasScopeMatrix(t *testing.T) {
	cases := []struct {
		scopes   string
		required string
		want     bool
	}{
		{"*", "anything", true},
		{"*", "", true}, // wildcard answers true for any required, including "" — calling with "" is a coding bug; we don't punish it
		{"", "read", false},
		{"read,write", "read", true},
		{"read,write", "admin", false},
		{" read , write ", "write", true}, // tolerates whitespace
		{"read,*", "admin", true},          // wildcard mid-list still wins
	}
	for _, c := range cases {
		ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
		ctx.Set("scopes", c.scopes)
		got := HasScope(ctx, c.required)
		if got != c.want {
			t.Errorf("HasScope(scopes=%q,required=%q) = %v, want %v", c.scopes, c.required, got, c.want)
		}
	}
}

// silence the "imported and not used" lint for context in case future tests
// add a context-injection path; pin it now so the file always compiles.
var _ = context.Background
