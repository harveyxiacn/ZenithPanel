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

Server-side probe of a single inbound. The server resolves the inbound's
`(host, port, protocol, transport, tls)` tuple from the DB, then performs a
connect + protocol handshake from inside the panel process. Used by
`zenithctl proxy test` and by the protocol-connectivity regression sweep
described in §6.

Response:
```json
{
  "code": 200, "msg": "ok",
  "data": {
    "inbound_id": 3,
    "protocol": "vless",
    "transport": "tcp+reality",
    "ok": true,
    "elapsed_ms": 87,
    "handshake": "reality:ok",
    "warnings": []
  }
}
```

On failure, `ok=false` and `handshake` carries the failing stage
(`tcp`, `tls`, `reality`, `vmess`, `trojan`, `ss`, `hy2`, `tuic`).

## 3. CLI ↔ HTTP Mapping

| CLI command                               | HTTP                                              |
|---                                        |---                                                |
| `status`                                  | `GET /health`                                     |
| `login`                                   | `POST /login` (caches token in `~/.config`)       |
| `token list`                              | `GET /admin/api-tokens`                           |
| `token create --name x --scopes s`        | `POST /admin/api-tokens`                          |
| `token revoke <id>`                       | `DELETE /admin/api-tokens/:id`                    |
| `token bootstrap`                         | `POST /admin/api-tokens/bootstrap` (unix only)    |
| `system info`                             | `GET /system/monitor`                             |
| `system bbr status`                       | `GET /system/bbr/status`                          |
| `system bbr enable`                       | `POST /system/bbr/enable`                         |
| `system swap create --size 1G`            | `POST /system/swap/create`                        |
| `inbound list`                            | `GET /inbounds`                                   |
| `inbound show <id>`                       | `GET /inbounds` (client-side filter)              |
| `inbound create --file f.json`            | `POST /inbounds`                                  |
| `inbound update <id> --file f.json`       | `PUT /inbounds/:id`                               |
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
