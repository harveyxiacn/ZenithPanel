#!/usr/bin/env bash
# Exercise a live VLESS+Reality or Hysteria2 inbound over IPv6. VLESS+Reality
# uses Xray because Xray 26.10.21 rejects sing-box 1.14.x's REALITY ClientHello.
# Unlike `zenithctl proxy test`, this proves an authenticated client tunnel.
set -euo pipefail
umask 077

usage() {
  cat <<'EOF'
Usage: proto_sweep_ipv6.sh --protocol vless|hysteria2 --tag TAG --server IPV6 [--email EMAIL] [--url HTTPS_URL] [--socks-port PORT]

The test reads the selected inbound and client through zenithctl, writes its
temporary client configuration with mode 0600, and removes it on exit. TLS
verification is never disabled. The default target reports the egress IP and
must return IPv6, proving both the IPv6 inbound path and IPv6 egress.
EOF
}

protocol="" tag="" server="" email="" target_url="https://ipv6.icanhazip.com/" socks_port="41097"
validate_ipv6_address() {
  local address=$1
  # `ip route get` parses a numeric IPv6 destination through the kernel. The
  # character check rejects hostnames, zones and URI brackets before then.
  [[ "$address" =~ ^[0-9A-Fa-f:]+$ && "$address" == *:* ]] || return 1
  ip -6 route get "$address" >/dev/null 2>&1
}
while [[ $# -gt 0 ]]; do
  case "$1" in
    --protocol) protocol=${2:?}; shift 2;; --tag) tag=${2:?}; shift 2;; --server) server=${2:?}; shift 2;;
    --email) email=${2:?}; shift 2;; --url) target_url=${2:?}; shift 2;; --socks-port) socks_port=${2:?}; shift 2;;
    -h|--help) usage; exit 0;; *) echo "unknown option: $1" >&2; usage >&2; exit 2;;
  esac
done
[[ "$protocol" == vless || "$protocol" == hysteria2 ]] || { echo "--protocol must be vless or hysteria2" >&2; exit 2; }
[[ -n "$tag" ]] || { echo "--tag is required" >&2; exit 2; }
[[ "$socks_port" =~ ^[1-9][0-9]{0,4}$ && "$socks_port" -le 65535 ]] || { echo "invalid SOCKS port" >&2; exit 2; }
[[ "$target_url" == https://* ]] || { echo "--url must use HTTPS" >&2; exit 2; }
for cmd in zenithctl jq curl ss ip; do command -v "$cmd" >/dev/null || { echo "missing command: $cmd" >&2; exit 2; }; done
if [[ "$protocol" == vless ]]; then
  command -v xray >/dev/null || { echo "missing command: xray (required for VLESS+REALITY)" >&2; exit 2; }
else
  command -v sing-box >/dev/null || { echo "missing command: sing-box (required for Hysteria2)" >&2; exit 2; }
fi
ip -6 addr show scope global | grep -q 'inet6 ' || { echo "host has no global IPv6 address" >&2; exit 1; }
validate_ipv6_address "$server" || { echo "--server must be a routable numeric IPv6 address" >&2; exit 2; }
ss -ltn "sport = :$socks_port" | grep -q ":$socks_port" && { echo "SOCKS port already in use" >&2; exit 1; }

tmpdir=$(mktemp -d); client_pid=""
cleanup() { [[ -n "$client_pid" ]] && kill "$client_pid" 2>/dev/null || true; rm -rf "$tmpdir"; }
trap cleanup EXIT INT TERM

inbounds=$(zenithctl -q raw GET /api/v1/inbounds)
clients=$(zenithctl -q raw GET /api/v1/clients)
inbound=$(jq -ce --arg tag "$tag" --arg protocol "$protocol" '.[]|select(.tag==$tag and .protocol==$protocol and .enable)' <<<"$inbounds") || { echo "enabled inbound not found" >&2; exit 1; }
id=$(jq -r '.id' <<<"$inbound")
if [[ -n "$email" ]]; then
  client=$(jq -ce --argjson id "$id" --arg email "$email" '[.[]|select(.inbound_id==$id and .email==$email and .enable)][0]' <<<"$clients")
else
  client=$(jq -ce --argjson id "$id" '[.[]|select(.inbound_id==$id and .enable)][0]' <<<"$clients")
fi
[[ "$client" != "null" ]] || { echo "no enabled client found for inbound" >&2; exit 1; }
port=$(jq -r '.port' <<<"$inbound"); uuid=$(jq -r '.uuid' <<<"$client")
stream=$(jq -ce '.stream|if type=="string" then fromjson else . end' <<<"$inbound")
settings=$(jq -ce '.settings|if type=="string" then fromjson else . end' <<<"$inbound")
config="$tmpdir/client.json"
secret_input="$tmpdir/secret-input.json"

if [[ "$protocol" == vless ]]; then
  sni=$(jq -r '.realitySettings.serverNames[0] // empty' <<<"$stream")
  pub=$(jq -r '.realitySettings.settings.publicKey // .realitySettings.publicKey // empty' <<<"$stream")
  sid=$(jq -r '.realitySettings.shortIds[0] // empty' <<<"$stream")
  fp=$(jq -r '.realitySettings.settings.fingerprint // "chrome"' <<<"$stream")
  flow=$(jq -r '.flow // ""' <<<"$settings")
  network=$(jq -r '.network // "tcp"' <<<"$stream")
  service_name=$(jq -r '.grpcSettings.serviceName // ""' <<<"$stream")
  [[ -n "$sni" && -n "$pub" ]] || { echo "incomplete Reality settings" >&2; exit 1; }
  case "$network" in tcp|raw) network=tcp;; grpc) ;; *) echo "VLESS+REALITY IPv6 sweep supports only TCP or gRPC transport, got $network" >&2; exit 1;; esac
  printf '%s\n' "$uuid" | jq -Rn '[inputs] | {uuid: .[0]}' >"$secret_input"
  jq -n --slurpfile secret "$secret_input" --arg server "$server" --argjson port "$port" --arg sni "$sni" --arg pub "$pub" --arg sid "$sid" --arg fp "$fp" --arg flow "$flow" --arg network "$network" --arg service "$service_name" --argjson socks "$socks_port" '
    $secret[0] as $secret | {log:{loglevel:"warning"},inbounds:[{listen:"::1",port:$socks,protocol:"socks",settings:{auth:"noauth",udp:false}}],outbounds:[{protocol:"vless",tag:"proxy",settings:{vnext:[{address:$server,port:$port,users:[({id:$secret.uuid,encryption:"none"}+(if $flow=="" then {} else {flow:$flow} end))}]},streamSettings:({network:$network,security:"reality",realitySettings:{show:false,fingerprint:$fp,serverName:$sni,publicKey:$pub,shortId:$sid}}+(if $network=="grpc" then {grpcSettings:{serviceName:$service}} else {} end)}]}' >"$config"
else
  sni=$(jq -r '.tlsSettings.serverName // .tls.server_name // empty' <<<"$stream")
  alpn=$(jq -c '.tlsSettings.alpn // .tls.alpn // ["h3"]' <<<"$stream")
  obfs_type=$(jq -r '.obfs.type // empty' <<<"$settings"); obfs_password=$(jq -r '.obfs.password // empty' <<<"$settings")
  [[ -n "$sni" ]] || { echo "Hysteria2 inbound lacks a TLS server name" >&2; exit 1; }
  { printf '%s\n' "$uuid"; printf '%s\n' "$obfs_password"; } | jq -Rn '[inputs] | {password: .[0], obfs_password: .[1]}' >"$secret_input"
  jq -n --slurpfile secret "$secret_input" --arg server "$server" --argjson port "$port" --arg sni "$sni" --argjson alpn "$alpn" --arg type "$obfs_type" --argjson socks "$socks_port" '
    $secret[0] as $secret | {log:{level:"warn"},inbounds:[{type:"socks",listen:"::1",listen_port:$socks}],outbounds:[({type:"hysteria2",tag:"proxy",server:$server,server_port:$port,password:$secret.password,tls:{enabled:true,server_name:$sni,alpn:$alpn}}+(if $type=="" then {} else {obfs:{type:$type,password:$secret.obfs_password}} end))],route:{final:"proxy"}}' >"$config"
fi

if [[ "$protocol" == vless ]]; then
  xray run -test -c "$config" >/dev/null
  xray run -c "$config" >"$tmpdir/xray.log" 2>&1 & client_pid=$!
  client_log="$tmpdir/xray.log"
else
  sing-box check -c "$config" >/dev/null
  sing-box run -c "$config" >"$tmpdir/sing-box.log" 2>&1 & client_pid=$!
  client_log="$tmpdir/sing-box.log"
fi
sleep 2; kill -0 "$client_pid" 2>/dev/null || { tail -30 "$client_log" >&2; exit 1; }
egress=$(curl --fail --silent --show-error --max-time 20 --socks5-hostname "[::1]:$socks_port" "$target_url" | tr -d '[:space:]')
validate_ipv6_address "$egress" || { echo "tunnel reached target but egress was not a valid routable IPv6 address" >&2; exit 1; }
printf '%s IPv6 E2E PASS: inbound=%s server=[%s]:%s egress=%s\n' "$protocol" "$tag" "$server" "$port" "$egress"
