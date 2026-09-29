package subserver

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/config"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/model"
	"gorm.io/gorm"
)

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func get(t *testing.T, url string) (int, string) {
	t.Helper()
	var resp *http.Response
	var err error
	for i := 0; i < 20; i++ { // listener starts asynchronously
		if resp, err = http.Get(url); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestServerServesOnlySecretPath(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:subsrv_%d?mode=memory&cache=shared", time.Now().UnixNano())), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.Setting{}); err != nil {
		t.Fatal(err)
	}
	orig := config.DB
	config.DB = db
	t.Cleanup(func() { config.DB = orig })

	s := New(func(c *gin.Context) { c.String(http.StatusOK, "sub:"+c.Param("uuid")) })
	port := freePort(t)
	cfg := Config{Enabled: true, Port: port, Secret: "s3cret"}
	if err := s.Apply(cfg); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Stop)

	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	if code, body := get(t, base+"/s3cret/abc-123"); code != 200 || body != "sub:abc-123" {
		t.Fatalf("secret path: %d %q", code, body)
	}
	for _, p := range []string{"/abc-123", "/wrong/abc-123", "/api/v1/sub/abc-123", "/"} {
		if code, _ := get(t, base+p); code != 404 {
			t.Errorf("%s: got %d, want 404", p, code)
		}
	}

	st := s.Status("203.0.113.10")
	if !st.Running || st.TLS || !strings.HasPrefix(st.BaseURL, fmt.Sprintf("http://203.0.113.10:%d/s3cret/", port)) {
		t.Fatalf("status = %+v", st)
	}

	// Moving to another port frees the old one; disabling stops it.
	port2 := freePort(t)
	cfg.Port = port2
	if err := s.Apply(cfg); err != nil {
		t.Fatal(err)
	}
	if code, _ := get(t, fmt.Sprintf("http://127.0.0.1:%d/s3cret/x", port2)); code != 200 {
		t.Fatalf("new port not serving")
	}
	cfg.Enabled = false
	if err := s.Apply(cfg); err != nil {
		t.Fatal(err)
	}
	if s.Status("h").Running {
		t.Fatal("still running after disable")
	}
	if _, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/s3cret/x", port2)); err == nil {
		t.Fatal("listener still accepting after disable")
	}
}

func TestNewSecret(t *testing.T) {
	a, b := NewSecret(), NewSecret()
	if len(a) != 20 || a == b {
		t.Fatalf("weak secrets: %q %q", a, b)
	}
}
