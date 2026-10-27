package diagnostic

import (
	"crypto/tls"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/harveyxiacn/ZenithPanel/backend/internal/model"
)

// TestProcContainsListenPort exercises the parser against handcrafted samples
// taken from a real /proc/net/tcp6 dump (port 443 LISTEN) and a stale udp
// dump that should NOT match because the state isn't 0A. We sample both
// IPv4 (8 hex chars) and IPv6 (32 hex chars) formats.
func TestProcContainsListenPort(t *testing.T) {
	tcpListen := `  sl  local_address                         remote_address                        st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 00000000000000000000000000000000:01BB 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 12345 1 0000000000000000 100 0 0 10 0
`
	tcpListenIPv4 := `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 0100007F:8AB1 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 1 1 0 100 0 0 10 0
`
	tcpEstablished := `  sl  local_address                         remote_address                        st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   1: 00000000000000000000000000000000:01BB 0A0A0A0A0A0A0A0A0A0A0A0A0A0A0A0A:1234 01 00000000:00000000 00:00000000 00000000     0        0 12346 1 0000000000000000 100 0 0 10 0
`
	udp443 := `  sl  local_address                         remote_address                        st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode ref pointer drops
   0: 00000000000000000000000000000000:01BB 00000000000000000000000000000000:0000 07 00000000:00000000 00:00000000 00000000     0        0 12347 1 0000000000000000 0
`

	cases := []struct {
		name  string
		body  string
		proto string
		port  int
		want  bool
	}{
		{"tcp6 LISTEN matches", tcpListen, "tcp6", 443, true},
		{"tcp6 ESTABLISHED does not match", tcpEstablished, "tcp6", 443, false},
		{"tcp4 LISTEN on port 35505 matches", tcpListenIPv4, "tcp", 35505, true},
		{"tcp6 wrong port", tcpListen, "tcp6", 8080, false},
		{"udp6 bound (any state) matches", udp443, "udp6", 443, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := procContainsListenPort(c.body, c.proto, c.port)
			if got != c.want {
				t.Errorf("procContainsListenPort(%q port %d): got %v, want %v", c.proto, c.port, got, c.want)
			}
		})
	}
}

func TestPortIsBoundForFamilyRecognizesDualStackKernelEntry(t *testing.T) {
	// Linux exposes a dual-stack wildcard socket in tcp6. An IPv4 health
	// probe must not reject it before attempting its real IPv4 dial.
	tcp6 := `  sl  local_address                         remote_address                        st
   0: 00000000000000000000000000000000:01BB 00000000000000000000000000000000:0000 0A
`
	oldRoot := procRoot
	tmp := t.TempDir()
	procRoot = tmp
	t.Cleanup(func() { procRoot = oldRoot })
	if err := os.WriteFile(filepath.Join(tmp, "tcp6"), []byte(tcp6), 0o600); err != nil {
		t.Fatal(err)
	}
	if !portIsBoundForFamily(443, "tcp", "ipv4") {
		t.Fatal("IPv4 probe did not recognize a tcp6 dual-stack candidate")
	}
}

// TestProbeInboundTCP brings up a localhost TCP listener, runs the prober,
// and verifies OK=true plus a short elapsed time. The probe never reaches
// outside the process so this is a real end-to-end exercise of the bound +
// dial logic. We bypass the /proc check by aiming the prober at the
// loopback dial path directly via a non-Linux fallback (portIsBound returns
// true when /proc/net doesn't exist) — on Linux runners we instead use a
// real LISTEN socket which DOES show up in /proc/net/tcp6.
func TestProbeInboundTCP(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("could not bind loopback listener: %v", err)
	}
	defer ln.Close()
	// Accept and drop in the background.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()

	in := model.Inbound{
		ID:       7,
		Tag:      "test-tcp",
		Protocol: "vless",
		Port:     ln.Addr().(*net.TCPAddr).Port,
		Stream:   `{"network":"tcp","security":"none"}`,
	}
	res := ProbeInbound(in)
	if !res.OK {
		t.Fatalf("expected OK=true, got %#v", res)
	}
	if res.Stage != "" {
		t.Errorf("expected no failure stage, got %q", res.Stage)
	}
	if res.Family != "ipv4" || res.Verification != "tcp_connect" {
		t.Errorf("unexpected verification result: %#v", res)
	}
	if res.ElapsedMs < 0 || res.ElapsedMs > 4000 {
		t.Errorf("elapsed_ms out of range: %d", res.ElapsedMs)
	}
}

func TestProbeInboundIPv6TCP(t *testing.T) {
	ln, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		t.Skipf("IPv6 loopback is unavailable: %v", err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()

	in := model.Inbound{ID: 10, Tag: "test-ipv6", Protocol: "vless", Listen: "::", Port: ln.Addr().(*net.TCPAddr).Port}
	res := ProbeInboundFamily(in, "ipv6")
	// Some Windows CI sandboxes permit binding ::1 but deny a subsequent
	// loopback IPv6 connect. That is an environment policy rather than a
	// prober failure; Linux deployment hosts still exercise this path.
	if !res.OK && strings.Contains(strings.ToLower(res.Err), "access") {
		t.Skipf("IPv6 loopback dial is blocked by the test environment: %s", res.Err)
	}
	if !res.OK || res.Family != "ipv6" || res.Verification != "tcp_connect" {
		t.Fatalf("expected IPv6 TCP probe to pass, got %#v", res)
	}
}

func TestProbeDialHostFamilySelection(t *testing.T) {
	cases := []struct {
		name, listen, requested, wantHost, wantFamily string
		wantErr                                       bool
	}{
		{"automatic wildcard keeps readiness IPv4", "", "auto", "127.0.0.1", "ipv4", false},
		{"explicit IPv6 chooses bound address", "2001:db8::10", "auto", "2001:db8::10", "ipv6", false},
		{"IPv6 wildcard uses loopback", "::", "ipv6", "::1", "ipv6", false},
		{"IPv6 request rejects IPv4 bind", "192.0.2.10", "ipv6", "", "ipv6", true},
		{"IPv4 request rejects IPv6 bind", "2001:db8::10", "ipv4", "", "ipv4", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			host, family, err := ProbeDialHost(model.Inbound{Listen: c.listen}, c.requested)
			if (err != nil) != c.wantErr {
				t.Fatalf("ProbeDialHost error = %v, wantErr %v", err, c.wantErr)
			}
			if host != c.wantHost || family != c.wantFamily {
				t.Fatalf("ProbeDialHost = (%q, %q), want (%q, %q)", host, family, c.wantHost, c.wantFamily)
			}
		})
	}
}

// TestProbeInboundTCPRefused points the prober at a port that nothing is
// listening on. Expect failure at the not_bound stage on Linux (because the
// /proc table won't have it) or at tcp stage on non-Linux (dial refused).
func TestProbeInboundTCPRefused(t *testing.T) {
	// Bind+close to grab a guaranteed-free port number.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("could not bind loopback listener: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	in := model.Inbound{
		ID:       8,
		Tag:      "test-refused",
		Protocol: "vless",
		Port:     port,
		Stream:   `{"network":"tcp"}`,
	}
	res := ProbeInbound(in)
	if res.OK {
		t.Fatalf("expected OK=false, got %#v", res)
	}
	if res.Stage != "not_bound" && res.Stage != "tcp" {
		t.Errorf("expected stage in {not_bound,tcp}, got %q (err=%s)", res.Stage, res.Err)
	}
}

// TestProbeInboundTLSHandshake stands up a TLS listener with a self-signed
// cert, then probes through. requiresTLSDial should pick up `security:tls`
// in the stream JSON and drive the handshake.
func TestProbeInboundTLSHandshake(t *testing.T) {
	cert, key, err := selfSignedPair()
	if err != nil {
		t.Fatalf("selfSignedPair: %v", err)
	}
	tlsCfg := &tls.Config{Certificates: []tls.Certificate{tlsKeyPair(t, cert, key)}}
	ln, err := tls.Listen("tcp", "127.0.0.1:0", tlsCfg)
	if err != nil {
		t.Skipf("could not bind tls listener: %v", err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			// Drive the server-side TLS handshake so the client's handshake
			// completes cleanly before we close. Without this the server can
			// abort the TCP conn mid-handshake on Windows.
			if tc, ok := conn.(*tls.Conn); ok {
				_ = tc.SetDeadline(time.Now().Add(2 * time.Second))
				_ = tc.Handshake()
			}
			conn.Close()
		}
	}()

	in := model.Inbound{
		ID:       9,
		Tag:      "test-tls",
		Protocol: "vless",
		Port:     ln.Addr().(*net.TCPAddr).Port,
		Stream:   `{"network":"tcp","security":"tls","tlsSettings":{"serverName":"test.local"}}`,
	}
	res := ProbeInbound(in)
	if !res.OK {
		t.Fatalf("expected OK=true after TLS handshake, got %#v", res)
	}
	if res.Verification != "tls_handshake" {
		t.Errorf("expected TLS handshake verification, got %#v", res)
	}
	if !strings.Contains(res.Transport, "tls") {
		t.Errorf("transport should contain 'tls', got %q", res.Transport)
	}
}

// TestRequiresTLSDial pins the heuristic: Reality and plain TCP do NOT need
// a TLS handshake from the prober; Xray-style `security:tls` and Sing-box
// native `tls.enabled` do.
func TestRequiresTLSDial(t *testing.T) {
	cases := map[string]bool{
		`{"network":"tcp","security":"tls"}`:                      true,
		`{"tls":{"enabled":true}}`:                                true,
		`{"network":"tcp","security":"reality"}`:                  false,
		`{"network":"ws"}`:                                        false,
		`{"network":"tcp","security":"reality","tlsSettings":{}}`: false,
	}
	for stream, want := range cases {
		in := model.Inbound{Stream: stream}
		if got := requiresTLSDial(in); got != want {
			t.Errorf("requiresTLSDial(%q) = %v, want %v", stream, got, want)
		}
	}
}

// --- helpers --------------------------------------------------------------

func tlsKeyPair(t *testing.T, certPEM, keyPEM []byte) tls.Certificate {
	t.Helper()
	c, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatalf("X509KeyPair: %v", err)
	}
	return c
}

// selfSignedPair returns an in-memory cert/key pair good for 1 hour on
// CN=test.local. Tests don't need a CA chain because the prober uses
// InsecureSkipVerify.
func selfSignedPair() (cert, key []byte, err error) {
	return generateSelfSigned("test.local", time.Hour)
}
