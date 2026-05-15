package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
)

// captureStdout reroutes os.Stdout to a pipe while fn runs, returning what
// the function wrote. Used to assert table-renderer output without poking
// at private internals.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	done := make(chan struct{})
	var buf bytes.Buffer
	go func() {
		_, _ = io.Copy(&buf, r)
		close(done)
	}()
	fn()
	_ = w.Close()
	os.Stdout = orig
	<-done
	return buf.String()
}

// TestPrintAsTableInboundList pins the column layout for an inbound list.
// We deliberately assert on column headers + a known value rather than exact
// whitespace so the test survives tabwriter alignment tweaks.
func TestPrintAsTableInboundList(t *testing.T) {
	body := []map[string]any{
		{"id": 1, "tag": "vless-reality", "protocol": "vless", "port": 443, "network": "tcp", "server_address": "1.2.3.4", "enable": true},
		{"id": 2, "tag": "vmess-ws", "protocol": "vmess", "port": 31402, "network": "ws", "enable": false},
	}
	data, _ := json.Marshal(body)
	env := &Envelope{Code: 200, Data: data}
	out := captureStdout(t, func() { PrintAsTable(env) })

	for _, expected := range []string{"ID", "TAG", "PROTOCOL", "PORT", "vless-reality", "vmess-ws", "31402", "✓", "✗"} {
		if !strings.Contains(out, expected) {
			t.Errorf("expected output to contain %q, got:\n%s", expected, out)
		}
	}
}

// TestPrintAsTableTokenList pins token-list rendering. The check for
// "active" / "revoked" guards the boolean → human mapping.
func TestPrintAsTableTokenList(t *testing.T) {
	body := []map[string]any{
		{"id": 1, "name": "ci-runner", "scopes": "read,write", "expires_at": 0, "last_used_at": 0, "revoked": false},
		{"id": 2, "name": "old-one", "scopes": "*", "expires_at": 0, "last_used_at": 0, "revoked": true},
	}
	data, _ := json.Marshal(body)
	env := &Envelope{Code: 200, Data: data}
	out := captureStdout(t, func() { PrintAsTable(env) })

	for _, expected := range []string{"NAME", "SCOPES", "ci-runner", "read,write", "old-one", "active", "revoked"} {
		if !strings.Contains(out, expected) {
			t.Errorf("expected %q in output, got:\n%s", expected, out)
		}
	}
}

// TestPrintAsTableProxyStatus exercises the keyed-table path used by
// `zenithctl proxy status`. The status block isn't an array — the renderer
// emits one row per field.
func TestPrintAsTableProxyStatus(t *testing.T) {
	data, _ := json.Marshal(map[string]any{
		"xray_running":               true,
		"singbox_running":            false,
		"dual_mode":                  true,
		"enabled_inbounds":           6,
		"enabled_clients":            6,
		"enabled_rules":              2,
		"xray_handed_off_to_singbox": []string{"hysteria2 (hysteria2)", "tuic-v5 (tuic)"},
	})
	env := &Envelope{Code: 200, Data: data}
	out := captureStdout(t, func() { PrintAsTable(env) })
	for _, expected := range []string{"xray_running", "singbox_running", "enabled_inbounds", "handed_off_to_singbox", "hysteria2"} {
		if !strings.Contains(out, expected) {
			t.Errorf("expected %q in output, got:\n%s", expected, out)
		}
	}
}

// TestPrintAsTableUnknownShapeFallsBackToJSON covers the "best-effort"
// promise: never crash; if no shape matches, print JSON.
func TestPrintAsTableUnknownShapeFallsBackToJSON(t *testing.T) {
	data, _ := json.Marshal(map[string]any{"some_unknown_field": 42, "another": "value"})
	env := &Envelope{Code: 200, Data: data}
	out := captureStdout(t, func() { PrintAsTable(env) })
	if !strings.Contains(out, "some_unknown_field") {
		t.Errorf("fallback should preserve raw field name, got:\n%s", out)
	}
}

// TestHumanBytes pins the byte-formatter behavior. Zero and negative both
// render as "-" so the table reader doesn't see misleading "0B".
func TestHumanBytes(t *testing.T) {
	cases := map[int64]string{
		0:                   "-",
		-1:                  "-",
		512:                 "512B",
		1024:                "1.0K",
		1024 * 1024:         "1.0M",
		1024 * 1024 * 1024:  "1.0G",
		3 * 1024 * 1024 / 2: "1.5M",
	}
	for in, want := range cases {
		if got := humanBytes(in); got != want {
			t.Errorf("humanBytes(%d) = %q, want %q", in, got, want)
		}
	}
}
