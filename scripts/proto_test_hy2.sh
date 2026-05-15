#!/bin/bash
# Hysteria2 client probe. Spins up a sing-box client config pointing at the
# in-host Hysteria2 listener, opens a local SOCKS5 on 41081, and curls the
# Internet through it. Self-signed cert is trusted via tls.insecure=true.
set -e

cat > /root/proto-tests/client-hy2.json <<JSON
{
  "log": {"level": "warn"},
  "inbounds": [{"type": "socks", "listen": "127.0.0.1", "listen_port": 41081}],
  "outbounds": [{
    "type": "hysteria2",
    "tag": "hy2-out",
    "server": "127.0.0.1",
    "server_port": 8443,
    "password": "2ea59f1e-8006-083b-9892-244a57e03005",
    "tls": {"enabled": true, "server_name": "hysteria2.fanni-panda.com", "insecure": true, "alpn": ["h3"]},
    "obfs": {"type": "salamander", "password": "228dc2c8f3c2acd6dc4005c3dd0c4e4c"}
  }]
}
JSON

pkill -f 'sing-box.*client-hy2' 2>/dev/null || true
sleep 1
nohup sing-box run -c /root/proto-tests/client-hy2.json > /root/proto-tests/hy2.log 2>&1 &
sleep 3

echo "== Hysteria2 probe via SOCKS5 =="
curl -s -o /dev/null -w 'http_code=%{http_code} time=%{time_total}s\n' --max-time 12 --socks5 127.0.0.1:41081 https://www.google.com || echo "google: FAIL"
echo "== egress IP =="
curl -s --max-time 12 --socks5 127.0.0.1:41081 https://ifconfig.me || echo "ifconfig: FAIL"
echo

pkill -f 'sing-box.*client-hy2' 2>/dev/null || true
echo "== client log tail =="
tail -10 /root/proto-tests/hy2.log
