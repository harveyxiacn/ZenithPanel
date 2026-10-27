package selftest

import (
	"strings"
	"testing"
)

func realityOutbound() map[string]any {
	return map[string]any{
		"type": "vless", "tag": "proxy", "server": "198.51.100.20", "server_port": 443,
		"uuid": "00000000-0000-4000-8000-000000000001", "flow": "xtls-rprx-vision",
		"tls": map[string]any{
			"enabled": true, "server_name": "reality.example", "utls": map[string]any{"enabled": true, "fingerprint": "chrome"},
			"reality": map[string]any{"enabled": true, "public_key": "PUBLIC", "short_id": "0123456789abcdef"},
		},
	}
}

func TestXrayRealityConfigUsesIPv6Override(t *testing.T) {
	ob := realityOutbound()
	redirectServer(ob, "2001:db8::42")
	cfg, err := xrayRealityConfig(ob, 1080, "vless://unused?spx=%2Fprobe")
	if err != nil {
		t.Fatal(err)
	}
	out := cfg["outbounds"].([]any)[0].(map[string]any)
	vnext := out["settings"].(map[string]any)["vnext"].([]any)[0].(map[string]any)
	if vnext["address"] != "2001:db8::42" || vnext["port"] != 443 {
		t.Fatalf("IPv6 override not preserved in Xray vnext: %#v", vnext)
	}
	user := vnext["users"].([]any)[0].(map[string]any)
	if user["id"] != ob["uuid"] || user["flow"] != "xtls-rprx-vision" || user["encryption"] != "none" {
		t.Fatalf("bad VLESS user: %#v", user)
	}
	stream := out["streamSettings"].(map[string]any)
	reality := stream["realitySettings"].(map[string]any)
	if stream["network"] != "tcp" || reality["serverName"] != "reality.example" || reality["publicKey"] != "PUBLIC" || reality["shortId"] != "0123456789abcdef" || reality["spiderX"] != "/probe" {
		t.Fatalf("bad REALITY stream mapping: %#v", stream)
	}
	in := cfg["inbounds"].([]any)[0].(map[string]any)
	if in["listen"] != "127.0.0.1" || in["port"] != 1080 {
		t.Fatalf("bad local SOCKS inbound: %#v", in)
	}
}

func TestXrayRealityConfigSupportsGRPCAndRejectsOtherTransport(t *testing.T) {
	ob := realityOutbound()
	ob["transport"] = map[string]any{"type": "grpc", "service_name": "vless"}
	cfg, err := xrayRealityConfig(ob, 1080, "vless://unused")
	if err != nil {
		t.Fatal(err)
	}
	stream := cfg["outbounds"].([]any)[0].(map[string]any)["streamSettings"].(map[string]any)
	if stream["network"] != "grpc" || stream["grpcSettings"].(map[string]any)["serviceName"] != "vless" {
		t.Fatalf("bad gRPC mapping: %#v", stream)
	}

	ob["transport"] = map[string]any{"type": "ws"}
	_, err = xrayRealityConfig(ob, 1080, "vless://unused")
	if err == nil || !strings.Contains(err.Error(), "only TCP or gRPC") {
		t.Fatalf("unsupported REALITY transport error = %v", err)
	}
	ob = realityOutbound()
	_, err = xrayRealityConfig(ob, 1080, "vless://unused?type=ws")
	if err == nil || !strings.Contains(err.Error(), "only TCP or gRPC") {
		t.Fatalf("unsupported link transport error = %v", err)
	}
}

func TestClientEngineAndBinaryOverride(t *testing.T) {
	if got := clientEngine(realityOutbound()); got != "xray" {
		t.Fatalf("REALITY engine = %q, want xray", got)
	}
	if got := clientEngine(map[string]any{"type": "hysteria2"}); got != "sing-box" {
		t.Fatalf("Hy2 engine = %q, want sing-box", got)
	}
	if got := clientBinary("xray", ""); got != "xray" {
		t.Fatalf("REALITY default binary = %q", got)
	}
	if got := clientBinary("xray", "/opt/zenithpanel/bin/xray"); got != "/opt/zenithpanel/bin/xray" {
		t.Fatalf("custom selected-engine binary = %q", got)
	}
	if got := clientBinary("sing-box", ""); got != "sing-box" {
		t.Fatalf("Hy2 default binary = %q", got)
	}
	for engine, wantScheme := range map[string]string{"xray": "socks5", "sing-box": "http"} {
		proxyURL, err := localProxyURL(engine, "127.0.0.1:1080")
		if err != nil || proxyURL.Scheme != wantScheme || proxyURL.Host != "127.0.0.1:1080" {
			t.Fatalf("%s proxy URL = %#v, %v", engine, proxyURL, err)
		}
	}

	ob := realityOutbound()
	ob["tls"].(map[string]any)["reality"].(map[string]any)["short_id"] = ""
	cfg, err := xrayRealityConfig(ob, 1080, "vless://unused")
	if err != nil {
		t.Fatalf("empty REALITY short ID should be accepted: %v", err)
	}
	stream := cfg["outbounds"].([]any)[0].(map[string]any)["streamSettings"].(map[string]any)
	if got := stream["realitySettings"].(map[string]any)["shortId"]; got != "" {
		t.Fatalf("empty REALITY short ID was not preserved: %#v", got)
	}
}
