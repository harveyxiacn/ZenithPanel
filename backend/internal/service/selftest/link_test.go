package selftest

import (
	"testing"

	"github.com/harveyxiacn/ZenithPanel/backend/internal/model"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/service/sub"
)

// Links are produced by the real subscription generator so the parser is
// tested against exactly what users import.
func TestOutboundFromGeneratedLinks(t *testing.T) {
	client := model.Client{UUID: "00000000-0000-4000-8000-000000000001", Email: "u1"}
	cases := []struct {
		name  string
		in    model.Inbound
		check func(t *testing.T, ob map[string]any)
	}{
		{
			name: "vless reality vision",
			in: model.Inbound{Tag: "r", Protocol: "vless", Port: 443,
				Settings: `{"decryption":"none","flow":"xtls-rprx-vision"}`,
				Stream:   `{"network":"tcp","security":"reality","realitySettings":{"target":"www.apple.com:443","serverNames":["www.apple.com"],"privateKey":"x","shortIds":["ab12"],"settings":{"publicKey":"PUBKEY","fingerprint":"chrome"}}}`},
			check: func(t *testing.T, ob map[string]any) {
				if ob["type"] != "vless" || ob["uuid"] != client.UUID || ob["flow"] != "xtls-rprx-vision" || ob["server_port"] != 443 {
					t.Fatalf("bad vless outbound: %v", ob)
				}
				tls := ob["tls"].(map[string]any)
				r := tls["reality"].(map[string]any)
				if tls["server_name"] != "www.apple.com" || r["public_key"] != "PUBKEY" || r["short_id"] != "ab12" {
					t.Fatalf("bad reality tls: %v", tls)
				}
				if tls["utls"].(map[string]any)["fingerprint"] != "chrome" {
					t.Fatalf("reality needs utls: %v", tls)
				}
			},
		},
		{
			name: "vmess ws tls",
			in: model.Inbound{Tag: "vm", Protocol: "vmess", Port: 31402,
				Stream: `{"network":"ws","security":"tls","wsSettings":{"path":"/vmess","headers":{"Host":"a.example"}},"tlsSettings":{"serverName":"a.example"}}`},
			check: func(t *testing.T, ob map[string]any) {
				tr := ob["transport"].(map[string]any)
				if ob["type"] != "vmess" || ob["uuid"] != client.UUID || tr["type"] != "ws" || tr["path"] != "/vmess" {
					t.Fatalf("bad vmess outbound: %v", ob)
				}
				if ob["tls"].(map[string]any)["server_name"] != "a.example" {
					t.Fatalf("bad vmess tls: %v", ob["tls"])
				}
			},
		},
		{
			name: "trojan tls insecure",
			in: model.Inbound{Tag: "tj", Protocol: "trojan", Port: 31403,
				Stream: `{"network":"tcp","security":"tls","tlsSettings":{"serverName":"b.example","allowInsecure":true}}`},
			check: func(t *testing.T, ob map[string]any) {
				tls := ob["tls"].(map[string]any)
				if ob["password"] != client.UUID || tls["insecure"] != true {
					t.Fatalf("bad trojan outbound: %v", ob)
				}
			},
		},
		{
			name: "shadowsocks 2022 multi-user",
			in: model.Inbound{Tag: "ss", Protocol: "shadowsocks", Port: 31404,
				Settings: `{"method":"2022-blake3-aes-128-gcm","password":"c2VydmVyUFNLMTIzNDU2Nw=="}`},
			check: func(t *testing.T, ob map[string]any) {
				if ob["method"] != "2022-blake3-aes-128-gcm" || ob["password"] != "c2VydmVyUFNLMTIzNDU2Nw==:"+client.UUID {
					t.Fatalf("bad ss outbound: %v", ob)
				}
			},
		},
		{
			name: "hysteria2 obfs",
			in: model.Inbound{Tag: "hy2", Protocol: "hysteria2", Port: 8443,
				Settings: `{"obfs":{"type":"salamander","password":"ob"}}`,
				Stream:   `{"network":"udp","security":"tls","tlsSettings":{"serverName":"c.example","alpn":["h3"]}}`},
			check: func(t *testing.T, ob map[string]any) {
				obfs := ob["obfs"].(map[string]any)
				if ob["password"] != client.UUID || obfs["type"] != "salamander" || obfs["password"] != "ob" {
					t.Fatalf("bad hy2 outbound: %v", ob)
				}
			},
		},
		{
			name: "tuic",
			in: model.Inbound{Tag: "tuic", Protocol: "tuic", Port: 31406,
				Settings: `{"congestion_control":"bbr"}`,
				Stream:   `{"network":"udp","security":"tls","tlsSettings":{"serverName":"d.example","alpn":["h3"]}}`},
			check: func(t *testing.T, ob map[string]any) {
				if ob["uuid"] != client.UUID || ob["password"] != client.UUID || ob["congestion_control"] != "bbr" {
					t.Fatalf("bad tuic outbound: %v", ob)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			link := sub.ShareLink(tc.in, client, "203.0.113.10")
			if link == "" {
				t.Fatalf("generator produced no link")
			}
			ob, err := Outbound(link)
			if err != nil {
				t.Fatalf("Outbound(%q): %v", link, err)
			}
			if ob["server"] != "203.0.113.10" || ob["tag"] != "proxy" {
				t.Fatalf("server/tag wrong: %v", ob)
			}
			tc.check(t, ob)
		})
	}
}

func TestOutboundRejectsGarbage(t *testing.T) {
	for _, l := range []string{"", "http://x", "vless://nouser", "vmess://!!!"} {
		if _, err := Outbound(l); err == nil {
			t.Errorf("expected error for %q", l)
		}
	}
}

func TestTraceIP(t *testing.T) {
	if got := traceIP("fl=1\nip=203.0.113.10\nloc=US\n"); got != "203.0.113.10" {
		t.Fatalf("traceIP = %q", got)
	}
}

func TestRedirectServerKeepsTLSName(t *testing.T) {
	ob := map[string]any{"server": "203.0.113.10", "tls": map[string]any{"enabled": true}}
	redirectServer(ob, "127.0.0.1")
	if ob["server"] != "127.0.0.1" || ob["tls"].(map[string]any)["server_name"] != "203.0.113.10" {
		t.Fatalf("got %v", ob)
	}
	ob = map[string]any{"server": "203.0.113.10", "tls": map[string]any{"enabled": true, "server_name": "www.apple.com"}}
	redirectServer(ob, "127.0.0.1")
	if ob["tls"].(map[string]any)["server_name"] != "www.apple.com" {
		t.Fatalf("explicit SNI overwritten: %v", ob)
	}
	ob = map[string]any{"server": "203.0.113.10", "method": "aes-128-gcm"} // no TLS
	redirectServer(ob, "127.0.0.1")
	if _, has := ob["tls"]; has {
		t.Fatalf("TLS block invented for a non-TLS outbound")
	}
}
