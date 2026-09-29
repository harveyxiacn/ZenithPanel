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
	Binary   string        // sing-box binary; default "sing-box" on PATH
}

const defaultCheckURL = "https://www.cloudflare.com/cdn-cgi/trace"

// Run tests `link` end to end: sing-box client → proxy node → CheckURL.
func Run(ctx context.Context, link string, opt Options) Result {
	if opt.CheckURL == "" {
		opt.CheckURL = defaultCheckURL
	}
	if opt.Timeout == 0 {
		opt.Timeout = 15 * time.Second
	}
	if opt.Binary == "" {
		opt.Binary = "sing-box"
	}
	bin, err := exec.LookPath(opt.Binary)
	if err != nil {
		return Result{Err: "sing-box binary not found; end-to-end test unavailable"}
	}
	ctx, cancel := context.WithTimeout(ctx, opt.Timeout)
	defer cancel()

	ob, err := Outbound(link)
	if err != nil {
		return Result{Err: "cannot parse share link: " + err.Error()}
	}
	if opt.Server != "" {
		ob["server"] = opt.Server
	}

	port, err := freePort()
	if err != nil {
		return Result{Err: err.Error()}
	}
	cfg := map[string]any{
		"log":       map[string]any{"level": "error"},
		"inbounds":  []any{map[string]any{"type": "mixed", "tag": "in", "listen": "127.0.0.1", "listen_port": port}},
		"outbounds": []any{ob},
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
	cmd := exec.CommandContext(ctx, bin, "run", "--disable-color", "-c", f.Name())
	cmd.Stderr = &stderr
	cmd.Stdout = io.Discard
	if err := cmd.Start(); err != nil {
		return Result{Err: "start sing-box client: " + err.Error()}
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	if err := waitListening(ctx, addr); err != nil {
		return Result{Err: "sing-box client did not start: " + firstLine(stderr.String(), err.Error())}
	}

	proxyURL, _ := url.Parse("http://" + addr)
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
