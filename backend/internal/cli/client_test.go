package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestClientDoSendsBearerForRemote stands up an httptest server, points the
// client at it via http://, and verifies the Authorization header is set
// from the profile's token. The client should NOT send the header for unix
// hosts (the in-host channel trusts the caller via the socket FS perms).
func TestClientDoSendsBearerForRemote(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "msg": "ok", "data": map[string]string{"hello": "world"}})
	}))
	defer srv.Close()

	c := NewClient(Profile{Host: srv.URL, Token: "ztk_sample"})
	env, st, err := c.Do("GET", "/api/v1/ping", nil)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if st != 200 || env.Code != 200 {
		t.Fatalf("expected 200, got st=%d env=%+v", st, env)
	}
	if gotAuth != "Bearer ztk_sample" {
		t.Errorf("Authorization header: got %q, want %q", gotAuth, "Bearer ztk_sample")
	}
}

// TestClientDoDecodesEnvelope verifies that the standard
// {code,msg,data} body shape is unmarshalled into Envelope. Non-envelope
// JSON (like the /health response) falls through to env.Data carrying the
// raw body — also asserted here.
func TestClientDoDecodesEnvelope(t *testing.T) {
	cases := []struct {
		name      string
		body      string
		wantCode  int
		wantData  string
	}{
		{"envelope", `{"code":200,"msg":"ok","data":{"x":1}}`, 200, `{"x":1}`},
		{"raw json", `{"status":"ok","disk_free_gb":42}`, 200, `{"status":"ok","disk_free_gb":42}`},
		{"non-json", `not json at all`, 200, `not json at all`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, c.body)
			}))
			defer srv.Close()
			client := NewClient(Profile{Host: srv.URL})
			env, st, err := client.Do("GET", "/", nil)
			if err != nil {
				t.Fatalf("Do: %v", err)
			}
			if st != 200 {
				t.Fatalf("status: got %d, want 200", st)
			}
			if env.Code != c.wantCode {
				t.Errorf("code: got %d, want %d", env.Code, c.wantCode)
			}
			if !strings.Contains(string(env.Data), strings.TrimSpace(c.wantData)) {
				t.Errorf("data: got %q, want substring %q", string(env.Data), c.wantData)
			}
		})
	}
}

// TestClientDoTransportFailure simulates a refused connection by pointing
// the client at an immediately-closed httptest server. We expect ErrTransport
// to flow through so callers can map to the documented exit code 4.
func TestClientDoTransportFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	srv.Close() // immediate close — subsequent dials are refused
	c := NewClient(Profile{Host: srv.URL})
	_, _, err := c.Do("GET", "/x", nil)
	if err == nil {
		t.Fatal("expected transport error, got nil")
	}
	if !strings.Contains(err.Error(), "transport") {
		t.Errorf("expected wrapped transport error, got %v", err)
	}
}
