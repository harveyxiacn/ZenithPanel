#!/bin/bash
# Seeds the panel with one inbound per protocol we still need to verify, then
# creates one client per inbound. Idempotent: re-running is safe because the
# tags are unique and `inbound create` rejects duplicates.
set -e

# Reusable: post a JSON body to the panel.
post() { zenithctl raw POST "$1" --data "$2"; }

uuid() { cat /proc/sys/kernel/random/uuid; }

# 1) VMess + WebSocket + TLS  (port 31402, served by Xray)
post /api/v1/inbounds "$(cat <<JSON
{
  "tag": "vmess-ws",
  "protocol": "vmess",
  "port": 31402,
  "network": "ws",
  "server_address": "127.0.0.1",
  "settings": "{\"clients\":[{\"email\":\"vmess-user\",\"id\":\"$(uuid)\",\"alterId\":0}]}",
  "stream": "{\"network\":\"ws\",\"security\":\"tls\",\"wsSettings\":{\"path\":\"/vmess\"},\"tlsSettings\":{\"serverName\":\"test.local\",\"certificates\":[{\"certificateFile\":\"/opt/zenithpanel/data/certs/fullchain.pem\",\"keyFile\":\"/opt/zenithpanel/data/certs/privkey.pem\"}]}}",
  "enable": true
}
JSON
)"

# 2) Trojan + TLS  (port 31403, served by Xray)
post /api/v1/inbounds "$(cat <<JSON
{
  "tag": "trojan-tls",
  "protocol": "trojan",
  "port": 31403,
  "network": "tcp",
  "server_address": "127.0.0.1",
  "settings": "{\"clients\":[{\"email\":\"trojan-user\",\"password\":\"trojan-secret-1234\"}]}",
  "stream": "{\"network\":\"tcp\",\"security\":\"tls\",\"tlsSettings\":{\"serverName\":\"test.local\",\"certificates\":[{\"certificateFile\":\"/opt/zenithpanel/data/certs/fullchain.pem\",\"keyFile\":\"/opt/zenithpanel/data/certs/privkey.pem\"}]}}",
  "enable": true
}
JSON
)"

# 3) Shadowsocks 2022 (port 31404, served by Xray) — psk is base64(32-byte key)
post /api/v1/inbounds "$(cat <<JSON
{
  "tag": "ss-2022",
  "protocol": "shadowsocks",
  "port": 31404,
  "network": "tcp",
  "server_address": "127.0.0.1",
  "settings": "{\"method\":\"2022-blake3-aes-128-gcm\",\"password\":\"q5Js+gMSXX/J6jM4WqsqVQ==\",\"clients\":[{\"email\":\"ss-user\",\"password\":\"x8xKpiSDQQYVMmlbPQ+pNw==\"}]}",
  "stream": "{\"network\":\"tcp\"}",
  "enable": true
}
JSON
)"

# 4) TUIC v5 (port 31406, served by Sing-box)
post /api/v1/inbounds "$(cat <<JSON
{
  "tag": "tuic-v5",
  "protocol": "tuic",
  "port": 31406,
  "network": "udp",
  "server_address": "127.0.0.1",
  "settings": "{\"clients\":[{\"email\":\"tuic-user\",\"uuid\":\"$(uuid)\",\"password\":\"tuic-secret\"}],\"congestion_control\":\"bbr\"}",
  "stream": "{\"network\":\"udp\",\"security\":\"tls\",\"tlsSettings\":{\"serverName\":\"test.local\",\"certificates\":[{\"certificateFile\":\"/opt/zenithpanel/data/certs/fullchain.pem\",\"keyFile\":\"/opt/zenithpanel/data/certs/privkey.pem\"}]}}",
  "enable": true
}
JSON
)"

echo "--- inbounds after seed ---"
zenithctl -q inbound list | python3 -c "import json,sys; [print(i['id'], i['tag'], i['protocol'], i['port']) for i in json.load(sys.stdin)]"
