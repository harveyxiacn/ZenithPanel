# ZenithPanel Development Roadmap

> **Last updated:** 2026-05-07  
> **Status:** Living document — updated after each major milestone  
> **Based on:** task.md analysis, dev_log.md, superpowers design specs, and full codebase audit

---

## Completed Milestones ✅

The following items from the original task checklist are **already implemented**:

| Item | Notes |
|------|-------|
| JWT randomization + bcrypt auth | Fully implemented |
| File sandbox (filepath.Clean) | Implemented; hardening pass pending (Phase B) |
| Setup wizard (one-time password + random URL) | Done |
| System monitoring (CPU/Memory/Disk/Network) | Done + SVG sparklines in Dashboard |
| WebSocket SSH terminal | Done |
| Web file manager | Done |
| Firewall/iptables management | Done |
| VPS diagnostics (vps_check.sh) | Done; script discovery fix pending (Phase B) |
| Cron job scheduler | Done |
| Xray + Sing-box config generation | Done; parity improved in d116708 |
| Inbound/Client/RoutingRule CRUD | Done |
| Subscription links (V2ray + Clash) | Done |
| ACME/Let's Encrypt (go-acme/lego) | Done (implemented in d116708) |
| Docker container start/stop/restart/remove | Done (basic ops only) |
| TOTP 2FA | Done |
| Audit log | Done |
| JWT refresh token | Done |
| Dark mode | Done |
| i18n (EN/ZH-CN/ZH-TW/JA) | Done |
| Toast notifications | Done |
| One-click system optimization (BBR/Swap/sysctl) | Done |
| Smart Deploy (Phase 1) | Done — SmartDeploy.vue + /api/v1/deploy/* |
| Engine selector (Xray ↔ Sing-box) | Done (d116708) |
| Shadowsocks AEAD-2022 per-user tracking | Done (d116708) |
| subscription-userinfo expire header | Done (d116708) |
| GitHub Actions CI (backend + frontend + Docker) | Done |
| 3x-ui import/export bridge | Done (three_xui_bridge.go) |
| Backup/restore service (JSON-in-zip) | Done (backup.go) |

---

## Pending Work — Prioritized Phases

### Phase A · Urgent: CI/CD & Quick Patches
**ETA: 2–4 hours · Priority: 🔴 Critical**

> Deadline-driven: GitHub Actions Node.js 20 EOL on 2026-06-02.

#### A1. GitHub Actions Node.js Upgrade
- **File:** `.github/workflows/main.yml`
- Change `node-version: 20` → `24` (lines 30 and 64)
- Verify `actions/setup-node@v6.3.0` is compatible with Node.js 24
- **Verification:** All 3 CI jobs pass green on next push

#### A2. Install Script Auto-Download from GitHub Releases
- **File:** `scripts/install.sh`
- **Problem:** Requires manual `.tar.gz` upload; cannot be used as one-liner
- **Changes:**
  - Add `detect_arch()` → amd64 / arm64
  - Add `ZENITH_VERSION` env var support (default: fetch latest from GitHub API)
  - Download release asset via `curl -L` from GitHub API response
  - SHA256 checksum verification against `.sha256` release asset
  - Keep local tarball fallback for offline/air-gapped environments
  - Print installed version + service status on completion
- **Verification:** Run on clean Debian 12 VM — panel installs and starts without any manual file upload

---

### Phase B · High: Project Hardening
**ETA: 3–5 days · Priority: 🔴 High**

> Based on `docs/superpowers/specs/2026-04-20-project-hardening-design.md`  
> Full implementation plan: `docs/superpowers/plans/2026-04-20-phase1-project-hardening.md`

This is a **pre-existing approved design**. Implement all tasks in the plan file. Summary of key tasks:

#### B1. File Sandbox Hardening
- **Files:** Create `backend/internal/api/fs_sandbox.go` + `fs_sandbox_test.go`; modify `router.go`
- Fix sibling-prefix traversal bug in file manager (e.g., `/home-evil` bypasses `/home` check)
- Add symlink escape detection
- Regression tests for all boundary cases

#### B2. JWT Refresh / Session Behavior Normalization
- **Files:** `frontend/src/api/client.ts`, new `frontend/src/api/session-recovery.ts` + test
- Remove recursive refresh-on-401 behavior
- Switch to deterministic logout redirect on auth failure
- Test: protected-route 401 vs login/setup 401 handled differently

#### B3. Proxy Connectivity Check Honest Rename
- **Files:** `frontend/src/api/proxy.ts`, `frontend/src/views/ProxyView.vue`, i18n locales
- Rename "Test Connection" button to "Check Server Network" — it checks the server's public IP, not the proxy itself
- Update all 4 i18n files with corrected copy

#### B4. Diagnostics Script Discovery Fix
- **File:** `backend/internal/service/diagnostic/diagnostic.go`, new `diagnostic_test.go`
- Replace cwd-relative discovery with deterministic paths: packaged binary layout + source layout fallback
- Test both layouts

#### B5. Updater Hardening
- **File:** `backend/internal/service/updater/updater.go`, new `updater_test.go`
- Replace pull-on-check with registry digest inspection (no pulling until confirmed update)
- Replace `apk add` helper flow with a helper config built from the panel image
- Test registry digest comparison logic

#### B6. Frontend Dependency Patch
- **File:** `frontend/package.json`, `frontend/package-lock.json`
- Upgrade `axios` to a non-vulnerable patch line
- Add `tsx` for TypeScript unit test execution
- Create `frontend/src/api/session-recovery.test.ts`

#### B7. Dockerfile: Package vps_check.sh
- **File:** `Dockerfile`
- Copy `scripts/vps_check.sh` into runtime image so diagnostics work in container deployments

**Phases B2–B5 (release reproducibility, test quality gates, maintainability refactors) are defined in the design doc and should be planned separately after Phase B1 lands.**

---

### Phase C · High: Docker SDK Feature Completion
**ETA: 2–3 days · Priority: 🟠 High**

> Current state: only List/Start/Stop/Restart/Remove exist (53-line file). ZenithPanel's "1Panel replacement" positioning requires full container lifecycle management.

#### C1. Backend Docker Manager Extensions
- **File:** `backend/internal/docker/manager.go`
- Add methods following existing code style:

| Method | Docker API | Purpose |
|--------|-----------|---------|
| `ListImages(ctx)` | `ImageList` | Image inventory |
| `PullImage(ctx, ref)` | `ImagePull` | Pull from registry |
| `RemoveImage(ctx, id, force)` | `ImageRemove` | Delete image |
| `GetContainerLogs(ctx, id, tail int)` | `ContainerLogs` | Tail N lines |
| `GetContainerStats(ctx, id)` | `ContainerStatsOneShot` | CPU/mem snapshot |
| `RunContainer(ctx, req RunContainerRequest)` | `ContainerCreate` + `ContainerStart` | Create & start |
| `InspectContainer(ctx, id)` | `ContainerInspect` | Full container details |
| `ListVolumes(ctx)` | `VolumeList` | Volume inventory |
| `ListNetworks(ctx)` | `NetworkList` | Network inventory |

```go
type RunContainerRequest struct {
    Image         string
    Name          string
    Ports         []string // ["8080:80/tcp"]
    Volumes       []string // ["/host:/container"]
    Env           []string // ["KEY=VALUE"]
    Cmd           []string
    RestartPolicy string   // "always"|"unless-stopped"|"no"
    NetworkMode   string   // "bridge"|"host"
}
```

#### C2. Backend API Route Extensions
- **File:** `backend/internal/api/router.go` (Docker section ~line 811)

```
GET    /docker/images                 — Image list
POST   /docker/images/pull            — Pull image (body: {image: "nginx:latest"})
DELETE /docker/images/:id             — Remove image
GET    /docker/containers/:id/logs    — Container logs (?tail=100)
GET    /docker/containers/:id/stats   — Resource snapshot
POST   /docker/containers/run         — Create and start container
GET    /docker/containers/:id/inspect — Container details
GET    /docker/volumes                — Volume list
GET    /docker/networks               — Network list
```

#### C3. Frontend Docker UI Extensions
- **Files:** `frontend/src/views/ServersView.vue`, `frontend/src/api/docker.ts`
- **New UI components:**
  - Image management tab: list (name/tag/size/created), pull form, remove button
  - Run Container modal: image, name, port-mapping rows (dynamic), volumes, env, restart policy
  - Container logs viewer modal: `xterm.js` (already installed) rendering tail output
  - Container resource inline panel: click row to expand CPU%/memory snapshot
- Add all corresponding API functions to `docker.ts`

---

### Phase D · High: Real-Time Traffic Monitoring
**ETA: 3–5 days · Priority: 🟠 High**

> Current state: Client traffic fields exist in DB but are never updated — no Xray stats API integration.

#### D1. Backend: Xray Stats gRPC Integration
- **Files:** `backend/internal/service/proxy/xray.go`, new `backend/internal/service/proxy/stats.go`
- Ensure generated Xray config includes stats/policy/api blocks:
  ```json
  "stats": {},
  "policy": {"system": {"statsUserUplink": true, "statsUserDownlink": true}},
  "api": {"tag": "api", "services": ["StatsService"]},
  ```
  Plus a dokodemo inbound on `127.0.0.1:10085` and routing rule for api tag.
- `stats.go` provides:
  - `QueryUserTraffic(uuid string) (up, down int64, err error)` — gRPC call to Xray StatsService
  - `SyncAllClientTraffic(db *gorm.DB)` — bulk sync all enabled clients

#### D2. Backend: Periodic Traffic Sync
- **File:** `backend/main.go` (or `service/scheduler`)
- Every 5 minutes: call `SyncAllClientTraffic` to update `Client.UpLoad`/`DownLoad` from Xray

#### D3. Backend: Traffic History Endpoint
- **File:** `backend/internal/api/router.go`
- `GET /api/v1/proxy/traffic-history` — returns last 60 samples (1-minute intervals) of in/out bytes per second
- In-memory ring buffer; resets on restart

#### D4. Frontend: Install ECharts
- **File:** `frontend/package.json`
- `npm install echarts vue-echarts`

#### D5. Frontend: Dashboard Bandwidth Chart
- **File:** `frontend/src/views/DashboardView.vue`
- Wide card with ECharts line chart: upload + download bandwidth over 1-hour rolling window
- Polls `/proxy/traffic-history` every 30s
- Reuse existing `setInterval` pattern from system monitor polling

#### D6. Frontend: Per-User Traffic Progress Bar
- **File:** `frontend/src/views/ProxyView.vue`
- Reuse existing `formatTraffic()` (line ~430)
- For clients with `total > 0`: colored progress bar + percentage
- Color coding: <70% green → 70–90% amber → >90% red

---

### Phase E · Medium: Sing-box Configuration Parity
**ETA: 1–2 days · Priority: 🟡 Medium**

> Partially resolved in d116708. These items remain.

#### E1. Reality Format: `dest` → `server` + `server_port`
- **File:** `backend/internal/service/proxy/singbox.go`
- Parse `dest` field (e.g., `"microsoft.com:443"`) and split into Sing-box `handshake.server` + `handshake.server_port`

#### E2. HTTP/2 Transport Mapping
- `"h2"/"http"` transport: generate Sing-box `{"type": "http", "host": [...], "path": "..."}`
- Map from Xray `httpSettings` host array + path

#### E3. TLS Fingerprint & ALPN
- Extract `fingerprint` from `tlsSettings` → `utls.enabled + utls.fingerprint`
- Extract `alpn` array → `tls.alpn`

#### E4. Sing-box Unit Tests
- **File:** New `backend/internal/service/proxy/singbox_test.go`
- Cover Reality format, HTTP/2 transport, TLS fingerprint output
- Pattern: mirror `xray_test.go` structure

---

### Phase F · Medium: WARP / WireGuard Outbound Integration
**ETA: 3–4 days · Priority: 🟡 Medium**

> No Outbound DB model exists. Currently only `direct`/`block` are generated (hardcoded).

#### F1. Outbound Database Model
- **File:** `backend/internal/model/proxy.go`
```go
type Outbound struct {
    ID          uint           `gorm:"primaryKey" json:"id"`
    Tag         string         `gorm:"uniqueIndex;not null" json:"tag"`
    Protocol    string         `gorm:"not null" json:"protocol"` // "wireguard"|"socks5"|"http"|"freedom"|"blackhole"
    Config      string         `gorm:"type:text" json:"config"`  // protocol-specific JSON
    Description string         `json:"description"`
    Enable      bool           `gorm:"default:true" json:"enable"`
    CreatedAt   time.Time      `json:"created_at"`
    UpdatedAt   time.Time      `json:"updated_at"`
    DeletedAt   gorm.DeletedAt `gorm:"index" json:"-"`
}
```
- Add `&model.Outbound{}` to `config/db.go` AutoMigrate

#### F2. Outbound CRUD API
- **File:** `backend/internal/api/router.go`
```
GET    /api/v1/outbounds        — List
POST   /api/v1/outbounds        — Create
PUT    /api/v1/outbounds/:id    — Update
DELETE /api/v1/outbounds/:id    — Delete
```

#### F3. Sing-box Config Generator: Dynamic Outbounds
- **File:** `backend/internal/service/proxy/singbox.go`
- Replace hardcoded `direct/block/dns-out` with DB query of enabled Outbounds
- For `protocol="wireguard"`: parse Config JSON → generate Sing-box WireGuard outbound block
- Preserve `direct`, `block`, `dns-out` as system-managed defaults (not in DB)

#### F4. WARP Key Fetch Helper
- **File:** New `backend/internal/service/proxy/warp.go`
- `FetchWARPConfig(accountID, token string)` — calls Cloudflare WARP Teams API to obtain WireGuard private/public keys
- Returns JSON blob suitable for `Outbound.Config`

#### F5. Frontend Outbound Management UI
- **File:** `frontend/src/views/ProxyView.vue` (new Outbound tab), `frontend/src/api/proxy.ts`
- Outbound list table with Tag / Protocol / Enable / Actions
- Create modal: protocol selector → protocol-specific fields
- WARP: endpoint, private key, public key fields + "Fetch from Cloudflare" button (calls F4)
- Routing rule OutboundTag dropdown: load from DB instead of hardcoded `direct/block`

---

### Phase G · Medium: Usage Profile UX
**ETA: 2–3 days · Priority: 🟡 Medium**

> Based on `docs/superpowers/specs/2026-03-27-usage-profile-ux-design.md`  
> Full implementation plan: `docs/superpowers/plans/2026-03-27-usage-profile-ux.md`

This is a **pre-existing approved design**. Implement all tasks in the plan file. Summary:

#### G1. Backend: `usage_profile` Setting
- Normalize to `personal_proxy` / `vps_ops` / `mixed` (invalid → `mixed`)
- Expose via Setup Wizard completion payload and `GET/PUT /api/v1/admin/access`

#### G2. Frontend: Profile Store
- New `frontend/src/store/profile.ts` — Pinia store reading `usage_profile` setting
- Reactive composable `useProfile()` consumed by layout/dashboard

#### G3. Profile-Driven Navigation
- **File:** `frontend/src/components/layout/MainLayout.vue`
- `personal_proxy`: Proxy section first, VPS tools in "Advanced"
- `vps_ops`: Servers section first, Proxy tools in "Advanced"
- `mixed`: current order unchanged

#### G4. Profile-Driven Dashboard
- **File:** `frontend/src/views/DashboardView.vue`
- `personal_proxy`: lead with proxy status + active clients; system metrics secondary
- `vps_ops`: lead with system metrics + Docker; proxy status secondary
- Profile selector in Settings page

---

### Phase H · Medium: Backup/Restore UI + 3x-ui Import UI
**ETA: 1–2 days · Priority: 🟡 Medium**

> Backend services exist but have no frontend exposure.  
> `backup.go`: full JSON-in-zip export/restore. `three_xui_bridge.go`: import/export 3x-ui inbound format.

#### H1. Backup/Restore Frontend
- **File:** `frontend/src/views/SecurityView.vue` (new "Backup" tab)
- **Export button**: calls new `POST /api/v1/admin/backup/export`, downloads `.zip` file
- **Restore file picker**: upload `.zip`, calls `POST /api/v1/admin/backup/restore`
- Backend routes added to `router.go` (thin wrappers over `backup.Export()`/`backup.Restore()`)
- Confirmation dialog before restore (overwrites all data)

#### H2. 3x-ui Import Frontend
- **File:** `frontend/src/views/ProxyView.vue` (import button in Inbounds tab toolbar)
- Paste-JSON or file-upload of 3x-ui inbound export
- Calls existing `POST /api/v1/import/3xui` (already registered in router.go)
- Show import summary: N inbounds + M clients imported / K skipped

---

### Phase I · Low: Notification System
**ETA: 2–3 days · Priority: 🟢 Low**

> No notification system exists. Users manually check panel for expiring clients.

#### I1. Backend: Notification Service
- **File:** New `backend/internal/service/notify/notify.go`
- Event types: `ClientExpiringSoon` (within 3 days), `ClientExpired`, `TrafficLimitReached` (>90%), `ProxyCoreCrashed`
- Delivery channels: **Telegram Bot** (primary) + **Webhook** (secondary)
- Config stored in `settings` table: `notify_telegram_token`, `notify_telegram_chat_id`, `notify_webhook_url`

#### I2. Backend: Notification Trigger
- Run in existing Cron scheduler: daily check for expiring/expired clients
- Proxy core crash detection: on unexpected process exit, trigger `ProxyCoreCrashed` event

#### I3. Frontend: Notification Settings
- **File:** `frontend/src/views/SecurityView.vue` (new "Notifications" sub-tab)
- Telegram: Bot Token + Chat ID + "Send Test" button
- Webhook URL + "Send Test" button
- Event toggle switches (enable/disable per event type)

---

### Phase J · Low: Internal Web Server & Reverse Proxy
**ETA: 5–7 days · Priority: 🟢 Low**

> Major feature gap vs 1Panel. No implementation exists. Requires new DB model + Go reverse proxy service.

#### J1. Site Database Model
- **File:** New `backend/internal/model/site.go`
```go
type Site struct {
    ID            uint           `gorm:"primaryKey" json:"id"`
    Name          string         `gorm:"uniqueIndex;not null" json:"name"`
    Domain        string         `gorm:"not null" json:"domain"`
    Type          string         `gorm:"not null" json:"type"` // "reverse_proxy"|"static"|"redirect"
    UpstreamURL   string         `json:"upstream_url"`          // http://127.0.0.1:3000
    RootPath      string         `json:"root_path"`             // /var/www/mysite
    TLSMode       string         `json:"tls_mode"`              // "none"|"acme"|"custom"
    CertPath      string         `json:"cert_path"`
    KeyPath       string         `json:"key_path"`
    CustomHeaders string         `gorm:"type:text" json:"custom_headers"` // JSON [{key,value}]
    Enable        bool           `gorm:"default:true" json:"enable"`
    CreatedAt     time.Time      `json:"created_at"`
    UpdatedAt     time.Time      `json:"updated_at"`
    DeletedAt     gorm.DeletedAt `gorm:"index" json:"-"`
}
```

#### J2. Web Server Manager Service
- **File:** New `backend/internal/service/webserver/manager.go`
- Pure Go implementation using `net/http/httputil.ReverseProxy`
- Single `WebServerManager` singleton listening on :80 and :443
- SNI-based routing via `crypto/tls.Config.GetConfigForClient`
- Handler types:
  - `reverse_proxy`: `httputil.NewSingleHostReverseProxy` + custom header injection
  - `static`: `http.FileServer(http.Dir(rootPath))`
  - `redirect`: `http.RedirectHandler`
- `Reload()` method: re-reads DB and hot-swaps TLS config without downtime
- ACME integration: reuse `cert.ObtainCert()` from `backend/internal/service/cert/acme.go`

#### J3. Site CRUD API
- **File:** `backend/internal/api/router.go`
```
GET    /api/v1/sites               — List
POST   /api/v1/sites               — Create (triggers Reload)
PUT    /api/v1/sites/:id           — Update (triggers Reload)
DELETE /api/v1/sites/:id           — Delete (triggers Reload)
POST   /api/v1/sites/:id/enable    — Toggle enable
POST   /api/v1/sites/:id/cert      — Trigger ACME certificate issue
```

#### J4. Frontend: Site Management Page
- **File:** New `frontend/src/views/SitesView.vue`, new `frontend/src/api/sites.ts`
- Site card grid: domain / type / TLS badge / enable toggle / edit / delete
- Create/edit modal: type selector → dynamic field set
- TLS section: radio (None / ACME auto / Custom paths) + issue button + status

#### J5. Router Registration & Navigation
- **File:** `frontend/src/router/index.ts`, `frontend/src/components/layout/MainLayout.vue`
- Add `/sites` route and sidebar nav item
- Add i18n keys for all 4 locales

#### J6. main.go Integration
- **File:** `backend/main.go`
- Initialize `WebServerManager` after DB setup; load existing Sites
- Register in graceful shutdown

---

### Phase K · Low: Smart Deploy Phase 2 & 3
**ETA: 5–10 days · Priority: 🟢 Low**

> Based on `docs/superpowers/specs/2026-04-21-smart-deploy-design.md` (Phase 1 already shipped).

#### K1. Phase 2: Rule-Based Preset Recommendation
- Backend: env probe results → scoring logic → recommended preset with reasoning
- Frontend: `SmartDeploy.vue` — show recommendation card with explanation before user picks preset

#### K2. Phase 3: Protocol Completeness
- ShadowTLS inbound support
- NaiveProxy inbound support  
- AnyTLS inbound support
- WireGuard inbound (Sing-box) in Smart Deploy preset options
- Benchmark integration: post-deploy latency + throughput test

---

## Additional Improvements (Backlog)

These items add incremental value without a defined timeline:

| Item | Area | Notes |
|------|------|-------|
| Monthly traffic auto-reset for clients | Backend/Frontend | Cron job at month start resetting `up_load`/`down_load` |
| Batch operations on client list | Frontend | Multi-select + batch enable/disable/delete/reset-traffic |
| Port conflict detection on inbound create | Backend | Warn if `lsof -i :PORT` finds existing listener |
| Prometheus metrics endpoint | Backend | `GET /metrics` for Prometheus scraping (CPU/mem/conn counts) |
| Self-service client subscription page | Frontend | Public page (authenticated by client UUID) for subscription QR + info |
| Search/filter on all list tables | Frontend | Real-time filter input on Inbounds/Clients/RoutingRules/CronJobs |
| Inbound traffic reset button | Frontend | Per-inbound reset in Proxy view |
| IPv6 panel listening | Backend | Dual-stack `[::]:PORT` in main.go |
| Multi-admin / role-based access | Backend + Frontend | Phase 2 feature; requires auth model redesign |
| Telegram bot command interface | Backend | Minimal `/status`, `/clients` commands |

---

## Key File Index

| File | Phases |
|------|--------|
| `.github/workflows/main.yml` | A1 |
| `scripts/install.sh` | A2 |
| `backend/internal/api/fs_sandbox.go` (new) | B1 |
| `backend/internal/api/router.go` | B1–B3, C2, D3, F2, H1–H2, J3 |
| `backend/internal/service/diagnostic/diagnostic.go` | B4 |
| `backend/internal/service/updater/updater.go` | B5 |
| `frontend/src/api/client.ts` | B2 |
| `frontend/src/api/session-recovery.ts` (new) | B2 |
| `frontend/src/views/ProxyView.vue` | B3, D6, E-frontend, F5, H2 |
| `frontend/package.json` | B6, D4 |
| `Dockerfile` | B7 |
| `backend/internal/docker/manager.go` | C1 |
| `frontend/src/views/ServersView.vue` | C3 |
| `frontend/src/api/docker.ts` | C3 |
| `backend/internal/service/proxy/xray.go` | D1 |
| `backend/internal/service/proxy/stats.go` (new) | D1 |
| `frontend/src/views/DashboardView.vue` | D5 |
| `backend/internal/service/proxy/singbox.go` | E1–E3 |
| `backend/internal/service/proxy/singbox_test.go` (new) | E4 |
| `backend/internal/model/proxy.go` | F1 |
| `backend/internal/config/db.go` | F1, J1 |
| `backend/internal/service/proxy/warp.go` (new) | F4 |
| `frontend/src/store/profile.ts` (new) | G2 |
| `frontend/src/components/layout/MainLayout.vue` | G3, J5 |
| `backend/internal/service/notify/notify.go` (new) | I1 |
| `frontend/src/views/SecurityView.vue` | H1, I3 |
| `backend/internal/model/site.go` (new) | J1 |
| `backend/internal/service/webserver/manager.go` (new) | J2 |
| `frontend/src/views/SitesView.vue` (new) | J4 |
| `frontend/src/api/sites.ts` (new) | J4 |
| `frontend/src/router/index.ts` | J5 |
| `frontend/src/i18n/locales/*.ts` | G, J5 |
| `backend/main.go` | J6 |

---

## Development Conventions

- All new backend handlers follow the `router.go` pattern: `config.DB` global, `c.JSON(code, gin.H{...})` responses, `recordAudit()` for mutating ops
- All new frontend pages use: `useToast`, `useConfirm`, `useValidation` composables; `SkeletonTable` during loading
- New GORM models added to `AutoMigrate` call in `config/db.go` (non-fatal migration)
- Each phase must pass `go test ./...` and `npm run build` before merging
- No `Co-Authored-By: Claude` trailers in commits

---

## Verification Checklist (per Phase)

| Phase | Gate |
|-------|------|
| A1 | GitHub Actions 3 jobs green after push |
| A2 | Fresh Debian 12 install via curl-pipe works end-to-end |
| A3 | Client progress bars shown with correct colors |
| B | `go test ./...` green; `npm run build` green; manual file traversal test blocked |
| C | Pull image + run container + view logs work in panel UI |
| D | ECharts chart shows live bandwidth; client traffic updates every 5 min |
| E | `go test ./internal/service/proxy/...` green; Sing-box config passes JSON schema |
| F | WARP outbound appears in Sing-box config; traffic exits via WARP IP |
| G | Profile selector changes nav order and dashboard layout |
| H | Export .zip → restore on fresh DB reconstructs all inbounds/clients |
| I | Telegram notification received when test client expires |
| J | Custom domain reverse-proxied with TLS cert through panel |
| K1 | Smart Deploy shows recommended preset with reasoning |
