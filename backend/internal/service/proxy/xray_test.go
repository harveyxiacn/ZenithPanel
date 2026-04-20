package proxy

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/harveyxiacn/ZenithPanel/backend/internal/model"
)

func TestRingBufferRetainsOnlyLastNBytes(t *testing.T) {
	r := newRingBuffer(10)

	// Small writes
	r.Write([]byte("hello"))
	r.Write([]byte("world"))
	if got := r.String(); got != "helloworld" {
		t.Fatalf("expected helloworld, got %q", got)
	}

	// Write that exceeds capacity — must keep only the trailing 10 bytes
	r.Write([]byte("abcdefghij")) // head wraps; content now: "worldabcdefghij"[-10:] == "bcdefghij" + last char
	got := r.String()
	if len(got) != 10 {
		t.Fatalf("expected len 10, got %d: %q", len(got), got)
	}
	if !strings.HasSuffix(got, "j") {
		t.Fatalf("expected ring buffer to end with last written byte, got %q", got)
	}

	// Huge write larger than buffer — must keep only last 10 bytes
	r.Reset()
	big := strings.Repeat("X", 100) + "ABCDEFGHIJ"
	r.Write([]byte(big))
	if got := r.String(); got != "ABCDEFGHIJ" {
		t.Fatalf("expected ABCDEFGHIJ, got %q", got)
	}
}

func TestSplitAndTrimCSV(t *testing.T) {
	input := " geosite:cn, geoip:private , ,443 , 8443-9443 "
	got := splitAndTrimCSV(input)
	want := []string{"geosite:cn", "geoip:private", "443", "8443-9443"}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("splitAndTrimCSV() = %#v, want %#v", got, want)
	}
}

func TestBuildXrayRoutingRuleIncludesPortAndSlices(t *testing.T) {
	rule := model.RoutingRule{
		OutboundTag: "block",
		Domain:      "geosite:category-ads-all, geosite:cn",
		IP:          "geoip:private, 1.1.1.1",
		Port:        "443, 8443-9443",
	}

	got := buildXrayRoutingRule(rule)

	if got["type"] != "field" {
		t.Fatalf("expected type=field, got %v", got["type"])
	}
	if got["outboundTag"] != "block" {
		t.Fatalf("expected outboundTag=block, got %v", got["outboundTag"])
	}

	wantDomains := []string{"geosite:category-ads-all", "geosite:cn"}
	if !reflect.DeepEqual(got["domain"], wantDomains) {
		t.Fatalf("expected domain=%#v, got %#v", wantDomains, got["domain"])
	}

	wantIPs := []string{"geoip:private", "1.1.1.1"}
	if !reflect.DeepEqual(got["ip"], wantIPs) {
		t.Fatalf("expected ip=%#v, got %#v", wantIPs, got["ip"])
	}

	if got["port"] != "443,8443-9443" {
		t.Fatalf("expected port=%q, got %#v", "443,8443-9443", got["port"])
	}
}

func TestBuildXrayRoutingRuleOmitsEmptyFields(t *testing.T) {
	rule := model.RoutingRule{OutboundTag: "direct"}
	got := buildXrayRoutingRule(rule)

	if _, ok := got["domain"]; ok {
		t.Fatalf("expected empty domain to be omitted")
	}
	if _, ok := got["ip"]; ok {
		t.Fatalf("expected empty ip to be omitted")
	}
	if _, ok := got["port"]; ok {
		t.Fatalf("expected empty port to be omitted")
	}
}

func TestNormalizeRoutingRuleCanonicalizesValues(t *testing.T) {
	rule := NormalizeRoutingRule(model.RoutingRule{
		RuleTag:     "  Block Ads  ",
		Domain:      " geosite:cn,geosite:cn, geosite:category-ads-all ",
		IP:          " geoip:private, 1.1.1.1,geoip:private ",
		Port:        " 8443-9443, 443,443 ",
		OutboundTag: " block ",
	})

	if rule.RuleTag != "Block Ads" {
		t.Fatalf("expected trimmed rule tag, got %q", rule.RuleTag)
	}
	if rule.Domain != "geosite:category-ads-all,geosite:cn" {
		t.Fatalf("unexpected normalized domain: %q", rule.Domain)
	}
	if rule.IP != "1.1.1.1,geoip:private" {
		t.Fatalf("unexpected normalized ip: %q", rule.IP)
	}
	if rule.Port != "443,8443-9443" {
		t.Fatalf("unexpected normalized port: %q", rule.Port)
	}
	if rule.OutboundTag != "block" {
		t.Fatalf("expected trimmed outbound tag, got %q", rule.OutboundTag)
	}
}

func TestUniqueRoutingRulesSkipsEquivalentEntries(t *testing.T) {
	rules := []model.RoutingRule{
		{ID: 1, RuleTag: "Block Ads", Domain: "geosite:category-ads-all", OutboundTag: "block", Enable: true},
		{ID: 2, RuleTag: "Block Ads Copy", Domain: " geosite:category-ads-all ", OutboundTag: " block ", Enable: true},
		{ID: 3, RuleTag: "Block Private", IP: "geoip:private", OutboundTag: "block", Enable: true},
	}

	got := UniqueRoutingRules(rules)
	if len(got) != 2 {
		t.Fatalf("expected 2 unique rules, got %d", len(got))
	}
	if got[0].ID != 1 || got[1].ID != 3 {
		t.Fatalf("expected to keep first instance of duplicates, got IDs %d and %d", got[0].ID, got[1].ID)
	}
}

func TestNormalizeXrayStreamSettingsRealityCompatibility(t *testing.T) {
	stream := map[string]interface{}{}
	if err := json.Unmarshal([]byte(`{
		"network": "tcp",
		"security": "reality",
		"realitySettings": {
			"dest": "gateway.icloud.com:443",
			"serverNames": ["gateway.icloud.com"],
			"privateKey": "priv",
			"publicKey": "legacy-public",
			"shortIds": ["1d"],
			"fingerprint": "chrome",
			"settings": {
				"publicKey": "nested-public",
				"fingerprint": "firefox",
				"serverName": "",
				"spiderX": "/"
			}
		}
	}`), &stream); err != nil {
		t.Fatalf("unmarshal stream: %v", err)
	}

	got := NormalizeXrayStreamSettings(stream)
	reality, ok := got["realitySettings"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected realitySettings map")
	}
	// Xray-core uses "dest", not "target"
	if reality["dest"] != "gateway.icloud.com:443" {
		t.Fatalf("expected dest to be set, got %#v", reality["dest"])
	}
	if _, ok := reality["target"]; ok {
		t.Fatalf("expected legacy target field to be removed")
	}
	if _, ok := reality["settings"]; ok {
		t.Fatalf("expected client-only settings metadata to be removed from runtime config")
	}
	if _, ok := reality["publicKey"]; ok {
		t.Fatalf("expected publicKey to be removed from runtime config")
	}
	if _, ok := reality["fingerprint"]; ok {
		t.Fatalf("expected fingerprint to be removed from runtime config")
	}
	if _, ok := got["tcpSettings"]; !ok {
		t.Fatalf("expected tcpSettings defaults for tcp reality inbound")
	}
}

func TestNormalizeXrayStreamSettingsRealityFromTarget(t *testing.T) {
	// Test that "target" from frontend is also normalized to "dest"
	stream := map[string]interface{}{}
	if err := json.Unmarshal([]byte(`{
		"network": "tcp",
		"security": "reality",
		"realitySettings": {
			"target": "www.microsoft.com:443",
			"serverNames": ["www.microsoft.com"],
			"privateKey": "priv",
			"shortIds": ["ab"]
		}
	}`), &stream); err != nil {
		t.Fatalf("unmarshal stream: %v", err)
	}

	got := NormalizeXrayStreamSettings(stream)
	reality := got["realitySettings"].(map[string]interface{})
	if reality["dest"] != "www.microsoft.com:443" {
		t.Fatalf("expected dest=www.microsoft.com:443, got %v", reality["dest"])
	}
	if _, ok := reality["target"]; ok {
		t.Fatalf("target should be removed after normalization to dest")
	}
}
