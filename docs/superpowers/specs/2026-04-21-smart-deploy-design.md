# ZenithPanel Smart Deploy Design

**Date:** 2026-04-21

**Status:** Approved (user gave final authority on 2026-04-21)

## Goal

Make ZenithPanel deploy a stable, unique-IP egress tunnel from a single user choice, including VPS tuning, cert automation, and client subscription, with idempotent re-apply and one-click rollback.

## Primary Use Case

The user operates one VPS with a fixed public IP. They want a reliable personal egress tunnel so that websites and apps see a consistent IP tied to their identity.

**Adversary model:** application-level risk control (banks, e-commerce, trading platforms, SaaS) that flags IP instability or shared-proxy signatures. **Not** the GFW or DPI (secondary concern, addressed by the stealth-leaning preset and Phase 2 recommendation).

**Non-goals for the primary use case:**
- IP rotation or pooling
- Multi-hop chains
- Per-site routing (already supported by existing routing rules; unchanged)

## Problem

ZenithPanel today has the pieces — protocols (VLESS+Reality, Trojan, Hysteria2, TUIC), foundational VPS tuning (BBR, sysctl, swap), Quick Setup — but the end-to-end flow is:

1. Install panel
2. Quick Setup creates admin and picks UI layout only
3. User manually creates an inbound, generates Reality keys, picks ports, writes a cert path
4. User manually enables tuning from a separate page
5. User manually generates client configs per client

For a user whose priority is "stable tunnel that just works," the current flow is friction.

## Design Summary

Introduce a **Smart Deploy** feature that orchestrates:

```
probe → select preset → (optional) domain → cert → tune → deploy inbound → subscription
```

Shipped in three phases:

| Phase | Scope | Status |
|---|---|---|
| **1** | Preset-driven deploy, 4 presets, env probe, cert automation, extended tuning, rollback | **this doc** |
| 2 | Rule-based recommendation (probe → recommended preset + reasoning) | deferred |
| 3 | Protocol completeness (ShadowTLS, WireGuard, NaiveProxy, AnyTLS, ...) + benchmarks | deferred |

Each phase leaves the product in a shippable state. Phase 2 layers on Phase 1 without changing it. Phase 3 is independent protocol work.

## Guiding Principles

1. **Stability over features.** Reliability and repeatability come before optional knobs.
2. **Every change is reversible.** Snapshot before, rollback on failure or user request.
3. **Transparent, not magic.** Every op is logged with pre/post values visible to the user.
4. **Preset = typed contract.** Presets expand deterministically into operation lists; no hidden heuristics in Phase 1.
5. **Don't break existing flows.** Smart Deploy and manual inbound management coexist.
6. **YAGNI Phase 1.** Skip benchmarks, GFW detection, geo routing — they belong to Phase 2/3.

## Phase 1: Preset-Driven Smart Deploy

### Objectives

- Introduce a Smart Deploy flow independent of Quick Setup
- Ship 4 presets covering the primary use case and two common variants
- Add env probe with 12 detectors, ≤ 5 seconds runtime, read-only
- Add cert automation: ACME (lego) on domain input, self-signed fallback, Reality needs none
- Extend VPS tuning with UDP buffers, qdisc selection, TFO full-on, systemd limits, time sync
- Make every deployment idempotent with snapshot-based rollback
- Generate client subscriptions and QR codes on completion
- End-to-end target: zero to working tunnel ≤ 3 minutes

### Architecture

```
Frontend: SmartDeploy.vue (5-step wizard)
  ↓
API: /api/v1/deploy/*
  ↓
┌───────────────────────────────────────────┐
│ Deploy Orchestrator (new service)         │
│   ├─ Probe             env detection      │
│   ├─ PresetEngine      preset → DeployPlan│
│   ├─ CertManager       ACME / self-signed │
│   ├─ ProtocolDeployer  reuse inbound svc  │
│   ├─ SystemTuner       extended optimize  │
│   └─ RollbackLog       snapshot + undo    │
└───────────────────────────────────────────┘
  ↓
SQLite: deployments + deployment_ops + existing inbounds
```

New package: `backend/internal/service/deploy`. Existing services (`service/proxy`, `service/system`) are reused, not duplicated.

### Presets

Four presets. Each is a typed struct; `PresetEngine.Expand(preset, probe, input) → DeployPlan`.

| ID | Name (display) | Protocols | Target | Needs domain? |
|---|---|---|---|---|
| `stable_egress` | 稳定出口 ⭐ 推荐默认 | VLESS+Reality on 443/TCP | 风控规避、账号稳定、普通科学上网 | No |
| `speed` | 速度优先 | Hysteria2 on 443/UDP | 低延迟、高吞吐 | Optional (self-signed fallback) |
| `combo` | 全能组合 | VLESS+Reality (TCP) + Hysteria2 (UDP) | 想要 TCP+UDP 双入口 | Optional |
| `weak_network` | 移动弱网 | Hysteria2 + TUIC (both UDP) | 手机 4G/5G、丢包高、机场用户 | Optional |

**Why `stable_egress` is the recommended default for the primary use case:**
- Reality mimics a real TLS handshake to a benign target (default `www.microsoft.com:443`); risk engines see standard TLS fingerprint, not a proxy signature
- TCP is the most universally allowed protocol — minimal connectivity surprises
- Single inbound = single IP, single port, no rotation
- No domain requirement → deployable immediately with only the VPS IP
- XTLS-Vision flow gives close-to-native throughput

**Preset definition format (illustrative):**

```go
type Preset struct {
    ID          string
    DisplayName map[string]string // i18n
    Inbounds    []InboundSpec
    Tuning      []TuneSpec
    CertMode    CertMode // reality | acme | self_signed | auto
    Summary     string   // UI hint
}
```

Preset specs live in `preset.go` as Go literals, not config files — typos and drift caught at compile time.

### Probe

Single-pass environment detection. Read-only. Idempotent. Target runtime ≤ 5 s.

| Detector | Drives |
|---|---|
| `root_check` | deployment feasibility (blocker if false) |
| `kernel` (version + bbr / fq / tfo capability flags) | tuning selection and warnings |
| `systemd` (present + version) | service install feasibility |
| `distro` (id + version_id) | package manager, time-sync tool choice |
| `time_sync` (enabled, synced, error) | warn; offer one-click fix |
| `public_ip` (v4 + optional v6) | client config, cert SAN |
| `hardware` (cpu_cores, ram_mb, swap_mb) | buffer sizing |
| `nic` (primary_iface + link_speed_mbps) | buffer sizing |
| `port_avail` (check 80, 443, 8443 + sampled 10000-20000) | port selection |
| `inbound_ports` (set from DB) | collision avoidance |
| `firewall` (ufw / firewalld / nftables / iptables / none) | auto-allow |
| `docker` (running y/n) | informational display only |

**Output:** `ProbeResult` struct serialized as JSON into `deployments.probe_snapshot` for audit.

**Explicitly out of scope for Phase 1:**
- GFW / DPI detection
- Geographic location, ISP / AS
- Bandwidth, latency, jitter benchmarks
- DNS pollution checks
- UDP outbound reachability tests
- MTU discovery

These arrive in Phase 2 or 3.

### Cert Manager

Single responsibility: produce a cert path + key path for the deployer, or signal "no cert needed."

**Modes:**

| Mode | Trigger | Source |
|---|---|---|
| `reality` | Preset uses Reality | None — Reality borrows target site's cert at handshake |
| `acme` | User provided domain + port 80 reachable | lego v4 HTTP-01 → `/etc/zenithpanel/certs/<domain>/` |
| `self_signed` | User has no domain, preset needs TLS | stdlib crypto, SAN = public IP, 10y validity |
| `existing` | User provided cert + key paths | Validate files, validate pair matches |

**Renewal:** daemon tick every 12 h. Renews certs with < 30 days remaining validity. Self-signed certs are not auto-renewed (10-year validity).

**Self-signed UX:** client config generated with `insecure=true` (Hy2) / `skip-cert-verify=true` (TUIC) and a visible warning in the UI.

**Dependency:** `github.com/go-acme/lego/v4` (adds ~5 MB to binary; acceptable tradeoff over implementing ACME from scratch).

### System Tuner (extended)

Extends existing `backend/internal/service/system/optimize.go`. All new ops are reversible with snapshot + restore.

| Op | Sysctl / action | Purpose |
|---|---|---|
| `udp_buffer` | `net.core.rmem_max` / `wmem_max`, scaled to NIC speed | Hy2/TUIC throughput |
| `udp_per_socket` | `net.core.rmem_default` / `wmem_default` | default socket buffers |
| `qdisc` | `net.core.default_qdisc` — fq / fq_codel / cake | fq for BBR, cake for weak_network |
| `tfo_full` | `net.ipv4.tcp_fastopen=3` (client + server) | faster TLS handshakes |
| `somaxconn` | already exists; raise cap | higher burst accept |
| `systemd_nofile` | `DefaultLimitNOFILE=1048576` in `/etc/systemd/system.conf.d/` | proxy server fd headroom |
| `time_sync_enable` | enable `chronyd` or `systemd-timesyncd` | TLS correctness |
| `docker_log` (optional, off by default) | `/etc/docker/daemon.json` log rotation | prevent disk fill |

**Snapshot model:** before every op, `deployment_ops` row is written with `pre_value` JSON. Rollback iterates in reverse and restores each `pre_value`. An op is considered reverted only when its restore command returns success.

### Deploy Orchestrator

Coordinates the pipeline:

```
func Deploy(ctx, req) (*Deployment, error):
    probe := Probe.Run(ctx)
    if !probe.RootCheck.OK: return DEPLOY_BLOCKED
    plan := PresetEngine.Expand(req.PresetID, probe, req.Input)
    dep := CreateDeploymentRecord(plan, probe)
    defer func() { if err != nil: RollbackDeployment(dep) }()
    for seq, op := range plan.Operations:
        if err := ApplyOp(dep, op, seq): return dep.Fail(err)
    return dep.Succeed()
```

**Idempotency:** on re-deploy of the same preset on the same machine, `ApplyOp` short-circuits when the observed current value already equals `post_value`. Ops where current != post but pre == post (meaning something else changed it) proceed and record the new pre-value.

**Rollback:** `POST /api/v1/deploy/:id/rollback` reads ops in reverse order and invokes each op's reverse function. Inbound-creation ops are reversed by inbound deletion. Cert-ACME ops do not revoke the cert (keeps fallback usable) but mark it unmanaged.

**Failure within a deploy:** any op failure aborts the pipeline and auto-rolls back completed ops within the same deployment.

### Data Model

New GORM models, auto-migrated:

```go
type Deployment struct {
    ID            uint
    PresetID      string         // stable_egress | speed | combo | weak_network
    Status        string         // pending | running | succeeded | failed | rolled_back
    ProbeSnapshot datatypes.JSON // full ProbeResult
    PlanSnapshot  datatypes.JSON // expanded DeployPlan
    Domain        string
    CertMode      string         // reality | acme | self_signed | existing
    InboundIDs    datatypes.JSON // []uint
    Error         string
    CreatedAt     time.Time
    UpdatedAt     time.Time
}

type DeploymentOp struct {
    ID           uint
    DeploymentID uint
    Sequence     int
    OpType       string   // probe | tune | cert | inbound | firewall
    OpName       string   // e.g. "sysctl.net.core.rmem_max"
    PreValue     datatypes.JSON
    PostValue    datatypes.JSON
    Status       string   // pending | applied | failed | reverted
    Error        string
    AppliedAt    time.Time
}
```

### API Contract

All endpoints require admin auth. All responses follow the existing `{code, msg, data}` convention.

| Method | Path | Body / Query | Returns |
|---|---|---|---|
| `POST` | `/api/v1/deploy/probe` | — | `ProbeResult` |
| `POST` | `/api/v1/deploy/preview` | `{preset_id, domain?, options?}` | `DeployPlan` (no execution) |
| `POST` | `/api/v1/deploy/apply` | `{preset_id, domain?, options?}` | `Deployment` (id + initial status) |
| `GET` | `/api/v1/deploy/:id` | — | `Deployment` + `Ops[]` |
| `GET` | `/api/v1/deploy` | `?limit=&offset=` | `Deployment[]` |
| `POST` | `/api/v1/deploy/:id/rollback` | — | updated `Deployment` |
| `GET` | `/api/v1/deploy/:id/clients` | — | `{configs[], sub_url, qr_codes[]}` |

### Frontend UX

New route: `/smart-deploy` → `frontend/src/views/SmartDeploy.vue`. Five-step wizard component.

| Step | Content | Backend call |
|---|---|---|
| 1 Probe | Detected env, colored chips (green OK / yellow warn / red blocker) | `POST /deploy/probe` |
| 2 Preset | 4 cards, `stable_egress` highlighted by default, short reasoning per card | — |
| 3 Options | (Optional, skippable) domain, port override, Reality target override | — |
| 4 Preview | Human-readable DeployPlan, "what will change" | `POST /deploy/preview` |
| 5 Apply + Result | Streaming progress, then QR + sub link + download | `POST /deploy/apply` → poll `/deploy/:id` |

Nav entry: add "智能部署 / Smart Deploy" to main menu alongside existing items.

### Testing Strategy

**Backend**

- Probe unit tests with mocked exec + file reads
- PresetEngine expansion tests — golden plan JSON per preset
- CertManager tests — self-signed generation, existing-pair validation, ACME against a local test server (pebble or built-in)
- Orchestrator tests — happy path + rollback + idempotent re-apply (fake probe + stubbed apply)
- SystemTuner — extend existing tests with new ops

**Frontend**

- Wizard state machine tests with `tsx --test` (matches Phase 1 hardening convention)
- API client tests

**Integration**

- A scripted end-to-end run in CI on a throwaway container is deferred to Phase 2. Phase 1 relies on unit + component tests plus manual smoke on a reference VPS.

### Rollout

- Quick Setup and manual inbound flows unchanged
- Smart Deploy is an opt-in entry added to the main menu
- After Smart Deploy lands, Quick Setup's final step can optionally recommend running Smart Deploy (Phase 2 concern, not Phase 1)

### Risks

**Risk: Port 443 already in use by nginx / caddy / another panel.**
Mitigation: probe detects occupied ports; preset engine auto-falls back to 8443 with a visible "fallback port" notice. User can override.

**Risk: ACME fails due to DNS misconfig or port 80 blocked.**
Mitigation: probe tests port 80; if unreachable, offer self-signed path with clear warning. Never silently swallow ACME errors.

**Risk: System tuning destabilizes other services.**
Mitigation: every op is snapshot + reversible. Rollback is one click. Presets default to conservative values; aggressive tuning is behind the `weak_network` preset.

**Risk: Self-signed cert + Hy2 forces `insecure=true` client flag.**
Mitigation: client config and UI explicitly mark this as self-signed; domain path is shown as the recommended upgrade.

**Risk: Idempotent re-apply surprises user by changing something they manually edited.**
Mitigation: before apply, if a post-value slot holds a non-default value not written by a prior ZenithPanel deploy, show a diff and require confirmation.

### Success Criteria

Phase 1 is successful when:

- A new ZenithPanel install can go from zero to a working egress tunnel in ≤ 3 minutes using Smart Deploy
- All 4 presets successfully deploy on a reference VPS (1 vCPU / 1 GB RAM / 1 Gbps Debian 12)
- Idempotent re-apply of the same preset is a no-op at the op level
- Rollback of any successful deployment restores sysctl, services, and removes created inbounds
- Probe completes in < 5 s on the reference VPS
- ACME cert issuance succeeds end-to-end with a valid domain
- Client can connect using a QR-scanned config without manual edits
- Existing features (Quick Setup, manual inbound, manual optimize) continue to work unchanged

## Phase 2: Rule-Based Recommendation (preview)

Layer an "Intelligent Recommendation" engine on top of Phase 1. After probe, apply rules to recommend a preset + explain the reasoning. New detectors: GFW likely, UDP outbound blocked, DNS pollution, ISP blocking patterns. Preset engine is reused unchanged.

## Phase 3: Protocol Completeness + Benchmarks (preview)

Add protocols: ShadowTLS, WireGuard, NaiveProxy, Hysteria v1, AnyTLS, forward HTTP/SOCKS. Add real network benchmarks (RTT, jitter, loss, throughput). New preset variants: `stealth_heavy` (ShadowTLS+SS2022), `vpn_pure` (WireGuard).

## File Ownership

### New files (backend)

- `backend/internal/service/deploy/probe.go`
- `backend/internal/service/deploy/probe_test.go`
- `backend/internal/service/deploy/preset.go`
- `backend/internal/service/deploy/preset_test.go`
- `backend/internal/service/deploy/cert.go`
- `backend/internal/service/deploy/cert_test.go`
- `backend/internal/service/deploy/orchestrator.go`
- `backend/internal/service/deploy/orchestrator_test.go`
- `backend/internal/service/deploy/model.go`
- `backend/internal/api/deploy.go`
- `backend/internal/api/deploy_test.go`

### Modified files (backend)

- `backend/internal/service/system/optimize.go` — new tuning ops
- `backend/internal/api/router.go` — register `/api/v1/deploy/*`
- `backend/internal/storage/*` (GORM init) — AutoMigrate new models
- `backend/go.mod` / `go.sum` — add `github.com/go-acme/lego/v4`

### New files (frontend)

- `frontend/src/views/SmartDeploy.vue`
- `frontend/src/api/deploy.ts`
- `frontend/src/composables/useDeployWizard.ts`
- `frontend/src/types/deploy.ts`
- `frontend/src/api/deploy.test.ts`

### Modified files (frontend)

- `frontend/src/router/index.ts` — add `/smart-deploy` route
- `frontend/src/components/layout/*` (main nav) — add entry
- `frontend/src/i18n/locales/{en,ja,zh-CN,zh-TW}.ts` — add strings

### Docs

- `docs/superpowers/specs/2026-04-21-smart-deploy-design.md` — this spec
- `docs/superpowers/plans/2026-04-21-phase1-smart-deploy.md` — implementation plan
- `docs/proxy-setup-guide{,-cn}.md` — add Smart Deploy quickstart
- `docs/user_manual{,_CN}.md` — add Smart Deploy chapter

## Recommendation

Build Phase 1 in the order: data model → probe → cert manager → system tuner extensions → preset engine → orchestrator → API → frontend → user docs. Each slice lands with tests and is independently mergeable. Defer Phases 2 and 3 until Phase 1 has shipped and received real-world feedback on a live VPS.
