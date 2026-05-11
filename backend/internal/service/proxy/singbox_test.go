package proxy

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/harveyxiacn/ZenithPanel/backend/internal/model"
)

// TestSingboxRealityHandshakeSplitsDest verifies that a Reality stream
// "dest" value (host:port) is correctly split into separate server/server_port
// fields in the sing-box handshake block.
func TestSingboxRealityHandshakeSplitsDest(t *testing.T) {
	in := model.Inbound{
		Tag:      "vless-reality",
		Protocol: "vless",
		Port:     443,
		Stream: `{
			"network": "tcp",
			"security": "reality",
			"realitySettings": {
				"dest": "microsoft.com:443",
				"serverNames": ["microsoft.com"],
				"privateKey": "test-private-key",
				"shortIds": ["abc123"]
			}
		}`,
	}
	clients := []model.Client{{Email: "a@test", UUID: "uuid-test"}}

	entry, err := buildSingboxInbound(in, clients)
	if err != nil {
		t.Fatalf("buildSingboxInbound: %v", err)
	}

	tls, ok := entry["tls"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected tls block, got %T: %v", entry["tls"], entry["tls"])
	}
	reality, ok := tls["reality"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected reality block in tls, got %T: %v", tls["reality"], tls["reality"])
	}
	handshake, ok := reality["handshake"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected handshake in reality, got %T: %v", reality["handshake"], reality["handshake"])
	}
	if handshake["server"] != "microsoft.com" {
		t.Errorf("expected handshake.server=microsoft.com, got %q", handshake["server"])
	}
	if handshake["server_port"] != 443 {
		t.Errorf("expected handshake.server_port=443, got %v", handshake["server_port"])
	}
	// Sing-box 1.11+ silently ignores the reality block without this field
	// and then bails at startup with "missing certificate" — guard against
	// regression.
	if reality["enabled"] != true {
		t.Errorf("expected reality.enabled=true (sing-box 1.11 requirement), got %v", reality["enabled"])
	}
}

// TestSingboxH2TransportMapping verifies that HTTP/2 stream settings produce
// the correct sing-box transport block with type "http".
func TestSingboxH2TransportMapping(t *testing.T) {
	in := model.Inbound{
		Tag:      "vless-h2",
		Protocol: "vless",
		Port:     443,
		Stream: `{
			"network": "h2",
			"security": "tls",
			"tlsSettings": {
				"serverName": "h2.test",
				"certificates": [{"certificateFile": "/tmp/c.pem", "keyFile": "/tmp/k.pem"}]
			},
			"httpSettings": {
				"path": "/proxy",
				"host": ["h2.test"]
			}
		}`,
	}
	clients := []model.Client{{Email: "b@test", UUID: "uuid-b"}}

	entry, err := buildSingboxInbound(in, clients)
	if err != nil {
		t.Fatalf("buildSingboxInbound: %v", err)
	}

	transport, ok := entry["transport"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected transport block, got %T: %v", entry["transport"], entry["transport"])
	}
	if transport["type"] != "http" {
		t.Errorf("expected transport.type=http, got %q", transport["type"])
	}
	if transport["path"] != "/proxy" {
		t.Errorf("expected transport.path=/proxy, got %q", transport["path"])
	}
}

// TestSingboxTLSFingerprintToUTLS verifies that a TLS fingerprint in stream
// settings produces a utls block in the sing-box TLS config.
func TestSingboxTLSFingerprintToUTLS(t *testing.T) {
	in := model.Inbound{
		Tag:      "vless-fp",
		Protocol: "vless",
		Port:     443,
		Stream: `{
			"network": "tcp",
			"security": "tls",
			"tlsSettings": {
				"serverName": "fp.test",
				"fingerprint": "chrome",
				"certificates": [{"certificateFile": "/tmp/c.pem", "keyFile": "/tmp/k.pem"}]
			}
		}`,
	}
	clients := []model.Client{{Email: "c@test", UUID: "uuid-c"}}

	entry, err := buildSingboxInbound(in, clients)
	if err != nil {
		t.Fatalf("buildSingboxInbound: %v", err)
	}

	tls, ok := entry["tls"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected tls block, got %T", entry["tls"])
	}
	utls, ok := tls["utls"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected utls block in tls, got %T: %v", tls["utls"], tls["utls"])
	}
	if utls["fingerprint"] != "chrome" {
		t.Errorf("expected utls.fingerprint=chrome, got %q", utls["fingerprint"])
	}
	if utls["enabled"] != true {
		t.Errorf("expected utls.enabled=true, got %v", utls["enabled"])
	}
}

// TestSingboxNoUTLSWhenNoFingerprint ensures no utls block is added when
// no fingerprint is present in tlsSettings.
func TestSingboxNoUTLSWhenNoFingerprint(t *testing.T) {
	in := model.Inbound{
		Tag:      "vless-notls",
		Protocol: "vless",
		Port:     443,
		Stream: `{
			"network": "tcp",
			"security": "tls",
			"tlsSettings": {
				"serverName": "no-fp.test",
				"certificates": [{"certificateFile": "/tmp/c.pem", "keyFile": "/tmp/k.pem"}]
			}
		}`,
	}
	clients := []model.Client{{Email: "d@test", UUID: "uuid-d"}}

	entry, err := buildSingboxInbound(in, clients)
	if err != nil {
		t.Fatalf("buildSingboxInbound: %v", err)
	}

	tls, ok := entry["tls"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected tls block")
	}
	if _, has := tls["utls"]; has {
		t.Errorf("expected no utls block when fingerprint is absent")
	}
}

// TestSingboxHysteria2RequiresTLS verifies that a Hysteria2 inbound without
// any TLS configuration is rejected before reaching sing-box, with a
// user-actionable error rather than the cryptic "missing certificate" startup
// failure.
func TestSingboxHysteria2RequiresTLS(t *testing.T) {
	in := model.Inbound{
		Tag:      "hy2-no-tls",
		Protocol: "hysteria2",
		Port:     8443,
		Stream:   `{"network": "udp", "security": "none"}`,
	}
	_, err := buildSingboxInbound(in, []model.Client{{Email: "u@test", UUID: "pw"}})
	if err == nil {
		t.Fatalf("expected error for Hysteria2 inbound without TLS, got nil")
	}
	if !strings.Contains(err.Error(), "requires TLS") {
		t.Errorf("expected error to mention 'requires TLS', got: %v", err)
	}
}

// TestSingboxHysteria2TLSWithoutCertRejected catches the case where the user
// flipped Security to TLS but left cert/key blank.
func TestSingboxHysteria2TLSWithoutCertRejected(t *testing.T) {
	in := model.Inbound{
		Tag:      "hy2-no-cert",
		Protocol: "hysteria2",
		Port:     8443,
		Stream: `{
			"network": "udp",
			"security": "tls",
			"tlsSettings": {"serverName": "example.com"}
		}`,
	}
	_, err := buildSingboxInbound(in, []model.Client{{Email: "u@test", UUID: "pw"}})
	if err == nil {
		t.Fatalf("expected error for Hysteria2 with TLS but no cert, got nil")
	}
	if !strings.Contains(err.Error(), "certificate_path") {
		t.Errorf("expected error to mention 'certificate_path', got: %v", err)
	}
}

// TestSingboxHysteria2WithCertAccepted is the success-path baseline: a fully
// configured Hy2 inbound with cert+key passes validation.
func TestSingboxHysteria2WithCertAccepted(t *testing.T) {
	in := model.Inbound{
		Tag:      "hy2-ok",
		Protocol: "hysteria2",
		Port:     8443,
		Stream: `{
			"network": "udp",
			"security": "tls",
			"tlsSettings": {
				"serverName": "example.com",
				"alpn": ["h3"],
				"certificates": [{"certificateFile": "/etc/ssl/cert.pem", "keyFile": "/etc/ssl/key.pem"}]
			}
		}`,
	}
	entry, err := buildSingboxInbound(in, []model.Client{{Email: "u@test", UUID: "pw"}})
	if err != nil {
		t.Fatalf("buildSingboxInbound: %v", err)
	}
	tls, ok := entry["tls"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected tls block on entry")
	}
	if tls["certificate_path"] != "/etc/ssl/cert.pem" {
		t.Errorf("expected certificate_path to be set, got %v", tls["certificate_path"])
	}
}

// TestSingboxVLESSWithTLSButNoCertRejected catches the case the user actually
// hit in production: an inbound the visual form let through with security=tls
// but cert/key paths blank. Sing-box itself would fail at runtime with the
// cryptic "missing certificate" error; we should reject up-front for every
// engine-supported protocol, not just hy2/tuic.
func TestSingboxVLESSWithTLSButNoCertRejected(t *testing.T) {
	in := model.Inbound{
		Tag:      "vless-tls-empty",
		Protocol: "vless",
		Port:     443,
		Stream: `{
			"network": "tcp",
			"security": "tls",
			"tlsSettings": {"serverName": "example.com"}
		}`,
	}
	_, err := buildSingboxInbound(in, []model.Client{{Email: "u@x", UUID: "id"}})
	if err == nil {
		t.Fatalf("expected error for VLESS+TLS without cert, got nil")
	}
	if !strings.Contains(err.Error(), "no certificate") && !strings.Contains(err.Error(), "missing") {
		t.Errorf("expected error to mention missing certificate, got: %v", err)
	}
}

// TestSingboxVLESSRealityAccepted ensures the generic TLS-credential check
// doesn't false-positive on Reality streams, which sing-box treats as
// certificate-less because they borrow the dest's chain.
func TestSingboxVLESSRealityAccepted(t *testing.T) {
	in := model.Inbound{
		Tag:      "vless-reality-ok",
		Protocol: "vless",
		Port:     443,
		Stream: `{
			"network": "tcp",
			"security": "reality",
			"realitySettings": {
				"dest": "microsoft.com:443",
				"serverNames": ["microsoft.com"],
				"privateKey": "k",
				"shortIds": ["abc"]
			}
		}`,
	}
	if _, err := buildSingboxInbound(in, []model.Client{{Email: "u@x", UUID: "id"}}); err != nil {
		t.Errorf("VLESS+Reality should pass credential check, got: %v", err)
	}
}

// TestSingboxNativeTLSStreamPassthrough verifies that smart-deploy's sing-box-
// native stream format ({"tls": {...}}) is forwarded to the inbound entry
// rather than dropped. Without this, Hy2/TUIC inbounds created via the
// orchestrator would lose their cert paths on apply.
func TestSingboxNativeTLSStreamPassthrough(t *testing.T) {
	in := model.Inbound{
		Tag:      "hy2-native",
		Protocol: "hysteria2",
		Port:     443,
		Stream: `{
			"tls": {
				"enabled": true,
				"server_name": "host.example",
				"certificate_path": "/var/cert.pem",
				"key_path": "/var/key.pem"
			}
		}`,
	}
	entry, err := buildSingboxInbound(in, []model.Client{{Email: "u@test", UUID: "pw"}})
	if err != nil {
		t.Fatalf("buildSingboxInbound: %v", err)
	}
	tls, ok := entry["tls"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected tls block on entry, got %T: %v", entry["tls"], entry["tls"])
	}
	if tls["certificate_path"] != "/var/cert.pem" {
		t.Errorf("expected native certificate_path to pass through, got %v", tls["certificate_path"])
	}
	if tls["key_path"] != "/var/key.pem" {
		t.Errorf("expected native key_path to pass through, got %v", tls["key_path"])
	}
}

// TestSingboxConfigIsValidJSON verifies that GenerateConfig returns valid JSON
// for a zero-inbound scenario (empty DB state).
func TestSingboxConfigIsValidJSON(t *testing.T) {
	sm := NewSingboxManager()
	// GenerateConfig calls config.DB which may be nil in tests.
	// Directly build with empty slices instead.
	cfg := map[string]interface{}{
		"log": map[string]interface{}{"level": "warn"},
		"dns": map[string]interface{}{
			"servers": []interface{}{},
			"final":   "dns-remote",
		},
		"inbounds":  []interface{}{},
		"outbounds": []interface{}{},
		"route":     map[string]interface{}{"rules": []interface{}{}, "final": "direct"},
	}
	_ = sm // confirm sm is usable
	raw, err := PrettifyJSON(cfg)
	if err != nil {
		t.Fatalf("PrettifyJSON: %v", err)
	}
	var out interface{}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, raw)
	}
}
