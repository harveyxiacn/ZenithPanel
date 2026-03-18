# ZenithPanel Review And Implementation Plan

Date: 2026-03-17

## Context

This review was triggered after proxy subscription behavior showed real-world failures on mobile clients:

- Android `V2rayNG` imported nodes but reported `-1ms`
- iOS `Clash Mi` could not access the network in rule mode
- Global/direct switching did not reliably reflect expected VPS egress

An initial hotfix on this working branch already restored the missing "Apply Configuration" execution path so proxy changes can actually reach `xray`.

## Audit Summary

### Confirmed Issues

1. Proxy configuration was not actually applied
- The frontend had an "Apply Configuration" button with no click handler.
- The backend exposed config preview endpoints, but no endpoint to generate and restart the core.
- Result: users could create nodes and clients while `xray` continued running stale config or not running at all.

2. Proxy core process lifecycle was fragile
- `BaseCore.Stop()` only killed the process.
- It did not clear manager state or wait for exit.
- Result: `Restart()` could incorrectly think the core was still running and refuse to start a fresh process.

3. Client traffic limit payload mismatched the backend model
- The frontend submitted `traffic_limit`.
- The backend model binds `total`.
- Result: limits created from the UI were silently ignored.

4. Routing rules ignored the `port` field during config generation
- The data model and UI both support port-based rules.
- The Xray config generator only emitted `domain` and `ip`.
- Result: rules like QUIC blocking by port never took effect.

5. Quick Setup TLS defaults were inconsistent
- Some TLS presets pointed to `/opt/zenithpanel/certs/...`
- The rest of the project stores runtime certs under `/opt/zenithpanel/data/certs/...`
- Result: newly created TLS-based nodes could be invalid out of the box.

6. CI coverage was too narrow
- GitHub Actions only built the Docker image.
- There was no dedicated backend test or frontend build verification job.
- Result: regressions in app code could slip through while Docker still built.

7. Documentation drift
- The README badge points to a workflow filename that does not exist.
- Proxy setup and implementation docs do not reflect the current apply flow and current runtime defaults.

### Residual Risk Not Fully Addressed In This Pass

- `sing-box` support is still substantially less complete than the `xray` path.
- Several frontend views swallow errors aggressively with empty catches; these are lower-risk UX issues but still worth future cleanup.

## Implementation Goals

### Goal 1
Make proxy apply/restart reliable end-to-end.

### Goal 2
Fix correctness bugs that directly affect user-visible proxy behavior.

### Goal 3
Add lightweight observability so operators can tell whether the proxy core is actually running.

### Goal 4
Strengthen CI so routine regressions are caught before merge.

### Goal 5
Bring documentation back in sync with actual product behavior.

## Planned Changes

### Backend

1. Harden proxy core lifecycle management
- Add synchronized command ownership in `BaseCore`
- Clear stale process state during stop/restart
- Track background process exit and release manager state automatically

2. Expose proxy runtime status
- Add a status endpoint returning:
  - running state per engine
  - enabled inbound count
  - enabled client count
  - enabled routing rule count

3. Fix client payload compatibility
- Accept `traffic_limit` as a backward-compatible alias for `total`
- Keep the canonical stored field as `total`

4. Fix routing rule generation
- Support `port`
- Split comma-separated `domain`, `ip`, and `port` values into valid config arrays/fields

5. Keep startup behavior safe
- Auto-start `xray` only when enabled inbounds exist
- Preserve explicit apply behavior for subsequent changes

### Frontend

1. Finish the Apply Configuration flow
- Call the backend apply endpoint
- Show loading and success/error feedback

2. Add proxy runtime status display
- Show whether `xray` is running
- Show current counts for nodes, users, and rules

3. Fix client limit submission
- Submit `total` instead of `traffic_limit`
- Keep the UI label unchanged for user clarity

4. Keep Quick Setup TLS defaults aligned with runtime storage

### CI / Repository Hygiene

1. Expand GitHub Actions
- Run `go test ./...`
- Run frontend production build
- Keep Docker build/publish after verification passes

2. Fix README workflow badge

3. Review `.gitignore`
- Confirm generated binaries, dist output, local DBs, and local logs remain excluded
- Only update if a newly introduced artifact needs coverage

## Validation Plan

1. Backend
- `go test ./...`

2. Frontend
- `npm run build`

3. Functional smoke checks
- Apply proxy config from UI path
- Confirm runtime status endpoint reflects state changes
- Confirm client creation persists `total`
- Confirm generated Xray routing includes `port` when provided

4. CI
- Push branch changes
- Inspect GitHub Actions for:
  - frontend verification
  - backend verification
  - Docker workflow success

## Deliverables

- Code fixes for lifecycle, routing, payload compatibility, and proxy status
- One small operational feature: proxy runtime status in the UI
- Updated docs covering the reviewed behavior
- Clean commit history for this pass
