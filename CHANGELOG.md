# Changelog

All notable changes to ZenithPanel are documented here. Dates use ISO 8601
(`YYYY-MM-DD`). The project loosely follows [Keep a Changelog](https://keepachangelog.com/).

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
