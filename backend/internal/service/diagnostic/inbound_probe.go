package diagnostic

import (
	"context"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/harveyxiacn/ZenithPanel/backend/internal/model"
)

// InboundProbeResult is the shape returned by /api/v1/proxy/test/:id.
// `Stage` follows a short, machine-readable vocabulary that mirrors the
// progression of an inbound connection. On success it is empty; on failure
// it pins down where the probe stopped.
type InboundProbeResult struct {
	InboundID uint   `json:"inbound_id"`
	Tag       string `json:"tag"`
	Protocol  string `json:"protocol"`
	Transport string `json:"transport"`
	Port      int    `json:"port"`
	// End-to-end result (see service/selftest): "passed", "failed" or
	// "skipped" (no enabled client, or quick mode). Empty when the port
	// probe already failed. On "failed", OK is false and Stage is "proxy".
	Family string `json:"family"`
	// Verification is the strongest check completed by this local probe. A
	// UDP listener check is deliberately not a QUIC/Hysteria2 handshake.
	Verification string `json:"verification"`
	OK           bool   `json:"ok"`
	Stage        string `json:"stage,omitempty"`
	ElapsedMs    int64  `json:"elapsed_ms"`
	Err          string `json:"err,omitempty"`
	E2E          string `json:"e2e,omitempty"`
	ExitIP       string `json:"exit_ip,omitempty"`
	E2ELatency   int64  `json:"e2e_latency_ms,omitempty"`
}

// quicProtocols are served over UDP/QUIC; they cannot be TCP-dialed and the
// probe checks for a bound UDP listener instead. Keep this in sync with the
// engine partitioner in service/proxy — anything not here that's not
// vless/vmess/trojan/ss falls back to a generic TCP probe.
var quicProtocols = map[string]bool{
	"hysteria2": true,
	"tuic":      true,
}

// requiresTLSDial decides whether the prober should drive a TLS handshake
// after the TCP connect. We only do this for inbounds that explicitly speak
// TLS in their stream config (security=tls). Reality and plain TCP carry no
// TLS handshake the prober can complete from outside the protocol.
func requiresTLSDial(in model.Inbound) bool {
	s := strings.ToLower(in.Stream)
	if strings.Contains(s, "\"security\":\"reality\"") {
		return false
	}
	// Match both Xray-style `"security":"tls"` and Sing-box native `"tls":{"enabled":true`.
	if strings.Contains(s, "\"security\":\"tls\"") || strings.Contains(s, "\"tls\":{\"enabled\":true") {
		return true
	}
	return false
}

// ProbeInbound performs a defensive, panel-local connectivity check against
// an inbound's bound port:
//
//   - Step 1 ("bound"): the kernel must report the port as listening in
//     /proc/net/{tcp,udp}{,6}. If not, no further work is useful.
//   - Step 2 ("tcp"): for TCP-style inbounds we dial the selected local
//     address family with a short timeout; success means the engine accepted
//     a TCP connection.
//   - Step 3 ("tls"): if the inbound's stream config asks for TLS, we drive
//     a TLS handshake with InsecureSkipVerify so a self-signed cert (test
//     setup) doesn't poison the result.
//   - Step 3 ("udp"): for QUIC-style inbounds we send a 16-byte sentinel.
//     Sing-box's QUIC servers silently drop malformed packets, so this only
//     proves a local UDP listener is reachable, not that a client can
//     authenticate.
//
// The probe never reaches outside the box and never modifies state.
func ProbeInbound(in model.Inbound) InboundProbeResult {
	return ProbeInboundFamily(in, "auto")
}

// ProbeInboundFamily performs a panel-local probe using "auto", "ipv4", or
// "ipv6". Requesting IPv6 verifies that listener specifically instead of
// allowing a successful IPv4 probe to mask a missing IPv6 bind.
func ProbeInboundFamily(in model.Inbound, family string) InboundProbeResult {
	start := time.Now()
	res := InboundProbeResult{
		InboundID: in.ID,
		Tag:       in.Tag,
		Protocol:  in.Protocol,
		Port:      in.Port,
	}
	res.Transport = inferTransport(in)
	dialHost, selectedFamily, err := ProbeDialHost(in, family)
	if err != nil {
		res.Family = normalizeProbeFamily(family)
		res.Stage = "address_family"
		res.Err = err.Error()
		res.ElapsedMs = time.Since(start).Milliseconds()
		return res
	}
	res.Family = selectedFamily

	isQUIC := quicProtocols[in.Protocol]
	netProto := "tcp"
	if isQUIC {
		netProto = "udp"
	}

	if !portIsBoundForFamily(in.Port, netProto, selectedFamily) {
		res.Stage = "not_bound"
		res.Err = fmt.Sprintf("nothing is listening on %s port %d", netProto, in.Port)
		res.ElapsedMs = time.Since(start).Milliseconds()
		return res
	}

	dialAddr := net.JoinHostPort(dialHost, strconv.Itoa(in.Port))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if isQUIC {
		// UDP "probe": open a connected socket and send a 16-byte sentinel.
		// QUIC servers reject malformed packets silently, but the kernel will
		// surface ECONNREFUSED if nothing is bound. portIsBound covers the
		// happy path above; this catches a stale /proc table.
		conn, err := (&net.Dialer{}).DialContext(ctx, "udp", dialAddr)
		if err != nil {
			res.Stage = "udp"
			res.Err = err.Error()
			res.ElapsedMs = time.Since(start).Milliseconds()
			return res
		}
		defer conn.Close()
		sentinel, _ := hex.DecodeString("c0a83f7f00000000000000000000007f")
		_ = conn.SetWriteDeadline(time.Now().Add(time.Second))
		if _, err := conn.Write(sentinel); err != nil {
			res.Stage = "udp"
			res.Err = err.Error()
			res.ElapsedMs = time.Since(start).Milliseconds()
			return res
		}
		res.OK = true
		// A successful UDP write is only local bind evidence. QUIC servers
		// commonly drop this malformed packet without replying, so callers
		// must use a real protocol client to prove authentication/egress.
		res.Verification = "udp_local_bind"
		res.ElapsedMs = time.Since(start).Milliseconds()
		return res
	}

	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", dialAddr)
	if err != nil {
		res.Stage = "tcp"
		res.Err = err.Error()
		res.ElapsedMs = time.Since(start).Milliseconds()
		return res
	}
	defer conn.Close()

	if requiresTLSDial(in) {
		// Probe is panel-local; we only verify the listener responds with a
		// TLS hello, never that the peer is who they claim to be — so verify
		// is deliberately skipped here.
		tlsConn := tls.Client(conn, &tls.Config{
			InsecureSkipVerify: true,
			ServerName:         "localhost",
		})
		_ = tlsConn.SetDeadline(time.Now().Add(3 * time.Second))
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			res.Stage = "tls"
			res.Err = err.Error()
			res.ElapsedMs = time.Since(start).Milliseconds()
			return res
		}
		res.Verification = "tls_handshake"
	} else {
		res.Verification = "tcp_connect"
	}

	res.OK = true
	res.ElapsedMs = time.Since(start).Milliseconds()
	return res
}

func normalizeProbeFamily(family string) string {
	switch strings.ToLower(strings.TrimSpace(family)) {
	case "ipv4", "4":
		return "ipv4"
	case "ipv6", "6":
		return "ipv6"
	default:
		return "auto"
	}
}

// ProbeDialHost chooses the local target used by ProbeInboundFamily. Callers
// that perform a higher-level check after a successful probe must use this
// helper rather than selecting their own loopback address, so an IPv6 check
// cannot accidentally run its data path over IPv4.
//
// An explicit address is used in auto
// mode so the probe matches the bound family; wildcard and empty binds use
// loopback. Invalid cross-family requests fail before an ambiguous dial.
func ProbeDialHost(in model.Inbound, family string) (host, selectedFamily string, err error) {
	requested := normalizeProbeFamily(family)
	listen := strings.Trim(strings.TrimSpace(in.Listen), "[]")
	listenIP := net.ParseIP(strings.Split(listen, "%")[0])
	if requested == "ipv4" {
		if listenIP != nil && listenIP.To4() == nil && !listenIP.IsUnspecified() {
			return "", requested, fmt.Errorf("inbound is bound to IPv6 address %q", in.Listen)
		}
		if listenIP != nil && listenIP.To4() != nil && !listenIP.IsUnspecified() {
			return listen, requested, nil
		}
		return "127.0.0.1", requested, nil
	}
	if requested == "ipv6" {
		if listenIP != nil && listenIP.To4() != nil && !listenIP.IsUnspecified() {
			return "", requested, fmt.Errorf("inbound is bound to IPv4 address %q", in.Listen)
		}
		if listenIP != nil && listenIP.To4() == nil && !listenIP.IsUnspecified() {
			return listen, requested, nil
		}
		return "::1", requested, nil
	}
	if listenIP != nil && !listenIP.IsUnspecified() {
		if listenIP.To4() == nil {
			return listen, "ipv6", nil
		}
		return listen, "ipv4", nil
	}
	// Preserve existing readiness semantics. Callers can explicitly request
	// IPv6 when they need to prove the IPv6 listener.
	return "127.0.0.1", "ipv4", nil
}

// inferTransport returns the human-readable "transport" tag used by probe
// results. It parses the stream JSON properly rather than scanning for
// formatted substrings — the panel itself emits canonical Go-style JSON
// (no spaces), but inbounds round-tripped through other tools (Python's
// `json.dumps`, hand-edited config, etc.) come back with spaces and the
// old substring match would silently mis-classify them.
func inferTransport(in model.Inbound) string {
	if quicProtocols[in.Protocol] {
		return "udp+quic"
	}
	var stream map[string]any
	if in.Stream != "" {
		_ = json.Unmarshal([]byte(in.Stream), &stream)
	}
	network, _ := stream["network"].(string)
	security, _ := stream["security"].(string)
	parts := []string{}
	switch strings.ToLower(network) {
	case "ws":
		parts = append(parts, "ws")
	case "grpc":
		parts = append(parts, "grpc")
	case "h2":
		parts = append(parts, "h2")
	case "httpupgrade":
		parts = append(parts, "httpupgrade")
	default:
		parts = append(parts, "tcp")
	}
	switch strings.ToLower(security) {
	case "reality":
		parts = append(parts, "reality")
	case "tls":
		parts = append(parts, "tls")
	}
	return strings.Join(parts, "+")
}

// portIsBound reads /proc/net/{tcp,udp}{,6} and reports whether `port` shows
// up as a LISTENing socket (TCP) or as a bound socket (UDP). Linux-only;
// returns true on non-Linux to avoid false negatives in dev environments.
// Both IPv4 and IPv6 tables are checked because Sing-box binds [::] by
// default.
var procRoot = "/proc/net"

func portIsBoundForFamily(port int, proto, family string) bool {
	if _, err := os.Stat(procRoot); err != nil {
		// Non-Linux: trust the dial step to give us the real answer.
		return true
	}
	tables := []string{proto, proto + "6"}
	switch normalizeProbeFamily(family) {
	case "ipv4":
		// Linux reports an IPv6 dual-stack wildcard in tcp6/udp6 even when
		// it also accepts IPv4-mapped traffic. Keep tcp6/udp6 here and let
		// the actual IPv4 dial decide whether v6only prevents that traffic.
		tables = []string{proto, proto + "6"}
	case "ipv6":
		tables = []string{proto + "6"}
	}
	for _, t := range tables {
		data, err := os.ReadFile(procRoot + "/" + t)
		if err != nil {
			continue
		}
		if procContainsListenPort(string(data), proto, port) {
			return true
		}
	}
	return false
}

// procContainsListenPort scans the body of /proc/net/{tcp,udp}{,6}. The
// canonical format has whitespace-separated columns; col index 1 is
// `local_address` formatted as `IPHEX:PORTHEX`. For TCP we also check the
// `st` column (col 3) for state 0A (LISTEN). For UDP any row with the right
// local port counts as bound. Exposed as a package-level function so the
// unit test can exercise it without touching the filesystem.
func procContainsListenPort(body, proto string, port int) bool {
	want := strings.ToUpper(strconv.FormatInt(int64(port), 16))
	if len(want) < 4 {
		want = strings.Repeat("0", 4-len(want)) + want
	}
	for _, line := range strings.Split(body, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		local := fields[1]
		colon := strings.LastIndex(local, ":")
		if colon < 0 || local[colon+1:] != want {
			continue
		}
		if proto == "tcp" || proto == "tcp6" {
			if fields[3] != "0A" {
				continue
			}
		}
		return true
	}
	return false
}

// ErrInboundNotFound is returned by the handler when the requested inbound
// ID doesn't exist; surfaces a 404 to the caller.
var ErrInboundNotFound = errors.New("inbound not found")
