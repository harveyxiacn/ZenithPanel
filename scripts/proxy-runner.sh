#!/bin/sh
# Runs the data-plane cores independently from the ZenithPanel web process.
# This script is intentionally only activated by an operator-created runner
# container. The panel never creates one automatically, because moving an
# already-bound TCP/QUIC listener necessarily disconnects existing clients.

set -eu

data_dir=/opt/zenithpanel/data
runtime_dir="$data_dir/runtime"
mkdir -p "$runtime_dir"
xray_pid=""
singbox_pid=""

clear_pid() {
  rm -f "$runtime_dir/xray.pid" "$runtime_dir/singbox.pid"
}

stop() {
  [ -n "$xray_pid" ] && kill "$xray_pid" 2>/dev/null || true
  [ -n "$singbox_pid" ] && kill "$singbox_pid" 2>/dev/null || true
  wait 2>/dev/null || true
  clear_pid
  exit 0
}
trap stop INT TERM
clear_pid

if [ -f "$runtime_dir/xray.enabled" ]; then
  xray run -c "$data_dir/xray_config.json" &
  xray_pid=$!
  printf '%s\n' "$xray_pid" > "$runtime_dir/xray.pid"
fi
if [ -f "$runtime_dir/singbox.enabled" ]; then
  sing-box run -c "$data_dir/singbox_config.json" &
  singbox_pid=$!
  printf '%s\n' "$singbox_pid" > "$runtime_dir/singbox.pid"
fi

while :; do
  if [ -n "$xray_pid" ] && ! kill -0 "$xray_pid" 2>/dev/null; then
    wait "$xray_pid" || true
    echo "proxy runner: xray exited" >&2
    exit 1
  fi
  if [ -n "$singbox_pid" ] && ! kill -0 "$singbox_pid" 2>/dev/null; then
    wait "$singbox_pid" || true
    echo "proxy runner: sing-box exited" >&2
    exit 1
  fi
  sleep 1
done
