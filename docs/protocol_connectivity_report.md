# Protocol Connectivity Sweep — 2026-05-15

> **Update (2026-05-15, second pass)**: The three "follow-up" items at the
> bottom of this report are all fixed in commits on this same day. The sweep
> now passes cleanly on both single-engine and dual-engine modes with no
> manual JSON editing required.

Test target: rabisu VPS (Ubuntu 24.04, panel running inside `ghcr.io/harveyxiacn/zenithpanel:main`).
Driver: `scripts/proto_sweep.sh`, invoked from a normal SSH session via the new
`zenithctl` CLI. Each protocol gets its own sing-box client that opens a local
SOCKS5 listener; the script then `curl`s `https://www.google.com` through that
SOCKS5 and parses the HTTP status.

## Result

| Inbound        | Engine    | Port  | Transport     | Result | Notes                                                |
|---             |---        |---    |---            |---     |---                                                   |
| `vless-reality`| sing-box  | 443   | tcp + reality | ✅ PASS | Baseline; matches user's pre-existing confidence    |
| `hysteria2`    | sing-box  | 8443  | udp + tls(h3) | ✅ PASS | Required fixing `ENABLE_DEPRECATED_GEOSITE` (below)  |
| `vmess-ws`     | sing-box  | 31402 | ws  + tls     | ✅ PASS | New inbound, self-signed cert, server_name=test.local |
| `trojan-tls`   | sing-box  | 31403 | tcp + tls     | ✅ PASS | New inbound                                          |
| `ss-2022`      | sing-box  | 31404 | tcp           | ✅ PASS | 2022-blake3-aes-128-gcm                              |
| `tuic-v5`      | sing-box  | 31406 | udp + tls(h3) | ✅ PASS | After fixes #2 and #3 below                          |

All six pass `curl --socks5 ... https://www.google.com` → HTTP 200. Egress IP
matches the VPS IP, confirming traffic actually flowed through the panel
listeners.

## Fixes Made During the Sweep

1. **sing-box refused to start at all** because the panel's generated config
   references the geosite database, which sing-box 1.11 deprecated. Fixed in
   `backend/internal/service/proxy/singbox.go` by setting
   `ENABLE_DEPRECATED_GEOSITE=true` on the sing-box `exec.Cmd` env. Long-term
   we should migrate geosite rules to rule-sets (tracked as separate work).

2. **TUIC inbound emitted without `alpn`** ✅ **Fixed**. The sing-box config
   generator now defaults `tls.alpn` to `["h3"]` for both Hysteria2 and TUIC
   when the inbound doesn't supply one. Admins can still override via
   `tlsSettings.alpn`. Regression-pinned by
   `TestTUICDefaultsALPNToH3` / `TestHysteria2DefaultsALPNToH3`.

3. **TUIC password not actually configurable** ✅ **Fixed**. The sing-box
   generator now reads `inbound.Settings.clients[].password` keyed by email
   and uses it as the TUIC user's password; UUID stays as the fallback when
   no password is supplied. See `perUserPasswordsFromSettings` in
   `backend/internal/service/proxy/singbox.go` and the regression test
   `TestTUICPerUserPasswordOverride`.

## Dual-Engine Mode (2026-05-15)

The panel now runs Xray and Sing-box side-by-side by default. At startup (and
on `POST /api/v1/proxy/apply` without an engine query param), the engine
partitioner splits enabled inbounds:

- **Xray** serves VLESS / VMess / Trojan / Shadowsocks
- **Sing-box** serves Hysteria2 / TUIC (and any future singbox-only protocol)

Both processes run concurrently, each binds the ports for the inbounds it
owns, and `/api/v1/proxy/status` reports `dual_mode: true` plus
`xray_handed_off_to_singbox` instead of the old `xray_skipped_protocols`
warning. Single-engine override is preserved for users who explicitly POST
with `?engine=xray` or `?engine=singbox`.

Verified on the rabisu VPS:
```
tcp6 :::443    xray         (vless-reality)
tcp6 :::31402  xray         (vmess-ws)
tcp6 :::31403  xray         (trojan-tls)
tcp6 :::31404  xray         (ss-2022)
udp6 :::8443   sing-box     (hysteria2)
udp6 :::31406  sing-box     (tuic-v5)
```
All six pass `curl --socks5 ... https://www.google.com` in the same run.

## Reproducing

```bash
# On the VPS, with zenithctl already bootstrapped:
scp scripts/proto_test_setup.sh root@vps:/root/proto-tests/setup.sh
scp scripts/proto_seed_inbounds.sh root@vps:/root/proto-tests/seed.sh
scp scripts/proto_sweep.sh        root@vps:/root/proto-tests/sweep.sh

ssh root@vps bash /root/proto-tests/setup.sh   # writes self-signed cert
ssh root@vps bash /root/proto-tests/seed.sh    # adds the 4 missing inbounds
ssh root@vps zenithctl raw POST '/api/v1/proxy/apply?engine=singbox'
ssh root@vps bash /root/proto-tests/sweep.sh   # prints PASS/FAIL per protocol
```

## Test Artifacts Layout (on the VPS)

```
/root/proto-tests/
├── setup.sh          # generates /opt/zenithpanel/data/certs/{fullchain,privkey}.pem
├── seed.sh           # POSTs vmess-ws / trojan-tls / ss-2022 / tuic-v5 inbounds
├── sweep.sh          # main entry — one client config per protocol, curl probe
├── sb-reality.json   # sing-box client configs (generated on each run)
├── sb-hy2.json
├── sb-vmess.json
├── sb-trojan.json
├── sb-ss.json
├── sb-tuic.json
└── *.log             # client-side sing-box stderr per protocol
```

## Known Limitations of This Sweep

- Self-signed certs with `insecure: true` on the client. A real ACME-issued
  cert would also verify chain handling, which this test doesn't cover.
- Loopback connectivity only — does not catch firewall / NAT misconfiguration
  that only shows up from the public Internet.
- One client per protocol, single `curl` request. Doesn't catch
  stability/throughput issues under sustained load.

## Recommended Follow-ups

1. Bake the three fixes above (especially #2 and #3) into the singbox config
   generator so freshly-created inbounds work without manual edits.
2. Add `/api/v1/proxy/test/:inbound_id` server-side probe as designed in
   `docs/cli_api_spec.md` §2.5, then promote `zenithctl proxy test-all` from
   client-side script to a single panel call.
3. Migrate geosite rules to rule-sets and remove the `ENABLE_DEPRECATED_GEOSITE`
   env hack.
