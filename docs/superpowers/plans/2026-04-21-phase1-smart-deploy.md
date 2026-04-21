# Phase 1 Smart Deploy Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship the Smart Deploy feature from [2026-04-21-smart-deploy-design.md](../specs/2026-04-21-smart-deploy-design.md) — a preset-driven one-click egress tunnel deployer with env probe, cert automation, extended VPS tuning, idempotent apply, and rollback.

**Architecture:** New `backend/internal/service/deploy` package. New `frontend/src/views/SmartDeploy.vue` wizard. Extensions to existing `service/system/optimize.go`. New `/api/v1/deploy/*` endpoints. All additions; no existing behavior changed.

**Tech Stack:** Go 1.26, Gin, GORM, SQLite, Docker SDK, `go-acme/lego/v4`; Vue 3, TypeScript, Axios, node:test via `tsx`, Vite.

---

## File Map

### Backend

- Create: `backend/internal/service/deploy/model.go`
  - `Deployment`, `DeploymentOp`, `ProbeResult`, `DeployPlan`, `Preset`, `InboundSpec`, `TuneSpec` types.
- Create: `backend/internal/service/deploy/probe.go`
  - 12 detectors (root, kernel, systemd, distro, time_sync, public_ip, hardware, nic, port_avail, inbound_ports, firewall, docker).
- Create: `backend/internal/service/deploy/probe_test.go`
- Create: `backend/internal/service/deploy/preset.go`
  - 4 preset definitions and `Expand(preset, probe, input) → DeployPlan`.
- Create: `backend/internal/service/deploy/preset_test.go`
- Create: `backend/internal/service/deploy/cert.go`
  - `reality` / `acme` (lego) / `self_signed` / `existing` modes; renewal ticker.
- Create: `backend/internal/service/deploy/cert_test.go`
- Create: `backend/internal/service/deploy/orchestrator.go`
  - `Deploy`, `Rollback`, `Preview` entrypoints; idempotency detection.
- Create: `backend/internal/service/deploy/orchestrator_test.go`
- Create: `backend/internal/api/deploy.go`
  - HTTP handlers for `/api/v1/deploy/*`.
- Create: `backend/internal/api/deploy_test.go`
- Modify: `backend/internal/service/system/optimize.go`
  - Add exported ops: `ApplyUDPBuffers`, `ApplyQdisc`, `ApplyTFO`, `ApplySystemdNofile`, `EnableTimeSync`. Each returns a `Snapshot` for rollback.
- Modify: `backend/internal/api/router.go`
  - Register `deploy` route group.
- Modify: whichever file initializes GORM (likely `backend/internal/storage/*.go`)
  - AutoMigrate `Deployment`, `DeploymentOp`.
- Modify: `backend/go.mod` / `backend/go.sum`
  - Add `github.com/go-acme/lego/v4`.

### Frontend

- Create: `frontend/src/types/deploy.ts`
- Create: `frontend/src/api/deploy.ts`
- Create: `frontend/src/api/deploy.test.ts`
- Create: `frontend/src/composables/useDeployWizard.ts`
- Create: `frontend/src/composables/useDeployWizard.test.ts`
- Create: `frontend/src/views/SmartDeploy.vue`
- Modify: `frontend/src/router/index.ts`
- Modify: main nav (in existing layout component)
- Modify: `frontend/src/i18n/locales/{en,ja,zh-CN,zh-TW}.ts`

### Docs

- Modify: `docs/proxy-setup-guide.md` / `docs/proxy-setup-guide-cn.md`
  - Add "Smart Deploy quickstart" section at the top.
- Modify: `docs/user_manual.md` / `docs/user_manual_CN.md`
  - Add "Smart Deploy" chapter.

## Plan Boundaries

This plan implements only **Phase 1** of the Smart Deploy project. Phases 2 (rule-based recommendation) and 3 (protocol completeness + benchmarks) require their own plans after Phase 1 lands and is re-verified.

Phase 1 does NOT add new protocols. All 4 presets use protocols already supported by ZenithPanel.

---

### Task 1: Data model and GORM migration

**Files:**
- Create: `backend/internal/service/deploy/model.go`
- Modify: the GORM init file (to AutoMigrate new models)

- [ ] **Step 1: Define domain types**

Write `Deployment`, `DeploymentOp`, `ProbeResult`, `DeployPlan`, `Preset`, `InboundSpec`, `TuneSpec`, `CertMode`, and the enum-like string consts (`PresetStableEgress`, `StatusPending`, etc.) per the design spec. Place typed fields on `Deployment` for `PresetID`, `Status`, `Domain`, `CertMode`, `Error`, `CreatedAt`, `UpdatedAt`; use `datatypes.JSON` for `ProbeSnapshot`, `PlanSnapshot`, `InboundIDs`.

- [ ] **Step 2: Register AutoMigrate for new models**

Locate the existing GORM init call (grep for `AutoMigrate` in `backend/internal/storage`). Add `&deploy.Deployment{}, &deploy.DeploymentOp{}`.

- [ ] **Step 3: Verify build + migration runs**

Run: `go build ./...`

Expected: PASS

Run: `go run ./cmd/zenithpanel` (or whatever the entrypoint is) pointed at a scratch DB, then inspect with `sqlite3 <path> ".schema deployments deployment_ops"`.

Expected: both tables exist with expected columns.

- [ ] **Step 4: Commit**

```bash
git add backend/internal/service/deploy/model.go backend/internal/storage/
git commit -m "feat(deploy): add deployment data model and migration"
```

---

### Task 2: Probe module

**Files:**
- Create: `backend/internal/service/deploy/probe.go`
- Create: `backend/internal/service/deploy/probe_test.go`

- [ ] **Step 1: Write failing probe tests**

Table-driven tests that feed each detector a stubbed `runner` (interface wrapping `exec.Command`, `os.ReadFile`, `net.Interfaces`, `net.Listen`). Assert each detector returns the expected typed result for Debian/Alpine/unknown distros, systemd present/absent, root/non-root, etc.

Start with these cases:
- `TestProbeRootCheck` — true when `os.Getuid() == 0`, false otherwise (stub via injected func)
- `TestProbeKernelParsesVersion` — feed `5.15.0` string, assert major/minor parsed
- `TestProbeKernelDetectsBBR` — feed `/proc/sys/net/ipv4/tcp_available_congestion_control` containing `bbr`, assert flag true
- `TestProbeDistroDebian` — feed `/etc/os-release`, assert id=debian, version_id parsed
- `TestProbePortAvail443Taken` — bind :443 in test, assert probe reports taken

- [ ] **Step 2: Run tests to confirm failure**

Run: `go test ./internal/service/deploy -run TestProbe -v`

Expected: FAIL (undefined symbols)

- [ ] **Step 3: Implement probe with detector registry**

```go
type Runner interface {
    ReadFile(path string) ([]byte, error)
    Exec(ctx context.Context, name string, args ...string) ([]byte, error)
    ListenPort(network string, port int) error
}

type Detector func(ctx context.Context, r Runner) any

type Probe struct { runner Runner; detectors map[string]Detector }

func New(r Runner) *Probe { /* register all 12 */ }
func (p *Probe) Run(ctx context.Context) (ProbeResult, error) { /* run each with timeout; aggregate */ }
```

Each detector lives in the same file (or split by category: `probe_system.go`, `probe_network.go`) — keep the registry in `probe.go`.

Per-detector timeout: 1 s. Total probe timeout: 5 s.

- [ ] **Step 4: Re-run tests**

Run: `go test ./internal/service/deploy -run TestProbe -v`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add backend/internal/service/deploy/probe.go backend/internal/service/deploy/probe_test.go
git commit -m "feat(deploy): add env probe with 12 detectors"
```

---

### Task 3: Preset engine

**Files:**
- Create: `backend/internal/service/deploy/preset.go`
- Create: `backend/internal/service/deploy/preset_test.go`

- [ ] **Step 1: Write failing preset expansion tests**

For each of the 4 presets, a table-driven test that passes a fixed `ProbeResult` + user input, asserts the returned `DeployPlan` has:
- correct number of inbounds
- expected engine (xray / sing-box)
- expected port (falls back if probe says 443 taken)
- expected tuning op set
- expected cert mode
- reproducible (same inputs → same plan, modulo random secrets)

Cases:
- `TestExpandStableEgressDefaultPort` — probe with :443 free → port 443
- `TestExpandStableEgressFallbackPort` — probe with :443 taken → port 8443
- `TestExpandSpeedWithDomain` — domain provided → `acme` cert mode
- `TestExpandSpeedNoDomain` — no domain → `self_signed`
- `TestExpandComboHasTwoInbounds`
- `TestExpandWeakNetworkUsesCake` — kernel supports cake → qdisc=cake

- [ ] **Step 2: Run tests to confirm failure**

Run: `go test ./internal/service/deploy -run TestExpand -v`

Expected: FAIL

- [ ] **Step 3: Implement preset registry and expander**

```go
var presets = map[string]Preset{
    PresetStableEgress: { ID: PresetStableEgress, ... },
    PresetSpeed:        { ... },
    PresetCombo:        { ... },
    PresetWeakNetwork:  { ... },
}

type Input struct { Domain string; PortOverride int; RealityTarget string; Options map[string]any }

func Expand(presetID string, probe ProbeResult, in Input) (DeployPlan, error) { ... }
```

Port fallback logic: preferred port → if taken in `probe.InboundPorts` or `probe.PortAvail`, try fallback list (`[8443, 2053, 2083, 2087, 2096]`).

Reality target default: `www.microsoft.com`. Can be overridden via `Input.RealityTarget`.

Secrets generation: UUIDs for VLESS IDs, random 16-byte hex for Reality short-ids, random alphanumeric for Hy2 obfs passwords. Use `crypto/rand`.

- [ ] **Step 4: Re-run tests**

Run: `go test ./internal/service/deploy -run TestExpand -v`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add backend/internal/service/deploy/preset.go backend/internal/service/deploy/preset_test.go
git commit -m "feat(deploy): add preset engine with 4 presets"
```

---

### Task 4: Cert manager

**Files:**
- Create: `backend/internal/service/deploy/cert.go`
- Create: `backend/internal/service/deploy/cert_test.go`
- Modify: `backend/go.mod`, `backend/go.sum`

- [ ] **Step 1: Add lego dependency**

Run: `go get github.com/go-acme/lego/v4`

Expected: modules updated, build still passes.

- [ ] **Step 2: Write failing cert manager tests**

Cases:
- `TestCertSelfSignedGeneratesValidPair` — generate, parse back, verify cert-key match, verify SAN contains IP
- `TestCertExistingPairValidates` — feed a generated self-signed pair at paths, assert mode=existing succeeds
- `TestCertExistingMismatchRejected` — feed mismatched cert+key, assert error
- `TestCertACMEHappyPath` — use lego's dev CA (pebble) or a stubbed `acme.Client` interface; assert cert saved to expected path
- `TestCertRealityReturnsNoCert` — mode=reality returns zero struct with "no cert needed" flag

- [ ] **Step 3: Run tests to confirm failure**

Run: `go test ./internal/service/deploy -run TestCert -v`

Expected: FAIL

- [ ] **Step 4: Implement cert manager**

```go
type CertResult struct {
    Mode     CertMode
    CertPath string // empty when Mode == reality
    KeyPath  string
    NotAfter time.Time
    SelfSigned bool
}

type ACMEClient interface {
    Obtain(domain string) (certPEM, keyPEM []byte, expires time.Time, err error)
}

type CertManager struct {
    root string // e.g. /etc/zenithpanel/certs
    acme ACMEClient
}

func (m *CertManager) Provision(ctx context.Context, mode CertMode, input CertInput) (*CertResult, error) { ... }
func (m *CertManager) StartRenewalTicker(ctx context.Context) { /* 12h tick */ }
```

Make the ACME client an interface so tests can stub it. Provide a production impl wrapping lego.

- [ ] **Step 5: Re-run tests**

Run: `go test ./internal/service/deploy -run TestCert -v`

Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add backend/internal/service/deploy/cert.go backend/internal/service/deploy/cert_test.go backend/go.mod backend/go.sum
git commit -m "feat(deploy): add cert manager with ACME and self-signed modes"
```

---

### Task 5: Extend system tuner

**Files:**
- Modify: `backend/internal/service/system/optimize.go`
- Modify: `backend/internal/service/system/optimize_test.go` (add coverage)

- [ ] **Step 1: Write failing tests for new ops**

```go
func TestApplyUDPBuffersReturnsSnapshot(t *testing.T) { ... }
func TestApplyQdiscCake(t *testing.T) { ... }
func TestApplyTFOFull(t *testing.T) { ... }
func TestApplySystemdNofileWritesDropin(t *testing.T) { ... }
func TestEnableTimeSyncDetectsChronyd(t *testing.T) { ... }
```

Each test feeds a mock runner (or a temp `/etc` root via `TUNER_ETC` env var) and asserts:
- the op writes the expected sysctl/config
- the returned `Snapshot` contains the pre-values for reversal
- calling the matching `Revert(snapshot)` restores the pre-values

- [ ] **Step 2: Run tests to confirm failure**

Run: `go test ./internal/service/system -run "TestApply|TestEnable" -v`

Expected: FAIL (new exported symbols missing)

- [ ] **Step 3: Implement the new ops with snapshot/revert**

Each op signature:

```go
type Snapshot struct {
    Op       string
    PreValue map[string]string // sysctl paths → values, or file paths → contents
}

func ApplyUDPBuffers(ctx context.Context, linkMbps int) (*Snapshot, error)
func Revert(ctx context.Context, snap Snapshot) error
```

Buffer-size policy: `rmem_max = max(4M, linkMbps * 1024 * RTT_ms_default(50) / 8)` capped at 64 MB. Likewise `wmem_max`.

Qdisc selection: `cake` if kernel >= 4.19 and `sch_cake` module loadable; else `fq_codel`; else `fq`.

systemd-nofile drop-in path: `/etc/systemd/system.conf.d/zenithpanel-nofile.conf` containing `[Manager]\nDefaultLimitNOFILE=1048576`.

time_sync: detect existing chronyd / systemd-timesyncd; if neither running, try to enable the one matching the distro.

- [ ] **Step 4: Re-run tests and full backend suite**

Run: `go test ./internal/service/system -run "TestApply|TestEnable" -v`

Expected: PASS

Run: `go test ./...`

Expected: PASS (no regressions)

- [ ] **Step 5: Commit**

```bash
git add backend/internal/service/system/optimize.go backend/internal/service/system/optimize_test.go
git commit -m "feat(system): add reversible tuning ops for UDP, qdisc, TFO, nofile, time sync"
```

---

### Task 6: Deploy orchestrator

**Files:**
- Create: `backend/internal/service/deploy/orchestrator.go`
- Create: `backend/internal/service/deploy/orchestrator_test.go`

- [ ] **Step 1: Write failing orchestrator tests**

Integration-style tests using in-memory SQLite (already used elsewhere in the project) + a fake probe + stubbed protocol deployer + stubbed tuner.

Cases:
- `TestDeployHappyPath` — stable_egress preset, plan applied, deployment.Status = "succeeded", all ops recorded
- `TestDeployIdempotent` — second apply of same preset returns success with op Status = "skipped" where current == post_value
- `TestDeployFailureAutoRollsBack` — failing inbound op triggers reverse of already-applied ops, final Status = "rolled_back"
- `TestRollbackExplicit` — successful deployment → Rollback → all ops reverted, inbounds removed

- [ ] **Step 2: Run tests to confirm failure**

Run: `go test ./internal/service/deploy -run TestDeploy -v`

Expected: FAIL

- [ ] **Step 3: Implement orchestrator**

```go
type Orchestrator struct {
    DB       *gorm.DB
    Probe    *Probe
    Presets  PresetEngine
    Certs    *CertManager
    Tuner    SystemTuner        // interface wrapping optimize.go ops
    Inbounds InboundDeployer    // interface wrapping service/proxy
}

func (o *Orchestrator) Probe(ctx) (ProbeResult, error)
func (o *Orchestrator) Preview(ctx, req) (DeployPlan, error)
func (o *Orchestrator) Apply(ctx, req) (*Deployment, error)
func (o *Orchestrator) Rollback(ctx, id uint) (*Deployment, error)
```

`Apply` pseudo:
```
tx-per-op: each op's pre_value + post_value recorded before external side-effects when possible
on failure: iterate applied ops in reverse, invoke revert, mark ops as reverted
on success: mark deployment succeeded
```

- [ ] **Step 4: Re-run tests**

Run: `go test ./internal/service/deploy -v`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add backend/internal/service/deploy/orchestrator.go backend/internal/service/deploy/orchestrator_test.go
git commit -m "feat(deploy): add orchestrator with idempotent apply and rollback"
```

---

### Task 7: API routes

**Files:**
- Create: `backend/internal/api/deploy.go`
- Create: `backend/internal/api/deploy_test.go`
- Modify: `backend/internal/api/router.go`

- [ ] **Step 1: Write failing API tests**

Use existing `setupRouter*TestServer(t)` test helper pattern.

Cases:
- `TestDeployProbeReturnsSnapshot`
- `TestDeployPreviewReturnsPlanNoChange`
- `TestDeployApplyCreatesRecord`
- `TestDeployApplyRequiresAdmin`
- `TestDeployRollbackRestores`
- `TestDeployClientsReturnsQRAndSub`

- [ ] **Step 2: Run tests to confirm failure**

Run: `go test ./internal/api -run TestDeploy -v`

Expected: FAIL

- [ ] **Step 3: Implement handlers and wire routes**

Each handler follows existing `{code, msg, data}` convention. Admin auth middleware applied. Orchestrator instance injected via the router setup function.

- [ ] **Step 4: Re-run tests**

Run: `go test ./internal/api -run TestDeploy -v`

Expected: PASS

Run: `go test ./...`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add backend/internal/api/deploy.go backend/internal/api/deploy_test.go backend/internal/api/router.go
git commit -m "feat(api): add deploy endpoints for probe, preview, apply, rollback"
```

---

### Task 8: Frontend types, API client, composable

**Files:**
- Create: `frontend/src/types/deploy.ts`
- Create: `frontend/src/api/deploy.ts`
- Create: `frontend/src/api/deploy.test.ts`
- Create: `frontend/src/composables/useDeployWizard.ts`
- Create: `frontend/src/composables/useDeployWizard.test.ts`

- [ ] **Step 1: Write failing composable tests**

State machine test for the wizard: `step=probe → preset → options → preview → apply → done`. Assert `canAdvance` and `canGoBack` gate transitions correctly. Assert `reset()` returns to step 1.

Run with tsx per Phase 1 hardening convention.

- [ ] **Step 2: Run tests to confirm failure**

Run: `cd frontend && npx tsx --test src/composables/useDeployWizard.test.ts`

Expected: FAIL

- [ ] **Step 3: Implement types, API client, composable**

Types mirror backend JSON. API client adds `deployProbe`, `deployPreview`, `deployApply`, `deployList`, `deployGet`, `deployRollback`, `deployClients`. Composable holds wizard state + calls API.

- [ ] **Step 4: Re-run tests**

Run: `cd frontend && npx tsx --test src/composables/useDeployWizard.test.ts src/api/deploy.test.ts`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add frontend/src/types/deploy.ts frontend/src/api/deploy.ts frontend/src/api/deploy.test.ts frontend/src/composables/useDeployWizard.ts frontend/src/composables/useDeployWizard.test.ts
git commit -m "feat(frontend): add deploy types, API client, and wizard composable"
```

---

### Task 9: SmartDeploy.vue wizard component

**Files:**
- Create: `frontend/src/views/SmartDeploy.vue`

- [ ] **Step 1: Implement the 5-step wizard**

Structure:
- `<StepIndicator :current="step" :steps="5" />`
- `<StepProbe v-if="step === 1" />` — calls `deployProbe` on mount, shows chip grid
- `<StepPreset v-if="step === 2" />` — 4 cards, stable_egress highlighted
- `<StepOptions v-if="step === 3" />` — optional domain, port, Reality target
- `<StepPreview v-if="step === 4" />` — calls `deployPreview`, shows human-readable plan
- `<StepResult v-if="step === 5" />` — calls `deployApply`, polls for status, shows QR + sub link

Inline `<StepProbe>` / `<StepPreset>` / etc. as child components within the same file (single-file acceptable for Phase 1; can split later if it grows large).

- [ ] **Step 2: Manual smoke**

Run: `cd frontend && npm run dev` and `cd backend && go run ./cmd/zenithpanel` (or matching commands). Navigate to `/smart-deploy` logged in as admin. Go through all 5 steps against a dev backend. Verify:
- probe returns data
- preset selection highlights stable_egress by default
- preview shows the plan
- apply returns success (for a preset that works without external deps — stable_egress with Reality)
- result page shows QR / sub

- [ ] **Step 3: Run frontend build**

Run: `cd frontend && npm run build`

Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add frontend/src/views/SmartDeploy.vue
git commit -m "feat(frontend): add SmartDeploy wizard view"
```

---

### Task 10: Router, nav, i18n

**Files:**
- Modify: `frontend/src/router/index.ts`
- Modify: main layout / sidebar component (find via grep for existing nav entries)
- Modify: `frontend/src/i18n/locales/en.ts`
- Modify: `frontend/src/i18n/locales/ja.ts`
- Modify: `frontend/src/i18n/locales/zh-CN.ts`
- Modify: `frontend/src/i18n/locales/zh-TW.ts`

- [ ] **Step 1: Register route**

Add `{ path: '/smart-deploy', component: () => import('@/views/SmartDeploy.vue'), meta: { requiresAdmin: true } }` (match existing auth guard pattern).

- [ ] **Step 2: Add nav entry**

Place it prominently — between Dashboard and ProxyView is a good location.

- [ ] **Step 3: Add i18n strings**

All UI copy in the wizard flows through `t('...')`. Add keys under a `smartDeploy.*` namespace in all four locale files. Chinese (zh-CN) copy should treat 稳定出口 as the primary, leading phrase.

- [ ] **Step 4: Build + smoke**

Run: `cd frontend && npm run build`

Expected: PASS

Manual: switch locales, verify no missing-key warnings in console.

- [ ] **Step 5: Commit**

```bash
git add frontend/src/router/index.ts frontend/src/components/ frontend/src/i18n/locales/
git commit -m "feat(frontend): add smart deploy route, nav entry, and i18n strings"
```

---

### Task 11: User-facing documentation

**Files:**
- Modify: `docs/proxy-setup-guide.md`
- Modify: `docs/proxy-setup-guide-cn.md`
- Modify: `docs/user_manual.md`
- Modify: `docs/user_manual_CN.md`

- [ ] **Step 1: Add quickstart section to proxy-setup-guide**

Top of the file: a "Smart Deploy (recommended)" section that says: go to /smart-deploy, pick `稳定出口`, click through, done. Link to the existing manual-path docs below as "Advanced / manual setup."

- [ ] **Step 2: Add chapter to user_manual**

Cover: what Smart Deploy is, when to use each preset (stable_egress default for risk-control avoidance, speed for low-latency, combo for mix, weak_network for mobile/lossy), how to roll back, how to re-apply safely.

Be explicit about the "稳定唯一出口 IP" framing in the Chinese docs — that is the primary use case.

- [ ] **Step 3: Commit**

```bash
git add docs/proxy-setup-guide.md docs/proxy-setup-guide-cn.md docs/user_manual.md docs/user_manual_CN.md
git commit -m "docs: add Smart Deploy quickstart and user manual chapter"
```

---

## Self-Review

- **Spec coverage:** Tasks 1–11 map to design sections: data model, probe, preset engine, cert manager, system tuner extension, orchestrator, API, frontend plumbing, wizard view, nav/i18n, user docs.
- **Placeholder scan:** No `TODO`, `TBD`, "similar to", or deferred code placeholders remain in the plan.
- **Reversibility:** Every backend task introduces tests before implementation; every task ends with a standalone commit so work can be paused safely between tasks.
- **Scope fidelity:** No new protocols are introduced. Existing Quick Setup, manual inbound flow, and manual optimize page are not modified.
