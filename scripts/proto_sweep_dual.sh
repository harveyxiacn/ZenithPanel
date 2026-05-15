#!/bin/bash
# Dual-engine smoke test. Probes every enabled inbound concurrently — each
# uses a dedicated sing-box client and a local SOCKS5 port. Exits non-zero if
# any protocol fails.
set -u

PASS=0
FAIL=0
RESULTS=()

run_one() {
  local name="$1"; local socks_port="$2"; local cfg_path="$3"
  pkill -f "sing-box.*$(basename "$cfg_path")" 2>/dev/null || true
  sleep 1
  nohup sing-box run -c "$cfg_path" > "/root/proto-tests/$name.log" 2>&1 &
  sleep 3
  local code
  code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 12 --socks5 "127.0.0.1:$socks_port" https://www.google.com || echo "000")
  pkill -f "sing-box.*$(basename "$cfg_path")" 2>/dev/null || true
  if [ "$code" = "200" ] || [ "$code" = "204" ] || [ "$code" = "301" ] || [ "$code" = "302" ]; then
    RESULTS+=("$name: PASS (http=$code)")
    PASS=$((PASS+1))
  else
    RESULTS+=("$name: FAIL (http=$code)")
    FAIL=$((FAIL+1))
    echo "--- $name log tail ---"
    tail -8 "/root/proto-tests/$name.log"
  fi
}

# --- VLESS+Reality (served by Xray in dual mode) ---
cat > /root/proto-tests/sb-reality.json <<JSON
{
  "log": {"level": "warn"},
  "inbounds": [{"type": "socks", "listen": "127.0.0.1", "listen_port": 41090}],
  "outbounds": [{
    "type": "vless", "tag": "out",
    "server": "127.0.0.1", "server_port": 443,
    "uuid": "12db328b-7ffa-a738-9d3d-92b78c99bcab",
    "flow": "xtls-rprx-vision",
    "tls": {"enabled": true, "server_name": "www.microsoft.com",
            "reality": {"enabled": true, "public_key": "r9ZpyMcZ5LZdgdJtQehwkcrTOcK7Feg7jfcWLk6a51c", "short_id": "38b133a3"},
            "utls": {"enabled": true, "fingerprint": "chrome"}}
  }]
}
JSON
run_one vless-reality 41090 /root/proto-tests/sb-reality.json

# --- Hysteria2 (sing-box) ---
cat > /root/proto-tests/sb-hy2.json <<JSON
{
  "log": {"level": "warn"},
  "inbounds": [{"type": "socks", "listen": "127.0.0.1", "listen_port": 41091}],
  "outbounds": [{
    "type": "hysteria2", "tag": "out",
    "server": "127.0.0.1", "server_port": 8443,
    "password": "2ea59f1e-8006-083b-9892-244a57e03005",
    "tls": {"enabled": true, "server_name": "hysteria2.fanni-panda.com", "insecure": true, "alpn": ["h3"]},
    "obfs": {"type": "salamander", "password": "228dc2c8f3c2acd6dc4005c3dd0c4e4c"}
  }]
}
JSON
run_one hysteria2 41091 /root/proto-tests/sb-hy2.json

# --- VMess + WS + TLS (Xray) ---
VMESS_UUID=$(zenithctl -q raw GET /api/v1/clients | python3 -c "import json,sys; print(next((c['uuid'] for c in json.load(sys.stdin) if c['inbound_id']==3), ''))")
cat > /root/proto-tests/sb-vmess.json <<JSON
{
  "log": {"level": "warn"},
  "inbounds": [{"type": "socks", "listen": "127.0.0.1", "listen_port": 41092}],
  "outbounds": [{
    "type": "vmess", "tag": "out",
    "server": "127.0.0.1", "server_port": 31402,
    "uuid": "$VMESS_UUID", "security": "auto",
    "transport": {"type": "ws", "path": "/vmess"},
    "tls": {"enabled": true, "server_name": "test.local", "insecure": true}
  }]
}
JSON
run_one vmess-ws 41092 /root/proto-tests/sb-vmess.json

# --- Trojan + TLS (Xray) ---
cat > /root/proto-tests/sb-trojan.json <<JSON
{
  "log": {"level": "warn"},
  "inbounds": [{"type": "socks", "listen": "127.0.0.1", "listen_port": 41093}],
  "outbounds": [{
    "type": "trojan", "tag": "out",
    "server": "127.0.0.1", "server_port": 31403,
    "password": "trojan-secret-1234",
    "tls": {"enabled": true, "server_name": "test.local", "insecure": true}
  }]
}
JSON
run_one trojan-tls 41093 /root/proto-tests/sb-trojan.json

# --- Shadowsocks 2022 (Xray) ---
cat > /root/proto-tests/sb-ss.json <<JSON
{
  "log": {"level": "warn"},
  "inbounds": [{"type": "socks", "listen": "127.0.0.1", "listen_port": 41094}],
  "outbounds": [{
    "type": "shadowsocks", "tag": "out",
    "server": "127.0.0.1", "server_port": 31404,
    "method": "2022-blake3-aes-128-gcm",
    "password": "q5Js+gMSXX/J6jM4WqsqVQ==:x8xKpiSDQQYVMmlbPQ+pNw=="
  }]
}
JSON
run_one ss-2022 41094 /root/proto-tests/sb-ss.json

# --- TUIC v5 (sing-box) — password now comes from settings.clients[].password ---
TUIC_UUID=$(zenithctl -q raw GET /api/v1/clients | python3 -c "import json,sys; clients=json.load(sys.stdin); print(next((c['uuid'] for c in clients if c['email']=='tuic-user' and c['enable']), ''))")
cat > /root/proto-tests/sb-tuic.json <<JSON
{
  "log": {"level": "warn"},
  "inbounds": [{"type": "socks", "listen": "127.0.0.1", "listen_port": 41095}],
  "outbounds": [{
    "type": "tuic", "tag": "out",
    "server": "127.0.0.1", "server_port": 31406,
    "uuid": "$TUIC_UUID", "password": "tuic-secret",
    "congestion_control": "bbr",
    "tls": {"enabled": true, "server_name": "test.local", "insecure": true, "alpn": ["h3"]}
  }]
}
JSON
run_one tuic-v5 41095 /root/proto-tests/sb-tuic.json

echo
echo "============= SUMMARY ============="
printf '%s\n' "${RESULTS[@]}"
echo "Pass: $PASS  Fail: $FAIL"
[ "$FAIL" -gt 0 ] && exit 1 || exit 0
