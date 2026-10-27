package sub

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/config"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/model"
	"gopkg.in/yaml.v3"
	"gorm.io/gorm"
)

var subscriptionTestSeq atomic.Uint64

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

func testSubscriptionDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:sub_generator_%d?mode=memory&cache=shared", subscriptionTestSeq.Add(1))), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.Inbound{}, &model.Client{}); err != nil {
		t.Fatal(err)
	}
	original := config.DB
	config.DB = db
	InvalidateSubCache()
	t.Cleanup(func() {
		InvalidateSubCache()
		config.DB = original
	})
	return db
}

func subscriptionResponse(t *testing.T, uuid, query string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/sub/:uuid", GenerateSubscription)
	req := httptest.NewRequest(http.MethodGet, "/sub/"+uuid+query, nil)
	req.Host = "panel.example.test"
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func parsedClash(t *testing.T, body string) map[string]any {
	t.Helper()
	var parsed map[string]any
	if err := yaml.Unmarshal([]byte(body), &parsed); err != nil {
		t.Fatalf("invalid Clash YAML: %v\n%s", err, body)
	}
	return parsed
}

func proxyGroup(t *testing.T, config map[string]any, name string) map[string]any {
	t.Helper()
	groups, ok := config["proxy-groups"].([]any)
	if !ok {
		t.Fatalf("proxy-groups missing or malformed: %#v", config["proxy-groups"])
	}
	for _, raw := range groups {
		group, ok := raw.(map[string]any)
		if ok && group["name"] == name {
			return group
		}
	}
	t.Fatalf("proxy group %q missing", name)
	return nil
}

func stringList(t *testing.T, raw any) []string {
	t.Helper()
	items, ok := raw.([]any)
	if !ok {
		t.Fatalf("expected YAML list, got %#v", raw)
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		value, ok := item.(string)
		if !ok {
			t.Fatalf("expected string item, got %#v", item)
		}
		out = append(out, value)
	}
	return out
}

func TestDualStackClashSubscriptionAllProtocolsAndGroups(t *testing.T) {
	db := testSubscriptionDB(t)
	const uuid = "dual-stack-user"
	protocols := []string{"vless", "vmess", "trojan", "shadowsocks", "hysteria2", "tuic"}
	for i, protocol := range protocols {
		in := model.Inbound{
			Tag:               "node-" + protocol,
			Protocol:          protocol,
			Port:              20000 + i,
			Enable:            true,
			Settings:          `{"method":"aes-256-gcm","password":"server"}`,
			Stream:            `{"network":"tcp","security":"tls","tlsSettings":{"serverName":"node.example.com"}}`,
			ServerAddress:     "203.0.113.10",
			ServerAddressIPv4: "203.0.113.10",
			ServerAddressIPv6: "2001:db8:1::10",
		}
		if protocol == "hysteria2" || protocol == "tuic" {
			in.Network = "udp"
			in.Stream = `{"network":"udp","security":"tls","tlsSettings":{"serverName":"node.example.com","alpn":["h3"]}}`
		}
		if protocol == "vless" {
			// Exercise YAML quoting for proxy names and every group reference.
			in.Tag = "node-\"vless\\edge"
		}
		if err := db.Create(&in).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&model.Client{InboundID: in.ID, Email: "member-" + protocol, UUID: uuid, Enable: true}).Error; err != nil {
			t.Fatal(err)
		}
	}

	response := subscriptionResponse(t, uuid, "?format=clash&dual_stack=1")
	if response.Code != http.StatusOK {
		t.Fatalf("dual-stack subscription status = %d: %s", response.Code, response.Body.String())
	}
	parsed := parsedClash(t, response.Body.String())
	if parsed["allow-lan"] != false {
		t.Fatal("dual-stack profile must keep the client proxy local by default")
	}
	if parsed["ipv6"] != true {
		t.Fatalf("global IPv6 was not enabled: %#v", parsed["ipv6"])
	}
	dns, ok := parsed["dns"].(map[string]any)
	if !ok || dns["ipv6"] != true {
		t.Fatalf("DNS IPv6 was not enabled: %#v", parsed["dns"])
	}
	proxies, ok := parsed["proxies"].([]any)
	if !ok || len(proxies) != len(protocols)*2 {
		t.Fatalf("proxies = %#v, want %d dual-family proxies", parsed["proxies"], len(protocols)*2)
	}
	for _, raw := range proxies {
		proxy, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("malformed proxy: %#v", raw)
		}
		family, _ := proxy["ip-version"].(string)
		server, _ := proxy["server"].(string)
		switch family {
		case "ipv6":
			if server != "2001:db8:1::10" {
				t.Fatalf("IPv6 proxy server = %q", server)
			}
		case "ipv4":
			if server != "203.0.113.10" {
				t.Fatalf("IPv4 proxy server = %q", server)
			}
		default:
			t.Fatalf("unexpected proxy family %#v", proxy["ip-version"])
		}
	}

	proxy := proxyGroup(t, parsed, "PROXY")
	proxyChoices := stringList(t, proxy["proxies"])
	if len(proxyChoices) < 3 || proxyChoices[0] != "AUTO" || proxyChoices[1] != "IPV6-FIRST" || strings.Contains(strings.Join(proxyChoices, "|"), "FASTEST") {
		t.Fatalf("PROXY choices = %#v", proxyChoices)
	}
	if auto := proxyGroup(t, parsed, "AUTO"); auto["type"] != "url-test" || auto["url"] != "https://www.gstatic.com/generate_204" || auto["interval"] != 60 || auto["lazy"] != false || auto["timeout"] != 5000 || auto["tolerance"] != 20 {
		t.Fatalf("AUTO = %#v", auto)
	}
	fallback := proxyGroup(t, parsed, "IPV6-FIRST")
	if fallback["type"] != "fallback" {
		t.Fatalf("IPV6-FIRST = %#v", fallback)
	}
	fallbackNodes := stringList(t, fallback["proxies"])
	if len(fallbackNodes) != len(protocols)*2 || !strings.Contains(fallbackNodes[0], "· IPv6") || !strings.Contains(fallbackNodes[len(protocols)-1], "· IPv6") || !strings.Contains(fallbackNodes[len(protocols)], "· IPv4") {
		t.Fatalf("IPV6-FIRST order = %#v", fallbackNodes)
	}
}

func TestDualStackClashSubscriptionAuthorizationExpiryAndCacheSeparation(t *testing.T) {
	db := testSubscriptionDB(t)
	const uuid = "shared-user"
	valid := model.Inbound{Tag: "valid", Protocol: "vless", Port: 2443, Enable: true, ServerAddress: "198.51.100.20", ServerAddressIPv4: "198.51.100.20", ServerAddressIPv6: "2001:db8:2::20", Stream: `{"network":"tcp","security":"reality"}`}
	expired := model.Inbound{Tag: "expired", Protocol: "hysteria2", Port: 2444, Enable: true, ServerAddress: "198.51.100.21", ServerAddressIPv4: "198.51.100.21", ServerAddressIPv6: "2001:db8:2::21", Stream: `{"network":"udp","security":"tls"}`}
	unrelated := model.Inbound{Tag: "unrelated", Protocol: "tuic", Port: 2445, Enable: true, ServerAddress: "198.51.100.22", ServerAddressIPv4: "198.51.100.22", ServerAddressIPv6: "2001:db8:2::22", Stream: `{"network":"udp","security":"tls"}`}
	for _, in := range []*model.Inbound{&valid, &expired, &unrelated} {
		if err := db.Create(in).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Create(&model.Client{InboundID: valid.ID, Email: "valid", UUID: uuid, Enable: true}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.Client{InboundID: expired.ID, Email: "expired", UUID: uuid, Enable: true, ExpiryTime: time.Now().Add(-time.Minute).Unix()}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.Client{InboundID: unrelated.ID, Email: "unrelated", UUID: "other-user", Enable: true}).Error; err != nil {
		t.Fatal(err)
	}

	regular := subscriptionResponse(t, uuid, "?format=clash")
	if regular.Code != http.StatusOK || strings.Contains(regular.Body.String(), "ip-version:") || !strings.Contains(regular.Body.String(), "server: 198.51.100.20") {
		t.Fatalf("regular subscription changed or rejected: %d\n%s", regular.Code, regular.Body.String())
	}
	dual := subscriptionResponse(t, uuid, "?format=clash&dual_stack=true")
	if dual.Code != http.StatusOK {
		t.Fatalf("dual-stack subscription status = %d: %s", dual.Code, dual.Body.String())
	}
	if !strings.Contains(dual.Body.String(), "valid · IPv6") || !strings.Contains(dual.Body.String(), "valid · IPv4") {
		t.Fatalf("valid dual-stack nodes missing:\n%s", dual.Body.String())
	}
	for _, forbidden := range []string{"expired", "unrelated", "2001:db8:2::21", "2001:db8:2::22"} {
		if strings.Contains(dual.Body.String(), forbidden) {
			t.Fatalf("subscription leaked %q:\n%s", forbidden, dual.Body.String())
		}
	}
}

func TestDualStackClashNodesDoNotFabricateMissingFamily(t *testing.T) {
	inbounds := []model.Inbound{
		{ID: 1, Tag: "v4", Protocol: "vless", Port: 443, ServerAddress: "198.51.100.30"},
		{ID: 2, Tag: "v6", Protocol: "hysteria2", Port: 8443, ServerAddress: "2001:db8:3::30"},
		{ID: 3, Tag: "dual-dns", Protocol: "tuic", Port: 9443, ServerAddress: "edge.example.com"},
		{ID: 4, Tag: "mixed", Protocol: "vmess", Port: 10443, ServerAddress: "198.51.100.31", ServerAddressIPv6: "2001:db8:3::31"},
		{ID: 5, Tag: "duplicate", Protocol: "trojan", Port: 11443, ServerAddressIPv4: "198.51.100.32"},
		{ID: 6, Tag: "duplicate", Protocol: "trojan", Port: 12443, ServerAddressIPv4: "198.51.100.33"},
		{ID: 7, Tag: "unsupported", Protocol: "wireguard", Port: 13443, ServerAddressIPv4: "198.51.100.34"},
	}
	nodes := dualStackClashNodes(inbounds, "panel.example.test")
	if len(nodes) != 8 {
		t.Fatalf("nodes = %#v, want eight real/family-resolved endpoints", nodes)
	}
	want := map[string]string{
		"v4 · IPv4": "198.51.100.30", "v6 · IPv6": "2001:db8:3::30",
		"dual-dns · IPv6": "edge.example.com", "dual-dns · IPv4": "edge.example.com",
		"mixed · IPv6": "2001:db8:3::31", "mixed · IPv4": "198.51.100.31",
		"duplicate · IPv4": "198.51.100.32", "duplicate · IPv4 #6": "198.51.100.33",
	}
	for _, node := range nodes {
		if got, ok := want[node.Name]; !ok || got != node.Server {
			t.Fatalf("unexpected node %#v", node)
		}
	}
}

func TestResolveServerAddressPrefersConfiguredFamilyLiterals(t *testing.T) {
	in := model.Inbound{Protocol: "vless", Port: 443, ServerAddressIPv6: "2001:db8:4::40", Stream: `{"network":"tcp","security":"tls","tlsSettings":{"serverName":"ignored.example.com"}}`}
	if got := ResolveServerAddress(in, "127.0.0.1"); got != "2001:db8:4::40" {
		t.Fatalf("ResolveServerAddress = %q, want configured IPv6", got)
	}
	client := model.Client{UUID: "test-uuid"}
	links := buildBase64Links([]model.Inbound{in}, client, "127.0.0.1")
	raw, err := base64.StdEncoding.DecodeString(links)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "@[2001:db8:4::40]:443") {
		t.Fatalf("base64 subscription did not use configured IPv6: %s", raw)
	}
	clash := buildClashConfig([]model.Inbound{in}, client, "127.0.0.1")
	if !strings.Contains(clash, "server: 2001:db8:4::40") || strings.Contains(clash, "server: 127.0.0.1") || strings.Contains(clash, "server: ignored.example.com") {
		t.Fatalf("regular Clash subscription did not use configured IPv6:\n%s", clash)
	}
}

// TestMihomoParsesGeneratedDualStackAllProtocols is intentionally opt-in so
// normal unit-test runs do not download or require a client binary. CI/release
// validation sets MIHOMO_BIN to an official Mihomo executable. The YAML comes
// from the production generator, not a hand-written fixture.
func TestMihomoParsesGeneratedDualStackAllProtocols(t *testing.T) {
	binary := os.Getenv("MIHOMO_BIN")
	if binary == "" {
		t.Skip("set MIHOMO_BIN to validate generated YAML with Mihomo")
	}
	const uuid = "11111111-1111-4111-8111-111111111111"
	protocols := []string{"vless", "vmess", "trojan", "shadowsocks", "hysteria2", "tuic"}
	inbounds := make([]model.Inbound, 0, len(protocols))
	for i, protocol := range protocols {
		in := model.Inbound{
			ID:                uint(i + 1),
			Tag:               "mihomo-" + protocol,
			Protocol:          protocol,
			Port:              31000 + i,
			Settings:          `{"method":"aes-256-gcm","password":"classic-shared-password"}`,
			Stream:            `{"network":"tcp","security":"tls","tlsSettings":{"serverName":"node.example.com"}}`,
			ServerAddressIPv4: "203.0.113.10",
			ServerAddressIPv6: "2001:db8:1::10",
		}
		if protocol == "vless" {
			in.Settings = `{"flow":"xtls-rprx-vision"}`
			in.Stream = `{"network":"tcp","security":"reality","realitySettings":{"serverNames":["www.microsoft.com"],"shortIds":["0123456789abcdef"],"settings":{"publicKey":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA","fingerprint":"chrome"}}}`
		}
		if protocol == "hysteria2" || protocol == "tuic" {
			in.Network = "udp"
			in.Stream = `{"network":"udp","security":"tls","tlsSettings":{"serverName":"node.example.com","alpn":["h3"]}}`
		}
		inbounds = append(inbounds, in)
	}

	path := filepath.Join(t.TempDir(), "dual-stack-all-protocols.yaml")
	if err := os.WriteFile(path, []byte(buildClashDualStackConfig(inbounds, model.Client{UUID: uuid}, "")), 0o600); err != nil {
		t.Fatal(err)
	}
	args := []string{"-t", "-f", path}
	if home := os.Getenv("MIHOMO_HOME"); home != "" {
		args = append(args, "-d", home)
	}
	output, err := exec.Command(binary, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("mihomo rejected generated dual-stack configuration: %v\n%s", err, output)
	}
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

func TestSubscriptionLinksBracketIPv6Authorities(t *testing.T) {
	const server = "2001:db8:42::7"
	const endpoint = "[2001:db8:42::7]:443"
	client := model.Client{UUID: "client-uuid"}

	vless := model.Inbound{Tag: "vless", Protocol: "vless", Port: 443, Stream: `{"network":"tcp","security":"reality"}`}
	hy2 := model.Inbound{Tag: "hy2", Protocol: "hysteria2", Port: 443, Stream: `{"network":"udp","security":"tls"}`}
	trojan := model.Inbound{Tag: "trojan", Protocol: "trojan", Port: 443, Stream: `{"network":"tcp","security":"tls"}`}
	ss := model.Inbound{Tag: "ss", Protocol: "shadowsocks", Port: 443, Settings: `{"method":"aes-256-gcm","password":"secret"}`}
	tuic := model.Inbound{Tag: "tuic", Protocol: "tuic", Port: 443, Stream: `{"network":"udp","security":"tls"}`}

	links := []string{
		buildVLESSLink(vless, client, server, parseStream(vless.Stream)),
		buildHysteria2Link(hy2, client, server, parseStream(hy2.Stream), hy2.Tag),
		buildTrojanLink(trojan, client, server, parseStream(trojan.Stream), trojan.Tag),
		buildSSLink(ss, client, server, ss.Tag),
		buildTUICLink(tuic, client, server, parseStream(tuic.Stream), tuic.Tag),
	}
	for _, link := range links {
		if !strings.Contains(link, "@"+endpoint) {
			t.Fatalf("expected bracketed IPv6 authority %q in %q", endpoint, link)
		}
		if strings.Contains(link, "@"+server+":443") {
			t.Fatalf("found ambiguous unbracketed IPv6 authority in %q", link)
		}
	}

	if got := subscriptionHostPort("["+server+"]", 443); got != endpoint {
		t.Fatalf("already bracketed IPv6 endpoint = %q, want %q", got, endpoint)
	}
}

func TestBuildClashConfigUsesIPv6CIDRRule(t *testing.T) {
	inbounds := []model.Inbound{
		{Tag: "vless-v6", Protocol: "vless", Port: 443, Stream: `{"network":"tcp","security":"reality"}`},
		{Tag: "hy2-v6", Protocol: "hysteria2", Port: 8443, Stream: `{"network":"udp","security":"tls"}`},
	}
	yaml := buildClashConfig(inbounds, model.Client{UUID: "client-uuid"}, "2001:db8:42::7")

	if !strings.Contains(yaml, "IP-CIDR6,2001:db8:42::7/128,DIRECT,no-resolve") {
		t.Fatalf("expected IPv6 direct rule, got:\n%s", yaml)
	}
	if strings.Contains(yaml, "IP-CIDR,2001:db8:42::7/32") {
		t.Fatalf("IPv6 address must not be emitted as an IPv4 CIDR, got:\n%s", yaml)
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
	subCachePut("expired-entitlement", subCacheEntry{body: "x", storedAt: time.Now(), expiresAt: time.Now().Add(-time.Second)})
	if _, ok := subCacheGet("expired-entitlement"); ok {
		t.Fatalf("cache served a subscription after its client entitlement expired")
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

func TestIsLocalHost(t *testing.T) {
	for _, h := range []string{"127.0.0.1", "localhost", "unix", "::1", "[::1]", ""} {
		if !isLocalHost(h) {
			t.Errorf("%q should be local", h)
		}
	}
	for _, h := range []string{"203.0.113.10", "vpn.example.com", "2603:c020::1"} {
		if isLocalHost(h) {
			t.Errorf("%q should not be local", h)
		}
	}
}

// Smart Deploy stores sing-box-native TLS; self-signed nodes must be shared
// with the insecure flag or clients reject the certificate.
func TestParseStreamSingboxNativeTLS(t *testing.T) {
	si := parseStream(`{"tls":{"enabled":true,"server_name":"203.0.113.10","insecure":true,"alpn":["h3"]}}`)
	if si.Security != "tls" || si.SNI != "203.0.113.10" || !si.AllowInsecure || si.ALPN != "h3" {
		t.Fatalf("got %+v", si)
	}
	link := buildHysteria2Link(model.Inbound{Tag: "hy2", Port: 443, Settings: "{}",
		Stream: `{"tls":{"enabled":true,"server_name":"203.0.113.10","insecure":true}}`},
		model.Client{UUID: "pw"}, "203.0.113.10", si, "hy2")
	if !strings.Contains(link, "insecure=1") || !strings.Contains(link, "sni=203.0.113.10") {
		t.Fatalf("link missing insecure/sni: %s", link)
	}
}
