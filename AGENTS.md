# ZenithPanel — Operator Guide for AI Agents

[简体中文](AGENTS_CN.md) | English

This file is the operations manual for **AI agents (Claude Code, Cursor,
Aider, etc.) and other automation** that drives a ZenithPanel install
end-to-end without a human in the loop. It assumes you have shell access
to a target VPS and can authenticate either over SSH or via an API token.

Human-facing docs:
- [docs/user_manual.md](docs/user_manual.md) — the manual for operators.
- [docs/qr_setup_guide.md](docs/qr_setup_guide.md) — QR-scan + cert walkthrough.
- [docs/cli_design.md](docs/cli_design.md) — `zenithctl` command tree.
- [docs/cli_api_spec.md](docs/cli_api_spec.md) — every HTTP endpoint
  this guide references. Read it when a sub-command doesn't behave the
  way you expect.

---

## 1. How to authenticate (pick exactly one path)

| Path | Use when | Auth | Caveats |
|---|---|---|---|
| **Unix socket** (`/run/zenithpanel.sock`) | You're SSH'd into the VPS as root and the panel is running locally | None — kernel FS perms gate it | Linux only; only root can `connect()` |
| **API token over HTTPS** | You're remote, or the panel is in a Docker container and you don't want to `docker exec` | `Authorization: Bearer ztk_…` | Issue with `zenithctl token bootstrap` once on the host; persisted in `~/.config/zenithctl/config.toml` |
| **JWT** | Driving the panel like a human-typed browser session | `Authorization: Bearer <jwt>` from `POST /api/v1/login` | 24h expiry; not recommended for unattended automation |

**Recommended for agents**: on first SSH into the host, run `zenithctl
token bootstrap` once. This mints a `ztk_…` token, writes
`~/.config/zenithctl/config.toml`, and from then on every `zenithctl`
command resolves the token automatically — no per-call flags. For cross-host
agents (e.g. CI), copy that token into `ZENITHCTL_TOKEN` / `ZENITHCTL_HOST`
env vars.

---

## 2. The 60-second smoke test

Drop these in before doing anything else. If any one fails, fix it first
or escalate — don't proceed.

```bash
# 1. Panel responding?
curl -sf http://127.0.0.1:31310/api/v1/health | jq .
# expect: {"db":"ok","proxy":"running"|"stopped",...}

# 2. CLI authenticated?
zenithctl status
# expect: {"code":200,...,"data":{"status":"ok",...}}

# 3. Engines + inbounds aligned?
zenithctl --output table proxy status
# expect: xray_running ✓, singbox_running ✓, dual_mode ✓ (if you have
# both Xray-style and QUIC inbounds)

# 4. Every inbound reachable from the host loopback?
zenithctl --output table proxy test all
# expect: all rows OK=✓ (column 5)
```

If `proxy test all` shows a row failing on stage `not_bound`, the engine
didn't actually bind that port — usually a config-generator error.
`stage=tcp` means kernel-level refuse (firewall on the host?). `stage=tls`
means cert/key mismatch — see §6.

---

## 3. Standard install on a fresh VPS

The panel ships as a Docker image. The install script does this for the
user, but if you're operating bare-metal:

```bash
docker run -d \
  --name zenithpanel \
  --restart always \
  --network host \
  -v /opt/zenithpanel/data:/opt/zenithpanel/data \
  -v /var/run/docker.sock:/var/run/docker.sock \
  ghcr.io/harveyxiacn/zenithpanel:main

# Find the setup wizard URL in the boot logs:
docker logs zenithpanel 2>&1 | grep -E 'one-time password|setup-'
```

The setup wizard is reachable at `http://<vps>:31310/zenith-setup-<random>`.
Visit it once, complete the form, then mint a CLI token:

```bash
ln -sf /opt/zenithpanel/zenithpanel /usr/local/bin/zenithctl
docker exec zenithpanel /opt/zenithpanel/zenithpanel ctl token bootstrap
# Copy the ztk_… token; it's persisted in /root/.config/zenithctl/config.toml
```

**Volume mount is non-negotiable**: `-v /opt/zenithpanel/data:...` is the
contract that makes OTA upgrades preserve everything. Skipping it loses
inbound rows, certs, audit logs on the first `docker rm`.

---

## 4. Setting up protocols via the API

The standard recipe is: **inbound + client + apply + cert (optional)**.

### 4.1 Create a VLESS+Reality inbound (no cert needed)

```bash
zenithctl raw POST /api/v1/proxy/generate-reality-keys
# Response data: {private_key, public_key, short_id}

zenithctl raw POST /api/v1/inbounds --data '{
  "tag": "vless-reality",
  "protocol": "vless",
  "port": 443,
  "network": "tcp",
  "server_address": "<vps-public-ip>",
  "settings": "{\"decryption\":\"none\",\"flow\":\"xtls-rprx-vision\"}",
  "stream": "{\"network\":\"tcp\",\"security\":\"reality\",\"realitySettings\":{\"show\":false,\"target\":\"www.microsoft.com:443\",\"serverNames\":[\"www.microsoft.com\"],\"privateKey\":\"<from-step-1>\",\"shortIds\":[\"<from-step-1>\"],\"settings\":{\"publicKey\":\"<from-step-1>\",\"fingerprint\":\"chrome\"}}}",
  "enable": true
}'

zenithctl client add --inbound 1 --email user1
zenithctl proxy apply

# Open the port at the host firewall:
zenithctl raw POST /api/v1/firewall/rules --data \
  '{"port":"443","protocol":"tcp","action":"ACCEPT"}'

# Grab the share URL / QR
zenithctl raw GET /api/v1/clients | jq '.[0].uuid'
curl -s http://127.0.0.1:31310/api/v1/sub/<that-uuid> | base64 -d
```

### 4.2 Create a Hysteria2 inbound (needs a real TLS cert)

Issue the cert first (see §6), then:

```bash
zenithctl raw POST /api/v1/inbounds --data '{
  "tag": "hysteria2",
  "protocol": "hysteria2",
  "port": 8443,
  "network": "udp",
  "server_address": "<vps-public-ip>",
  "settings": "{\"obfs\":{\"type\":\"salamander\",\"password\":\"<random>\"},\"up_mbps\":100,\"down_mbps\":100}",
  "stream": "{\"network\":\"udp\",\"security\":\"tls\",\"tlsSettings\":{\"serverName\":\"<your-domain>\",\"alpn\":[\"h3\"],\"certificates\":[{\"certificateFile\":\"/opt/zenithpanel/data/certs/<your-domain>.crt\",\"keyFile\":\"/opt/zenithpanel/data/certs/<your-domain>.key\"}]}}",
  "enable": true
}'
```

### 4.3 Protocol-engine assignment (dual mode is default)

| Protocol | Runs on |
|---|---|
| VLESS / VMess / Trojan / Shadowsocks | Xray |
| Hysteria2 / TUIC | Sing-box |

`POST /api/v1/proxy/apply` (no `engine=` param) partitions enabled inbounds
across both engines. Each engine binds disjoint ports. Force a single
engine with `?engine=xray` or `?engine=singbox` only for diagnostic
reasons.

### 4.4 One-click recipes (agent should map user request → snippet)

When a user says *"set up VLESS for me"* or *"give me all 6 protocols
with one user each"*, drop in the matching snippet below. Each is
idempotent on re-run (existing tag → 409, treat as already-done).

> **Pre-flight every recipe assumes**:
> - `PUBLIC_IP` and `DOMAIN` env vars are set (use the VPS IP for
>   `PUBLIC_IP`; for `DOMAIN`, either a real domain pointed at the IP,
>   or `<dashed-ip>.nip.io` as a fallback — see §6).
> - `CERT=/opt/zenithpanel/data/certs/$DOMAIN.crt` and
>   `KEY=/opt/zenithpanel/data/certs/$DOMAIN.key` exist (run
>   `zenithctl cert issue` first for TLS-using protocols).
> - `zenithctl` is authenticated (token bootstrapped).

#### A. VLESS + Reality (no cert needed)

```bash
KEYS=$(zenithctl -q raw POST /api/v1/proxy/generate-reality-keys)
PK=$(jq -r '.data.private_key' <<<"$KEYS")
PUB=$(jq -r '.data.public_key' <<<"$KEYS")
SID=$(jq -r '.data.short_id' <<<"$KEYS")

zenithctl raw POST /api/v1/inbounds --data "$(jq -nc \
  --arg ip "$PUBLIC_IP" --arg pk "$PK" --arg pub "$PUB" --arg sid "$SID" '{
  tag:"vless-reality", protocol:"vless", port:443, network:"tcp",
  server_address:$ip,
  settings:({decryption:"none",flow:"xtls-rprx-vision"}|tostring),
  stream:({network:"tcp",security:"reality",realitySettings:{
    target:"www.microsoft.com:443",
    serverNames:["www.microsoft.com"],
    privateKey:$pk, shortIds:[$sid],
    settings:{publicKey:$pub,fingerprint:"chrome"}
  }}|tostring),
  enable:true}')"

INBOUND_ID=$(zenithctl -q raw GET /api/v1/inbounds | jq '[.[]|select(.tag=="vless-reality")][0].id')
zenithctl client add --inbound "$INBOUND_ID" --email user1
zenithctl raw POST /api/v1/firewall/rules --data '{"port":"443","protocol":"tcp","action":"ACCEPT"}'
zenithctl proxy apply
```

#### B. VMess + WS + TLS (needs cert)

```bash
zenithctl raw POST /api/v1/inbounds --data "$(jq -nc \
  --arg ip "$PUBLIC_IP" --arg dom "$DOMAIN" --arg cert "$CERT" --arg key "$KEY" '{
  tag:"vmess-ws", protocol:"vmess", port:31402, network:"ws",
  server_address:$ip,
  settings:({clients:[]}|tostring),
  stream:({network:"ws",security:"tls",
    wsSettings:{path:"/vmess"},
    tlsSettings:{serverName:$dom,certificates:[{certificateFile:$cert,keyFile:$key}]}
  }|tostring),
  enable:true}')"

INBOUND_ID=$(zenithctl -q raw GET /api/v1/inbounds | jq '[.[]|select(.tag=="vmess-ws")][0].id')
zenithctl client add --inbound "$INBOUND_ID" --email user1
zenithctl raw POST /api/v1/firewall/rules --data '{"port":"31402","protocol":"tcp","action":"ACCEPT"}'
zenithctl proxy apply
```

#### C. Trojan + TLS (needs cert)

```bash
PW="trojan-$(openssl rand -hex 8)"
zenithctl raw POST /api/v1/inbounds --data "$(jq -nc \
  --arg ip "$PUBLIC_IP" --arg dom "$DOMAIN" --arg cert "$CERT" --arg key "$KEY" --arg pw "$PW" '{
  tag:"trojan-tls", protocol:"trojan", port:31403, network:"tcp",
  server_address:$ip,
  settings:({clients:[{email:"trojan-user",password:$pw}]}|tostring),
  stream:({network:"tcp",security:"tls",
    tlsSettings:{serverName:$dom,certificates:[{certificateFile:$cert,keyFile:$key}]}
  }|tostring),
  enable:true}')"

# Trojan password equals the client UUID in the panel's model — write it
# explicitly so subscription URLs round-trip.
INBOUND_ID=$(zenithctl -q raw GET /api/v1/inbounds | jq '[.[]|select(.tag=="trojan-tls")][0].id')
zenithctl client add --inbound "$INBOUND_ID" --email trojan-user --uuid "$PW"
zenithctl raw POST /api/v1/firewall/rules --data '{"port":"31403","protocol":"tcp","action":"ACCEPT"}'
zenithctl proxy apply
```

#### D. Shadowsocks-2022 (no cert needed)

```bash
SERVER_PSK=$(openssl rand -base64 16)
USER_PSK=$(openssl rand -base64 16)

zenithctl raw POST /api/v1/inbounds --data "$(jq -nc \
  --arg ip "$PUBLIC_IP" --arg spsk "$SERVER_PSK" --arg upsk "$USER_PSK" '{
  tag:"ss-2022", protocol:"shadowsocks", port:31404, network:"tcp",
  server_address:$ip,
  settings:({method:"2022-blake3-aes-128-gcm",password:$spsk}|tostring),
  stream:({network:"tcp"}|tostring),
  enable:true}')"

INBOUND_ID=$(zenithctl -q raw GET /api/v1/inbounds | jq '[.[]|select(.tag=="ss-2022")][0].id')
zenithctl client add --inbound "$INBOUND_ID" --email ss-user --uuid "$USER_PSK"
zenithctl raw POST /api/v1/firewall/rules --data '{"port":"31404","protocol":"tcp","action":"ACCEPT"}'
zenithctl proxy apply
```

#### E. Hysteria2 (needs cert; UDP)

```bash
OBFS_PW=$(openssl rand -hex 16)
zenithctl raw POST /api/v1/inbounds --data "$(jq -nc \
  --arg ip "$PUBLIC_IP" --arg dom "$DOMAIN" --arg cert "$CERT" --arg key "$KEY" --arg obfs "$OBFS_PW" '{
  tag:"hysteria2", protocol:"hysteria2", port:8443, network:"udp",
  server_address:$ip,
  settings:({obfs:{type:"salamander",password:$obfs},up_mbps:100,down_mbps:100}|tostring),
  stream:({network:"udp",security:"tls",
    tlsSettings:{serverName:$dom,alpn:["h3"],certificates:[{certificateFile:$cert,keyFile:$key}]}
  }|tostring),
  enable:true}')"

INBOUND_ID=$(zenithctl -q raw GET /api/v1/inbounds | jq '[.[]|select(.tag=="hysteria2")][0].id')
zenithctl client add --inbound "$INBOUND_ID" --email hy2-user
zenithctl raw POST /api/v1/firewall/rules --data '{"port":"8443","protocol":"udp","action":"ACCEPT"}'
zenithctl proxy apply
```

#### F. TUIC v5 (needs cert; UDP)

```bash
zenithctl raw POST /api/v1/inbounds --data "$(jq -nc \
  --arg ip "$PUBLIC_IP" --arg dom "$DOMAIN" --arg cert "$CERT" --arg key "$KEY" '{
  tag:"tuic-v5", protocol:"tuic", port:31406, network:"udp",
  server_address:$ip,
  settings:({congestion_control:"bbr"}|tostring),
  stream:({network:"udp",security:"tls",
    tlsSettings:{serverName:$dom,alpn:["h3"],certificates:[{certificateFile:$cert,keyFile:$key}]}
  }|tostring),
  enable:true}')"

INBOUND_ID=$(zenithctl -q raw GET /api/v1/inbounds | jq '[.[]|select(.tag=="tuic-v5")][0].id')
zenithctl client add --inbound "$INBOUND_ID" --email tuic-user
zenithctl raw POST /api/v1/firewall/rules --data '{"port":"31406","protocol":"udp","action":"ACCEPT"}'
zenithctl proxy apply
```

#### Z. All-in-one: every protocol, one user per protocol

```bash
# Prereqs (run these first):
#   export PUBLIC_IP=<vps ip>
#   export DOMAIN=<your domain or <dashed-ip>.nip.io>
#   zenithctl cert issue --domain "$DOMAIN" --email <you@example.com>
#   export CERT=/opt/zenithpanel/data/certs/$DOMAIN.crt
#   export KEY=/opt/zenithpanel/data/certs/$DOMAIN.key

# Then run recipes A → F in sequence. Or grab the canned helper:
bash scripts/agent_seed_all_protocols.sh
```

The helper script is in `scripts/agent_seed_all_protocols.sh` — see §13.
It runs A–F serially, surfaces each step's result, and verifies the
final state with `proxy test all` (must return 6/6 OK).

### 4.5 What "one user" means here

The panel models a *user* as a `Client` row tied to one `Inbound`. The
panel doesn't have a "global user that owns multiple inbounds" concept
— each (inbound, email) pair is its own row. The all-in-one recipe
creates 6 separate Client rows that happen to share an email-like
suffix, not a single Client across 6 inbounds. Subscription URLs are
per-client, so the user will scan 6 QR codes (one per protocol) and pick
between them in Clash Meta.

### 4.7 Common protocol gotchas (real bugs we've hit)

- **TUIC password = UUID**. Don't try to give TUIC users a separate
  password from settings.clients[].password — the share URL emits `UUID:UUID`,
  so the server must too. The panel enforces this.
- **SS-2022 multi-user password is `serverPSK:userPSK`** (joined by `:`).
  The panel's sub generator does this; if you build share URLs by hand,
  remember to concat.
- **Hysteria2 / TUIC need `alpn: ["h3"]`** in their TLS settings. The
  panel defaults this if omitted, but if you pass an empty/wrong ALPN
  the clients fail to handshake with `tls: server did not select an ALPN`.
- **`geoip:private`** in a routing rule will fail — the SagerNet repo
  doesn't ship a `geoip-private.srs`. The panel rewrites it to
  sing-box's built-in `ip_is_private: true` attribute automatically.

---

## 5. Changing inbound ports

```bash
# Atomic: GET → PUT → proxy apply, optionally UFW-open the new port:
zenithctl inbound set-port <id> <new-port> --sync-firewall

# After:
zenithctl --output table inbound list  # verify the new port
ufw status                              # the new port should show ALLOW
# Old port left in UFW; remove manually after confirming no one else uses it:
ufw delete allow <old>/tcp              # (or /udp for hy2/tuic)
```

The panel rejects port collisions at save time — two enabled inbounds
can't share a port. The CLI surfaces that as exit code 2.

---

## 6. ACME / Let's Encrypt (real certs, end-to-end)

### Prerequisites

- A domain (or `<dashed-ip>.nip.io` / `<dashed-ip>.sslip.io`) that
  resolves to the VPS public IP.
- Port **80/tcp open on the host firewall**.
- The panel's own webserver may be bound to :80 if you have any Sites
  configured — the cert package handles handing off the port via a
  `PortBouncer` interface, so you don't have to stop anything manually.

### Issue (one shot)

```bash
zenithctl cert issue --domain panel.example.com --email you@example.com
# On success: stdout shows cert_path, key_path, not_after Unix ts.
```

The cert lands at `/opt/zenithpanel/data/certs/<domain>.{crt,key}`. The
panel persists `acme_email` so the **12-hour background renewer** can
re-issue at <30 days remaining without re-prompting.

### Wire the cert into an inbound

Edit the inbound and replace `tlsSettings.certificates[0].certificateFile`
and `keyFile` with the new paths. Then drop `allowInsecure` if it's set.

```bash
# Example helper script: see scripts/rabisu_switch_to_real_cert.py for
# a reference impl that updates all TLS inbounds in one pass.
```

### Wire the cert into the panel HTTPS UI (optional)

```bash
curl -X POST http://127.0.0.1:31310/api/v1/admin/tls/upload \
  -H "Authorization: Bearer $ZENITHCTL_TOKEN" \
  -F cert=@/opt/zenithpanel/data/certs/<domain>.crt \
  -F key=@/opt/zenithpanel/data/certs/<domain>.key
# Restart panel after upload (`docker restart zenithpanel`) so it picks
# up the new cert and starts serving HTTPS on its own port.
```

### Renewal evidence

```bash
zenithctl raw GET /api/v1/metrics | grep cert
# (no built-in cert metric yet, but uptime + last_apply give you the gist)

# Direct: parse the cert on disk
openssl x509 -in /opt/zenithpanel/data/certs/<domain>.crt -noout -dates
```

---

## 7. OTA upgrades preserve operator state

The updater (`POST /api/v1/system/update/apply` or **Settings → Panel
Update**) re-uses the **old container's `HostConfig`** — specifically
`Binds` (the volume mount), `Env`, `NetworkMode`. Therefore:

| State | Survives OTA? |
|---|---|
| Inbound port, protocol, settings, clients | ✅ |
| API tokens, audit log, routing rules, AdBlock toggle | ✅ |
| ACME certs + `acme_email` for the renewer | ✅ |
| Host firewall (UFW) rules | ✅ (host-level, not in container) |
| Docker image's CMD/Entrypoint | ❌ — replaced by the new image |

A change you made through the Web UI or CLI in v1 will still be there in
v2 without re-migration. The single way this breaks: someone deployed
the original container without `-v /opt/zenithpanel/data:...`.

---

## 8. Observability for unattended operation

### Health (unauthenticated)

```bash
curl -sf http://<vps>:31310/api/v1/health | jq .
# {db, proxy, status, xray_uptime_seconds, singbox_uptime_seconds, last_apply_unix, ...}
```

Use this for liveness probes, dashboards, alerting.

### Prometheus metrics (authenticated)

```bash
curl -sH "Authorization: Bearer $TOKEN" http://<vps>:31310/api/v1/metrics
```

Scrape this with Prometheus. Useful queries:
- `zenithpanel_xray_running == 0 or zenithpanel_singbox_running == 0` →
  engine flapped, alert.
- `rate(zenithpanel_client_traffic_bytes[5m])` → bandwidth per client.
- `time() - zenithpanel_last_apply_unix > 86400` → config stale.
  (`last_apply_unix` is a `/health` field, not yet a metric; emit it
  yourself from a scrape script if you need it.)

### Audit log

```bash
zenithctl raw GET /api/v1/admin/audit-log
# Returns the most recent 50 entries. Adjust with ?limit=200&offset=N.
```

The retention sweeper auto-prunes rows older than 90 days (configurable
via `audit_retention_days` setting). Don't write your own pruner.

---

## 9. When something is broken

In rough decision-tree order:

1. **CLI 401** → token revoked or expired. Re-bootstrap:
   `docker exec zenithpanel /opt/zenithpanel/zenithpanel ctl token bootstrap`.
2. **`proxy apply` returns 500** → look at `proxy status`'s
   `xray_last_error` / `singbox_last_error` field, then at
   `docker logs zenithpanel | tail -50`. Usually a malformed inbound
   stream JSON.
3. **Sing-box won't start with `unexpected status: 404 Not Found`** →
   the routing rule references a `geosite:` or `geoip:` tag SagerNet
   doesn't ship. Special-case `geoip:private` aside, you may have
   typo'd a non-existent category. List valid tags at
   `https://github.com/SagerNet/sing-geosite/tree/rule-set`.
4. **Cert renewal fails with `429 rateLimited`** → Let's Encrypt's
   per-registered-domain weekly limit. Wait it out or switch to a
   different IP-as-domain provider (sslip.io ↔ nip.io).
5. **`proxy test` says `not_bound`** → run `proxy apply`; if still
   not_bound, the engine crashed at start. `docker logs` will show why.
6. **Client connects but no traffic flows** → check the AdBlock
   `geosite:category-ads-all → block` rule didn't catch a domain you
   need; toggle AdBlock off and retest.
7. **OTA upgrade rolled back inbound ports** → confirms the volume
   mount was missing in the original deployment. The data is gone;
   restore from `zenithctl backup export`.

---

## 10. Self-restraint rules (please follow)

- **Don't run `docker rm zenithpanel`** without first taking
  `zenithctl backup export --out /tmp/backup.zip`.
- **Don't disable `Block Private IP`** (rule id 2 by default) — it
  prevents proxied users from probing the VPS's internal services.
- **Don't bypass the `validateInbound` port-uniqueness check** by
  writing to the DB directly. The panel relies on it.
- **Don't issue ACME certs to test domains** like `example.com` —
  Let's Encrypt blacklists them and you'll burn a 5/week issuance quota
  on the failures.
- **Don't store the bootstrap token in code or environment variables
  in version-controlled files.** Use `~/.config/zenithctl/config.toml`
  (mode 0600) or a secret manager.

---

## 11. Useful one-liners

```bash
# Snapshot everything an agent should know about a panel
zenithctl --output table proxy status
zenithctl --output table inbound list
zenithctl --output table client list
zenithctl --output table token list
zenithctl --output table proxy test all
curl -sf http://127.0.0.1:31310/api/v1/health | jq .

# Mint a scoped token for a downstream CI runner
zenithctl token create --name ci-2026-q2 --scopes 'read,write,proxy:apply' --expires-in 90

# Bulk-test every protocol from outside the panel (real client perspective)
bash scripts/proto_sweep_dual.sh  # only on the panel host

# Wipe and re-seed inbounds (DANGER — preserves nothing except certs)
zenithctl backup export --out /tmp/before.zip
zenithctl backup restore --file /path/to/fresh-seed.zip
```

---

## 12. Reading order when you're new

1. This file (top to bottom).
2. [`docs/cli_design.md`](docs/cli_design.md) — command tree + flags.
3. [`docs/cli_api_spec.md`](docs/cli_api_spec.md) — endpoint contracts.
4. [`docs/qr_setup_guide.md`](docs/qr_setup_guide.md) — production
   walkthrough you'll likely guide a human through.
5. [`docs/protocol_connectivity_report.md`](docs/protocol_connectivity_report.md)
   — what "works" looks like.

Stay current with [`CHANGELOG.md`](CHANGELOG.md). New endpoints land
there before they land in `cli_api_spec.md`.

---

## 13. Reference scripts that match this guide

These files in `scripts/` are versioned in the repo and stay in lockstep
with the recipes in §4.4. Prefer running them over reproducing the JSON
in chat — they're easier to maintain.

| Script | Purpose | Section |
|---|---|---|
| `scripts/agent_seed_all_protocols.sh` | Seeds all 6 protocols + one client each, opens UFW, applies, verifies with `proxy test all`. Idempotent. | §4.4.Z |
| `scripts/rabisu_fix_inbounds.py` | Reference: patch every inbound's `server_address` + `allowInsecure` for an existing install. | §6 |
| `scripts/rabisu_switch_to_real_cert.py` | Reference: switch every TLS inbound from self-signed to an ACME-issued cert in one pass. | §6 |
| `scripts/proto_sweep_dual.sh` | End-to-end real-curl test: spins one sing-box client per protocol, curls google through each. Use to verify a seed worked from a real client's perspective. | §2 |

Read them once, especially `agent_seed_all_protocols.sh` — its
`post_inbound` / `add_client` / `open_fw` helpers are good templates
for any new recipe you write.
