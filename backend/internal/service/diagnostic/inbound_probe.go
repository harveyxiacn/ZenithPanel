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
	OK        bool   `json:"ok"`
	Stage     string `json:"stage,omitempty"`
	ElapsedMs int64  `json:"elapsed_ms"`
	Err       string `json:"err,omitempty"`
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
//   - Step 2 ("tcp"): for TCP-style inbounds we dial 127.0.0.1:port with a
//     short timeout; success means the engine accepted the connection.
//   - Step 3 ("tls"): if the inbound's stream config asks for TLS, we drive
//     a TLS handshake with InsecureSkipVerify so a self-signed cert (test
//     setup) doesn't poison the result.
//   - Step 3 ("udp"): for QUIC-style inbounds we send a 16-byte sentinel
//     and call it good as soon as the OS doesn't refuse it. Sing-box's
//     QUIC servers will silently drop an unrecognised packet but at least
//     the port is alive.
//
// The probe never reaches outside the box and never modifies state.
func ProbeInbound(in model.Inbound) InboundProbeResult {
	start := time.Now()
	res := InboundProbeResult{
		InboundID: in.ID,
		Tag:       in.Tag,
		Protocol:  in.Protocol,
		Port:      in.Port,
	}
	res.Transport = inferTransport(in)

	isQUIC := quicProtocols[in.Protocol]
	netProto := "tcp"
	if isQUIC {
		netProto = "udp"
	}

	if !portIsBound(in.Port, netProto) {
		res.Stage = "not_bound"
		res.Err = fmt.Sprintf("nothing is listening on %s port %d", netProto, in.Port)
		res.ElapsedMs = time.Since(start).Milliseconds()
		return res
	}

	dialAddr := fmt.Sprintf("127.0.0.1:%d", in.Port)
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
	}

	res.OK = true
	res.ElapsedMs = time.Since(start).Milliseconds()
	return res
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

func portIsBound(port int, proto string) bool {
	if _, err := os.Stat(procRoot); err != nil {
		// Non-Linux: trust the dial step to give us the real answer.
		return true
	}
	tables := []string{proto, proto + "6"}
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
