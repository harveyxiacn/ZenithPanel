package cli

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"
)

// TestParseGlobalFlagsRecognizesShortAndLongForms covers the parser's job:
// strip well-known global flags out of argv, leave the rest in g.rest in
// original order, and honor both `--flag value` and `--flag=value`.
func TestParseGlobalFlagsRecognizesShortAndLongForms(t *testing.T) {
	cases := []struct {
		name        string
		in          []string
		wantHost    string
		wantToken   string
		wantOut     string
		wantQuiet   bool
		wantTimeout time.Duration
		wantRest    []string
	}{
		{
			name:     "long-form host + token",
			in:       []string{"--host", "https://x", "--token", "ztk_a", "status"},
			wantHost: "https://x", wantToken: "ztk_a", wantOut: "json", wantTimeout: 30 * time.Second,
			wantRest: []string{"status"},
		},
		{
			name:     "equals form",
			in:       []string{"--host=https://y", "--output=table", "system", "info"},
			wantHost: "https://y", wantOut: "table", wantTimeout: 30 * time.Second,
			wantRest: []string{"system", "info"},
		},
		{
			name:     "--socket maps to unix:// host",
			in:       []string{"--socket", "/tmp/x.sock", "status"},
			wantHost: "unix:///tmp/x.sock", wantOut: "json", wantTimeout: 30 * time.Second,
			wantRest: []string{"status"},
		},
		{
			name:      "-q short flag",
			in:        []string{"-q", "inbound", "list"},
			wantQuiet: true, wantOut: "json", wantTimeout: 30 * time.Second,
			wantRest: []string{"inbound", "list"},
		},
		{
			name:    "unknown flags fall through to rest",
			in:      []string{"--not-mine", "value", "status"},
			wantOut: "json", wantTimeout: 30 * time.Second,
			wantRest: []string{"--not-mine", "value", "status"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := parseGlobalFlags(c.in)
			if got.host != c.wantHost {
				t.Errorf("host: got %q, want %q", got.host, c.wantHost)
			}
			if got.token != c.wantToken {
				t.Errorf("token: got %q, want %q", got.token, c.wantToken)
			}
			if got.output != c.wantOut {
				t.Errorf("output: got %q, want %q", got.output, c.wantOut)
			}
			if got.quiet != c.wantQuiet {
				t.Errorf("quiet: got %v, want %v", got.quiet, c.wantQuiet)
			}
			if got.timeout != c.wantTimeout {
				t.Errorf("timeout: got %v, want %v", got.timeout, c.wantTimeout)
			}
			if !reflect.DeepEqual(got.rest, c.wantRest) {
				t.Errorf("rest: got %v, want %v", got.rest, c.wantRest)
			}
		})
	}
}

func TestParseGlobalFlagsTimeoutFormsAndValidation(t *testing.T) {
	valid := []struct {
		name string
		args []string
		want time.Duration
	}{
		{"separate", []string{"--timeout", "120s", "status"}, 120 * time.Second},
		{"equals", []string{"--timeout=2500ms", "status"}, 2500 * time.Millisecond},
	}
	for _, tc := range valid {
		t.Run(tc.name, func(t *testing.T) {
			got := parseGlobalFlags(tc.args)
			if got.parseErr != nil || got.timeout != tc.want {
				t.Fatalf("timeout=%v parseErr=%v, want %v", got.timeout, got.parseErr, tc.want)
			}
		})
	}
	for _, value := range []string{"", "0", "-1s", "not-a-duration"} {
		t.Run("invalid-"+value, func(t *testing.T) {
			got := parseGlobalFlags([]string{"--timeout=" + value, "status"})
			if got.parseErr == nil {
				t.Fatalf("expected parse error for %q", value)
			}
		})
	}
	got := parseGlobalFlags([]string{"--timeout", "status"})
	if got.parseErr == nil {
		t.Fatal("missing timeout value was accepted")
	}
}

func TestNewClientWithTimeoutAppliesHTTPDeadline(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()

	c := newClientWithTimeout(Profile{Host: srv.URL}, 10*time.Millisecond)
	if c.Inner.Timeout != 10*time.Millisecond {
		t.Fatalf("client timeout=%v, want 10ms", c.Inner.Timeout)
	}
	_, _, err := c.Do("GET", "/slow", nil)
	if !errors.Is(err, ErrTransport) {
		t.Fatalf("slow request error=%v, want transport timeout", err)
	}
}

// TestResolveProfileFlagOverridesEnvAndConfig pins the precedence chain:
// flag override > env > config profile. Without any of the above, fall
// back to the implicit local socket (skipped here because no /run/zenith
// path exists in CI) or an error.
func TestResolveProfileFlagOverridesEnvAndConfig(t *testing.T) {
	cfg := &Config{
		Default: "prod",
		Profile: map[string]Profile{
			"prod": {Host: "https://prod", Token: "ztk_prod"},
			"dev":  {Host: "https://dev", Token: "ztk_dev"},
		},
	}

	// 1) No overrides → default profile "prod".
	t.Setenv("ZENITHCTL_HOST", "")
	t.Setenv("ZENITHCTL_TOKEN", "")
	p, err := resolveProfile(cfg, "", "", "")
	if err != nil {
		t.Fatalf("default: %v", err)
	}
	if p.Host != "https://prod" || p.Token != "ztk_prod" {
		t.Errorf("default profile: got %+v", p)
	}

	// 2) --profile override picks the named profile.
	p, err = resolveProfile(cfg, "", "", "dev")
	if err != nil {
		t.Fatalf("named: %v", err)
	}
	if p.Host != "https://dev" {
		t.Errorf("named profile: got host %q", p.Host)
	}

	// 3) Env override beats config; flag override beats env.
	t.Setenv("ZENITHCTL_HOST", "https://env")
	t.Setenv("ZENITHCTL_TOKEN", "ztk_env")
	p, _ = resolveProfile(cfg, "", "", "")
	if p.Host != "https://env" || p.Token != "ztk_env" {
		t.Errorf("env override: got %+v", p)
	}
	p, _ = resolveProfile(cfg, "https://flag", "ztk_flag", "")
	if p.Host != "https://flag" || p.Token != "ztk_flag" {
		t.Errorf("flag override: got %+v", p)
	}
}

// TestSplitVersionedName pins the rotate-name math so a name like
// "ci-runner-v7" advances to v8 instead of wrapping or duplicating.
func TestSplitVersionedName(t *testing.T) {
	cases := []struct {
		in       string
		wantBase string
		wantVer  int
	}{
		{"ci-runner", "ci-runner", 1},
		{"ci-runner-v2", "ci-runner", 2},
		{"ci-runner-v123", "ci-runner", 123},
		{"weird-v", "weird-v", 1},       // empty suffix → not a version
		{"weird-vabc", "weird-vabc", 1}, // non-numeric → not a version
		{"weird-v-1", "weird-v-1", 1},   // negative → not a version
		{"-v5", "", 5},                  // edge case but consistent
	}
	for _, c := range cases {
		base, ver := splitVersionedName(c.in)
		if base != c.wantBase || ver != c.wantVer {
			t.Errorf("splitVersionedName(%q) = (%q, %d), want (%q, %d)", c.in, base, ver, c.wantBase, c.wantVer)
		}
	}
}

// TestIsUnixHost is a one-liner but worth pinning because it gates a lot of
// branching (skipping token header, treating bootstrap as available, etc.).
func TestIsUnixHost(t *testing.T) {
	cases := map[string]bool{
		"unix:///run/zenithpanel.sock": true,
		"unix:///tmp/x.sock":           true,
		"http://127.0.0.1":             false,
		"https://example.com":          false,
		"":                             false,
	}
	for in, want := range cases {
		if got := isUnixHost(in); got != want {
			t.Errorf("isUnixHost(%q) = %v, want %v", in, got, want)
		}
	}
}
