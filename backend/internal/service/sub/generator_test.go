package sub

import (
	"encoding/base64"
	"reflect"
	"strings"
	"testing"
	"time"

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

func TestBuildVMessLinkIncludesHTTPUpgradeAndReality(t *testing.T) {
	// httpupgrade transport
	in := model.Inbound{
		Tag: "vmess-hu", Protocol: "vmess", Port: 8443,
		Stream: `{
			"network": "httpupgrade",
			"security": "tls",
			"tlsSettings": {"serverName": "example.com", "alpn": ["h2", "http/1.1"]},
			"httpupgradeSettings": {"path": "/hu", "host": "api.example.com"}
		}`,
	}
	client := model.Client{UUID: "abc-uuid"}
	si := parseStream(in.Stream)
	link := buildVMessLink(in, client, "1.2.3.4", si)

	if !strings.HasPrefix(link, "vmess://") {
		t.Fatalf("expected vmess:// prefix, got %s", link)
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(link, "vmess://"))
	if err != nil {
		t.Fatalf("decode vmess payload: %v", err)
	}
	payload := string(raw)
	if !strings.Contains(payload, `"net":"httpupgrade"`) {
		t.Fatalf("expected httpupgrade net field, got: %s", payload)
	}
	if !strings.Contains(payload, `"path":"/hu"`) {
		t.Fatalf("expected path=/hu, got: %s", payload)
	}
	if !strings.Contains(payload, `"host":"api.example.com"`) {
		t.Fatalf("expected host header, got: %s", payload)
	}
	if !strings.Contains(payload, `"alpn":"h2,http/1.1"`) {
		t.Fatalf("expected alpn preserved, got: %s", payload)
	}

	// Reality for VMess
	inReality := model.Inbound{
		Tag: "vmess-reality", Protocol: "vmess", Port: 443,
		Stream: `{
			"network": "tcp",
			"security": "reality",
			"realitySettings": {
				"serverNames": ["www.microsoft.com"],
				"shortIds": ["ab"],
				"settings": {"publicKey": "pbk-x", "fingerprint": "chrome"}
			}
		}`,
	}
	siR := parseStream(inReality.Stream)
	linkR := buildVMessLink(inReality, client, "1.2.3.4", siR)
	rawR, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(linkR, "vmess://"))
	if !strings.Contains(string(rawR), `"tls":"reality"`) {
		t.Fatalf("expected reality security, got: %s", string(rawR))
	}
	if !strings.Contains(string(rawR), `"pbk":"pbk-x"`) {
		t.Fatalf("expected pbk field, got: %s", string(rawR))
	}
}

func TestBuildTrojanLinkIncludesRealityParams(t *testing.T) {
	in := model.Inbound{
		Tag: "trojan-reality", Protocol: "trojan", Port: 443,
		Stream: `{
			"network": "tcp",
			"security": "reality",
			"realitySettings": {
				"serverNames": ["www.google.com"],
				"shortIds": ["ff"],
				"settings": {"publicKey": "tpbk", "fingerprint": "firefox"}
			}
		}`,
	}
	client := model.Client{UUID: "trojan-pass"}
	si := parseStream(in.Stream)
	link := buildTrojanLink(in, client, "1.2.3.4", si, "trojan-reality")

	if !strings.Contains(link, "security=reality") {
		t.Fatalf("expected security=reality, got: %s", link)
	}
	if !strings.Contains(link, "pbk=tpbk") {
		t.Fatalf("expected pbk in trojan link, got: %s", link)
	}
	if !strings.Contains(link, "sid=ff") {
		t.Fatalf("expected sid in trojan link, got: %s", link)
	}
	if !strings.Contains(link, "fp=firefox") {
		t.Fatalf("expected fingerprint in trojan link, got: %s", link)
	}
}

func TestBuildHysteria2LinkRespectsObfsAndInsecureFlag(t *testing.T) {
	in := model.Inbound{
		Tag: "hy2", Protocol: "hysteria2", Port: 443,
		Settings: `{
			"obfs": {"type": "salamander", "password": "obfspw"},
			"ports": "20000-30000"
		}`,
		Stream: `{
			"network": "udp",
			"security": "tls",
			"tlsSettings": {"serverName": "hy.example.com", "allowInsecure": false}
		}`,
	}
	client := model.Client{UUID: "hy-uuid"}
	si := parseStream(in.Stream)
	link := buildHysteria2Link(in, client, "1.2.3.4", si, "hy2")

	if !strings.HasPrefix(link, "hysteria2://hy-uuid@") {
		t.Fatalf("expected hysteria2 prefix, got: %s", link)
	}
	if !strings.Contains(link, "obfs=salamander") {
		t.Fatalf("expected obfs param, got: %s", link)
	}
	if !strings.Contains(link, "obfs-password=obfspw") {
		t.Fatalf("expected obfs-password, got: %s", link)
	}
	if !strings.Contains(link, "mport=20000-30000") {
		t.Fatalf("expected port hopping mport, got: %s", link)
	}
	// allowInsecure=false means no insecure=1
	if strings.Contains(link, "insecure=1") {
		t.Fatalf("expected no insecure when cert verification enabled, got: %s", link)
	}

	// Now test with allowInsecure=true
	in.Stream = `{"network":"udp","security":"tls","tlsSettings":{"serverName":"hy","allowInsecure":true}}`
	si2 := parseStream(in.Stream)
	link2 := buildHysteria2Link(in, client, "1.2.3.4", si2, "hy2")
	if !strings.Contains(link2, "insecure=1") {
		t.Fatalf("expected insecure=1 when allowInsecure=true, got: %s", link2)
	}
}

// TestBuildSSLinkSS2022CombinesServerAndUserPSK pins the multi-user-mode
// password format: clients must authenticate with `serverPSK:userPSK`.
// Pre-fix the share URL was emitting only the server PSK, so SS-2022
// inbounds silently rejected every client.
func TestBuildSSLinkSS2022CombinesServerAndUserPSK(t *testing.T) {
	in := model.Inbound{
		Tag: "ss-2022", Protocol: "shadowsocks", Port: 31404,
		Settings: `{"method":"2022-blake3-aes-128-gcm","password":"SERVER-PSK"}`,
	}
	client := model.Client{UUID: "USER-PSK", Email: "u"}
	link := buildSSLink(in, client, "1.2.3.4", "ss-2022")

	at := strings.Index(link, "@")
	if at < 0 {
		t.Fatalf("malformed ss link: %s", link)
	}
	userInfoB64 := link[len("ss://"):at]
	decoded, err := base64.RawURLEncoding.DecodeString(userInfoB64)
	if err != nil {
		t.Fatalf("userinfo not base64-url decodable: %v", err)
	}
	want := "2022-blake3-aes-128-gcm:SERVER-PSK:USER-PSK"
	if string(decoded) != want {
		t.Errorf("userinfo = %q, want %q", string(decoded), want)
	}
}

// TestSSClientPasswordLegacyCipherIsServerOnly: for legacy non-2022 ciphers
// there is no multi-user mode, so the client password is just the server PSK
// regardless of any UUID set on the row.
func TestSSClientPasswordLegacyCipherIsServerOnly(t *testing.T) {
	got := ssClientPassword("aes-256-gcm", "server-pw", "user-pw")
	if got != "server-pw" {
		t.Errorf("legacy cipher: want server-only password, got %q", got)
	}
}

func TestBuildSSLinkUsesRawURLEncodingAndPlugin(t *testing.T) {
	in := model.Inbound{
		Tag: "ss", Protocol: "shadowsocks", Port: 8388,
		Settings: `{
			"method": "aes-256-gcm",
			"password": "secret",
			"plugin": "obfs-local",
			"plugin_opts": "obfs=tls;obfs-host=www.bing.com"
		}`,
	}
	link := buildSSLink(in, model.Client{}, "1.2.3.4", "ss-node")

	if !strings.HasPrefix(link, "ss://") {
		t.Fatalf("expected ss:// prefix, got: %s", link)
	}
	if strings.Contains(link, "=") && !strings.Contains(link, "plugin=") {
		// userinfo shouldn't contain padding — check no '=' before '@'
		at := strings.Index(link, "@")
		if at > 0 && strings.Contains(link[5:at], "=") {
			t.Fatalf("expected SIP002 raw url-safe userinfo (no padding), got: %s", link)
		}
	}
	if !strings.Contains(link, "plugin=obfs-local") {
		t.Fatalf("expected plugin query param, got: %s", link)
	}
}

func TestBuildTUICLink(t *testing.T) {
	in := model.Inbound{
		Tag: "tuic-node", Protocol: "tuic", Port: 8443,
		Settings: `{"congestion_control":"bbr","udp_relay_mode":"native","zero_rtt_handshake":true}`,
		Stream: `{
			"network": "udp",
			"security": "tls",
			"tlsSettings": {"serverName": "tuic.example.com", "alpn": ["h3"]}
		}`,
	}
	client := model.Client{UUID: "tuic-uuid"}
	si := parseStream(in.Stream)
	link := buildTUICLink(in, client, "1.2.3.4", si, "tuic-node")

	if !strings.HasPrefix(link, "tuic://tuic-uuid:tuic-uuid@1.2.3.4:8443") {
		t.Fatalf("expected tuic://uuid:password@host:port form, got: %s", link)
	}
	if !strings.Contains(link, "sni=tuic.example.com") {
		t.Fatalf("expected sni in tuic link, got: %s", link)
	}
	if !strings.Contains(link, "alpn=h3") {
		t.Fatalf("expected alpn in tuic link, got: %s", link)
	}
	if !strings.Contains(link, "congestion_control=bbr") {
		t.Fatalf("expected congestion_control in tuic link, got: %s", link)
	}
	if !strings.Contains(link, "udp_relay_mode=native") {
		t.Fatalf("expected udp_relay_mode in tuic link, got: %s", link)
	}
	if !strings.Contains(link, "zero_rtt_handshake=1") {
		t.Fatalf("expected zero_rtt_handshake in tuic link, got: %s", link)
	}
}

func TestBuildClashTUICEmitsCorrectFields(t *testing.T) {
	inbounds := []model.Inbound{{
		Tag: "tuic-clash", Protocol: "tuic", Port: 9443,
		Settings: `{"congestion_control":"cubic","udp_relay_mode":"native"}`,
		Stream:   `{"network":"udp","security":"tls","tlsSettings":{"serverName":"tuic.test"}}`,
	}}
	yaml := buildClashConfig(inbounds, model.Client{UUID: "u"}, "1.2.3.4")
	if !strings.Contains(yaml, "type: tuic") {
		t.Fatalf("expected type: tuic in clash output, got:\n%s", yaml)
	}
	if !strings.Contains(yaml, "congestion-controller: cubic") {
		t.Fatalf("expected congestion-controller field, got:\n%s", yaml)
	}
	if !strings.Contains(yaml, "udp-relay-mode: native") {
		t.Fatalf("expected udp-relay-mode field, got:\n%s", yaml)
	}
}

func TestSubCacheInvalidate(t *testing.T) {
	subCachePut("test-key", subCacheEntry{body: "x", storedAt: time.Now()})
	if _, ok := subCacheGet("test-key"); !ok {
		t.Fatalf("expected cache hit before invalidation")
	}
	InvalidateSubCache()
	if _, ok := subCacheGet("test-key"); ok {
		t.Fatalf("expected cache miss after InvalidateSubCache")
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
