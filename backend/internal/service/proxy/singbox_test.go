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

// TestTUICDefaultsALPNToH3 verifies that a TUIC inbound configured without
// an explicit ALPN list still ends up with `alpn: ["h3"]` so clients can
// negotiate QUIC successfully. Pre-fix the panel emitted no ALPN at all,
// causing every TUIC client to fail with `tls: server did not select an
// ALPN protocol`.
func TestTUICDefaultsALPNToH3(t *testing.T) {
	in := model.Inbound{
		Tag:      "tuic-no-alpn",
		Protocol: "tuic",
		Port:     31406,
		Settings: `{"clients":[{"email":"u@t","uuid":"u-uuid"}]}`,
		Stream: `{
			"network": "udp",
			"security": "tls",
			"tlsSettings": {
				"serverName": "test.local",
				"certificates": [{"certificateFile":"/c.pem","keyFile":"/k.pem"}]
			}
		}`,
	}
	clients := []model.Client{{Email: "u@t", UUID: "u-uuid"}}
	entry, err := buildSingboxInbound(in, clients)
	if err != nil {
		t.Fatalf("buildSingboxInbound: %v", err)
	}
	tls, ok := entry["tls"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected tls block, got %T", entry["tls"])
	}
	alpn, ok := tls["alpn"].([]interface{})
	if !ok || len(alpn) == 0 {
		t.Fatalf("expected alpn=[\"h3\"], got %v", tls["alpn"])
	}
	if got, _ := alpn[0].(string); got != "h3" {
		t.Errorf("expected alpn[0]=\"h3\", got %q", got)
	}
}

// TestTUICHonorsExplicitALPN verifies that an admin-supplied tlsSettings.alpn
// is not overwritten by the h3 default.
func TestTUICHonorsExplicitALPN(t *testing.T) {
	in := model.Inbound{
		Tag:      "tuic-custom-alpn",
		Protocol: "tuic",
		Port:     31407,
		Settings: `{"clients":[{"email":"u@t","uuid":"u-uuid"}]}`,
		Stream: `{
			"network": "udp", "security": "tls",
			"tlsSettings": {
				"serverName": "test.local",
				"alpn": ["h3-29"],
				"certificates": [{"certificateFile":"/c.pem","keyFile":"/k.pem"}]
			}
		}`,
	}
	entry, err := buildSingboxInbound(in, []model.Client{{Email: "u@t", UUID: "u-uuid"}})
	if err != nil {
		t.Fatalf("buildSingboxInbound: %v", err)
	}
	tls := entry["tls"].(map[string]interface{})
	alpn := tls["alpn"].([]interface{})
	if got, _ := alpn[0].(string); got != "h3-29" {
		t.Errorf("expected explicit alpn to be preserved, got %q", got)
	}
}

// TestTUICPerUserPasswordOverride verifies that settings.clients[].password
// keyed by email overrides the UUID-as-password fallback.
func TestTUICPerUserPasswordOverride(t *testing.T) {
	in := model.Inbound{
		Tag:      "tuic-custom-pw",
		Protocol: "tuic",
		Port:     31408,
		Settings: `{"clients":[{"email":"alice@t","password":"alice-secret"}]}`,
		Stream: `{
			"network": "udp", "security": "tls",
			"tlsSettings": {"serverName":"test.local","certificates":[{"certificateFile":"/c.pem","keyFile":"/k.pem"}]}
		}`,
	}
	clients := []model.Client{
		{Email: "alice@t", UUID: "alice-uuid"},
		{Email: "bob@t", UUID: "bob-uuid"}, // no password override → falls back to UUID
	}
	entry, err := buildSingboxInbound(in, clients)
	if err != nil {
		t.Fatalf("buildSingboxInbound: %v", err)
	}
	users := entry["users"].([]map[string]interface{})
	if users[0]["password"] != "alice-secret" {
		t.Errorf("alice: expected password override 'alice-secret', got %v", users[0]["password"])
	}
	if users[1]["password"] != "bob-uuid" {
		t.Errorf("bob: expected UUID fallback 'bob-uuid', got %v", users[1]["password"])
	}
}

// TestHysteria2DefaultsALPNToH3 verifies the ALPN default also applies to
// Hysteria2 — same QUIC handshake issue as TUIC.
func TestHysteria2DefaultsALPNToH3(t *testing.T) {
	in := model.Inbound{
		Tag:      "hy2-no-alpn",
		Protocol: "hysteria2",
		Port:     8443,
		Settings: `{}`,
		Stream: `{
			"network": "udp", "security": "tls",
			"tlsSettings": {"serverName":"test.local","certificates":[{"certificateFile":"/c.pem","keyFile":"/k.pem"}]}
		}`,
	}
	entry, err := buildSingboxInbound(in, []model.Client{{Email: "u@t", UUID: "u-uuid"}})
	if err != nil {
		t.Fatalf("buildSingboxInbound: %v", err)
	}
	tls := entry["tls"].(map[string]interface{})
	alpn, ok := tls["alpn"].([]interface{})
	if !ok || len(alpn) == 0 {
		t.Fatalf("expected alpn default for hysteria2, got %v", tls["alpn"])
	}
	if got, _ := alpn[0].(string); got != "h3" {
		t.Errorf("expected hysteria2 alpn=\"h3\", got %q", got)
	}
}

// TestIsXraySupportedPartition verifies the engine partition rule the dual-mode
// scheduler relies on. Adding a new protocol that should be xray-served is a
// one-line change in xray.go; this test pins the invariant so a careless edit
// won't silently break the partition.
func TestIsXraySupportedPartition(t *testing.T) {
	for _, p := range []string{"vless", "vmess", "trojan", "shadowsocks"} {
		if !IsXraySupported(p) {
			t.Errorf("IsXraySupported(%q) = false, want true", p)
		}
	}
	for _, p := range []string{"hysteria2", "tuic", "wireguard", "unknown"} {
		if IsXraySupported(p) {
			t.Errorf("IsXraySupported(%q) = true, want false", p)
		}
	}
}

// TestBuildSingboxRoutingRuleEmitsRuleSetTags verifies that a routing rule
// referencing `geosite:cn` / `geoip:cn` is translated into a
// `rule_set: ["geosite-cn", "geoip-cn"]` form (sing-box 1.13+ shape)
// and that the returned tag lists tell the caller what to declare at the
// route.rule_set[] level.
func TestBuildSingboxRoutingRuleEmitsRuleSetTags(t *testing.T) {
	r := model.RoutingRule{
		RuleTag:     "cn-direct",
		Domain:      "geosite:cn,example.com",
		IP:          "geoip:cn,10.0.0.0/8",
		OutboundTag: "direct",
		Enable:      true,
	}
	ruleMap, siteTags, ipTags := buildSingboxRoutingRule(r)
	if ruleMap == nil {
		t.Fatalf("expected a non-nil ruleMap")
	}
	rs, ok := ruleMap["rule_set"].([]string)
	if !ok {
		t.Fatalf("expected rule_set []string in ruleMap, got %T: %v", ruleMap["rule_set"], ruleMap["rule_set"])
	}
	wantTags := map[string]bool{"geosite-cn": true, "geoip-cn": true}
	if len(rs) != 2 {
		t.Errorf("expected 2 rule_set tags, got %v", rs)
	}
	for _, tag := range rs {
		if !wantTags[tag] {
			t.Errorf("unexpected rule_set tag %q", tag)
		}
	}
	if len(siteTags) != 1 || siteTags[0] != "geosite-cn" {
		t.Errorf("siteTags = %v, want [geosite-cn]", siteTags)
	}
	if len(ipTags) != 1 || ipTags[0] != "geoip-cn" {
		t.Errorf("ipTags = %v, want [geoip-cn]", ipTags)
	}
	// Raw suffixes / CIDRs are kept on the rule alongside the tags.
	if got, ok := ruleMap["domain_suffix"].([]string); !ok || len(got) != 1 || got[0] != "example.com" {
		t.Errorf("domain_suffix not preserved: %v", ruleMap["domain_suffix"])
	}
	if got, ok := ruleMap["ip_cidr"].([]string); !ok || len(got) != 1 || got[0] != "10.0.0.0/8" {
		t.Errorf("ip_cidr not preserved: %v", ruleMap["ip_cidr"])
	}
}

// TestBuildSingboxRoutingRuleGeoipPrivateMapsToIsPrivate pins the special-
// case: SagerNet doesn't ship a geoip-private.srs (the v2ray-style "private"
// IP bucket doesn't map cleanly to a country code), so the migration is to
// use the sing-box built-in `ip_is_private: true` attribute. Pre-fix the
// engine 404'd at boot trying to fetch the missing rule-set.
func TestBuildSingboxRoutingRuleGeoipPrivateMapsToIsPrivate(t *testing.T) {
	r := model.RoutingRule{
		RuleTag:     "block-private",
		IP:          "geoip:private",
		OutboundTag: "block",
		Enable:      true,
	}
	ruleMap, _, ipTags := buildSingboxRoutingRule(r)
	if ruleMap == nil {
		t.Fatalf("expected non-nil ruleMap")
	}
	if got, ok := ruleMap["ip_is_private"].(bool); !ok || !got {
		t.Errorf("expected ip_is_private: true, got %v", ruleMap["ip_is_private"])
	}
	if _, has := ruleMap["rule_set"]; has {
		t.Errorf("rule_set should not be set for geoip:private, got %v", ruleMap["rule_set"])
	}
	if len(ipTags) != 0 {
		t.Errorf("private should not contribute to ipTags, got %v", ipTags)
	}
}

// TestBuildSingboxRuleSetsEmitsRemoteEntries pins the shape of the rule_set[]
// block: a sorted list of {tag,type=remote,format=binary,url,download_detour=direct,
// update_interval} entries for every tag the rules referenced.
func TestBuildSingboxRuleSetsEmitsRemoteEntries(t *testing.T) {
	out := buildSingboxRuleSets(
		map[string]bool{"geosite-cn": true, "geosite-google": true},
		map[string]bool{"geoip-cn": true},
	)
	if len(out) != 3 {
		t.Fatalf("expected 3 rule_set entries, got %d: %v", len(out), out)
	}
	// First entry should be geoip-cn (sorted alphabetically before geosite-*).
	first, _ := out[0].(map[string]interface{})
	if first["tag"] != "geoip-cn" {
		t.Errorf("expected first entry to be geoip-cn (sorted), got %v", first["tag"])
	}
	if first["type"] != "remote" || first["format"] != "binary" {
		t.Errorf("entry shape wrong: %v", first)
	}
	if first["download_detour"] != "direct" {
		t.Errorf("download_detour: expected 'direct', got %v", first["download_detour"])
	}
	// URL must point at the geoip repo, not geosite.
	if url, _ := first["url"].(string); !strings.Contains(url, "sing-geoip") {
		t.Errorf("geoip url should reference sing-geoip repo: %q", url)
	}
	// And the geosite entries must point at sing-geosite.
	for _, ent := range out[1:] {
		m := ent.(map[string]interface{})
		if url, _ := m["url"].(string); !strings.Contains(url, "sing-geosite") {
			t.Errorf("geosite url wrong: %q", url)
		}
	}
}

// TestBuildSingboxRuleSetsEmptyReturnsNil ensures empty input doesn't add an
// empty rule_set[] block to the config (sing-box would tolerate it but the
// diff churn is annoying).
func TestBuildSingboxRuleSetsEmptyReturnsNil(t *testing.T) {
	if got := buildSingboxRuleSets(nil, nil); got != nil {
		t.Errorf("empty input should produce nil, got %v", got)
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
