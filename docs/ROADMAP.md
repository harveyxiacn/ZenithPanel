# ZenithPanel Development Roadmap

> **Last updated:** 2026-05-07  
> **Status:** Living document — updated after each major milestone

---

## Completed Milestones ✅

### Original task checklist

| Item | Status |
|------|--------|
| JWT randomization + bcrypt auth | ✅ |
| File sandbox | ✅ (hardened 2026-05-07) |
| Setup wizard (one-time password + random URL) | ✅ |
| System monitoring (CPU/Memory/Disk/Network) | ✅ |
| WebSocket SSH terminal | ✅ |
| Web file manager | ✅ |
| Firewall/iptables management | ✅ |
| VPS diagnostics (vps_check.sh) | ✅ (script discovery fixed 2026-05-07) |
| Cron job scheduler | ✅ |
| Xray + Sing-box config generation | ✅ (parity improved 2026-05-07) |
| Inbound/Client/RoutingRule CRUD | ✅ |
| Subscription links (V2ray + Clash) | ✅ |
| ACME/Let's Encrypt (go-acme/lego) | ✅ |
| Docker container management | ✅ (full SDK 2026-05-07) |
| TOTP 2FA | ✅ |
| Audit log | ✅ |
| JWT refresh token | ✅ |
| Dark mode | ✅ |
| i18n (EN/ZH-CN/ZH-TW/JA) | ✅ |
| Toast notifications | ✅ |
| One-click system optimization (BBR/Swap/sysctl) | ✅ |
| Smart Deploy Phase 1 | ✅ |
| Engine selector (Xray ↔ Sing-box) | ✅ |
| Shadowsocks AEAD-2022 per-user tracking | ✅ |
| GitHub Actions CI (backend + frontend + Docker) | ✅ |
| 3x-ui import/export bridge | ✅ (UI added 2026-05-07) |
| Backup/restore (JSON-in-zip + UI) | ✅ |
| Usage Profile UX (personal_proxy/vps_ops/mixed) | ✅ |

### 2026-05-07 sprint (Phases A–J)

| Phase | Feature | Commit |
|-------|---------|--------|
| A | Node.js 20→24 CI upgrade + install script auto-download + traffic quota bars | `d69d088` |
| B | File sandbox fix, Auth 401, Diagnostics discovery, Updater hardening | `56e0713` |
| C | Docker SDK: 9 new methods (images/run/logs/stats/volumes/networks) | `9f7291c` |
| D | Sing-box TLS fingerprint→uTLS + singbox_test.go (5 tests) | `a081c82` |
| E | WARP/WireGuard Outbound model, API, Sing-box integration, UI | `c943649` |
| — | 3x-ui Import UI + ECharts bandwidth chart + Telegram/Webhook notifications | `4a38d0b` |
| J | Built-in web server (Sites): reverse proxy, static files, redirect, TLS | `978c9cf` |
| — | `.dockerignore` fix for vps_check.sh in Docker build context | `b68d08a` |

---

## Remaining Work

### Phase K · Smart Deploy Phase 2 & 3
**Priority: 🟢 Low · Deferred by design**

> Based on `docs/superpowers/specs/2026-04-21-smart-deploy-design.md`.

#### K1. Rule-Based Preset Recommendation (Phase 2)
- Backend: env probe results → scoring → recommended preset with reasoning text
- Frontend: `SmartDeploy.vue` — recommendation card before user selects preset

#### K2. Protocol Completeness (Phase 3)
- ShadowTLS, NaïveProxy, AnyTLS presets in Smart Deploy
- Post-deploy latency/throughput benchmark

---

### Backlog — Small Improvements

| Item | Area | Size |
|------|------|------|
| Monthly traffic auto-reset for clients | Backend + Frontend | S |
| Batch operations on client list (multi-select) | Frontend | S |
| Port conflict detection on inbound create | Backend | S |
| Prometheus metrics endpoint (`GET /metrics`) | Backend | M |
| IPv6 panel dual-stack listening (`[::]:PORT`) | Backend | S |
| Self-service client subscription portal | Frontend | M |
| Search/filter on all list tables | Frontend | S |
| Per-inbound traffic reset button | Frontend | S |
| Telegram bot command interface (`/status`, `/clients`) | Backend | M |
| Multi-admin / role-based access | Backend + Frontend | L |

---

## Development Conventions

- All new backend handlers follow the `router.go` pattern: `config.DB` global, `c.JSON(code, gin.H{...})` responses, `recordAudit()` for mutating ops
- All new frontend pages use: `useToast`, `useConfirm`, `useValidation` composables
- New GORM models: non-fatal `AutoMigrate` in `config/db.go`
- Each change must pass `go test ./...` and `npm run build` before merging
- No `Co-Authored-By: Claude` trailers in commits

---

## CI Status

| Job | Current State |
|-----|---------------|
| `backend-verify` | ✅ passing |
| `frontend-verify` | ✅ passing |
| `build-and-push-image` | ✅ passing |

Last successful run: `b68d08a` (2026-05-07)
