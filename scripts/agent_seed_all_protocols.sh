#!/bin/bash
# Seed every protocol the panel supports with one client each. Idempotent:
# existing tags return 409 from the panel and are skipped. Intended for
# AI agents driving a fresh ZenithPanel install — see AGENTS.md §4.4.Z.
#
# Pre-requisites (the script bails if any are missing):
#   - zenithctl on PATH and authenticated (token bootstrapped)
#   - PUBLIC_IP        = VPS public IP                   (env var)
#   - DOMAIN           = a name resolving to PUBLIC_IP   (env var)
#   - CERT / KEY paths under /opt/zenithpanel/data/certs (cert issued)
#
# Override any port/email/password by exporting the matching env var below
# before running.
set -euo pipefail

: "${PUBLIC_IP:?must export PUBLIC_IP=<vps-ip>}"
: "${DOMAIN:?must export DOMAIN=<your domain or <dashed-ip>.nip.io>}"
: "${CERT:=/opt/zenithpanel/data/certs/${DOMAIN}.crt}"
: "${KEY:=/opt/zenithpanel/data/certs/${DOMAIN}.key}"
[[ -f "$CERT" && -f "$KEY" ]] || {
  echo "[fatal] cert not found at $CERT / $KEY. Run:"
  echo "        zenithctl cert issue --domain $DOMAIN --email you@example.com"
  exit 1
}

# Per-protocol overridable defaults.
: "${VLESS_PORT:=443}"
: "${VMESS_PORT:=31402}"
: "${TROJAN_PORT:=31403}"
: "${SS_PORT:=31404}"
: "${HY2_PORT:=8443}"
: "${TUIC_PORT:=31406}"

# Helper: POST an inbound; tolerate 409 (already exists) so re-runs are safe.
post_inbound() {
  local body=$1
  local resp
  resp=$(zenithctl raw POST /api/v1/inbounds --data "$body" 2>&1 || true)
  case "$resp" in
    *'"code": 200'*)  echo "  [ok] created" ;;
    *'409'*)          echo "  [skip] already exists" ;;
    *)                echo "  [warn] unexpected response: $resp" ;;
  esac
}

# Helper: open a firewall port; tolerate "already exists" responses.
open_fw() {
  local port=$1 proto=$2
  zenithctl raw POST /api/v1/firewall/rules \
    --data "{\"port\":\"$port\",\"protocol\":\"$proto\",\"action\":\"ACCEPT\"}" \
    >/dev/null 2>&1 || true
}

# Helper: add a client; tolerate "Email already exists" on this inbound.
add_client() {
  local tag=$1 email=$2 uuid=${3:-}
  local inbound_id
  inbound_id=$(zenithctl -q raw GET /api/v1/inbounds |
    jq --arg t "$tag" '[.[]|select(.tag==$t)][0].id // empty')
  [[ -n "$inbound_id" ]] || { echo "  [warn] no inbound with tag $tag"; return; }
  if [[ -n "$uuid" ]]; then
    zenithctl client add --inbound "$inbound_id" --email "$email" --uuid "$uuid" \
      >/dev/null 2>&1 || true
  else
    zenithctl client add --inbound "$inbound_id" --email "$email" \
      >/dev/null 2>&1 || true
  fi
}

echo "==> A. VLESS + Reality"
KEYS=$(zenithctl -q raw POST /api/v1/proxy/generate-reality-keys)
PK=$(jq -r '.data.private_key' <<<"$KEYS")
PUB=$(jq -r '.data.public_key' <<<"$KEYS")
SID=$(jq -r '.data.short_id' <<<"$KEYS")
post_inbound "$(jq -nc \
  --arg ip "$PUBLIC_IP" --arg pk "$PK" --arg pub "$PUB" --arg sid "$SID" --argjson port "$VLESS_PORT" '{
  tag:"vless-reality", protocol:"vless", port:$port, network:"tcp",
  server_address:$ip,
  settings:({decryption:"none",flow:"xtls-rprx-vision"}|tostring),
  stream:({network:"tcp",security:"reality",realitySettings:{
    target:"www.microsoft.com:443",serverNames:["www.microsoft.com"],
    privateKey:$pk,shortIds:[$sid],
    settings:{publicKey:$pub,fingerprint:"chrome"}
  }}|tostring),
  enable:true}')"
add_client vless-reality user1
open_fw "$VLESS_PORT" tcp

echo "==> B. VMess + WS + TLS"
post_inbound "$(jq -nc \
  --arg ip "$PUBLIC_IP" --arg dom "$DOMAIN" --arg cert "$CERT" --arg key "$KEY" --argjson port "$VMESS_PORT" '{
  tag:"vmess-ws", protocol:"vmess", port:$port, network:"ws",
  server_address:$ip,
  settings:({clients:[]}|tostring),
  stream:({network:"ws",security:"tls",
    wsSettings:{path:"/vmess"},
    tlsSettings:{serverName:$dom,certificates:[{certificateFile:$cert,keyFile:$key}]}
  }|tostring),
  enable:true}')"
add_client vmess-ws vmess-user
open_fw "$VMESS_PORT" tcp

echo "==> C. Trojan + TLS"
TROJAN_PW="${TROJAN_PASSWORD:-trojan-$(openssl rand -hex 8)}"
post_inbound "$(jq -nc \
  --arg ip "$PUBLIC_IP" --arg dom "$DOMAIN" --arg cert "$CERT" --arg key "$KEY" --argjson port "$TROJAN_PORT" --arg pw "$TROJAN_PW" '{
  tag:"trojan-tls", protocol:"trojan", port:$port, network:"tcp",
  server_address:$ip,
  settings:({clients:[{email:"trojan-user",password:$pw}]}|tostring),
  stream:({network:"tcp",security:"tls",
    tlsSettings:{serverName:$dom,certificates:[{certificateFile:$cert,keyFile:$key}]}
  }|tostring),
  enable:true}')"
add_client trojan-tls trojan-user "$TROJAN_PW"
open_fw "$TROJAN_PORT" tcp

echo "==> D. Shadowsocks-2022"
SERVER_PSK="${SS_SERVER_PSK:-$(openssl rand -base64 16)}"
USER_PSK="${SS_USER_PSK:-$(openssl rand -base64 16)}"
post_inbound "$(jq -nc \
  --arg ip "$PUBLIC_IP" --arg spsk "$SERVER_PSK" --argjson port "$SS_PORT" '{
  tag:"ss-2022", protocol:"shadowsocks", port:$port, network:"tcp",
  server_address:$ip,
  settings:({method:"2022-blake3-aes-128-gcm",password:$spsk}|tostring),
  stream:({network:"tcp"}|tostring),
  enable:true}')"
add_client ss-2022 ss-user "$USER_PSK"
open_fw "$SS_PORT" tcp

echo "==> E. Hysteria2"
HY2_OBFS_PW="${HY2_OBFS_PW:-$(openssl rand -hex 16)}"
post_inbound "$(jq -nc \
  --arg ip "$PUBLIC_IP" --arg dom "$DOMAIN" --arg cert "$CERT" --arg key "$KEY" --argjson port "$HY2_PORT" --arg obfs "$HY2_OBFS_PW" '{
  tag:"hysteria2", protocol:"hysteria2", port:$port, network:"udp",
  server_address:$ip,
  settings:({obfs:{type:"salamander",password:$obfs},up_mbps:100,down_mbps:100}|tostring),
  stream:({network:"udp",security:"tls",
    tlsSettings:{serverName:$dom,alpn:["h3"],certificates:[{certificateFile:$cert,keyFile:$key}]}
  }|tostring),
  enable:true}')"
add_client hysteria2 hy2-user
open_fw "$HY2_PORT" udp

echo "==> F. TUIC v5"
post_inbound "$(jq -nc \
  --arg ip "$PUBLIC_IP" --arg dom "$DOMAIN" --arg cert "$CERT" --arg key "$KEY" --argjson port "$TUIC_PORT" '{
  tag:"tuic-v5", protocol:"tuic", port:$port, network:"udp",
  server_address:$ip,
  settings:({congestion_control:"bbr"}|tostring),
  stream:({network:"udp",security:"tls",
    tlsSettings:{serverName:$dom,alpn:["h3"],certificates:[{certificateFile:$cert,keyFile:$key}]}
  }|tostring),
  enable:true}')"
add_client tuic-v5 tuic-user
open_fw "$TUIC_PORT" udp

echo
echo "==> proxy apply (auto-partition between Xray and Sing-box)"
zenithctl proxy apply

echo
echo "==> verify with server-side probe"
zenithctl --output table proxy test all
echo
echo "If every row shows OK=✓ the seed succeeded. To grab subscription"
echo "URLs for each client, run:"
echo "  zenithctl -q raw GET /api/v1/clients | jq -r '.[]|.uuid'"
echo "  curl -s http://127.0.0.1:31310/api/v1/sub/<uuid> | base64 -d"
