# QR Setup & Real-Cert Guide

[简体中文](qr_setup_guide_CN.md) | English

This is the operator-facing companion to [user_manual.md](user_manual.md).
It covers the two things you usually do after first install:

1. Scanning subscription QR codes into your client (Clash Meta, V2RayN,
   Shadowrocket, NekoBox…) so you can actually use the proxies.
2. Replacing the self-signed test cert with a real Let's Encrypt one so
   you stop seeing "untrusted certificate" warnings.

---

## 1. Scanning a Subscription into Clash Meta

ZenithPanel emits a per-client subscription URL at
`/api/v1/sub/<client-uuid>`. The same URL serves two formats depending on
the requester's `User-Agent` header:

- **Clash / Clash Meta / Stash** → a full Clash YAML with sensible
  defaults (fake-ip DNS, geoip CN fallback, AUTO group)
- **V2RayN / NekoBox / Shadowrocket / generic** → a base64-encoded list
  of one-line protocol URIs (`vless://`, `vmess://`, `trojan://`, etc.)

### Step-by-step (Clash Meta on phone)

1. In the panel, open **Proxy → Users & Subs**.
2. Find the client row and click the **QR Code** button on the right.
3. Toggle the format selector to **Clash / Mihomo**.
4. In Clash Meta on your phone: **+ → Scan QR Code** → point at the
   panel screen.
5. The new proxy group appears in **Profiles**. Tap to activate.

### Step-by-step (V2RayN / NekoBox)

Same flow but pick the **V2Ray / V2RayN** format in the QR modal. The
QR is a standard `vmess://`/`vless://`/etc. URI that every mainstream
client understands.

### Multiple clients on one device

Each client UUID returns *that client's* inbound only. To test multiple
protocols on the same device, scan multiple QRs (one per client) — they
each become a separate proxy entry in your client and you switch between
them from the proxy selector.

---

## 2. The Self-Signed Cert Problem

When ZenithPanel ships test inbounds it generates a 7-day **self-signed**
certificate so TLS-bound protocols (VMess+WS+TLS, Trojan, Hysteria2,
TUIC) can run end-to-end without external setup. The subscription URL
for these inbounds carries `allowInsecure=1` so clients accept the
self-signed cert.

This is **fine for testing** but two things make it unsuitable for
long-term use:

- The cert expires every 7 days and the panel doesn't renew test certs
  (only ACME-managed certs are renewed).
- `allowInsecure` is exactly what its name says — your client trusts
  *any* certificate, which means a path-on-the-wire MITM could swap in
  one of their own and read your traffic.

For anything beyond "does this protocol work at all?" you want a real
cert. Read on.

---

## 3. Issue a Real Let's Encrypt Cert

### Prerequisites

- A domain (or sub-domain) you own.
- A DNS A record pointing that domain at the VPS public IP.
- Firewall port **80/tcp open** during issuance (HTTP-01 challenge).
  Open it via `zenithctl firewall add --port 80 --proto tcp --action ACCEPT`,
  or with `ufw allow 80/tcp`, before clicking the issue button.

### From the Web UI

1. **Settings → HTTPS / TLS Configuration → Let's Encrypt (ACME)**.
2. Enter your domain (e.g. `proxy.example.com`) and an email address
   Let's Encrypt can use for renewal notices.
3. Click **Issue certificate**. Lego (the embedded ACME client) binds
   `:80` briefly, runs the challenge, and writes the cert + key to:
   - `/opt/zenithpanel/data/certs/<domain>.crt`
   - `/opt/zenithpanel/data/certs/<domain>.key`
4. The success banner shows both paths plus the expiry date.

### From the CLI

```bash
zenithctl cert issue --domain proxy.example.com --email you@example.com
```

Output includes the on-disk paths, the `not_after` Unix timestamp, and
exit code 0 on success.

### Use the cert in an inbound

ACME issuance only produces the files — you still need to point an
inbound at them. The fastest path:

- **For a single inbound**: edit it in the Web UI, switch the
  **stream → tlsSettings** form to manual JSON, and set
  `certificateFile` / `keyFile` to the paths from step 3.
- **For the panel itself (HTTPS UI)**: use the **Upload Certificate**
  section in the same TLS panel — pick the two files from
  `/opt/zenithpanel/data/certs/` — then restart the panel.

Once the inbound (or panel) is using the real cert, drop the
`allowInsecure=1` from the subscription by removing `allowInsecure: true`
from that inbound's stream — operators are encouraged to flip this off
once a real cert is in place.

### Renewal

The panel's renewal goroutine ticks every 12 hours, scans
`/opt/zenithpanel/data/certs/` for `.crt` files, and any cert within
**30 days of expiry** gets re-issued using the `acme_email` setting
captured at first issuance. No manual action required.

---

## 4. Troubleshooting

| Symptom | What it usually means |
|---|---|
| `400 :: invalidContact :: contact email has forbidden domain` | Let's Encrypt rejects `example.com`/`example.org`/etc. addresses. Use a real mailbox you own. |
| `400 :: rejectedIdentifier :: …` | The domain doesn't resolve to this VPS yet. Fix DNS, wait 5 min, retry. |
| `connection refused on :80` | Firewall not open. `ufw allow 80/tcp` or `iptables` rule, then retry. |
| `acme: error: 429 :: urn:ietf:params:acme:error:rateLimited` | You've issued too many certs for the same domain. Wait per Let's Encrypt's [rate limits](https://letsencrypt.org/docs/rate-limits/). |
| Cert issued but clients still reject | Make sure the **inbound** is using `certificateFile`/`keyFile` (or `certificate_path`/`key_path` for native sing-box) pointing at the ACME paths, and re-apply proxy config. |
| Cert renewed but proxy hasn't picked it up | Sing-box reads cert files at start — you may need to `zenithctl proxy apply` after renewal. (Xray hot-reloads them on each connection.) |

---

## 5. End-to-end Sanity Checklist

When everything's right, you should see:

```bash
$ zenithctl --output table proxy status
FIELD                  VALUE
xray_running           ✓
singbox_running        ✓
dual_mode              ✓
…

$ zenithctl --output table proxy test all
ID  TAG            PROTOCOL     TRANSPORT    OK  STAGE  ELAPSED
1   …              vless        tcp+reality  ✓   -      1ms
…

$ curl -sf https://your-domain.example.com/api/v1/health
{"db":"ok","proxy":"running",…}
```

Then on the phone: open Clash Meta → tap the proxy group → tap a node
→ traffic flows through that protocol → the egress IP matches your
VPS public IP.
