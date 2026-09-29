package middleware

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRequiredScope(t *testing.T) {
	cases := []struct {
		method, path, want string
	}{
		{"GET", "/api/v1/inbounds", "read"},
		{"POST", "/api/v1/inbounds", "write"},
		{"DELETE", "/api/v1/clients/3", "write"},
		{"POST", "/api/v1/proxy/apply", "proxy:apply"},
		{"GET", "/api/v1/proxy/status", "read"},
		{"GET", "/api/v1/terminal", "admin"},
		{"GET", "/api/v1/fs/read", "admin"},
		{"POST", "/api/v1/fs/write", "admin"},
		{"GET", "/api/v1/cron/jobs", "admin"},
		{"GET", "/api/v1/admin/backup", "admin"},
		{"GET", "/api/v1/docker/containers", "read"},
		{"POST", "/api/v1/docker/containers/run", "admin"},
		{"GET", "/api/v1/firewall/rules", "read"},
		{"POST", "/api/v1/firewall/rules", "firewall"},
		{"POST", "/api/v1/system/bbr", "system"},
	}
	for _, tc := range cases {
		if got := RequiredScope(tc.method, tc.path); got != tc.want {
			t.Errorf("RequiredScope(%s %s) = %q, want %q", tc.method, tc.path, got, tc.want)
		}
	}
}

func TestScopeMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := []struct {
		scopes, method, path string
		want                 int
	}{
		{"*", "POST", "/api/v1/fs/write", 200},
		{"read", "GET", "/api/v1/inbounds", 200},
		{"read", "POST", "/api/v1/inbounds", 403},
		{"read", "GET", "/api/v1/terminal", 403},
		{"write", "GET", "/api/v1/inbounds", 200},
		{"read,write", "POST", "/api/v1/fs/write", 403},
		{"read,write", "POST", "/api/v1/proxy/apply", 403},
		{"read,write,proxy:apply", "POST", "/api/v1/proxy/apply", 200},
		{"admin", "POST", "/api/v1/firewall/rules", 200},
		{"admin", "GET", "/api/v1/terminal", 200},
		{"firewall", "POST", "/api/v1/firewall/rules", 200},
	}
	for _, tc := range cases {
		r := gin.New()
		r.Use(func(c *gin.Context) { c.Set("scopes", tc.scopes); c.Next() }, ScopeMiddleware())
		r.Handle(tc.method, tc.path, func(c *gin.Context) { c.Status(200) })
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
		if w.Code != tc.want {
			t.Errorf("scopes=%q %s %s: got %d, want %d", tc.scopes, tc.method, tc.path, w.Code, tc.want)
		}
	}
}
