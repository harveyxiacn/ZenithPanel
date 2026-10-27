#!/usr/bin/env bash
# Local Linux CI coverage for actual IPv6 VLESS+Reality and Hysteria2 flows.
set -euo pipefail
umask 077
: "${XRAY_BIN:?set XRAY_BIN}"; : "${SINGBOX_BIN:?set SINGBOX_BIN}"
[[ -x "$XRAY_BIN" && -x "$SINGBOX_BIN" ]] || { echo "proxy binaries are not executable" >&2; exit 2; }

tmpdir=$(mktemp -d); pids=(); failure_reported=0
dump_logs() {
  local log
  for log in "$@"; do
    [ -f "$tmpdir/$log.log" ] || continue
    printf '\n--- %s (last 30 lines) ---\n' "$log" >&2
    tail -n 30 -- "$tmpdir/$log.log" >&2 || true
  done
}
report_failure() {
  [ "$failure_reported" = 1 ] || dump_logs http reality-target xray vless-client hy2-server hy2-client
  failure_reported=1
}
cleanup() {
  local status=$? p
  [ "$status" -eq 0 ] || report_failure
  for p in "${pids[@]:-}"; do kill "$p" 2>/dev/null || true; done
  rm -rf "$tmpdir"
  exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT TERM
wait_alive() { sleep 1; kill -0 "$1" 2>/dev/null || { echo "$2 did not stay running" >&2; report_failure; exit 1; }; }
through_socks() { env -u NO_PROXY -u no_proxy curl -g --fail --silent --show-error --max-time 12 --noproxy '' --socks5-hostname "[::1]:$1" 'http://[::1]:48180/' | grep -q 'Zenith IPv6 E2E'; }
for tool in go openssl python3 curl; do command -v "$tool" >/dev/null || { echo "missing $tool" >&2; exit 2; }; done
[[ -r /proc/net/if_inet6 ]] || { echo "IPv6 is unavailable on this runner" >&2; exit 1; }

printf 'Zenith IPv6 E2E\n' >"$tmpdir/index.html"
python3 -m http.server 48180 --bind ::1 --directory "$tmpdir" >"$tmpdir/http.log" 2>&1 & pids+=("$!"); wait_alive "${pids[-1]}" http
# The Hysteria2 client trusts this temporary CA through SSL_CERT_FILE; no
# system trust store is edited and no insecure TLS mode is configured.
openssl req -x509 -newkey rsa:2048 -nodes -days 1 -subj '/CN=Zenith IPv6 E2E CA' -keyout "$tmpdir/ca.key" -out "$tmpdir/ca.crt" >/dev/null 2>&1
openssl req -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -subj '/CN=localhost' -keyout "$tmpdir/server.key" -out "$tmpdir/server.csr" >/dev/null 2>&1
printf 'subjectAltName=DNS:localhost\nextendedKeyUsage=serverAuth\n' >"$tmpdir/server.ext"
openssl x509 -req -days 1 -in "$tmpdir/server.csr" -CA "$tmpdir/ca.crt" -CAkey "$tmpdir/ca.key" -CAcreateserial -out "$tmpdir/server.crt" -extfile "$tmpdir/server.ext" >/dev/null 2>&1

uuid=$(cat /proc/sys/kernel/random/uuid)
read -r reality_private reality_public < <("$XRAY_BIN" x25519 | awk -F': ' '/^Private([[:space:]]key|Key):/{p=$2}/^(Public[[:space:]]key|Password)( \(PublicKey\))?:/{u=$2}END{print p, u}')
[[ -n "$reality_private" && -n "$reality_public" ]] || { echo "xray did not generate Reality keys" >&2; exit 1; }
# REALITY target guidance calls for TLS 1.3 and h2. Use Go's TLS stack for the
# local target so the test exercises a normal Go TLS endpoint, while accepting
# Xray's no-ALPN target probe as well as browser-style h2 clients.
cat >"$tmpdir/reality_target.go" <<'EOF'
package main

import (
	"crypto/tls"
	"io"
	"log"
	"net"
	"os"
)

func main() {
	certificate, err := tls.LoadX509KeyPair(os.Args[1], os.Args[2])
	if err != nil {
		log.Fatal(err)
	}
	listener, err := tls.Listen("tcp6", "[::1]:48182", &tls.Config{
		Certificates: []tls.Certificate{certificate},
		MinVersion:   tls.VersionTLS13,
		MaxVersion:   tls.VersionTLS13,
		NextProtos:   []string{"h2", "http/1.1"},
	})
	if err != nil {
		log.Fatal(err)
	}
	for {
		connection, err := listener.Accept()
		if err != nil {
			log.Fatal(err)
		}
		go func(connection net.Conn) {
			defer connection.Close()
			if err := connection.(*tls.Conn).Handshake(); err != nil {
				log.Printf("handshake: %v", err)
				return
			}
			_, _ = io.Copy(io.Discard, connection)
		}(connection)
	}
}
EOF
gofmt -w "$tmpdir/reality_target.go"
go build -o "$tmpdir/reality-target" "$tmpdir/reality_target.go" >"$tmpdir/reality-target.log" 2>&1 || { report_failure; exit 1; }
"$tmpdir/reality-target" "$tmpdir/server.crt" "$tmpdir/server.key" >>"$tmpdir/reality-target.log" 2>&1 & pids+=("$!"); wait_alive "${pids[-1]}" reality-target
cat >"$tmpdir/xray.json" <<EOF
{"log":{"loglevel":"debug"},"inbounds":[{"listen":"::1","port":48181,"protocol":"vless","settings":{"clients":[{"id":"$uuid","email":"zc:1","flow":"xtls-rprx-vision"}],"decryption":"none"},"streamSettings":{"network":"tcp","security":"reality","realitySettings":{"show":true,"target":"[::1]:48182","serverNames":["localhost"],"privateKey":"$reality_private","shortIds":["0123456789abcdef"]}}}],"outbounds":[{"protocol":"freedom","tag":"direct"}]}
EOF
"$XRAY_BIN" run -c "$tmpdir/xray.json" >"$tmpdir/xray.log" 2>&1 & pids+=("$!"); wait_alive "${pids[-1]}" xray
cat >"$tmpdir/vless-client.json" <<EOF
{"log":{"level":"debug"},"inbounds":[{"type":"socks","listen":"::1","listen_port":48183}],"outbounds":[{"type":"vless","tag":"proxy","server":"::1","server_port":48181,"uuid":"$uuid","flow":"xtls-rprx-vision","tls":{"enabled":true,"server_name":"localhost","alpn":["h2"],"reality":{"enabled":true,"public_key":"$reality_public","short_id":"0123456789abcdef"},"utls":{"enabled":true,"fingerprint":"chrome"}}}],"route":{"final":"proxy"}}
EOF
"$SINGBOX_BIN" check -c "$tmpdir/vless-client.json" >/dev/null
"$SINGBOX_BIN" run -c "$tmpdir/vless-client.json" >"$tmpdir/vless-client.log" 2>&1 & pids+=("$!"); wait_alive "${pids[-1]}" vless-client
through_socks 48183 || { report_failure; exit 1; }

cat >"$tmpdir/hy2-server.json" <<EOF
{"inbounds":[{"type":"hysteria2","listen":"::1","listen_port":48184,"users":[{"name":"zc:1","password":"$uuid"}],"up_mbps":100,"down_mbps":100,"obfs":{"type":"salamander","password":"e2e-obfs"},"tls":{"enabled":true,"server_name":"localhost","certificate_path":"$tmpdir/server.crt","key_path":"$tmpdir/server.key","alpn":["h3"]}}],"outbounds":[{"type":"direct","tag":"direct"}]}
EOF
cat >"$tmpdir/hy2-client.json" <<EOF
{"inbounds":[{"type":"socks","listen":"::1","listen_port":48185}],"outbounds":[{"type":"hysteria2","tag":"proxy","server":"::1","server_port":48184,"password":"$uuid","obfs":{"type":"salamander","password":"e2e-obfs"},"tls":{"enabled":true,"server_name":"localhost","alpn":["h3"]}}],"route":{"final":"proxy"}}
EOF
"$SINGBOX_BIN" check -c "$tmpdir/hy2-server.json" >/dev/null; "$SINGBOX_BIN" check -c "$tmpdir/hy2-client.json" >/dev/null
"$SINGBOX_BIN" run -c "$tmpdir/hy2-server.json" >"$tmpdir/hy2-server.log" 2>&1 & pids+=("$!"); wait_alive "${pids[-1]}" hy2-server
SSL_CERT_FILE="$tmpdir/ca.crt" "$SINGBOX_BIN" run -c "$tmpdir/hy2-client.json" >"$tmpdir/hy2-client.log" 2>&1 & pids+=("$!"); wait_alive "${pids[-1]}" hy2-client
through_socks 48185 || { report_failure; exit 1; }
echo 'IPv6 VLESS+Reality and Hysteria2 E2E PASS'
