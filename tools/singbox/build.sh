#!/usr/bin/env sh
# Builds the upstream v1.14.4 feature set with security-patched Go modules.
# Docker and the IPv6 CI both invoke this file so their Sing-box binaries stay
# behaviorally identical.
set -eu

: "${SINGBOX_OUTPUT:?set SINGBOX_OUTPUT to the destination binary path}"

tags='with_gvisor,with_quic,with_dhcp,with_wireguard,with_utls,with_acme,with_clash_api,with_tailscale,with_ccm,with_ocm,with_cloudflared,with_usbip,with_openvpn,with_openconnect,badlinkname,tfogo_checklinkname0'
ldflags='-X runtime.godebugDefault=multipathtcp=0,tlssha1=1 -checklinkname=0 -X github.com/sagernet/sing-box/constant.Version=1.14.4 -s -w -buildid='

mkdir -p "$(dirname "$SINGBOX_OUTPUT")"
export GOTOOLCHAIN=local
export CGO_ENABLED=0
go build -mod=readonly -trimpath -tags "$tags" -ldflags "$ldflags" -o "$SINGBOX_OUTPUT" github.com/sagernet/sing-box/cmd/sing-box
