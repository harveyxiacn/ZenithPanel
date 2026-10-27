package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

const (
	ScopeRead       = "read"
	ScopeWrite      = "write"
	ScopeProxyApply = "proxy:apply"
	ScopeInfraRead  = "infra:read"
	ScopeInfraWrite = "infra:write"
	ScopeTerminal   = "terminal"
	ScopeAdmin      = "admin"
)

// KnownScopes is the complete set accepted when API tokens are created.
// Keeping the vocabulary closed prevents a misspelled scope from producing a
// credential that looks restricted but does not match any authorization rule.
var KnownScopes = map[string]struct{}{
	ScopeRead: {}, ScopeWrite: {}, ScopeProxyApply: {},
	ScopeInfraRead: {}, ScopeInfraWrite: {}, ScopeTerminal: {}, ScopeAdmin: {},
}

// ValidateScopeList parses a comma-separated token scope list and returns its
// canonical representation. Wildcard is accepted for backwards-compatible
// root/bootstrap tokens; it cannot be mixed with narrower scopes.
func ValidateScopeList(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ScopeRead, true
	}
	seen := map[string]struct{}{}
	ordered := make([]string, 0, 4)
	for scope := range strings.SplitSeq(raw, ",") {
		scope = strings.TrimSpace(scope)
		if scope == "*" {
			if len(ordered) != 0 || strings.Contains(raw, ",") {
				return "", false
			}
			return "*", true
		}
		if _, ok := KnownScopes[scope]; !ok {
			return "", false
		}
		if _, duplicate := seen[scope]; duplicate {
			continue
		}
		seen[scope] = struct{}{}
		ordered = append(ordered, scope)
	}
	if len(ordered) == 0 {
		return ScopeRead, true
	}
	return strings.Join(ordered, ","), true
}

// ScopeAuthorizationMiddleware enforces a default-deny authorization policy
// for API-token principals. JWT administrators and the root-only Unix socket
// carry wildcard scope and therefore pass through this same policy.
func ScopeAuthorizationMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		required := requiredScope(c.Request.Method, c.Request.URL.Path)
		if required == "" || !HasScope(c, required) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"code": 403,
				"msg":  "Scope '" + requiredOrDenied(required) + "' required",
			})
			return
		}
		c.Next()
	}
}

func requiredOrDenied(required string) string {
	if required == "" {
		return "explicit route policy"
	}
	return required
}

func isReadMethod(method string) bool {
	return method == http.MethodGet || method == http.MethodHead
}

// requiredScope is intentionally ordered from most-sensitive to the generic
// method policy. A new route that is not covered by a sensitive prefix still
// requires read/write; unsupported methods remain denied.
func requiredScope(method, path string) string {
	path = strings.TrimSuffix(path, "/")

	if path == "/api/v1/terminal" || path == "/api/v1/terminal/ticket" {
		return ScopeTerminal
	}

	if strings.HasPrefix(path, "/api/v1/admin/") ||
		strings.HasPrefix(path, "/api/v1/sites") ||
		strings.HasPrefix(path, "/api/v1/system/update/") ||
		strings.HasPrefix(path, "/api/v1/proxy/tls/") {
		return ScopeAdmin
	}

	for _, prefix := range []string{
		"/api/v1/docker/", "/api/v1/fs/", "/api/v1/firewall/",
		"/api/v1/cron/", "/api/v1/diagnostics/",
		"/api/v1/system/bbr/", "/api/v1/system/swap/",
		"/api/v1/system/sysctl/", "/api/v1/system/cleanup/",
	} {
		if strings.HasPrefix(path, prefix) {
			if isReadMethod(method) {
				return ScopeInfraRead
			}
			if method == http.MethodPost || method == http.MethodPut ||
				method == http.MethodPatch || method == http.MethodDelete {
				return ScopeInfraWrite
			}
			return ""
		}
	}

	if method == http.MethodPost && (path == "/api/v1/proxy/apply" ||
		path == "/api/v1/proxy/clash-api/enable" ||
		path == "/api/v1/proxy/clash-api/disable") {
		return ScopeProxyApply
	}

	// Connection testing is observational even though it uses POST to trigger
	// a fresh outbound probe.
	if method == http.MethodPost && path == "/api/v1/proxy/test-connection" {
		return ScopeRead
	}

	if isReadMethod(method) {
		return ScopeRead
	}
	if method == http.MethodPost || method == http.MethodPut ||
		method == http.MethodPatch || method == http.MethodDelete {
		return ScopeWrite
	}
	return ""
}
