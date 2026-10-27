#!/usr/bin/env bash
# Linux Docker integration test for the opt-in external proxy runner.
#
# It proves the property that matters during a panel-only replacement: real
# VLESS TCP and Hysteria2/QUIC streams stay open while the panel container is
# stopped, renamed, and recreated. It intentionally does not test a
# registry-backed OTA pull; CI already exercises image construction separately,
# while this script uses one locally built image so it is deterministic and
# self-contained.
set -euo pipefail
umask 077

image=${1:?usage: test_external_proxy_runner.sh IMAGE}
command -v docker >/dev/null || { echo "docker is required" >&2; exit 2; }
docker info >/dev/null 2>&1 || { echo "a running Linux Docker daemon is required" >&2; exit 2; }
command -v openssl >/dev/null || { echo "openssl is required" >&2; exit 2; }
command -v curl >/dev/null || { echo "curl is required" >&2; exit 2; }

suffix="${RANDOM}${RANDOM}$$"
prefix="zenith-runner-e2e-${suffix}"
volume="${prefix}-data"
runner="${prefix}-runner"
panel="${prefix}-panel"
panel_old="${panel}-old"
vless_client="${prefix}-vless-client"
hy2_client="${prefix}-hy2-client"
origin="${prefix}-origin"
probe="${prefix}-probe"
workdir=$(mktemp -d)
runner_pid=""
xray_pid=""
singbox_pid=""
failure_reported=0

dump_container_logs() {
  local container
  for container in "$probe" "$vless_client" "$hy2_client" "$origin" "$panel" "$panel_old" "$runner"; do
    docker inspect "$container" >/dev/null 2>&1 || continue
    printf '\n--- %s (last 100 lines) ---\n' "$container" >&2
    docker logs --tail 100 "$container" >&2 || true
  done
}

report_failure() {
  [ "$failure_reported" = 1 ] || dump_container_logs
  failure_reported=1
}

cleanup() {
  local status=$?
  [ "$status" -eq 0 ] || report_failure
  docker rm -f "$probe" "$vless_client" "$hy2_client" "$origin" "$panel" "$panel_old" "$runner" >/dev/null 2>&1 || true
  docker volume rm "$volume" >/dev/null 2>&1 || true
  rm -rf "$workdir"
  exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT TERM

seed_manual_runtime_fixture() {
  # This is intentionally a test-only reload.  The preceding supported panel
  # apply validates the generated external-runtime candidate and topology;
  # before opening the held streams, restore the deterministic loopback
  # transport fixture that permits only this test's origin port.
  docker run --rm -v "$volume:/data" -v "$workdir:/seed:ro" busybox:1.36 sh -ceu '
    mkdir -p /data/runtime
    cp /seed/xray_config.json /data/xray_config.json
    cp /seed/singbox_config.json /data/singbox_config.json
    cp /seed/hy2.crt /seed/hy2.key /data/
    printf "enabled\\n" > /data/runtime/xray.enabled
    printf "enabled\\n" > /data/runtime/singbox.enabled
    printf "e2e-unmanaged\\n" > /data/runtime/external-managed
    rm -f /data/runtime/e2e-delay-core-start
  '
}

wait_for_runner_cores() {
  runner_pid=""
  xray_pid=""
  singbox_pid=""
  for _ in $(seq 1 30); do
    runner_pid=$(docker inspect --format '{{.State.Pid}}' "$runner")
    xray_pid=$(docker exec "$runner" sh -c 'cat /opt/zenithpanel/data/runtime/xray.pid 2>/dev/null || true')
    singbox_pid=$(docker exec "$runner" sh -c 'cat /opt/zenithpanel/data/runtime/singbox.pid 2>/dev/null || true')
    if [ -n "$runner_pid" ] && [ -n "$xray_pid" ] && [ -n "$singbox_pid" ] && docker exec "$runner" sh -c "kill -0 $xray_pid && kill -0 $singbox_pid"; then return 0; fi
    sleep 1
  done
  docker logs "$runner" >&2 || true
  return 1
}

# Container names and ports are unique per invocation so concurrent CI jobs do
# not share resources. A listener collision still fails closed during startup.
port_base=$((20000 + (RANDOM % 10000)))
vless_port=$port_base
vless_socks_port=$((port_base + 1))
hy2_port=$((port_base + 2))
hy2_socks_port=$((port_base + 3))
origin_port=$((port_base + 4))
panel_port=$((port_base + 5))
uuid="3a9c7da9-3f12-4dc6-b4ee-8c9bf6f58c0d"

cat > "$workdir/xray_config.json" <<EOF
{
  "log": {"loglevel": "warning"},
  "inbounds": [{
    "listen": "127.0.0.1", "port": ${vless_port}, "protocol": "vless",
    "settings": {"clients": [{"id": "${uuid}", "email": "e2e"}], "decryption": "none"},
    "streamSettings": {"network": "tcp", "security": "none"}
  }],
  "outbounds": [{
    "protocol": "freedom", "tag": "direct",
    "settings": {
      "finalRules": [{
        "action": "allow", "network": "tcp", "port": "${origin_port}",
        "ip": ["127.0.0.1/32"]
      }]
    }
  }]
}
EOF
cat > "$workdir/vless_client.json" <<EOF
{
  "log": {"loglevel": "warning"},
  "inbounds": [{"listen": "127.0.0.1", "port": ${vless_socks_port}, "protocol": "socks", "settings": {"auth": "noauth", "udp": false}}],
  "outbounds": [{"protocol": "vless", "settings": {"vnext": [{"address": "127.0.0.1", "port": ${vless_port}, "users": [{"id": "${uuid}", "encryption": "none"}]}]}}]
}
EOF
openssl req -x509 -newkey rsa:2048 -nodes -days 1 -subj '/CN=Zenith runner E2E CA' -keyout "$workdir/ca.key" -out "$workdir/ca.crt" >/dev/null 2>&1
openssl req -newkey rsa:2048 -nodes -subj '/CN=localhost' -keyout "$workdir/hy2.key" -out "$workdir/hy2.csr" >/dev/null 2>&1
printf 'subjectAltName=DNS:localhost\nextendedKeyUsage=serverAuth\n' > "$workdir/hy2.ext"
openssl x509 -req -days 1 -in "$workdir/hy2.csr" -CA "$workdir/ca.crt" -CAkey "$workdir/ca.key" -CAcreateserial -out "$workdir/hy2.crt" -extfile "$workdir/hy2.ext" >/dev/null 2>&1
cat > "$workdir/singbox_config.json" <<EOF
{
  "inbounds": [{
    "type": "hysteria2", "listen": "127.0.0.1", "listen_port": ${hy2_port},
    "users": [{"password": "${uuid}"}], "up_mbps": 100, "down_mbps": 100,
    "obfs": {"type": "salamander", "password": "runner-e2e-obfs"},
    "tls": {"enabled": true, "server_name": "localhost", "certificate_path": "/opt/zenithpanel/data/hy2.crt", "key_path": "/opt/zenithpanel/data/hy2.key", "alpn": ["h3"]}
  }],
  "outbounds": [{"type": "direct", "tag": "direct"}]
}
EOF
cat > "$workdir/hy2_client.json" <<EOF
{
  "inbounds": [{"type": "socks", "listen": "127.0.0.1", "listen_port": ${hy2_socks_port}}],
  "outbounds": [{
    "type": "hysteria2", "tag": "proxy", "server": "127.0.0.1", "server_port": ${hy2_port}, "password": "${uuid}",
    "obfs": {"type": "salamander", "password": "runner-e2e-obfs"},
    "tls": {"enabled": true, "server_name": "localhost", "alpn": ["h3"]}
  }],
  "route": {"final": "proxy"}
}
EOF
cat > "$workdir/vless_inbound.json" <<EOF
{
  "tag": "runner-e2e-vless", "protocol": "vless", "listen": "127.0.0.1",
  "server_address": "127.0.0.1", "port": ${vless_port}, "network": "tcp",
  "settings": "{\\"decryption\\":\\"none\\"}",
  "stream": "{\\"network\\":\\"tcp\\",\\"security\\":\\"none\\"}", "enable": true
}
EOF
cat > "$workdir/hy2_inbound.json" <<EOF
{
  "tag": "runner-e2e-hysteria2", "protocol": "hysteria2", "listen": "127.0.0.1",
  "server_address": "127.0.0.1", "port": ${hy2_port}, "network": "udp",
  "settings": "{\\"obfs\\":{\\"type\\":\\"salamander\\",\\"password\\":\\"runner-e2e-obfs\\"},\\"up_mbps\\":100,\\"down_mbps\\":100}",
  "stream": "{\\"network\\":\\"udp\\",\\"security\\":\\"tls\\",\\"tlsSettings\\":{\\"serverName\\":\\"localhost\\",\\"alpn\\":[\\"h3\\"],\\"certificates\\":[{\\"certificateFile\\":\\"/opt/zenithpanel/data/hy2.crt\\",\\"keyFile\\":\\"/opt/zenithpanel/data/hy2.key\\"}]}}", "enable": true
}
EOF
mkdir -p "$workdir/delay-bin"
cat > "$workdir/delay-bin/xray" <<'EOF'
#!/bin/sh
delay_server_start=1
for arg in "$@"; do
  [ "$arg" = '-test' ] && delay_server_start=0
done
if [ "$delay_server_start" = 1 ] && [ -f /opt/zenithpanel/data/runtime/e2e-delay-core-start ]; then
  echo 'runner e2e: delaying xray server start' >&2
  sleep 3
fi
exec /usr/local/bin/xray "$@"
EOF
cat > "$workdir/delay-bin/sing-box" <<'EOF'
#!/bin/sh
if [ "${1:-}" != 'check' ] && [ -f /opt/zenithpanel/data/runtime/e2e-delay-core-start ]; then
  echo 'runner e2e: delaying sing-box server start' >&2
  sleep 3
fi
exec /usr/local/bin/sing-box "$@"
EOF
chmod 755 "$workdir/delay-bin/xray" "$workdir/delay-bin/sing-box"
cat > "$workdir/origin.py" <<'EOF'
import os
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from time import monotonic, sleep

class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"
    def log_message(self, *_): pass
    def do_GET(self):
        print(f"origin: request from {self.client_address}", flush=True)
        self.send_response(200)
        self.send_header("Content-Length", "13")
        self.send_header("Connection", "close")
        self.end_headers()
        self.wfile.write(b"first\n")
        self.wfile.flush()
        # The host creates this only after the replacement panel is healthy
        # and the runner/Xray PIDs have been checked.  Do not substitute a
        # fixed delay: a fast CI replacement could otherwise finish after the
        # whole response has already been sent.
        deadline = monotonic() + 45
        while not os.path.exists("/test/replacement_complete"):
            if monotonic() >= deadline:
                self.wfile.write(b"timeout\n")
                self.wfile.flush()
                return
            sleep(0.1)
        self.wfile.write(b"second\n")
        self.wfile.flush()
        print("origin: released second chunk", flush=True)

ThreadingHTTPServer(("127.0.0.1", int(os.environ["ORIGIN_PORT"])), Handler).serve_forever()
EOF
cat > "$workdir/probe.py" <<'EOF'
import os, socket, struct

def progress(message):
    print(message, flush=True)

def recv_exact(sock, size):
    result = b""
    while len(result) < size:
        chunk = sock.recv(size - len(result))
        if not chunk: raise SystemExit("SOCKS connection closed unexpectedly")
        result += chunk
    return result

def recv_socks_reply(sock, label):
    header = recv_exact(sock, 4)
    if header[0] != 5 or header[1] != 0:
        raise SystemExit(f"{label}: SOCKS connect failed ({header[1]})")
    atyp = header[3]
    if atyp == 1:
        recv_exact(sock, 6)
    elif atyp == 4:
        recv_exact(sock, 18)
    elif atyp == 3:
        recv_exact(sock, recv_exact(sock, 1)[0] + 2)
    else:
        raise SystemExit(f"{label}: SOCKS returned unknown ATYP {atyp}")

def open_stream(label, socks_port):
    progress(f"{label}: connecting to SOCKS")
    sock = socket.create_connection(("127.0.0.1", socks_port), timeout=10)
    # Keep the application stream alive long enough to span the panel swap and
    # its health check, while still failing deterministically if that swap hangs.
    sock.settimeout(60)
    sock.sendall(b"\x05\x01\x00")
    if recv_exact(sock, 2) != b"\x05\x00": raise SystemExit(f"{label}: SOCKS authentication failed")
    sock.sendall(b"\x05\x01\x00\x01" + socket.inet_aton("127.0.0.1") + struct.pack("!H", int(os.environ["ORIGIN_PORT"])))
    recv_socks_reply(sock, label)
    progress(f"{label}: SOCKS tunnel established")
    sock.sendall(b"GET / HTTP/1.1\r\nHost: origin\r\nConnection: close\r\n\r\n")
    data = b""
    while b"first\n" not in data:
        chunk = sock.recv(4096)
        if not chunk: raise SystemExit(f"{label}: origin closed before first chunk")
        data += chunk
    progress(f"{label}: received first chunk")
    return sock, data

streams = [
    ("vless", *open_stream("vless", int(os.environ["VLESS_SOCKS_PORT"]))),
    ("hysteria2", *open_stream("hysteria2", int(os.environ["HY2_SOCKS_PORT"]))),
]
open("/test/first", "w").write("received\n")
progress("both: first chunks received")
for label, sock, data in streams:
    while True:
        chunk = sock.recv(4096)
        if not chunk: break
        data += chunk
    if b"first\nsecond\n" not in data: raise SystemExit(f"{label}: stream did not survive to second chunk")
    progress(f"{label}: received second chunk")
open("/test/complete", "w").write("received\n")
EOF

docker volume create "$volume" >/dev/null
seed_manual_runtime_fixture

# This test never uses :8080, which is reserved by the existing zsmoke
# container in CI.  All host-network listeners use the private random range
# selected above.
docker run -d --name "$origin" --network host -v "$workdir:/test:ro" -e ORIGIN_PORT="$origin_port" python:3.12-alpine python /test/origin.py >/dev/null
docker run -d --name "$runner" --network host -v "$volume:/opt/zenithpanel/data" -v "$workdir/delay-bin:/test/delay-bin:ro" \
  -e PATH=/test/delay-bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin \
  --entrypoint /opt/zenithpanel/scripts/proxy-runner.sh "$image" >/dev/null
wait_for_runner_cores

docker run -d --name "$vless_client" --network host -v "$workdir/vless_client.json:/test/client.json:ro" --entrypoint xray "$image" run -c /test/client.json >/dev/null
docker run -d --name "$hy2_client" --network host -v "$workdir/hy2_client.json:/test/client.json:ro" -v "$workdir/ca.crt:/test/ca.crt:ro" -e SSL_CERT_FILE=/test/ca.crt --entrypoint sing-box "$image" run -c /test/client.json >/dev/null
for _ in $(seq 1 20); do
  if docker exec "$vless_client" ss -ltn "sport = :$vless_socks_port" | grep -q ":$vless_socks_port" && docker exec "$hy2_client" ss -ltn "sport = :$hy2_socks_port" | grep -q ":$hy2_socks_port"; then break; fi
  sleep 1
done
docker exec "$vless_client" ss -ltn "sport = :$vless_socks_port" | grep -q ":$vless_socks_port" || { docker logs "$vless_client" >&2; exit 1; }
docker exec "$hy2_client" ss -ltn "sport = :$hy2_socks_port" | grep -q ":$hy2_socks_port" || { docker logs "$hy2_client" >&2; exit 1; }
docker run -d --name "$panel" --network host -v /var/run/docker.sock:/var/run/docker.sock -v "$volume:/opt/zenithpanel/data" -v "$workdir:/test:ro" \
  -e ZENITH_PORT="$panel_port" -e ZENITH_PROXY_RUNTIME=external -e ZENITH_PROXY_RUNNER_CONTAINER="$runner" "$image" >/dev/null

for _ in $(seq 1 30); do
  if docker exec "$panel" wget -qO- "http://127.0.0.1:${panel_port}/api/v1/health" | grep -q '"status":"ok"'; then break; fi
  sleep 1
done
docker exec "$panel" wget -qO- "http://127.0.0.1:${panel_port}/api/v1/health" | grep -q '"status":"ok"' || { docker logs "$panel" >&2; exit 1; }

# Seed the enabled rows through the panel's supported setup and local CLI
# surfaces.  The runtime files remain the deliberately hand-written fixture:
# this specifically verifies that a replacement panel sees enabled inbounds
# but does not auto-apply/restart the separately managed runner.
setup_password=""
for _ in $(seq 1 20); do
  setup_password=$(docker logs "$panel" 2>&1 | sed -n 's/^[[:space:]]*Password:[[:space:]]*//p' | tail -n 1)
  [ -n "$setup_password" ] && break
  sleep 1
done
[ -n "$setup_password" ] || { echo "failed to obtain setup password from panel logs" >&2; exit 1; }
setup_cookie="$workdir/setup.cookie"
curl --fail --silent --show-error --cookie-jar "$setup_cookie" -H 'Content-Type: application/json' \
  --data "{\"password\":\"${setup_password}\"}" "http://127.0.0.1:${panel_port}/api/setup/login" | grep -q '"code":200'
curl --fail --silent --show-error --cookie "$setup_cookie" -H 'Content-Type: application/json' \
  -H "Origin: http://127.0.0.1:${panel_port}" \
  --data '{"username":"runneradmin","password":"runner-e2e-password","usage_profile":"vps_ops"}' \
  "http://127.0.0.1:${panel_port}/api/setup/complete" | grep -q '"code":200'

vless_inbound=$(docker exec "$panel" /opt/zenithpanel/zenithpanel ctl --host unix:///run/zenithpanel.sock -q inbound create --file /test/vless_inbound.json)
hy2_inbound=$(docker exec "$panel" /opt/zenithpanel/zenithpanel ctl --host unix:///run/zenithpanel.sock -q inbound create --file /test/hy2_inbound.json)
vless_inbound_id=$(printf '%s\n' "$vless_inbound" | sed -nE 's/.*"id"[[:space:]]*:[[:space:]]*([0-9]+).*/\1/p' | head -n 1)
hy2_inbound_id=$(printf '%s\n' "$hy2_inbound" | sed -nE 's/.*"id"[[:space:]]*:[[:space:]]*([0-9]+).*/\1/p' | head -n 1)
[ -n "$vless_inbound_id" ] && [ -n "$hy2_inbound_id" ] || { echo "failed to parse seeded inbound IDs" >&2; exit 1; }
docker exec "$panel" /opt/zenithpanel/zenithpanel ctl --host unix:///run/zenithpanel.sock -q client add --inbound "$vless_inbound_id" --email runner-e2e-vless --uuid "$uuid" >/dev/null
docker exec "$panel" /opt/zenithpanel/zenithpanel ctl --host unix:///run/zenithpanel.sock -q client add --inbound "$hy2_inbound_id" --email runner-e2e-hysteria2 --uuid "$uuid" >/dev/null
seeded_inbounds=$(docker exec "$panel" /opt/zenithpanel/zenithpanel ctl --host unix:///run/zenithpanel.sock -q inbound list)
seeded_clients=$(docker exec "$panel" /opt/zenithpanel/zenithpanel ctl --host unix:///run/zenithpanel.sock -q client list)
[[ "$seeded_inbounds" == *'"tag":"runner-e2e-vless"'* && "$seeded_inbounds" == *'"tag":"runner-e2e-hysteria2"'* ]] || { echo "enabled runner inbounds were not persisted" >&2; exit 1; }
[[ "$seeded_clients" == *'"email":"runner-e2e-vless"'* && "$seeded_clients" == *'"email":"runner-e2e-hysteria2"'* ]] || { echo "runner clients were not persisted" >&2; exit 1; }

# Exercise the production external apply path while no user stream exists.
# It must inspect the real host-network runner topology, generate and validate
# both enabled protocol configs, and restart the runner successfully.  Then
# restore the deterministic loopback fixture for the actual stream test: the
# Xray version under test deliberately blocks private egress except the narrow
# fixture rule, which is not a production policy relaxation.
docker run --rm -v "$volume:/data" busybox:1.36 sh -ceu 'touch /data/runtime/e2e-delay-core-start'
apply_started=$(date +%s)
docker exec "$panel" /opt/zenithpanel/zenithpanel ctl --host unix:///run/zenithpanel.sock -q proxy apply >/dev/null
apply_elapsed=$(( $(date +%s) - apply_started ))
[ "$apply_elapsed" -ge 2 ] || { echo "external apply did not wait for delayed core listeners" >&2; exit 1; }
# Do not use grep -q here: with pipefail it may exit after the first match,
# causing docker logs to receive SIGPIPE and turning a successful assertion
# into a false failure. Plain grep drains the complete log stream.
docker logs "$runner" 2>&1 | grep -F 'runner e2e: delaying xray server start' >/dev/null || { echo "xray delayed-listener fixture was not used" >&2; exit 1; }
docker logs "$runner" 2>&1 | grep -F 'runner e2e: delaying sing-box server start' >/dev/null || { echo "sing-box delayed-listener fixture was not used" >&2; exit 1; }
seed_manual_runtime_fixture
docker restart "$runner" >/dev/null
wait_for_runner_cores

docker run -d --name "$probe" --network host -v "$workdir:/test" -e VLESS_SOCKS_PORT="$vless_socks_port" -e HY2_SOCKS_PORT="$hy2_socks_port" -e ORIGIN_PORT="$origin_port" python:3.12-alpine python /test/probe.py >/dev/null

for _ in $(seq 1 20); do
  [ -f "$workdir/first" ] && break
  sleep 1
done
[ -f "$workdir/first" ] || { report_failure; exit 1; }
[ "$(docker inspect --format '{{.State.Running}}' "$probe")" = "true" ] || { report_failure; exit 1; }

panel_mount=$(docker inspect --format '{{.HostConfig.NetworkMode}}|{{range .Mounts}}{{if eq .Destination "/opt/zenithpanel/data"}}{{.Name}}:{{.Source}}:{{.RW}}{{end}}{{end}}' "$panel")
runner_mount=$(docker inspect --format '{{.HostConfig.NetworkMode}}|{{range .Mounts}}{{if eq .Destination "/opt/zenithpanel/data"}}{{.Name}}:{{.Source}}:{{.RW}}{{end}}{{end}}' "$runner")
[ "$panel_mount" = "$runner_mount" ] && [[ "$panel_mount" == host\|*:true ]] || { echo "panel/runner topology mismatch" >&2; exit 1; }
runtime_configs_before=$(docker exec "$runner" sh -c 'sha256sum /opt/zenithpanel/data/xray_config.json /opt/zenithpanel/data/singbox_config.json')

# This is the same stop/rename/create boundary used by the OTA helper. The
# image stays local because testing a registry pull is outside this invariant.
docker stop "$panel" >/dev/null
docker rename "$panel" "$panel_old"
docker run -d --name "$panel" --network host -v /var/run/docker.sock:/var/run/docker.sock -v "$volume:/opt/zenithpanel/data" \
  -e ZENITH_PORT="$panel_port" -e ZENITH_PROXY_RUNTIME=external -e ZENITH_PROXY_RUNNER_CONTAINER="$runner" "$image" >/dev/null

[ "$(docker inspect --format '{{.State.Pid}}' "$runner")" = "$runner_pid" ] || { echo "runner container PID changed during panel replacement" >&2; exit 1; }
[ "$(docker exec "$runner" sh -c 'cat /opt/zenithpanel/data/runtime/xray.pid')" = "$xray_pid" ] || { echo "xray PID changed during panel replacement" >&2; exit 1; }
[ "$(docker exec "$runner" sh -c 'cat /opt/zenithpanel/data/runtime/singbox.pid')" = "$singbox_pid" ] || { echo "sing-box PID changed during panel replacement" >&2; exit 1; }
docker exec "$runner" sh -c "kill -0 $xray_pid && kill -0 $singbox_pid" || { echo "proxy process exited during panel replacement" >&2; exit 1; }

for _ in $(seq 1 30); do
  if docker exec "$panel" wget -qO- "http://127.0.0.1:${panel_port}/api/v1/health" | grep -q '"status":"ok"'; then break; fi
  sleep 1
done
docker exec "$panel" wget -qO- "http://127.0.0.1:${panel_port}/api/v1/health" | grep -q '"status":"ok"' || { docker logs "$panel" >&2; exit 1; }
runtime_configs_after=$(docker exec "$runner" sh -c 'sha256sum /opt/zenithpanel/data/xray_config.json /opt/zenithpanel/data/singbox_config.json')
[ "$runtime_configs_after" = "$runtime_configs_before" ] || { echo "panel replacement rewrote external runtime configs" >&2; exit 1; }
seeded_inbounds=$(docker exec "$panel" /opt/zenithpanel/zenithpanel ctl --host unix:///run/zenithpanel.sock -q inbound list)
seeded_clients=$(docker exec "$panel" /opt/zenithpanel/zenithpanel ctl --host unix:///run/zenithpanel.sock -q client list)
[[ "$seeded_inbounds" == *'"tag":"runner-e2e-vless"'* && "$seeded_inbounds" == *'"tag":"runner-e2e-hysteria2"'* ]] || { echo "replacement panel lost enabled inbounds" >&2; exit 1; }
[[ "$seeded_clients" == *'"email":"runner-e2e-vless"'* && "$seeded_clients" == *'"email":"runner-e2e-hysteria2"'* ]] || { echo "replacement panel lost clients" >&2; exit 1; }

# The origin cannot release the response's second half before this point.
# Confirm the probe is still attached, then let its already-established VLESS
# and Hysteria2 streams read their second halves after replacement completes.
[ "$(docker inspect --format '{{.State.Running}}' "$probe")" = "true" ] || { report_failure; exit 1; }
: > "$workdir/replacement_complete"

for _ in $(seq 1 20); do
  [ -f "$workdir/complete" ] && break
  sleep 1
done
[ -f "$workdir/complete" ] || { report_failure; exit 1; }
[ "$(docker wait "$probe")" = 0 ] || { report_failure; exit 1; }

echo "external runner E2E passed: VLESS and Hysteria2 streams survived a real panel container replacement"
