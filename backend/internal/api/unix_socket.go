package api

import (
	"context"
	"net/http"
	"os"

	"github.com/gin-gonic/gin"
)

// LocalSocketPath is the Unix domain socket exposed for in-host CLI access.
// Permissions are tightened to 0600 after bind so only the file owner (root)
// can connect — same trust level as anyone who could read the DB anyway.
const LocalSocketPath = "/run/zenithpanel.sock"

// ctxKey is a private type so external packages can't construct or guess it.
type ctxKey int

const trustedLocalCtxKey ctxKey = 0

// EngineWithLocalTrust wraps the engine so requests served through it carry a
// request-scoped marker. Mount this ONLY on the unix-socket http.Server. The
// TCP server stays on the bare engine, which means no over-the-wire request
// can ever carry the marker — context values are not constructible from
// outside the process.
func EngineWithLocalTrust(engine http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), trustedLocalCtxKey, true)
		engine.ServeHTTP(w, r.WithContext(ctx))
	})
}

// TrustedLocalFromContext is a gin global middleware that promotes the
// request-context marker (set by EngineWithLocalTrust) into the gin context
// so downstream auth code can read c.GetBool("trusted_local"). Mount it once
// on the shared engine; it's a no-op for TCP traffic.
func TrustedLocalFromContext() gin.HandlerFunc {
	return func(c *gin.Context) {
		if v, _ := c.Request.Context().Value(trustedLocalCtxKey).(bool); v {
			c.Set("trusted_local", true)
		}
		c.Next()
	}
}

// RemoveExistingSocket cleans up a stale socket file from a prior run. Safe to
// call before binding the listener. Errors other than "file doesn't exist"
// are returned.
func RemoveExistingSocket(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
