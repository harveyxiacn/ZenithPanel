package cli

import (
	"reflect"
	"testing"
)

// TestParseGlobalFlagsRecognizesShortAndLongForms covers the parser's job:
// strip well-known global flags out of argv, leave the rest in g.rest in
// original order, and honor both `--flag value` and `--flag=value`.
func TestParseGlobalFlagsRecognizesShortAndLongForms(t *testing.T) {
	cases := []struct {
		name       string
		in         []string
		wantHost   string
		wantToken  string
		wantOut    string
		wantQuiet  bool
		wantRest   []string
	}{
		{
			name:     "long-form host + token",
			in:       []string{"--host", "https://x", "--token", "ztk_a", "status"},
			wantHost: "https://x", wantToken: "ztk_a", wantOut: "json",
			wantRest: []string{"status"},
		},
		{
			name:     "equals form",
			in:       []string{"--host=https://y", "--output=table", "system", "info"},
			wantHost: "https://y", wantOut: "table",
			wantRest: []string{"system", "info"},
		},
		{
			name:     "--socket maps to unix:// host",
			in:       []string{"--socket", "/tmp/x.sock", "status"},
			wantHost: "unix:///tmp/x.sock", wantOut: "json",
			wantRest: []string{"status"},
		},
		{
			name:      "-q short flag",
			in:        []string{"-q", "inbound", "list"},
			wantQuiet: true, wantOut: "json",
			wantRest: []string{"inbound", "list"},
		},
		{
			name:     "unknown flags fall through to rest",
			in:       []string{"--not-mine", "value", "status"},
			wantOut:  "json",
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
			if !reflect.DeepEqual(got.rest, c.wantRest) {
				t.Errorf("rest: got %v, want %v", got.rest, c.wantRest)
			}
		})
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
