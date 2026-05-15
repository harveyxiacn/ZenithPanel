# ZenithPanel CLI / Headless API Design

[简体中文](cli_design_CN.md) | English

> Status: Design — implementation tracked in [plans/](plans/) and `task.md`.
> Audience: Maintainers, automation users, Claude Code agents that drive a panel after SSHing into the host.

## 1. Motivation

ZenithPanel today is entirely Web-UI driven. There is no first-class way for a
human or an agent to manage the panel from a shell. Concrete pain points:

- **Operator workflow** — when SSHed into the VPS, a maintainer wants to add a
  client, reload the proxy, or check status without opening a browser tab.
- **Headless automation** — CI pipelines, cron jobs, ansible-like remote
  runners, and AI agents (Claude Code) need a deterministic interface that does
  not depend on a TUI session.
- **Disaster recovery** — if TLS for the panel UI is broken or the panel's
  random port was lost, the operator still needs an in-host escape hatch.

The design adds a single new CLI surface (`zenithpanel ctl …`, also exposed as
the symlink `zenithctl`) and a small set of new HTTP endpoints. No existing
Web-UI behavior is changed.

## 2. Design Goals

1. **Single binary** — the CLI is a subcommand of the existing `zenithpanel`
   binary; no extra artifact to distribute or upgrade.
2. **Zero-friction on host** — root on the same VPS can run any command with no
   login flow.
3. **Safe-by-default for remote** — cross-host use requires an explicit,
   revocable, scoped API token; never a long-lived JWT.
4. **No new business logic** — CLI commands map 1:1 onto existing HTTP handlers
   wherever possible. The CLI is a thin client.
5. **Auditable** — every CLI invocation that mutates state lands in `AuditLog`
   with the principal (`token:<name>` or `local-root`).
6. **Composable** — JSON output by default for scripts; human table output via
   `--output table` when stdout is a TTY.

## 3. Architecture

```
                  ┌──────────────────────────────────────────────┐
                  │             zenithpanel binary               │
                  │                                              │
   argv[1]=ctl ──▶│  cli.Run()  ── reads ~/.config/zenithctl ─┐  │
                  │      │                                    │  │
                  │      ▼                                    │  │
                  │  HTTP client ── chooses transport ────────┤  │
                  │                                           │  │
                  │   ┌─── unix:///run/zenithpanel.sock ──────┼──┼──▶ same gin engine
                  │   └─── https://panel.example/api/v1 ─────┘  │     (trusted_local OR
                  │                                              │      bearer-token)
   no argv[1] ──▶│  server.Run()  → listens on TCP + unix sock │
                  └──────────────────────────────────────────────┘
```

- **Two listeners, one engine.** `main.go` creates the gin router once and
  attaches it to both an `http.Server` (TCP) and a second `http.Server` whose
  `Listener` is a `net.UnixListener` on `/run/zenithpanel.sock`, owned by
  `root:root` mode `0600`. The unix listener is wrapped in a middleware that
  marks `c.Set("trusted_local", true)`.
- **CLI mode.** When `argv[1] == "ctl"`, `main.go` shortcuts to `cli.Run(argv)`
  without touching DB, scheduler, or proxy managers. The CLI never imports
  service packages; it only talks HTTP.
- **Transport selection.** The CLI checks (in order):
  1. `--host` / `--socket` flag,
  2. `$ZENITHCTL_HOST`,
  3. `~/.config/zenithctl/config.toml`,
  4. default `/run/zenithpanel.sock` if the file exists and is reachable,
  5. otherwise error with a clear "run `zenithctl token bootstrap` or pass
     `--host`" message.

## 4. Authentication Model

Three principal types coexist:

| Principal      | How established                          | Auth scheme on request          | Audit name        |
|---             |---                                       |---                              |---                |
| `local-root`   | Connection arrived on the unix socket    | none (socket FS perms gate it)  | `local-root`      |
| `token:<name>` | API token issued by an admin             | `Authorization: Bearer ztk_…`   | `token:<name>`    |
| `admin:<u>`    | Browser session                          | `Authorization: Bearer <JWT>`   | `admin:<u>`       |

### 4.1 API Token Format

```
ztk_<22-char-base64url(random 16 bytes)>_<6-char-checksum>
```

Generated server-side with `crypto/rand`. The plaintext token is returned **once**
on creation. Only `sha256(token)` is stored in `api_tokens`. The 6-char checksum
is a `crc32` of the random body, lets the CLI fail fast if the user mistypes.

### 4.2 Schema

```go
type ApiToken struct {
    ID         uint
    Name       string    // unique, user-chosen label
    TokenHash  string    // sha256 of the plaintext
    Scopes     string    // comma-separated, see §6
    ExpiresAt  int64     // unix seconds, 0 = no expiry
    LastUsedAt int64
    Revoked    bool
    CreatedAt  time.Time
    UpdatedAt  time.Time
}
```

### 4.3 Middleware

`middleware.AuthMiddleware()` replaces `JWTAuthMiddleware` for routes that
should accept both browsers and CLI:

```
if c.GetBool("trusted_local") { c.Set("principal","local-root"); c.Next(); return }
if header startsWith "Bearer ztk_" { validate token, set principal=token:<name>, c.Next(); return }
if header startsWith "Bearer "     { existing JWT path }
abort 401
```

Existing routes keep working: a browser sends a JWT bearer; the CLI on the host
hits the unix socket and gets `trusted_local`; a remote `zenithctl` sends an
API token.

### 4.4 Audit log

Mutating handlers already call into the audit recorder. We extend the recorder
to pull the principal from `c.GetString("principal")` instead of `c.GetString("username")`,
falling back to the latter for compatibility.

## 5. CLI Command Tree

Top-level: `zenithctl <group> <verb> [flags]`.

```
zenithctl                                       — help
zenithctl version
zenithctl status                                — pings /api/v1/health
zenithctl login                                 — interactive password+TOTP, caches JWT (for remote use)
zenithctl logout                                — wipes cached JWT
zenithctl token list
zenithctl token create   --name X [--scopes …] [--expires-in 90d]
zenithctl token revoke   <id|name>
zenithctl token rotate   <name>                 — mints v2, revokes v1, updates active profile in place
zenithctl token bootstrap                       — local-root only; creates token, writes config
zenithctl system info                           — CPU/mem/disk one-shot
zenithctl system bbr (status|enable|disable)
zenithctl system swap (status|create|remove) [--size 1G]
zenithctl inbound list [--json]
zenithctl inbound show <id>
zenithctl inbound create   --file inbound.json
zenithctl inbound update   <id> --file inbound.json
zenithctl inbound set-port <id> <port> [--sync-firewall]
zenithctl inbound delete   <id>
zenithctl client list [--inbound <id>]
zenithctl client add      --inbound <id> --email foo [--uuid …] [--expires …]
zenithctl client delete   <id>
zenithctl proxy status
zenithctl proxy apply                           — re-render configs + reload xray/sing-box
zenithctl proxy test     <id> | all             — server-side connectivity probe
zenithctl proxy config (xray|singbox)
zenithctl cert  issue    --domain X --email Y   — ACME / Let's Encrypt
zenithctl backup restore --file backup.zip      — upload + replace inbounds/clients/rules/cron
zenithctl proxy reality-keys
zenithctl proxy test <inbound-id>               — server-side probe of an inbound
zenithctl sub url <client-uuid>                 — print subscription URL
zenithctl firewall list
zenithctl firewall add    --port 443 --proto tcp --action ACCEPT [--source …]
zenithctl firewall delete <line-no>
zenithctl backup export   [--out backup.zip]
zenithctl backup restore  --file backup.zip
zenithctl raw <METHOD> <PATH> [--data @file|-]  — escape hatch, prints JSON
```

Global flags: `--host`, `--socket`, `--token`, `--output (json|table|yaml)`,
`-q/--quiet`, `--no-color`.

## 6. Scopes

Tokens carry a comma-separated scope list (`*` = full). Per-route scope checks
live in a small helper used inside each handler:

| Scope         | Grants                                           |
|---            |---                                               |
| `read`        | All `GET` endpoints                              |
| `write`       | Mutations on `/inbounds /clients /routing-rules` |
| `proxy:apply` | `POST /proxy/apply`, reload                      |
| `system`      | BBR/swap/sysctl, cleanup                         |
| `firewall`    | iptables rules                                   |
| `backup`      | Export and restore                               |
| `admin`       | `admin/*` including 2FA, password change, TLS    |
| `*`           | Everything                                       |

Default for `token bootstrap` and Web-UI-issued tokens is `*`. Operators are
encouraged to scope tokens used by automation.

## 7. Security Notes

- **Unix socket permissions.** Created with `0600 root:root` after listener
  bind. Anyone able to `stat`/`connect` to the socket already has root on the
  box, which is by design the same trust level.
- **No token logging.** The plaintext token is printed once to stdout on
  creation and never persisted in audit logs.
- **Constant-time compare.** Token lookups use `subtle.ConstantTimeCompare`
  against the stored hash.
- **Rate limit.** Bearer-token failures share the existing per-IP rate limiter.
- **CSRF irrelevance.** API tokens are sent via `Authorization` header, never
  set as cookies, so cross-origin browser CSRF cannot replay them.
- **Lockout independence.** Token auth failures do **not** count toward the
  admin password-lockout counter; this prevents an attacker from locking out
  legitimate operators by spamming bad tokens.

## 8. Migration / Backwards Compatibility

- The new `api_tokens` table is created via `AutoMigrate` and is non-fatal on
  failure (matches the existing pattern for non-critical tables).
- All existing endpoints keep their JWT auth; the CLI uses the same endpoints
  via the new middleware that *adds* two more accepted principal types.
- `zenithpanel` binary with no `ctl` argv keeps the old behavior 100%.

## 9. Out of Scope (Future Work)

- Multi-admin RBAC. Today there is one admin; tokens act on its behalf.
- WebSocket commands in the CLI (terminal, container exec) — left to the Web
  UI for now.
- Output formats other than `json` / `table`.
