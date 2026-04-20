# ZenithPanel Project Hardening Design

**Date:** 2026-04-20

**Status:** Approved for specification review

## Goal

Harden ZenithPanel across security, correctness, release engineering, testing, and maintainability so the product is safer to operate, easier to verify, and less fragile to evolve.

## Problem

The current codebase builds and the existing backend tests pass, but the audit found several classes of issues:

- security-sensitive path validation is too weak
- authentication refresh behavior is inconsistent with the actual token model
- some diagnostics and proxy validation flows do not test what they claim to test
- updater and container lifecycle logic have side effects and brittle runtime assumptions
- release artifacts are not fully reproducible
- CI and test coverage are too weak in high-risk areas
- several core files are large enough to slow down safe iteration

The project needs a structured hardening pass that fixes real behavior first, then improves delivery and maintainability without mixing unrelated refactors into one unsafe change set.

## Design Summary

Implement hardening in five phases:

1. **Security and correctness fixes**
2. **Release and reproducibility fixes**
3. **Testing and CI quality gates**
4. **Maintainability refactors**
5. **Dependency modernization**

Each phase must leave the project in a releasable state. Behavior fixes come before structural refactors. Large refactors come before risky dependency upgrades only when those refactors reduce future upgrade risk without changing product behavior.

## Guiding Principles

1. Fix user-visible and security-relevant behavior before code structure.
2. Keep each phase independently verifiable.
3. Do not combine architectural cleanup with behavior changes unless the cleanup is required for the fix.
4. Prefer narrowing semantics over adding new features.
5. Add regression tests for every confirmed audit issue that is practical to automate.
6. Keep rollout reversible: a broken later phase must not invalidate earlier fixes.

## Scope

### In Scope

- file sandbox enforcement
- JWT refresh and session behavior
- proxy connectivity validation behavior
- diagnostics script discovery and packaging
- updater side effects and failure handling
- frontend package vulnerability remediation where low-risk
- Docker build reproducibility improvements
- CI and test coverage improvements
- targeted backend and frontend file decomposition
- dependency upgrades after safety rails are in place

### Out of Scope

- new end-user features unrelated to audit findings
- redesigning auth around a full multi-session identity system
- replacing Gin, Vue, SQLite, or Docker-based deployment
- broad UI redesign outside refactors needed for maintainability
- speculative performance work without measured risk or audit evidence

## Phase Plan

## Phase 1: Security and Correctness

### Objectives

- close the file manager sandbox boundary bug
- make token refresh behavior internally consistent
- remove misleading proxy connectivity validation
- make diagnostics work in actual deployment layouts
- reduce obvious updater failure modes
- patch the known frontend dependency advisory with minimal behavior change

### Design

#### File Sandbox

Replace string-prefix sandbox checks with path-boundary-safe resolution based on canonical paths. Validation must reject:

- sibling paths such as `/home2`
- parent traversal that resolves outside `/home`
- symlink escapes

The sandbox root remains `/home`, but enforcement becomes path-separator aware and canonicalized before access.

#### Auth Refresh Model

The current model behaves like a single access token model, not a true refresh-token model. Phase 1 will normalize behavior to that reality.

Design choice:

- keep a single JWT token model for now
- remove the false assumption that expired tokens can be refreshed by hitting an endpoint protected by the same expired token
- make frontend 401 handling non-recursive and deterministic

The system may still issue a fresh token when the user is authenticated and proactively renewing a valid session, but expired-session recovery should become a clean logout or an explicitly reworked mechanism in a later feature project.

#### Proxy Connectivity Check

The current endpoint must stop claiming that it validates proxy egress if it only validates server egress. Phase 1 should either:

- rename and reposition the endpoint as a server public network check, or
- reimplement it to actually test through the configured proxy path

Recommended approach: rename/re-scope first unless the proxy-manager code already exposes a safe, deterministic routed HTTP client. This keeps the fix honest and low-risk.

#### Diagnostics Script Resolution

Diagnostics should resolve the script relative to the running binary or to a known packaged location, not to the caller's current working directory. Runtime behavior must be valid for:

- source runs from repository root
- source runs from backend directory
- packaged container runs

If the script is optional, the API response should clearly report that diagnostics are unavailable instead of failing with an opaque path error.

#### Updater Reliability

Phase 1 focuses on the most obvious correctness issues, not a full updater redesign:

- separate "check for update" from "pull image" side effects where feasible
- stop depending on helper-container runtime package installation when avoidable
- improve rollback/error reporting around rename/create/start failure boundaries

The goal is to make failures predictable and easier to recover from.

#### Frontend Advisory Remediation

Upgrade `axios` to a non-vulnerable version and verify that request/response interceptors continue to behave correctly.

### Testing

- backend tests for sandbox boundaries and symlink cases
- backend tests for auth refresh behavior and 401 handling expectations where possible
- backend tests for diagnostics path resolution behavior
- backend tests for updater helper configuration logic where practical
- frontend tests for auth interceptor non-recursive logout behavior
- build + existing suite must still pass after `axios` upgrade

## Phase 2: Release and Reproducibility

### Objectives

- make Docker builds more deterministic
- reduce mutable "latest" dependencies in the build path
- align build and runtime packaging with actual deployment assumptions

### Design

#### Docker Build Inputs

Replace mutable install flows with locked inputs where practical:

- prefer `npm ci` over `npm install`
- pin base image tags more explicitly
- stop relying on runtime package installation for updater helper behavior

#### Xray Acquisition

Move from unbounded "latest at build time" behavior to a pinned or explicitly configurable version source. The version should be visible, reproducible, and intentionally changed.

#### Packaging Consistency

Any runtime dependency used by the application, such as diagnostic scripts, must either be embedded, copied into the image, or explicitly marked unavailable in container deployments.

### Testing

- Docker build still succeeds
- image runtime still starts correctly
- updater/diagnostic assumptions match packaged files
- CI build output remains compatible with current release flow

## Phase 3: Testing and CI Quality Gates

### Objectives

- add missing automated checks for already-existing frontend tests
- increase coverage in the riskiest backend services
- make CI reflect the real safety bar for future changes

### Design

#### Frontend Test Entry

Introduce a supported frontend test command and wire the current standalone tests into that command. The tool can be lightweight, but it must be stable in CI.

#### Backend Risk Coverage

Prioritize tests for:

- filesystem access validation
- updater decision logic
- diagnostics behavior
- auth/session handling
- any helper extracted during earlier phases

#### CI Gates

CI should verify at least:

- backend tests
- frontend build
- frontend tests
- backend vet/static checks already in use

If a Linux-capable environment is available in CI, add race detection there instead of relying on developer workstations.

### Testing

The CI workflow itself is part of the deliverable. A successful pipeline run becomes the validation artifact for this phase.

## Phase 4: Maintainability Refactors

### Objectives

- reduce the blast radius of changes in the largest files
- replace unsafe or noisy typing patterns in high-churn frontend code
- prepare the codebase for safer future upgrades

### Design

#### Backend Router Decomposition

Split `backend/internal/api/router.go` by domain while preserving one clear composition entrypoint. Candidate boundaries:

- auth/setup/access
- system/security/admin
- proxy/inbounds/clients/routing
- fs/terminal/docker/diagnostics

The goal is not to invent a new framework, only to reduce file size and isolate concerns.

#### Frontend View Decomposition

Split:

- `frontend/src/views/ProxyView.vue`
- `frontend/src/views/SecurityView.vue`

into focused components and composables. Shared state and API transforms should move into typed helpers when it reduces duplication.

#### Type Tightening

Reduce high-risk `any` usage in auth, proxy, and security flows first. Replace ad hoc response handling with explicit interfaces for the paths touched during refactor.

#### i18n Loading

Move locale bundles toward lazy loading if it can be done without destabilizing boot flow. This is a secondary optimization and should not block the structural split.

### Testing

- route/view behavior remains unchanged
- extracted helpers gain direct unit coverage where useful
- bundle and type-check outputs stay green

## Phase 5: Dependency Modernization

### Objectives

- upgrade stale high-risk backend dependencies
- reduce known vulnerability exposure
- complete upgrades only after earlier phases create enough test protection

### Design

Start with the dependencies that most directly affect exposed runtime behavior:

- Docker SDK and related packages
- Go standard library/toolchain baseline
- selected frontend packages still behind current safe patch lines

This phase should be incremental. Do not batch unrelated major-version jumps unless earlier verification shows they are tightly coupled.

### Testing

- rerun full backend/frontend validation
- rerun vulnerability scanning
- run packaging/build checks again
- confirm no compatibility regressions in updater, Docker manager, auth, and proxy flows

## File Ownership Strategy

This hardening project will touch a broad set of files, but phases should keep write scopes narrow:

- **Phase 1:** auth middleware, API client, router endpoints, diagnostics service, updater service, fs validation, targeted tests, frontend dependency manifests
- **Phase 2:** Dockerfile, release workflow/build scripts, packaging paths
- **Phase 3:** CI workflow, frontend package scripts, test files
- **Phase 4:** router/domain files, extracted Vue components/composables/types
- **Phase 5:** dependency manifests and compatibility fixes

## Error Handling Expectations

- security-related validation failures must be explicit and deterministic
- auth failures must not recurse or silently loop
- diagnostics/unavailable runtime capabilities must fail clearly
- updater failures must preserve or clearly report recoverable state
- CI must fail on the exact phase gate that is violated

## Validation Strategy

Each phase should end with fresh evidence:

- targeted unit/integration tests for the phase
- full backend test run
- frontend build
- frontend test run once introduced
- any phase-specific packaging or vulnerability checks

No later phase should start on the assumption that earlier changes "probably still work."

## Risks

### Risk: Scope Creep

Because the audit touched many concerns, this project could expand into uncontrolled cleanup.

Mitigation:

- keep each phase tied to explicit findings
- defer nice-to-have cleanup unless it directly lowers risk in the current phase

### Risk: Breakage During Refactor

Large-file decomposition can accidentally change behavior.

Mitigation:

- land behavior fixes first
- add regression tests before decomposition
- extract with unchanged interfaces where possible

### Risk: Dependency Upgrade Regressions

The Docker SDK and related stack may require compatibility changes.

Mitigation:

- leave major dependency work until after test and structure improvements
- upgrade in small steps with full re-verification

## Success Criteria

This hardening project is successful when:

- the known sandbox boundary issue is closed
- auth/session behavior is internally consistent
- misleading diagnostics or proxy checks are corrected
- updater and diagnostics are more predictable in real deployments
- builds are more reproducible
- CI covers frontend tests and key backend risk areas
- large high-churn files are meaningfully decomposed
- vulnerable or stale dependencies are reduced with verification evidence

## Recommendation

Execute the hardening work as phased subprojects with explicit verification after every phase.

This gives ZenithPanel the fastest path to safer behavior now while still making room for deeper structural improvements and dependency upgrades without losing control of regressions.
