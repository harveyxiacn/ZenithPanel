# Changelog

All notable changes to ZenithPanel are documented here. Dates use ISO 8601
(`YYYY-MM-DD`). The project loosely follows [Keep a Changelog](https://keepachangelog.com/).

## [Unreleased] — 2026-05-15 (QR-ready production rollout)

### Added

- **One-click ACME / Let's Encrypt** via Web UI (Settings → Let's Encrypt)
  and CLI (`zenithctl cert issue --domain X --email Y`). Background
  renewer ticks every 12 hours and re-issues any cert within 30 days of
  expiry. Panel webserver yields :80 to lego during the HTTP-01 challenge
  via a `PortBouncer` hook in `service/cert`.
- **Ad-block toggle** at Settings → Ad block. When on, the panel inserts
  a managed routing rule (`geosite:category-ads-all → block`) and
  restarts both engines in parallel. When off, the rule is removed.
  Admin-scope gated; CLI surface: `zenithctl raw PUT /api/v1/admin/adblock`.
- **CLI `zenithctl inbound set-port <id> <port> [--sync-firewall]`** —
  one-shot port change with auto-apply and optional UFW open.
- **CLI `zenithctl backup restore --file backup.zip`** — closes the
  previously-stubbed restore path.
- **CLI `zenithctl token rotate <name>`** — mints a fresh same-name-v2
  token, revokes the old one, **updates the active profile in place**
  so the rotating session doesn't 401 mid-flight.
- **CLI `zenithctl proxy test all`** — drives the server-side prober
  across every enabled inbound; honors `--output table` and exits
  non-zero on any failure.
- **Per-client traffic counters** in `/api/v1/metrics`
  (`zenithpanel_client_traffic_bytes{email,inbound,direction}`), plus
  engine uptime gauges (`zenithpanel_xray_uptime_seconds`,
  `zenithpanel_singbox_uptime_seconds`).
- **`/api/v1/health` enrichment**: `xray_uptime_seconds`,
  `singbox_uptime_seconds`, `last_apply_unix`. All accessible
  pre-setup so external monitors keep working on fresh installs.

### Changed

- **Webserver lazy-bind.** The vhost reverse-proxy no longer holds
  :80/:443 when zero sites are configured — idle binding was blocking
  ACME's HTTP-01 challenge and presenting a 404 listener for nothing.
  First Site creation triggers a Reload that re-acquires the ports.

### Fixed

- **TUIC password mismatch.** A previous round added a per-user TUIC
  password override on the server, but the subscription URL generator
  still emitted `UUID:UUID` — every TUIC client failed auth silently
  and looked "slow / unable to load YouTube". Reverted the override:
  TUIC users now always use UUID as the password, matching the share
  link. Regression: `TestTUICPasswordIsAlwaysUUID`.
- **SS-2022 multi-user password.** Subscription URL & Clash YAML now
  emit `serverPSK:userPSK` (joined by `:`) when the cipher is
  `2022-blake3-*`. Pre-fix the SS-2022 inbound silently rejected every
  client because the share link only carried the server PSK.
- **`inferTransport` mis-classified rewritten streams.** The probe
  result's `transport` field was a substring match against canonical
  Go JSON (no whitespace); streams round-tripped through tools that
  add `": "` spacing showed up as `tcp` instead of `ws+tls`/`tcp+tls`.
  Now uses real `json.Unmarshal`.
- **Xray sniff drops the dead `fakedns` destOverride.** The panel
  doesn't wire a fakedns outbound; listing it in sniff per inbound
  added a no-op DNS sniff per connection (~tens of ms each).

## [Unreleased] — 2026-05-15 (sing-box rule-sets + Probe All)

### Changed

- **Sing-box geosite → rule_set migration.** The routing-rule generator no
  longer emits the deprecated `geosite`/`geoip` keys; it now declares
  remote `rule_set` entries that sing-box fetches from
  `SagerNet/sing-geosite` and `SagerNet/sing-geoip` once per week. The
  `ENABLE_DEPRECATED_GEOSITE=true` env-var hack on the sing-box exec.Cmd
  is gone. `experimental.cache_file` is now emitted unconditionally so
  rule-set downloads survive a panel restart.

### Fixed

- **`geoip:private` 404 at boot.** SagerNet's sing-geoip repo doesn't
  ship a `private.srs` file, so the previous migration crashed the engine
  at start-up. Routes that mention `geoip:private` now set the sing-box
  built-in `ip_is_private: true` attribute instead of fetching a
  rule-set. Regression pinned by `TestBuildSingboxRoutingRuleGeoipPrivateMapsToIsPrivate`.

### Added

- **Probe All button** on the Proxy → Inbound Nodes page. Walks every
  enabled inbound with a concurrency cap of 4, fills the per-row probe
  chip as results land, exits when all rows have either a green/red
  badge. Strings localized for en/zh-CN/ja/zh-TW.

## [Unreleased] — 2026-05-15

### Added

- **Headless CLI** — `zenithpanel ctl …` (also `zenithctl` via symlink) is a
  thin HTTP client over the panel API. On the panel host, root connects
  without credentials via a new `/run/zenithpanel.sock` Unix socket; remote
  hosts authenticate with an API token. Subcommands cover system / inbound /
  client / proxy / firewall / backup / token / raw. See
  [docs/cli_design.md](docs/cli_design.md) and
  [docs/cli_api_spec.md](docs/cli_api_spec.md).
- **API tokens** — new `api_tokens` table + endpoints
  `GET/POST/DELETE /api/v1/admin/api-tokens` and a local-socket-only
  `POST /api/v1/admin/api-tokens/bootstrap` for self-service token minting.
  Tokens are stored as `sha256(plaintext)`; plaintext is shown exactly once.
  Carry comma-separated scopes; `admin` scope gates token CRUD itself.
- **Dual-engine mode** — Xray and Sing-box now run concurrently by default.
  `POST /api/v1/proxy/apply` without an `engine=` query partitions enabled
  inbounds (Xray handles vless/vmess/trojan/ss; Sing-box handles
  hysteria2/tuic) and starts both processes. Explicit `?engine=xray|singbox`
  keeps the legacy single-engine override.

### Fixed

- **sing-box 1.11 startup crash** — set `ENABLE_DEPRECATED_GEOSITE=true` on
  the sing-box `exec.Cmd` so configs that reference geosite still load. The
  proper migration to rule-sets is tracked separately.
- **TUIC inbound missing ALPN** — the sing-box generator now defaults
  `tls.alpn` to `["h3"]` for Hysteria2 and TUIC when the inbound doesn't
  supply one. Pre-fix every TUIC client failed handshake with
  `tls: server did not select an ALPN protocol`. Regression test:
  `TestTUICDefaultsALPNToH3`.
- **TUIC password not configurable** — generator now reads
  `inbound.Settings.clients[].password` (keyed by email) and uses it as the
  TUIC user's password, falling back to the UUID when none is supplied.
  Regression test: `TestTUICPerUserPasswordOverride`.

### Verified

- All six protocols (VLESS+Reality, VMess+WS+TLS, Trojan+TLS, SS-2022,
  Hysteria2, TUIC v5) pass end-to-end `curl --socks5 …` probes simultaneously
  in dual-engine mode. See [docs/protocol_connectivity_report.md](docs/protocol_connectivity_report.md).

## [Unreleased] — 2026-05-07 (second batch)

### Added — Phase A–J feature sprint

#### CI & Delivery
- **Node.js 20 → 24** in GitHub Actions and Dockerfile (EOL 2026-06-02 deadline)
- **Install script rewrite** (`scripts/install.sh`) — auto-detects arch (amd64/arm64), fetches latest release from GitHub API, SHA256 verification, apt/yum/dnf support, offline fallback
- **`.dockerignore` fix** — `!scripts/vps_check.sh` negation so the diagnostic script reaches the runtime image

#### Security hardening (Phase B)
- **File sandbox sibling-prefix fix** — replaced `strings.HasPrefix(abs, root)` with `filepath.Rel` in new `fs_sandbox.go`; fixes `/home2` bypass vulnerability; 6 regression tests
- **JWT 401 deterministic** — new `session-recovery.ts` helper; `shouldLogoutOnUnauthorized()` prevents redirect loops on login/setup 401s; 3 TS unit tests (tsx)
- **Server network check honest rename** — all responses include `scope: "server_public_network"`; injectable `networkCheckDo` transport for testability; i18n updated (4 locales)
- **Diagnostics script deterministic discovery** — `resolveScriptPath()` searches 4 candidate paths; returns `ErrDiagnosticScriptUnavailable` → 503 when not found; 3 tests
- **Updater hardening** — `CheckForUpdate` uses `DistributionInspect` (no image pull); helper container uses panel image instead of `alpine + apk add`; 3 tests

#### Docker SDK completion (Phase C)
- **9 new Docker Manager methods**: `ListImages`, `PullImage`, `RemoveImage`, `GetContainerLogs`, `GetContainerStats` (one-shot CPU/mem), `RunContainer`, `InspectContainer`, `ListVolumes`, `ListNetworks`
- **9 new API routes** for the above
- **Frontend Docker UI**: Containers/Images sub-tabs, container logs modal (xterm-style), stats inline row, Run Container form (ports/volumes/env)

#### Sing-box parity (Phase D)
- **TLS fingerprint → uTLS block**: `tlsSettings.fingerprint` now maps to `"utls": {"enabled": true, "fingerprint": "..."}` — enables browser-grade TLS fingerprinting
- **singbox_test.go**: 5 new tests covering Reality dest split, HTTP/2 transport, fingerprint, no-fingerprint, JSON validity

#### WARP / WireGuard Outbound (Phase E)
- **`Outbound` model** — new DB table for custom egress (WireGuard, SOCKS5, HTTP)
- **`FetchWARPConfig()`** — calls Cloudflare WARP API to obtain WireGuard keys
- **`buildSingboxOutbound()`** — converts DB Outbound records to Sing-box outbound blocks
- **CRUD API**: `GET/POST/PUT/DELETE /api/v1/outbounds` + `POST /api/v1/outbounds/warp/fetch`
- **Outbounds tab** in ProxyView — Add form, WARP credential fetch helper, Outbound list

#### 3x-ui Import UI
- "Import 3x-ui" button in Inbounds tab header — inline JSON paste panel with per-inbound result display

#### ECharts bandwidth chart (Phase D)
- **`monitor/history.go`**: 60-point network I/O ring buffer; updated on every `/system/monitor` poll
- **`GET /api/v1/system/network-history`**: returns timestamped in/out byte rates
- **Dashboard ECharts chart**: download (green) + upload (indigo) line chart, auto-hides until ≥3 samples
- **vite.config.ts**: `manualChunks` splits ECharts to separate 593KB chunk; main bundle stays 260KB

#### Notification system (Phase I)
- **`notify` package**: `Send()`/`SendTest()` via Telegram Bot API or webhook; 4 per-event enable flags
- **`RunClientChecks()`**: scans clients for expiry ≤3 days and traffic >90%; called every 6h from background goroutine
- **`GET/PUT /api/v1/admin/notify`**: read/write 7 notification settings
- **`POST /api/v1/admin/notify/test`**: send test message
- **SecurityView "Notifications" card**: Telegram token+chat-ID, webhook URL, event checkboxes, Save/Test

#### Built-in web server & reverse proxy (Phase J)
- **`Site` model** — DB table for virtual hosts (name/domain/type/upstream/root/redirect/tls)
- **`WebServerManager`** — listens on :80/:443; SNI-based certificate routing; hot-reload without downtime; handlers: reverse proxy (`httputil.ReverseProxy`), static files (`http.FileServer`), HTTP redirect
- **CRUD API**: `GET/POST/PUT/DELETE /api/v1/sites` + toggle enable + ACME cert issuance
- **`SitesView.vue`**: site card list, create form with TLS config, enable/disable, cert issue button
- **Sidebar "Sites" nav** (always visible, GlobeAltIcon)

#### Client traffic quota progress bars
- Colored progress bar (green/amber/red) for clients with `total > 0` in ProxyView client table

---

## [Unreleased] — 2026-05-07

### Fixed — Protocol engine (root cause of non-VLESS protocols not working)

- **Engine mutex on apply** — `POST /proxy/apply?engine=xray` now stops Sing-box
  before starting Xray, and vice versa. Previously both engines could bind the
  same inbound ports simultaneously, causing the second engine to fail silently
  with "address already in use". This was the primary reason VMess / Trojan /
  Shadowsocks appeared broken when users switched engines without manually stopping
  the other one.

- **Sing-box binary in Docker** — the runtime image now downloads and installs the
  latest `sing-box` release alongside Xray. Without this binary, Hysteria2 and
  TUIC inbounds were always silently skipped (Xray does not support them) and
  sing-box could never start. Verified with `sing-box version` at build time.

- **Trojan TLS validation** — `validateInbound` now rejects Trojan inbounds that
  lack `"security": "tls"` or `"security": "reality"` in their stream JSON before
  writing to the database. `buildSingboxInbound` also returns a clear error
  (`"trojan inbound %q requires TLS or Reality security"`) so a misconfigured
  Trojan inbound can no longer silently crash the entire Sing-box process on start.

- **Dead code removal** — removed the unreachable `case "hysteria2":` branch in
  `buildXrayInbound` (`xray.go`). Xray correctly skips non-supported protocols
  via the `xraySupportedProtocols` guard; the extra case was never executed and
  only caused confusion.

### Added — Protocol features

- **Shadowsocks AEAD-2022 multi-user** — when the inbound method is
  `2022-blake3-aes-128-gcm`, `2022-blake3-aes-256-gcm`, or
  `2022-blake3-chacha20-poly1305`, both Xray and Sing-box config generation now
  inject a per-client `users` array (Sing-box) / `clients` array (Xray) so each
  client gets an individual password (their UUID) and traffic can be tracked
  per-user. Classic methods retain the single shared-password behaviour.

- **Subscription `expire` field** — `subscription-userinfo` response header now
  includes `; expire=<unix_timestamp>` when the client record has `expiry_time`
  set. Clash Meta, v2rayN, and compatible clients will display the expiry date in
  their subscription overview.

- **`xray_skipped_protocols` in status API** — `GET /proxy/status` now includes
  `xray_skipped_protocols: [...]` listing any inbounds Xray skipped on its last
  start (e.g. Hysteria2, TUIC). Callers can surface this to users without parsing
  log output.

- **ACME / Let's Encrypt integration** — `IssueCertificate` now runs a real
  ACME HTTP-01 challenge via `go-acme/lego/v4` (the package was already an
  indirect dependency; it is now a direct one). On success the cert and key are
  written to `data/certs/<domain>.{crt,key}`. Requires port 80 to be reachable.
  Smart Deploy's `cert_mode=acme` path now triggers the real flow instead of
  falling back to a self-signed certificate silently.

### UI / UX

- **Engine radio selector** — the "Apply Config" header area in Proxy Nodes now
  shows an Xray / Sing-box toggle. Selecting an engine stops the other one and
  applies the chosen engine's config. When any inbound uses a Sing-box-only
  protocol (Hysteria2, TUIC) the selector auto-selects Sing-box on page load.

- **Sing-box status badge** — a second engine status badge appears next to the
  existing Xray badge so users can see at a glance which engine is running.

- **Sing-box-only protocol badge** — inbounds using Hysteria2 or TUIC now show an
  amber `Sing-box only` badge in the inbound table.

- **Skipped-protocol warning** — if Xray is active and has skipped any protocols,
  a `⚠ N 协议被跳过` warning badge appears in the page header with a tooltip
  listing the affected inbounds.

- **Traffic sparkline** — the Network card on the Dashboard now renders a 5-minute
  (60-sample) SVG sparkline showing real-time download (emerald) and upload (sky,
  dashed) byte rates, built from the existing polling loop without adding any
  new dependencies.

## [Previously Unreleased]

### Added — features
- **TUIC v5 protocol** — end-to-end support via Sing-box. Inbound generation
  writes users with `uuid` + `password`, lets settings JSON override
  `congestion_control` (default `bbr`), `udp_relay_mode`, and
  `zero_rtt_handshake`. Subscription output includes a spec-compliant
  `tuic://uuid:password@host:port?sni&alpn&...` link and a matching Clash
  `type: tuic` proxy block (`congestion-controller`, `udp-relay-mode`,
  `reduce-rtt`).
- **Backup / Restore** — `GET /api/v1/admin/backup/export` streams a zip with
  a `backup.json` entry covering inbounds, clients, routing rules, cron jobs,
  and non-secret settings. `POST /api/v1/admin/backup/restore` validates
  format version and rebuilds state inside a single transaction. Admin
  credentials, JWT secret, and TLS paths are never exported and never
  overwritten — restoring a backup keeps your current login. UI lives under
  **Security → Backup & Restore** in EN / 简体 / 繁體 / 日本語.

### Performance — tuned for 1C / 1–2G VPS
- **System monitor cache** — `/api/v1/system/monitor` now caches gopsutil
  snapshots for 3s and memoizes the hostname, cutting syscall pressure roughly
  in half under the 5s frontend poll cadence.
- **Gin release mode** — the web framework is switched to `ReleaseMode` by
  default and a slim logger skips noisy paths (`/system/monitor`,
  `/proxy/status`, `/api/v1/sub/*`). Respects `GIN_MODE` if exported.
- **Subscription endpoint** — collapsed three sequential SQLite queries into a
  single `clients JOIN inbounds` scan and added an 8-second response cache keyed
  by `(uuid, format, host)`. Cache is flushed automatically on any inbound /
  client mutation via `sub.InvalidateSubCache()`.
- **Proxy config generation** — `XrayManager.GenerateConfig()` and
  `SingboxManager.GenerateConfig()` now batch-load every relevant client in one
  query; previously each inbound triggered its own `SELECT` (N+1).
- **Bounded log buffer** — `BaseCore.outputBuf` is now an 8 KB ring buffer. The
  previous unbounded `bytes.Buffer` could grow indefinitely for long-running
  engines that emit periodic log lines.
- **Rate-limiter GC** — per-IP login and subscription limiters are pruned every
  10 minutes when idle, so the maps no longer accumulate over the process
  lifetime.

### Fixed — non-VLESS subscription protocols
- **VMess share link** — added the missing `httpupgrade` transport branch,
  propagated `alpn`, `fingerprint`, and `allowInsecure` under TLS, and added
  Reality parameters (`pbk`, `sid`, `spx`, `fp`) for `vmess+reality` deployments.
- **Trojan share link** — Reality parameters are now emitted, the UUID is
  URL-escaped, and the Clash YAML writer emits a matching `reality-opts` block.
- **Hysteria2** — the `insecure=1` flag is no longer hard-coded; it now follows
  the inbound's `allowInsecure` TLS setting. Added support for salamander
  `obfs` + `obfs-password`, port-hopping (`mport`), ALPN propagation, and the
  corresponding Clash fields (`ports`, `obfs`, `obfs-password`, `up`, `down`).
- **Shadowsocks share link** — userinfo is now encoded using SIP002
  `base64.RawURLEncoding` (no padding), and `plugin` / `plugin_opts` from the
  inbound settings are forwarded as a `plugin=` query parameter. Clash output
  now also enables UDP by default.
- **VLESS Clash output** — `skip-cert-verify` now reflects the `allowInsecure`
  flag rather than being hard-coded to `true`.

### Tests
- Ring-buffer + subscription-cache-invalidate unit tests, and link-format unit
  tests for VMess (httpupgrade + Reality), Trojan (Reality), Hysteria2 (obfs +
  insecure flag), Shadowsocks (SIP002 + plugin), and TUIC v5 (share link +
  Clash block).
- Backup round-trip test verifies `jwt_secret` is not leaked into the archive
  and survives untouched across restore.
