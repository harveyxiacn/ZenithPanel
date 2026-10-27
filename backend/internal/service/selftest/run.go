package selftest

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Result is the outcome of one end-to-end test.
type Result struct {
	OK        bool   `json:"ok"`
	ExitIP    string `json:"exit_ip,omitempty"`
	LatencyMs int64  `json:"latency_ms,omitempty"` // request round-trip through the proxy
	Err       string `json:"err,omitempty"`
}

// Options tunes a test run. Zero values pick sensible defaults.
type Options struct {
	// Server overrides the link's server address. The panel passes
	// 127.0.0.1 so the test exercises the engine, credentials, TLS/Reality
	// and the subscription link without depending on NAT hairpinning or
	// cloud firewalls (which it cannot observe from inside anyway).
	Server   string
	CheckURL string        // default: Cloudflare trace (reports the exit IP)
	Timeout  time.Duration // whole test; default 15s
	// Binary overrides the client binary selected for this run. VLESS links
	// using REALITY require an Xray client (default "xray") because the
	// bundled sing-box version is not compatible with this Xray REALITY
	// server; every other supported link uses sing-box (default "sing-box").
	Binary string
}

const defaultCheckURL = "https://www.cloudflare.com/cdn-cgi/trace"

// Run tests link end to end through the client engine compatible with it.
func Run(ctx context.Context, link string, opt Options) Result {
	if opt.CheckURL == "" {
		opt.CheckURL = defaultCheckURL
	}
	if opt.Timeout == 0 {
		opt.Timeout = 15 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, opt.Timeout)
	defer cancel()

	ob, err := Outbound(link)
	if err != nil {
		return Result{Err: "cannot parse share link: " + err.Error()}
	}
	if opt.Server != "" {
		redirectServer(ob, opt.Server)
	}

	port, err := freePort()
	if err != nil {
		return Result{Err: err.Error()}
	}
	engine := clientEngine(ob)
	var cfg map[string]any
	if engine == "xray" {
		cfg, err = xrayRealityConfig(ob, port, link)
	} else {
		cfg = map[string]any{
			"log":       map[string]any{"level": "error"},
			"inbounds":  []any{map[string]any{"type": "mixed", "tag": "in", "listen": "127.0.0.1", "listen_port": port}},
			"outbounds": []any{ob},
		}
	}
	if err != nil {
		return Result{Err: err.Error()}
	}
	opt.Binary = clientBinary(engine, opt.Binary)
	bin, err := exec.LookPath(opt.Binary)
	if err != nil {
		return Result{Err: fmt.Sprintf("%s client binary not found; end-to-end test unavailable", engine)}
	}
	raw, _ := json.Marshal(cfg)
	f, err := os.CreateTemp("", "zenith-selftest-*.json")
	if err != nil {
		return Result{Err: err.Error()}
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(raw); err != nil {
		f.Close()
		return Result{Err: err.Error()}
	}
	f.Close()

	var stderr bytes.Buffer
	args := []string{"run", "--disable-color", "-c", f.Name()}
	if engine == "xray" {
		args = []string{"run", "-c", f.Name()}
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Stderr = &stderr
	cmd.Stdout = io.Discard
	if err := cmd.Start(); err != nil {
		return Result{Err: "start " + engine + " client: " + err.Error()}
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	if err := waitListening(ctx, addr); err != nil {
		return Result{Err: engine + " client did not start: " + firstLine(stderr.String(), err.Error())}
	}

	proxyURL, err := localProxyURL(engine, addr)
	if err != nil {
		return Result{Err: "build local proxy URL: " + err.Error()}
	}
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL), DisableKeepAlives: true}}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, opt.CheckURL, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (ZenithPanel self-test)")
	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		msg := err.Error()
		if s := firstLine(stderr.String(), ""); s != "" {
			msg += " (client: " + s + ")"
		}
		return Result{Err: "request through proxy failed: " + msg}
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	latency := time.Since(start).Milliseconds()
	if resp.StatusCode >= 500 || resp.StatusCode == http.StatusProxyAuthRequired || resp.StatusCode == http.StatusBadGateway {
		return Result{LatencyMs: latency, Err: fmt.Sprintf("check URL returned HTTP %d through proxy", resp.StatusCode)}
	}
	return Result{OK: true, LatencyMs: latency, ExitIP: traceIP(string(body))}
}

func isVLESSReality(ob map[string]any) bool {
	if typ, _ := ob["type"].(string); typ != "vless" {
		return false
	}
	tls, _ := ob["tls"].(map[string]any)
	reality, _ := tls["reality"].(map[string]any)
	enabled, _ := reality["enabled"].(bool)
	return enabled
}

func clientEngine(ob map[string]any) string {
	if isVLESSReality(ob) {
		return "xray"
	}
	return "sing-box"
}

func clientBinary(engine, override string) string {
	if override != "" {
		return override
	}
	return engine
}

// localProxyURL matches the temporary inbound emitted by the selected client
// config. sing-box uses a mixed (HTTP/SOCKS) listener, whereas the Xray
// REALITY config deliberately emits a SOCKS-only listener.
func localProxyURL(engine, addr string) (*url.URL, error) {
	scheme := "http"
	if engine == "xray" {
		scheme = "socks5"
	}
	return url.Parse(scheme + "://" + addr)
}

// xrayRealityConfig converts the parsed VLESS share link into Xray's client
// schema. Current Xray REALITY supports raw TCP and gRPC only; rejecting other
// transports is intentional, because silently treating them as raw TCP would
// make the self-test pass against a different configuration than subscribers.
func xrayRealityConfig(ob map[string]any, socksPort int, link string) (map[string]any, error) {
	server, _ := ob["server"].(string)
	port, ok := ob["server_port"].(int)
	if !ok || server == "" || port < 1 || port > 65535 {
		return nil, fmt.Errorf("VLESS+REALITY self-test has an invalid server address or port")
	}
	uuid, _ := ob["uuid"].(string)
	if uuid == "" {
		return nil, fmt.Errorf("VLESS+REALITY self-test requires a UUID")
	}
	tls, _ := ob["tls"].(map[string]any)
	reality, _ := tls["reality"].(map[string]any)
	sni, _ := tls["server_name"].(string)
	publicKey, _ := reality["public_key"].(string)
	shortID, _ := reality["short_id"].(string)
	if sni == "" || publicKey == "" {
		return nil, fmt.Errorf("VLESS+REALITY self-test requires server name and public key")
	}
	fingerprint := "chrome"
	if utls, _ := tls["utls"].(map[string]any); utls != nil {
		if fp, _ := utls["fingerprint"].(string); fp != "" {
			fingerprint = fp
		}
	}

	network := "tcp"
	streamSettings := map[string]any{
		"network":  network,
		"security": "reality",
		"realitySettings": map[string]any{
			"show":        false,
			"fingerprint": fingerprint,
			"serverName":  sni,
			"publicKey":   publicKey,
			"shortId":     shortID,
		},
	}
	if transport, _ := ob["transport"].(map[string]any); transport != nil {
		typ, _ := transport["type"].(string)
		switch typ {
		case "grpc":
			network = "grpc"
			serviceName, _ := transport["service_name"].(string)
			streamSettings["network"] = network
			streamSettings["grpcSettings"] = map[string]any{"serviceName": serviceName}
		case "", "tcp":
			// Raw TCP is the default.
		default:
			return nil, fmt.Errorf("VLESS+REALITY self-test supports only TCP or gRPC transport, got %q", typ)
		}
	}
	if parsed, err := url.Parse(link); err == nil {
		linkNetwork := strings.ToLower(parsed.Query().Get("type"))
		switch linkNetwork {
		case "", "tcp", "raw", "grpc":
			// The parsed outbound above supplies the equivalent Xray setting.
		default:
			return nil, fmt.Errorf("VLESS+REALITY self-test supports only TCP or gRPC transport, got %q", linkNetwork)
		}
		if spiderX := parsed.Query().Get("spx"); spiderX != "" {
			streamSettings["realitySettings"].(map[string]any)["spiderX"] = spiderX
		}
	}

	user := map[string]any{"id": uuid, "encryption": "none"}
	if flow, _ := ob["flow"].(string); flow != "" {
		user["flow"] = flow
	}
	return map[string]any{
		"log": map[string]any{"loglevel": "error"},
		"inbounds": []any{map[string]any{
			"listen": "127.0.0.1", "port": socksPort, "protocol": "socks",
			"settings": map[string]any{"auth": "noauth", "udp": false},
		}},
		"outbounds": []any{map[string]any{
			"protocol": "vless", "tag": "proxy",
			"settings": map[string]any{"vnext": []any{map[string]any{
				"address": server, "port": port, "users": []any{user},
			}}},
			"streamSettings": streamSettings,
		}},
	}, nil
}

// redirectServer points the outbound at addr while keeping the TLS name a
// real client would use: with no explicit SNI in the link, clients send the
// server address, so that original address must stay the server_name or the
// certificate check would fail only because of the redirect.
func redirectServer(ob map[string]any, addr string) {
	if t, ok := ob["tls"].(map[string]any); ok {
		if sn, _ := t["server_name"].(string); sn == "" {
			if orig, _ := ob["server"].(string); orig != "" {
				t["server_name"] = orig
			}
		}
	}
	ob["server"] = addr
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

func waitListening(ctx context.Context, addr string) error {
	for {
		c, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			c.Close()
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// traceIP pulls "ip=…" out of a Cloudflare /cdn-cgi/trace body.
func traceIP(body string) string {
	sc := bufio.NewScanner(strings.NewReader(body))
	for sc.Scan() {
		if v, ok := strings.CutPrefix(sc.Text(), "ip="); ok {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func firstLine(s, fallback string) string {
	for _, l := range strings.Split(strings.TrimSpace(s), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			return l
		}
	}
	return fallback
}
