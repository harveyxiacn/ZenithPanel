package sub

import (
	"encoding/base64"
	"reflect"
	"strings"
	"testing"

	"github.com/harveyxiacn/ZenithPanel/backend/internal/model"
)

func setInboundServerAddress(t *testing.T, in *model.Inbound, value string) {
	t.Helper()

	rv := reflect.ValueOf(in).Elem()
	field := rv.FieldByName("ServerAddress")
	if !field.IsValid() {
		t.Fatalf("Inbound.ServerAddress field is missing")
	}
	if field.Kind() != reflect.String {
		t.Fatalf("Inbound.ServerAddress must be a string, got %s", field.Kind())
	}
	if !field.CanSet() {
		t.Fatalf("Inbound.ServerAddress is not settable")
	}
	field.SetString(value)
}

func TestParseStreamSupportsThreeXUIRealityShape(t *testing.T) {
	stream := `{
		"network": "tcp",
		"security": "reality",
		"realitySettings": {
			"target": "gateway.icloud.com:443",
			"serverNames": ["gateway.icloud.com"],
			"privateKey": "private-key",
			"shortIds": ["1d", "8aaa97"],
			"settings": {
				"publicKey": "public-key",
				"fingerprint": "chrome",
				"serverName": "",
				"spiderX": "/"
			}
		}
	}`

	got := parseStream(stream)

	if got.Network != "tcp" {
		t.Fatalf("expected network=tcp, got %q", got.Network)
	}
	if got.Security != "reality" {
		t.Fatalf("expected security=reality, got %q", got.Security)
	}
	if got.RealityPBK != "public-key" {
		t.Fatalf("expected reality public key, got %q", got.RealityPBK)
	}
	if got.RealitySID != "1d" {
		t.Fatalf("expected first short id, got %q", got.RealitySID)
	}
	if got.SNI != "gateway.icloud.com" {
		t.Fatalf("expected SNI from serverNames, got %q", got.SNI)
	}
	if got.Fingerprint != "chrome" {
		t.Fatalf("expected fingerprint from nested settings, got %q", got.Fingerprint)
	}
	if got.RealitySPX != "/" {
		t.Fatalf("expected spiderX=/, got %q", got.RealitySPX)
	}
}

func TestParseStreamNormalizesWSSToWS(t *testing.T) {
	stream := `{"network": "wss", "wsSettings": {"path": "/ws"}}`
	got := parseStream(stream)

	if got.Network != "ws" {
		t.Fatalf("expected wss normalized to ws, got %q", got.Network)
	}
	if got.Security != "tls" {
		t.Fatalf("expected wss to imply tls, got %q", got.Security)
	}
	if got.WSPath != "/ws" {
		t.Fatalf("expected wsPath=/ws, got %q", got.WSPath)
	}
}

func TestParseStreamH2Transport(t *testing.T) {
	stream := `{"network": "h2", "security": "tls", "httpSettings": {"path": "/h2path", "host": ["example.com"]}}`
	got := parseStream(stream)

	if got.Network != "h2" {
		t.Fatalf("expected network=h2, got %q", got.Network)
	}
	if got.H2Path != "/h2path" {
		t.Fatalf("expected h2Path=/h2path, got %q", got.H2Path)
	}
	if got.H2Host != "example.com" {
		t.Fatalf("expected h2Host=example.com, got %q", got.H2Host)
	}
}

func TestBuildVLESSLinkIncludesHeaderTypeAndReality(t *testing.T) {
	in := model.Inbound{
		Tag:      "vless-reality",
		Protocol: "vless",
		Port:     443,
		Settings: `{"flow": "xtls-rprx-vision"}`,
		Stream: `{
			"network": "tcp",
			"security": "reality",
			"realitySettings": {
				"serverNames": ["www.microsoft.com"],
				"shortIds": ["abcd"],
				"settings": {
					"publicKey": "test-pbk",
					"fingerprint": "chrome",
					"spiderX": "/"
				}
			}
		}`,
	}
	client := model.Client{UUID: "test-uuid"}
	si := parseStream(in.Stream)
	link := buildVLESSLink(in, client, "1.2.3.4", si)

	// Must contain headerType=none for TCP
	if !strings.Contains(link, "headerType=none") {
		t.Fatalf("expected headerType=none in VLESS link, got: %s", link)
	}
	// Must contain Reality params
	if !strings.Contains(link, "pbk=test-pbk") {
		t.Fatalf("expected pbk in VLESS link, got: %s", link)
	}
	if !strings.Contains(link, "sid=abcd") {
		t.Fatalf("expected sid in VLESS link, got: %s", link)
	}
	if !strings.Contains(link, "spx=") {
		t.Fatalf("expected spx in VLESS link, got: %s", link)
	}
	if !strings.Contains(link, "flow=xtls-rprx-vision") {
		t.Fatalf("expected flow in VLESS link, got: %s", link)
	}
	if !strings.Contains(link, "fp=chrome") {
		t.Fatalf("expected fp=chrome in VLESS link, got: %s", link)
	}
}

func TestBuildClashVLESSRealityHasFingerprint(t *testing.T) {
	inbounds := []model.Inbound{{
		Tag:      "test-reality",
		Protocol: "vless",
		Port:     443,
		Settings: `{"flow": "xtls-rprx-vision"}`,
		Stream: `{
			"network": "tcp",
			"security": "reality",
			"realitySettings": {
				"serverNames": ["www.microsoft.com"],
				"shortIds": ["ab"],
				"settings": {"publicKey": "pbk123", "fingerprint": "chrome"}
			}
		}`,
	}}
	client := model.Client{UUID: "test-uuid"}

	yaml := buildClashConfig(inbounds, client, "1.2.3.4")

	if !strings.Contains(yaml, "client-fingerprint: chrome") {
		t.Fatalf("expected client-fingerprint in Clash config, got:\n%s", yaml)
	}
	if !strings.Contains(yaml, "public-key: pbk123") {
		t.Fatalf("expected reality public-key in Clash config, got:\n%s", yaml)
	}
	if !strings.Contains(yaml, "flow: xtls-rprx-vision") {
		t.Fatalf("expected flow in Clash config, got:\n%s", yaml)
	}
}

func TestBuildClashConfigUsesInboundServerAddressOverride(t *testing.T) {
	inbounds := []model.Inbound{{
		Tag:      "test-reality",
		Protocol: "vless",
		Port:     443,
		Settings: `{"flow": "xtls-rprx-vision"}`,
		Stream: `{
			"network": "tcp",
			"security": "reality",
			"realitySettings": {
				"serverNames": ["www.microsoft.com"],
				"shortIds": ["ab"],
				"settings": {"publicKey": "pbk123", "fingerprint": "chrome"}
			}
		}`,
	}}
	setInboundServerAddress(t, &inbounds[0], "vpn.example.com")

	client := model.Client{UUID: "test-uuid"}
	yaml := buildClashConfig(inbounds, client, "panel.internal")

	if !strings.Contains(yaml, "server: vpn.example.com") {
		t.Fatalf("expected Clash config to use inbound server override, got:\n%s", yaml)
	}
	if !strings.Contains(yaml, "DOMAIN,vpn.example.com,DIRECT") {
		t.Fatalf("expected Clash direct rule to use inbound server override, got:\n%s", yaml)
	}
	if strings.Contains(yaml, "panel.internal") {
		t.Fatalf("expected panel request host to be excluded when inbound server override exists, got:\n%s", yaml)
	}
}

func TestBuildBase64LinksUsesTLSServerNameWhenRequestHostDiffers(t *testing.T) {
	inbounds := []model.Inbound{{
		Tag:      "trojan-tls",
		Protocol: "trojan",
		Port:     443,
		Stream: `{
			"network": "tcp",
			"security": "tls",
			"tlsSettings": {
				"serverName": "edge.example.com"
			}
		}`,
	}}

	client := model.Client{UUID: "test-uuid"}
	encoded := buildBase64Links(inbounds, client, "panel.internal")
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("decode base64 links: %v", err)
	}

	links := string(raw)
	if !strings.Contains(links, "trojan://test-uuid@edge.example.com:443") {
		t.Fatalf("expected base64 subscription links to use TLS serverName when request host differs, got: %s", links)
	}
	if strings.Contains(links, "@panel.internal:443") {
		t.Fatalf("expected request host to be excluded when TLS serverName is available, got: %s", links)
	}
}

func TestBuildBase64LinksUsesInboundServerAddressOverrideForReality(t *testing.T) {
	inbounds := []model.Inbound{{
		Tag:      "reality-node",
		Protocol: "vless",
		Port:     443,
		Settings: `{"flow":"xtls-rprx-vision"}`,
		Stream: `{
			"network":"tcp",
			"security":"reality",
			"realitySettings":{
				"serverNames":["www.microsoft.com"],
				"shortIds":["ab"],
				"settings":{"publicKey":"pbk123","fingerprint":"chrome","spiderX":"/"}
			}
		}`,
	}}
	setInboundServerAddress(t, &inbounds[0], "vpn.example.com")

	client := model.Client{UUID: "test-uuid"}
	encoded := buildBase64Links(inbounds, client, "panel.internal")
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("decode base64 links: %v", err)
	}

	links := string(raw)
	if !strings.Contains(links, "vless://test-uuid@vpn.example.com:443") {
		t.Fatalf("expected base64 reality link to use inbound server override, got: %s", links)
	}
	if strings.Contains(links, "@panel.internal:443") {
		t.Fatalf("expected request host to be excluded when inbound server override exists, got: %s", links)
	}
}
