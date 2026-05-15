# ZenithPanel CLI / API Spec

> Companion to [cli_design.md](cli_design.md). This document is the binding
> contract between the CLI implementation, the backend, and any third-party
> automation.

All endpoints are rooted at `/api/v1`. All bodies are JSON unless noted.
Success envelope is the existing `{"code":200,"msg":"...","data": ...}` shape;
errors are `{"code":<http-status>,"msg":"..."}` with the same HTTP status code.

## 1. Auth Headers

| Header                          | Required for                              |
|---                              |---                                        |
| `Authorization: Bearer <JWT>`   | Browser sessions (unchanged)              |
| `Authorization: Bearer ztk_…`   | CLI / automation via TCP listener         |
| *(none)*                        | Requests arriving on the Unix socket only |

Unauthenticated TCP requests get `401 {"code":401,"msg":"Authorization required"}`.

## 2. New Endpoints

### 2.1 `POST /api/v1/admin/api-tokens`

Create a new API token. Requires `admin` scope (or `local-root`).

Request:
```json
{
  "name": "ci-runner",
  "scopes": "read,write,proxy:apply",
  "expires_in_days": 90
}
```

Fields:
- `name` *(string, required)* — unique, 1-64 chars, `[a-zA-Z0-9_.-]+`.
- `scopes` *(string, optional)* — comma-separated scopes; default `*`.
- `expires_in_days` *(int, optional)* — `0` or omitted = no expiry.

Response `200`:
```json
{
  "code": 200,
  "msg": "Token created. Copy it now — it will not be shown again.",
  "data": {
    "id": 7,
    "name": "ci-runner",
    "scopes": "read,write,proxy:apply",
    "expires_at": 1747526400,
    "token": "ztk_AbCd…_4f9b21"
  }
}
```

Error `409` if `name` is already taken (case-insensitive).

### 2.2 `GET /api/v1/admin/api-tokens`

List tokens (hashes redacted). Requires `admin` scope.

Response:
```json
{
  "code": 200, "msg": "ok",
  "data": [
    {"id":7,"name":"ci-runner","scopes":"read,write","expires_at":1747526400,
     "last_used_at":1747000000,"revoked":false,"created_at":"2026-05-14T01:02:03Z"}
  ]
}
```

### 2.3 `DELETE /api/v1/admin/api-tokens/:id`

Revoke a token. Idempotent. Requires `admin` scope. Sets `revoked=true` but
keeps the row for audit.

Response `200 {"code":200,"msg":"revoked"}`.

### 2.4 `POST /api/v1/admin/api-tokens/bootstrap`

Reachable **only** when the request arrives on the Unix socket
(`trusted_local=true`). Creates a token named `local-root-<unix-ts>` with
scopes `*` and no expiry, returning the plaintext exactly like 2.1.

Used by `zenithctl token bootstrap` so root on the VPS can self-issue a
durable credential without needing the password.

### 2.5 `GET /api/v1/proxy/test/:inbound_id`

Server-side probe of a single inbound. Reads `/proc/net/{tcp,udp}{,6}` to
confirm the listener is bound, then performs a TCP dial (+ TLS handshake
where applicable) or a UDP sentinel send from inside the panel process.

Response:
```json
{
  "code": 200, "msg": "ok",
  "data": {
    "inbound_id": 3,
    "tag": "vless-reality",
    "protocol": "vless",
    "transport": "tcp+reality",
    "port": 443,
    "ok": true,
    "elapsed_ms": 1,
    "stage": "",
    "err": ""
  }
}
```

On failure, `ok=false` and `stage` carries one of `not_bound | tcp | tls |
udp` plus a free-form `err`. CLI uses this for `zenithctl proxy test <id>`
and `zenithctl proxy test all` (iterates every enabled inbound, exits
non-zero on any failure).

### 2.6 `POST /api/v1/proxy/tls/issue`

Issues a Let's Encrypt cert via lego's HTTP-01 challenge. Requires port 80
to be reachable. The panel's vhost webserver yields :80 to lego briefly
through the `PortBouncer` hook in `service/cert`, so the existing webserver
doesn't have to be torn down by hand.

Request:
```json
{ "domain": "panel.example.com", "email": "you@example.com" }
```

Response (`200`):
```json
{
  "code": 200,
  "msg": "Certificate issued. Wire the paths into an inbound's tlsSettings or panel TLS upload to put it into use.",
  "data": {
    "domain": "panel.example.com",
    "cert_path": "/opt/zenithpanel/data/certs/panel.example.com.crt",
    "key_path": "/opt/zenithpanel/data/certs/panel.example.com.key",
    "not_after": 1786601525
  }
}
```

Side effect: `acme_email` is persisted in `Settings` so the 12-hour
background renewer can re-issue at <30 days remaining without re-prompting.

### 2.7 `GET / PUT /api/v1/admin/adblock`

Toggle the panel-managed Block Ads routing rule. Both require `admin` scope.

`GET` response:
```json
{ "code": 200, "msg": "ok", "data": { "enabled": false } }
```

`PUT` request: `{"enabled": true}`. Response: same shape as GET, plus
`msg: "Ad-block true|false"`. Side effects:
- Inserts/removes a routing-rule row with `rule_tag: "Block Ads
  (panel-managed)"` and `domain: geosite:category-ads-all → block`.
- Restarts both engines in parallel (dual mode) so the new route table
  takes effect immediately.

### 2.8 `GET /api/v1/metrics`

Prometheus text-format scrape endpoint. Authenticated like every other
`/api/v1` route — mint a read-only API token and configure scrape with
`bearer_token`. Exposes:

- `zenithpanel_uptime_seconds` (gauge)
- `zenithpanel_xray_running` / `_singbox_running` / `_dual_mode` (0/1)
- `zenithpanel_xray_uptime_seconds` / `_singbox_uptime_seconds` (gauge)
- `zenithpanel_enabled_inbounds` / `_clients` / `_rules` (gauge)
- `zenithpanel_handed_off_singbox` (gauge, dual-mode partition size)
- `zenithpanel_api_tokens{state="active"|"revoked"}` (gauge)
- `zenithpanel_client_traffic_bytes{email,inbound,direction}` (counter)

## 3. CLI ↔ HTTP Mapping

| CLI command                               | HTTP                                              |
|---                                        |---                                                |
| `status`                                  | `GET /health`                                     |
| `login`                                   | `POST /login` (caches token in `~/.config`)       |
| `token list`                              | `GET /admin/api-tokens`                           |
| `token create --name x --scopes s`        | `POST /admin/api-tokens`                          |
| `token revoke <id>`                       | `DELETE /admin/api-tokens/:id`                    |
| `token rotate <name>`                     | list → POST + DELETE; updates active profile     |
| `token bootstrap`                         | `POST /admin/api-tokens/bootstrap` (unix only)    |
| `system info`                             | `GET /system/monitor`                             |
| `system bbr status`                       | `GET /system/bbr/status`                          |
| `system bbr enable`                       | `POST /system/bbr/enable`                         |
| `system swap create --size 1G`            | `POST /system/swap/create`                        |
| `inbound list`                            | `GET /inbounds`                                   |
| `inbound show <id>`                       | `GET /inbounds` (client-side filter)              |
| `inbound create --file f.json`            | `POST /inbounds`                                  |
| `inbound update <id> --file f.json`       | `PUT /inbounds/:id`                               |
| `inbound set-port <id> <port>`            | list → PUT + optional firewall POST + apply       |
| `inbound delete <id>`                     | `DELETE /inbounds/:id`                            |
| `client list [--inbound id]`              | `GET /clients`                                    |
| `client add --inbound <id> --email e`     | `POST /clients`                                   |
| `client delete <id>`                      | `DELETE /clients/:id`                             |
| `proxy status`                            | `GET /proxy/status`                               |
| `proxy apply`                             | `POST /proxy/apply`                               |
| `proxy config xray`                       | `GET /proxy/config/xray`                          |
| `proxy config singbox`                    | `GET /proxy/config/singbox`                       |
| `proxy reality-keys`                      | `POST /proxy/generate-reality-keys`               |
| `proxy test <id>`                         | `GET /proxy/test/:inbound_id`                     |
| `proxy test all`                          | list + per-row `GET /proxy/test/:id`              |
| `cert issue --domain X --email Y`         | `POST /proxy/tls/issue`                           |
| `sub url <uuid>`                          | builds `https://<host>/api/v1/sub/<uuid>`         |
| `firewall list`                           | `GET /firewall/rules`                             |
| `firewall add --port 443 --proto tcp`     | `POST /firewall/rules`                            |
| `firewall delete <line>`                  | `DELETE /firewall/rules?line=N`                   |
| `backup export`                           | `GET /admin/backup/export`                        |
| `backup restore --file f.zip`             | `POST /admin/backup/restore`                      |
| `raw <METHOD> <PATH>`                     | passthrough                                       |

## 4. CLI Config File

`$XDG_CONFIG_HOME/zenithctl/config.toml` (or `~/.config/zenithctl/config.toml`):

```toml
# default profile is the one zenithctl picks if --profile is not given
default = "local"

[profile.local]
host    = "unix:///run/zenithpanel.sock"
# token omitted on purpose — unix socket is trusted

[profile.prod]
host    = "https://panel.example.com"
token   = "ztk_…"
verify_tls = true
```

Permissions: file is `0600`. `zenithctl token bootstrap` and
`zenithctl login` write to it.

## 5. Output Formats

- `--output json` (default in non-TTY): newline-terminated JSON object.
- `--output table` (default in TTY): aligned ASCII with column headers.
- `--output yaml`: reserved; not in first cut.
- `--quiet/-q`: only print value of `data` for `GET` calls and exit 0/non-0
  by HTTP status — script-friendly.

Exit codes: `0` success; `1` generic CLI failure; `2` HTTP 4xx (client error);
`3` HTTP 5xx (server error); `4` transport failure (DNS/refused/timeout).

## 6. Protocol Connectivity Sweep

The CLI ships a hidden command, `zenithctl proxy test-all`, that:

1. lists every enabled inbound,
2. calls `GET /proxy/test/:id` for each,
3. exits non-zero if any inbound fails,
4. emits a table like:

```
TAG          PROTOCOL  TRANSPORT          OK  STAGE       ELAPSED
reality-1    vless     tcp+reality        ✓               73ms
vmess-ws-1   vmess     ws                 ✗   tls         210ms
trojan-1     trojan    tcp+tls            ✓               66ms
ss-2022      ss        tcp                ✓               41ms
hy2          hysteria2 udp                ✓               54ms
tuic-v5      tuic      udp+tls            ✗   tuic        180ms
```

This is the contract behind task #10 (VPS protocol connectivity test).

## 7. Versioning

The API stays at `/api/v1`. New endpoints in this document are additive.
Breaking changes would bump to `/api/v2`; CLI honors a top-level header
`X-Zenith-Api: v1` so a future server can refuse incompatible clients
cleanly.
