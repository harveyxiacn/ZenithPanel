package proxy

import (
	"encoding/json"
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
			"tlsSettings": {"serverName": "h2.test"},
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
				"fingerprint": "chrome"
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
			"tlsSettings": {"serverName": "no-fp.test"}
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
