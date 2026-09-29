// Package selftest verifies that a proxy node actually carries traffic: it
// turns the exact share link a user imports into a sing-box client, starts
// it, and fetches a URL through it. Unlike the port probe in
// service/diagnostic, this catches credential, Reality-target, TLS and
// subscription-generator mistakes that leave the port open but the node
// unusable.
package selftest

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// Outbound converts a share URI (vless://, vmess://, trojan://, ss://,
// hysteria2://, tuic://) into a sing-box outbound with tag "proxy".
func Outbound(link string) (map[string]any, error) {
	scheme, _, ok := strings.Cut(link, "://")
	if !ok {
		return nil, fmt.Errorf("not a share link")
	}
	switch strings.ToLower(scheme) {
	case "vless":
		return fromVLESSorTrojan(link, "vless")
	case "trojan":
		return fromVLESSorTrojan(link, "trojan")
	case "vmess":
		return fromVMess(link)
	case "ss":
		return fromSS(link)
	case "hysteria2", "hy2":
		return fromHysteria2(link)
	case "tuic":
		return fromTUIC(link)
	}
	return nil, fmt.Errorf("unsupported scheme %q", scheme)
}

func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func truthy(s string) bool { return s == "1" || strings.EqualFold(s, "true") }

func hostPort(u *url.URL) (string, int, error) {
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		return "", 0, fmt.Errorf("bad port in link")
	}
	return u.Hostname(), port, nil
}

// tlsBlock builds sing-box's outbound "tls" object from share-link fields.
func tlsBlock(security, sni, alpn, fp, pbk, sid string, insecure bool) map[string]any {
	if security != "tls" && security != "reality" {
		return nil
	}
	t := map[string]any{"enabled": true}
	if sni != "" {
		t["server_name"] = sni
	}
	if insecure {
		t["insecure"] = true
	}
	if a := splitCSV(alpn); len(a) > 0 {
		t["alpn"] = a
	}
	if security == "reality" {
		if fp == "" {
			fp = "chrome" // REALITY requires uTLS
		}
		t["reality"] = map[string]any{"enabled": true, "public_key": pbk, "short_id": sid}
	}
	if fp != "" {
		t["utls"] = map[string]any{"enabled": true, "fingerprint": fp}
	}
	return t
}

// transportBlock maps the share-link transport ("type"/"net") to sing-box's
// V2Ray transport object; nil means raw TCP.
func transportBlock(network, path, host, serviceName string) map[string]any {
	switch network {
	case "ws":
		t := map[string]any{"type": "ws"}
		if path != "" {
			t["path"] = path
		}
		if host != "" {
			t["headers"] = map[string]any{"Host": host}
		}
		return t
	case "grpc":
		return map[string]any{"type": "grpc", "service_name": serviceName}
	case "h2", "http":
		t := map[string]any{"type": "http"}
		if path != "" {
			t["path"] = path
		}
		if host != "" {
			t["host"] = []string{host}
		}
		return t
	case "httpupgrade":
		t := map[string]any{"type": "httpupgrade"}
		if path != "" {
			t["path"] = path
		}
		if host != "" {
			t["host"] = host
		}
		return t
	}
	return nil
}

func fromVLESSorTrojan(link, proto string) (map[string]any, error) {
	u, err := url.Parse(link)
	if err != nil {
		return nil, err
	}
	server, port, err := hostPort(u)
	if err != nil {
		return nil, err
	}
	q := u.Query()
	ob := map[string]any{"type": proto, "tag": "proxy", "server": server, "server_port": port}
	cred := u.User.Username()
	if proto == "vless" {
		ob["uuid"] = cred
		if f := q.Get("flow"); f != "" {
			ob["flow"] = f
		}
		ob["packet_encoding"] = "xudp"
	} else {
		ob["password"] = cred
	}
	insecure := truthy(q.Get("allowInsecure")) || truthy(q.Get("insecure"))
	if t := tlsBlock(q.Get("security"), q.Get("sni"), q.Get("alpn"), q.Get("fp"), q.Get("pbk"), q.Get("sid"), insecure); t != nil {
		ob["tls"] = t
	}
	if t := transportBlock(q.Get("type"), q.Get("path"), q.Get("host"), q.Get("serviceName")); t != nil {
		ob["transport"] = t
	}
	return ob, nil
}

func decodeB64(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if b, err := enc.DecodeString(s); err == nil {
			return b, nil
		}
	}
	return nil, fmt.Errorf("invalid base64")
}

func fromVMess(link string) (map[string]any, error) {
	raw, err := decodeB64(strings.TrimPrefix(link, "vmess://"))
	if err != nil {
		return nil, fmt.Errorf("vmess: %w", err)
	}
	var v map[string]any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, fmt.Errorf("vmess: %w", err)
	}
	str := func(k string) string { s, _ := v[k].(string); return s }
	port, err := strconv.Atoi(fmt.Sprint(v["port"]))
	if err != nil {
		return nil, fmt.Errorf("vmess: bad port")
	}
	ob := map[string]any{
		"type": "vmess", "tag": "proxy", "server": str("add"), "server_port": port,
		"uuid": str("id"), "security": "auto", "alter_id": 0,
	}
	if t := tlsBlock(str("tls"), str("sni"), str("alpn"), str("fp"), str("pbk"), str("sid"), truthy(str("allowInsecure"))); t != nil {
		ob["tls"] = t
	}
	if t := transportBlock(str("net"), str("path"), str("host"), str("path")); t != nil {
		ob["transport"] = t
	}
	return ob, nil
}

func fromSS(link string) (map[string]any, error) {
	u, err := url.Parse(link)
	if err != nil {
		return nil, err
	}
	if u.Query().Get("plugin") != "" {
		return nil, fmt.Errorf("shadowsocks plugins are not supported by the self-test")
	}
	server, port, err := hostPort(u)
	if err != nil {
		return nil, err
	}
	userinfo := u.User.String()
	if dec, err := url.PathUnescape(userinfo); err == nil {
		userinfo = dec
	}
	plain := userinfo
	if b, err := decodeB64(userinfo); err == nil && strings.Contains(string(b), ":") {
		plain = string(b)
	}
	method, password, ok := strings.Cut(plain, ":")
	if !ok {
		return nil, fmt.Errorf("shadowsocks: bad userinfo")
	}
	return map[string]any{
		"type": "shadowsocks", "tag": "proxy", "server": server, "server_port": port,
		"method": method, "password": password,
	}, nil
}

func fromHysteria2(link string) (map[string]any, error) {
	u, err := url.Parse(link)
	if err != nil {
		return nil, err
	}
	server, port, err := hostPort(u)
	if err != nil {
		return nil, err
	}
	q := u.Query()
	ob := map[string]any{
		"type": "hysteria2", "tag": "proxy", "server": server, "server_port": port,
		"password": u.User.Username(),
		"tls":      tlsBlock("tls", q.Get("sni"), q.Get("alpn"), "", "", "", truthy(q.Get("insecure"))),
	}
	if o := q.Get("obfs"); o != "" {
		ob["obfs"] = map[string]any{"type": o, "password": q.Get("obfs-password")}
	}
	return ob, nil
}

func fromTUIC(link string) (map[string]any, error) {
	u, err := url.Parse(link)
	if err != nil {
		return nil, err
	}
	server, port, err := hostPort(u)
	if err != nil {
		return nil, err
	}
	q := u.Query()
	pw, _ := u.User.Password()
	alpn := q.Get("alpn")
	if alpn == "" {
		alpn = "h3"
	}
	ob := map[string]any{
		"type": "tuic", "tag": "proxy", "server": server, "server_port": port,
		"uuid": u.User.Username(), "password": pw,
		"tls": tlsBlock("tls", q.Get("sni"), alpn, "", "", "", truthy(q.Get("allow_insecure")) || truthy(q.Get("insecure"))),
	}
	if cc := q.Get("congestion_control"); cc != "" {
		ob["congestion_control"] = cc
	}
	if m := q.Get("udp_relay_mode"); m != "" {
		ob["udp_relay_mode"] = m
	}
	if truthy(q.Get("zero_rtt_handshake")) {
		ob["zero_rtt_handshake"] = true
	}
	return ob, nil
}
