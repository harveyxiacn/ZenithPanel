# Protocol & Engine Guide

## Which engine handles which protocol?

| Protocol | Xray | Sing-box | Notes |
|----------|------|----------|-------|
| VLESS | ✅ | ✅ | Both engines; XTLS Vision flow only works in Xray |
| VMess | ✅ | ✅ | Both engines |
| Trojan | ✅ | ✅ | **Requires TLS or Reality** — Sing-box will refuse to start without it |
| Shadowsocks | ✅ | ✅ | AEAD-2022 enables per-user traffic tracking (see below) |
| Hysteria2 | ❌ | ✅ | **Sing-box only** — use Sing-box engine |
| TUIC v5 | ❌ | ✅ | **Sing-box only** — use Sing-box engine |
| WireGuard | ❌ | ❌ | Planned; not yet implemented |

## Engine mutual exclusivity

Xray and Sing-box **cannot run simultaneously** when they share inbound ports.
Attempting to start both causes the second engine to fail with "address already in
use". The **Apply Config** button now enforces mutual exclusivity:

- Selecting **Xray** stops Sing-box before starting Xray.
- Selecting **Sing-box** stops Xray before starting Sing-box.

The UI engine radio selector auto-selects Sing-box when any inbound uses Hysteria2
or TUIC.

## Shadowsocks multi-user (AEAD-2022)

Classic Shadowsocks methods (aes-256-gcm, chacha20-poly1305, etc.) use a single
shared password for all clients on the inbound. Traffic statistics are per-inbound,
not per-user.

AEAD-2022 methods enable per-user mode:
- `2022-blake3-aes-128-gcm`
- `2022-blake3-aes-256-gcm`
- `2022-blake3-chacha20-poly1305`

When one of these methods is configured, each client's UUID is used as their
individual password. This allows per-client traffic tracking and individual
revocation.

**Configuration**: Set `method` to an AEAD-2022 value in the inbound settings JSON.
Clients connect using their UUID as the password. The subscription link
(`ss://…`) uses the inbound's shared `password` field for the base64 userinfo,
so existing SIP002 clients can still import the link normally.

## Trojan TLS requirement

Trojan protocol operates over TLS (or Reality). A Trojan inbound without TLS
security configured:

- Is **rejected at creation time** (`validateInbound` returns an error).
- Would cause the entire Sing-box process to fail to start if somehow persisted.

Set `"security": "tls"` or `"security": "reality"` in the inbound's stream JSON.
For TLS you also need a valid `tlsSettings.certificates` entry pointing to cert
and key files.

## ACME / Let's Encrypt certificates

`POST /api/v1/proxy/tls/issue` now runs a real ACME HTTP-01 challenge via lego.

Requirements:
- Domain must resolve to the server's public IP.
- Port **80 must be reachable** from the internet for the HTTP-01 challenge token.
- Provide a valid `email` for ACME account registration.

Certificates are stored in `/opt/zenithpanel/data/certs/<domain>.{crt,key}` with
`0600` permissions.

Smart Deploy's `cert_mode=acme` preset now triggers this flow automatically when
a domain is supplied.

## Subscription links

The `subscription-userinfo` header now includes `expire=<unix>` when a client
has an expiry date set. Compatible clients (Clash Meta, v2rayN 6+) will display
the expiry date in their subscription UI.
