# Changelog

All notable changes to ZenithPanel are documented here. Dates use ISO 8601
(`YYYY-MM-DD`). The project loosely follows [Keep a Changelog](https://keepachangelog.com/).

## [Unreleased]

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

### Added
- Ring-buffer + subscription-cache-invalidate unit tests, and link-format unit
  tests for VMess (httpupgrade + Reality), Trojan (Reality), Hysteria2 (obfs +
  insecure flag), and Shadowsocks (SIP002 + plugin).
