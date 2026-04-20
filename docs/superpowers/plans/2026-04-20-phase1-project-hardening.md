# Phase 1 Project Hardening Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close the highest-risk audit findings in ZenithPanel by fixing the file sandbox boundary, normalizing auth/session failure handling, making the proxy/network check honest, making diagnostics work in real deployments, hardening the updater's helper flow, and removing the known frontend axios advisory.

**Architecture:** This phase keeps the existing product architecture intact and focuses on narrow behavior fixes plus regression coverage. New code is limited to small helpers and tests so that behavior changes are explicit, reviewable, and easy to verify before later phases tackle CI, refactors, and dependency modernization.

**Tech Stack:** Go 1.26, Gin, Docker SDK for Go, Vue 3, TypeScript, Axios, node:test via `tsx`, Vite

---

## File Map

### Backend

- Create: `backend/internal/api/fs_sandbox.go`
  - Canonical sandbox-root helpers used by the file-manager routes.
- Create: `backend/internal/api/fs_sandbox_test.go`
  - Regression tests for sibling-prefix and symlink-escape cases.
- Modify: `backend/internal/api/router.go`
  - Switch file routes to the new sandbox helper.
  - Add injectable network-check transport and explicit `scope` in the network-check response.
  - Return a clear "diagnostics unavailable" error when the script cannot be resolved.
- Modify: `backend/internal/api/router_validation_test.go`
  - Route-level regression test for the network-check endpoint's explicit server scope.
- Modify: `backend/internal/service/diagnostic/diagnostic.go`
  - Replace current-working-directory resolution with deterministic script discovery.
- Create: `backend/internal/service/diagnostic/diagnostic_test.go`
  - Tests for packaged and source-layout script resolution.
- Modify: `backend/internal/service/updater/updater.go`
  - Replace pull-on-check behavior with registry digest inspection.
  - Replace alpine helper runtime `apk add` flow with a helper config built from the panel image.
- Create: `backend/internal/service/updater/updater_test.go`
  - Tests for registry-digest comparison and helper-container config generation.

### Frontend

- Create: `frontend/src/api/session-recovery.ts`
  - Pure auth/session failure policy helper for 401 handling.
- Create: `frontend/src/api/session-recovery.test.ts`
  - Focused TypeScript tests for protected-route 401 handling vs login/setup failures.
- Modify: `frontend/src/api/client.ts`
  - Remove recursive refresh-on-401 behavior and switch to deterministic logout redirect.
- Modify: `frontend/src/api/proxy.ts`
  - Rename the misleading helper to an honest "server network" check while keeping the existing backend path.
- Modify: `frontend/src/views/ProxyView.vue`
  - Update button copy and result handling to describe server public network checks, not proxy validation.
- Modify: `frontend/src/i18n/locales/en.ts`
- Modify: `frontend/src/i18n/locales/ja.ts`
- Modify: `frontend/src/i18n/locales/zh-CN.ts`
- Modify: `frontend/src/i18n/locales/zh-TW.ts`
  - Update translated copy for the renamed network-check action.
- Modify: `frontend/package.json`
  - Add `tsx` for targeted TypeScript test execution.
  - Upgrade `axios` to a non-vulnerable patch line.
- Modify: `frontend/package-lock.json`
  - Refresh the lockfile for `tsx` and the upgraded `axios`.

### Packaging

- Modify: `Dockerfile`
  - Copy `scripts/vps_check.sh` into the runtime image and mark it executable so diagnostics work in containers.

## Plan Boundaries

This plan implements only **Phase 1** of the hardening project. Phase 2 through Phase 5 require their own plans after Phase 1 lands and is re-verified.

### Task 1: Harden the file-manager sandbox boundary

**Files:**
- Create: `backend/internal/api/fs_sandbox.go`
- Create: `backend/internal/api/fs_sandbox_test.go`
- Modify: `backend/internal/api/router.go`

- [ ] **Step 1: Write the failing sandbox regression tests**

```go
package api

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPathWithinSandboxRejectsSiblingPrefix(t *testing.T) {
	root := filepath.Join(t.TempDir(), "home")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir root: %v", err)
	}

	sibling := root + "2"
	if pathWithinSandbox(root, sibling) {
		t.Fatalf("expected sibling path %q to be rejected for root %q", sibling, root)
	}
}

func TestResolveSandboxPathRejectsSymlinkEscape(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "home")
	outside := filepath.Join(base, "outside")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir root: %v", err)
	}
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatalf("mkdir outside: %v", err)
	}

	link := filepath.Join(root, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unsupported on this runner: %v", err)
	}

	if _, ok := resolveSandboxPath(root, filepath.Join(link, "secret.txt")); ok {
		t.Fatalf("expected symlink escape to be rejected")
	}
}
```

- [ ] **Step 2: Run the API tests to verify they fail**

Run: `go test ./internal/api -run "TestPathWithinSandbox|TestResolveSandboxPath" -v`

Expected: FAIL with messages such as `undefined: pathWithinSandbox` and `undefined: resolveSandboxPath`

- [ ] **Step 3: Implement canonical sandbox helpers and switch the routes to them**

```go
package api

import (
	"os"
	"path/filepath"
	"strings"
)

func resolveSandboxPath(root, userPath string) (string, bool) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", false
	}
	resolvedRoot, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		resolvedRoot = absRoot
	}

	candidate, err := filepath.Abs(filepath.Clean(userPath))
	if err != nil {
		return "", false
	}
	resolvedCandidate, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		parent := filepath.Dir(candidate)
		resolvedParent, parentErr := filepath.EvalSymlinks(parent)
		if parentErr != nil {
			return "", false
		}
		resolvedCandidate = filepath.Join(resolvedParent, filepath.Base(candidate))
	}

	if !pathWithinSandbox(resolvedRoot, resolvedCandidate) {
		return "", false
	}
	if info, err := os.Lstat(candidate); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", false
	}

	return candidate, true
}

func pathWithinSandbox(root, candidate string) bool {
	rel, err := filepath.Rel(root, candidate)
	if err != nil {
		return false
	}
	rel = filepath.Clean(rel)
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)))
}
```

```go
func isPathSafe(userPath string) (string, bool) {
	return resolveSandboxPath(fsSandboxRoot, userPath)
}
```

- [ ] **Step 4: Run the API package tests to verify the sandbox logic passes**

Run: `go test ./internal/api -run "TestPathWithinSandbox|TestResolveSandboxPath" -v`

Expected: PASS

- [ ] **Step 5: Commit the sandbox hardening change**

```bash
git add backend/internal/api/fs_sandbox.go backend/internal/api/fs_sandbox_test.go backend/internal/api/router.go
git commit -m "fix: harden file sandbox boundaries"
```

### Task 2: Make 401 handling deterministic and non-recursive

**Files:**
- Modify: `frontend/package.json`
- Modify: `frontend/package-lock.json`
- Create: `frontend/src/api/session-recovery.ts`
- Create: `frontend/src/api/session-recovery.test.ts`
- Modify: `frontend/src/api/client.ts`

- [ ] **Step 1: Add `tsx` so focused TypeScript tests can run without waiting for the later CI phase**

```json
{
  "devDependencies": {
    "tsx": "^4.20.3"
  }
}
```

Run: `npm install --save-dev tsx`

Expected: `package.json` and `package-lock.json` update successfully

- [ ] **Step 2: Write the failing auth/session policy test**

```ts
import assert from 'node:assert/strict'
import test from 'node:test'

import { shouldLogoutOnUnauthorized } from './session-recovery'

test('logs out on unauthorized protected API requests', () => {
  assert.equal(shouldLogoutOnUnauthorized(401, '/v1/inbounds'), true)
})

test('does not redirect login failures back to /login', () => {
  assert.equal(shouldLogoutOnUnauthorized(401, '/v1/login'), false)
  assert.equal(shouldLogoutOnUnauthorized(401, '/setup/login'), false)
})

test('ignores non-401 responses', () => {
  assert.equal(shouldLogoutOnUnauthorized(500, '/v1/inbounds'), false)
})
```

- [ ] **Step 3: Run the focused frontend test to verify it fails**

Run: `npx tsx --test src/api/session-recovery.test.ts`

Expected: FAIL with an error such as `Cannot find module './session-recovery'`

- [ ] **Step 4: Implement the pure auth policy helper and wire it into the Axios interceptor**

```ts
export function shouldLogoutOnUnauthorized(status?: number, requestUrl = ''): boolean {
  if (status !== 401) return false

  return !requestUrl.startsWith('/v1/login') && !requestUrl.startsWith('/setup/login')
}
```

```ts
import axios from 'axios';
import { useAuthStore } from '@/store/auth';
import { shouldLogoutOnUnauthorized } from './session-recovery';

const api = axios.create({
  baseURL: '/api',
  timeout: 10000,
});

api.interceptors.request.use(
  (config) => {
    const authStore = useAuthStore();
    if (authStore.token) {
      config.headers['Authorization'] = `Bearer ${authStore.token}`;
    }
    return config;
  },
  (error) => Promise.reject(error)
);

api.interceptors.response.use(
  (response: any) => response.data,
  async (error: any) => {
    const status = error?.response?.status;
    const requestUrl = String(error?.config?.url || '');

    if (shouldLogoutOnUnauthorized(status, requestUrl)) {
      const authStore = useAuthStore();
      authStore.logout();
      if (window.location.pathname !== '/login') {
        window.location.href = '/login';
      }
    }

    return Promise.reject(error);
  }
);

export default api;
```

- [ ] **Step 5: Run the focused frontend test and the production build**

Run: `npx tsx --test src/api/session-recovery.test.ts`

Expected: PASS

Run: `npm run build`

Expected: PASS

- [ ] **Step 6: Commit the auth/session hardening change**

```bash
git add frontend/package.json frontend/package-lock.json frontend/src/api/session-recovery.ts frontend/src/api/session-recovery.test.ts frontend/src/api/client.ts
git commit -m "fix: make auth 401 handling deterministic"
```

### Task 3: Make the server network check honest in API responses and UI copy

**Files:**
- Modify: `backend/internal/api/router.go`
- Modify: `backend/internal/api/router_validation_test.go`
- Modify: `frontend/src/api/proxy.ts`
- Modify: `frontend/src/views/ProxyView.vue`
- Modify: `frontend/src/i18n/locales/en.ts`
- Modify: `frontend/src/i18n/locales/ja.ts`
- Modify: `frontend/src/i18n/locales/zh-CN.ts`
- Modify: `frontend/src/i18n/locales/zh-TW.ts`

- [ ] **Step 1: Write the failing route-level regression test for explicit server scope**

```go
func TestServerNetworkCheckReturnsExplicitServerScope(t *testing.T) {
	router, token := setupRouterValidationTestServer(t, true)

	oldDo := networkCheckDo
	networkCheckDo = func(req *http.Request) (*http.Response, error) {
		body := io.NopCloser(strings.NewReader(`{"ip":"1.1.1.1","country":"US","org":"Zenith Test"}`))
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       body,
			Header:     make(http.Header),
		}, nil
	}
	defer func() { networkCheckDo = oldDo }()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/proxy/test-connection", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got := resp.Data["scope"]; got != "server_public_network" {
		t.Fatalf("expected scope=server_public_network, got %#v", got)
	}
}
```

- [ ] **Step 2: Run the API test to verify it fails**

Run: `go test ./internal/api -run TestServerNetworkCheckReturnsExplicitServerScope -v`

Expected: FAIL with errors such as `undefined: networkCheckDo` or missing `scope`

- [ ] **Step 3: Add an injectable transport, include explicit scope in the response, and update the frontend copy**

```go
var networkCheckDo = func(req *http.Request) (*http.Response, error) {
	return http.DefaultClient.Do(req)
}
```

```go
resp, err := networkCheckDo(req)
if err != nil {
	c.JSON(200, gin.H{"code": 200, "data": gin.H{
		"success": false,
		"scope":   "server_public_network",
		"error":   fmt.Sprintf("Connection failed: %v", err),
	}})
	return
}

c.JSON(200, gin.H{"code": 200, "data": gin.H{
	"success": true,
	"scope":   "server_public_network",
	"ip":      result["ip"],
	"country": result["country"],
	"org":     result["org"],
}})
```

```ts
export function checkServerPublicNetwork() {
  return apiClient.post('/v1/proxy/test-connection')
}
```

```ts
import { listInbounds, createInbound, updateInbound, deleteInbound, listClients, createClient, deleteClient, listRoutingRules, createRoutingRule, deleteRoutingRule, generateRealityKeys, applyProxyConfig, getProxyStatus, checkServerPublicNetwork } from '@/api/proxy'

async function runServerNetworkCheck() {
  testLoading.value = true
  testResult.value = null
  try {
    const res: any = await checkServerPublicNetwork()
    testResult.value = res.data
  } catch {
    testResult.value = { success: false, scope: 'server_public_network', error: 'Request failed' }
  } finally {
    testLoading.value = false
  }
}
```

```ts
testConnection: 'Check Server Network',
```

- [ ] **Step 4: Run the route test and the frontend build**

Run: `go test ./internal/api -run TestServerNetworkCheckReturnsExplicitServerScope -v`

Expected: PASS

Run: `npm run build`

Expected: PASS

- [ ] **Step 5: Commit the honest network-check change**

```bash
git add backend/internal/api/router.go backend/internal/api/router_validation_test.go frontend/src/api/proxy.ts frontend/src/views/ProxyView.vue frontend/src/i18n/locales/en.ts frontend/src/i18n/locales/ja.ts frontend/src/i18n/locales/zh-CN.ts frontend/src/i18n/locales/zh-TW.ts
git commit -m "fix: label server network checks honestly"
```

### Task 4: Resolve diagnostics scripts deterministically and package them for containers

**Files:**
- Modify: `backend/internal/service/diagnostic/diagnostic.go`
- Create: `backend/internal/service/diagnostic/diagnostic_test.go`
- Modify: `backend/internal/api/router.go`
- Modify: `Dockerfile`

- [ ] **Step 1: Write the failing script-resolution tests**

```go
package diagnostic

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveScriptPathPrefersPackagedScriptNextToExecutable(t *testing.T) {
	base := t.TempDir()
	execDir := filepath.Join(base, "bin")
	scriptPath := filepath.Join(execDir, "scripts", "vps_check.sh")
	if err := os.MkdirAll(filepath.Dir(scriptPath), 0o755); err != nil {
		t.Fatalf("mkdir script dir: %v", err)
	}
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}

	got, err := resolveScriptPath(filepath.Join(execDir, "zenithpanel"), filepath.Join(base, "unused"))
	if err != nil {
		t.Fatalf("resolve script path: %v", err)
	}
	if got != scriptPath {
		t.Fatalf("expected %q, got %q", scriptPath, got)
	}
}

func TestResolveScriptPathFallsBackToRepositoryScripts(t *testing.T) {
	base := t.TempDir()
	workDir := filepath.Join(base, "backend")
	scriptPath := filepath.Join(base, "scripts", "vps_check.sh")
	if err := os.MkdirAll(filepath.Dir(scriptPath), 0o755); err != nil {
		t.Fatalf("mkdir script dir: %v", err)
	}
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}

	got, err := resolveScriptPath(filepath.Join(workDir, "zenithpanel"), workDir)
	if err != nil {
		t.Fatalf("resolve script path: %v", err)
	}
	if got != scriptPath {
		t.Fatalf("expected %q, got %q", scriptPath, got)
	}
}
```

- [ ] **Step 2: Run the diagnostics package tests to verify they fail**

Run: `go test ./internal/service/diagnostic -run "TestResolveScriptPath" -v`

Expected: FAIL with `undefined: resolveScriptPath`

- [ ] **Step 3: Implement deterministic script discovery, explicit unavailability handling, and copy the script into the image**

```go
package diagnostic

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

var ErrDiagnosticScriptUnavailable = errors.New("diagnostic script unavailable")

func resolveScriptPath(executablePath, workingDir string) (string, error) {
	candidates := []string{
		filepath.Join(filepath.Dir(executablePath), "scripts", "vps_check.sh"),
		filepath.Join(filepath.Dir(executablePath), "..", "scripts", "vps_check.sh"),
		filepath.Join(workingDir, "scripts", "vps_check.sh"),
		filepath.Join(workingDir, "..", "scripts", "vps_check.sh"),
	}

	for _, candidate := range candidates {
		info, err := os.Stat(candidate)
		if err == nil && !info.IsDir() {
			return candidate, nil
		}
	}

	return "", ErrDiagnosticScriptUnavailable
}

func RunNetworkDiagnostic() (string, error) {
	execPath, err := os.Executable()
	if err != nil {
		return "", err
	}
	workingDir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	scriptPath, err := resolveScriptPath(execPath, workingDir)
	if err != nil {
		return "", err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, "bash", scriptPath)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	err = cmd.Run()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return out.String(), context.DeadlineExceeded
		}
		return out.String(), err
	}

	return out.String(), nil
}
```

```go
output, err := diagnostic.RunNetworkDiagnostic()
if err != nil {
	if errors.Is(err, diagnostic.ErrDiagnosticScriptUnavailable) {
		c.JSON(http.StatusServiceUnavailable, gin.H{"code": 503, "msg": "Diagnostic script is unavailable in this deployment", "data": output})
		return
	}
	log.Printf("Diagnostic error: %v", err)
	c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "Diagnostic failed", "data": output})
	return
}
```

```dockerfile
COPY scripts/vps_check.sh /opt/zenithpanel/scripts/vps_check.sh
RUN chmod +x /opt/zenithpanel/scripts/vps_check.sh
```

- [ ] **Step 4: Run the diagnostics tests and rebuild the frontend-backed image artifacts**

Run: `go test ./internal/service/diagnostic -run "TestResolveScriptPath" -v`

Expected: PASS

Run: `go test ./internal/api -run TestServerNetworkCheckReturnsExplicitServerScope -v`

Expected: PASS

Run: `docker build -t zenithpanel-phase1-check .`

Expected: PASS

- [ ] **Step 5: Commit the diagnostics hardening change**

```bash
git add backend/internal/service/diagnostic/diagnostic.go backend/internal/service/diagnostic/diagnostic_test.go backend/internal/api/router.go Dockerfile
git commit -m "fix: make diagnostics work across deployments"
```

### Task 5: Remove updater helper fragility and stop update checks from pulling images

**Files:**
- Modify: `backend/internal/service/updater/updater.go`
- Create: `backend/internal/service/updater/updater_test.go`

- [ ] **Step 1: Write the failing updater tests**

```go
package updater

import (
	"strings"
	"testing"

	"github.com/docker/docker/api/types/registry"
	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

func TestNewUpdateInfoUsesRegistryDescriptorDigest(t *testing.T) {
	info := newUpdateInfo(
		"sha256:1111111111111111111111111111111111111111111111111111111111111111",
		registry.DistributionInspect{
			Descriptor: ocispec.Descriptor{
				Digest: digest.Digest("sha256:2222222222222222222222222222222222222222222222222222222222222222"),
			},
		},
	)

	if !info.Available {
		t.Fatalf("expected update to be available")
	}
	if info.CurrentID != "111111111111" {
		t.Fatalf("expected truncated current id, got %q", info.CurrentID)
	}
	if info.LatestID != "222222222222" {
		t.Fatalf("expected truncated latest id, got %q", info.LatestID)
	}
}

func TestBuildHelperContainerConfigUsesPanelImageWithoutApkInstall(t *testing.T) {
	cfg, hostCfg := buildHelperContainerConfig(DefaultImage, "echo swap")

	if cfg.Image != DefaultImage {
		t.Fatalf("expected helper image %q, got %q", DefaultImage, cfg.Image)
	}
	if strings.Contains(strings.Join(cfg.Cmd, " "), "apk add") {
		t.Fatalf("helper command must not install docker-cli at runtime: %#v", cfg.Cmd)
	}
	if len(hostCfg.Binds) != 1 || hostCfg.Binds[0] != "/var/run/docker.sock:/var/run/docker.sock" {
		t.Fatalf("unexpected helper binds: %#v", hostCfg.Binds)
	}
}
```

- [ ] **Step 2: Run the updater tests to verify they fail**

Run: `go test ./internal/service/updater -run "TestNewUpdateInfo|TestBuildHelperContainerConfig" -v`

Expected: FAIL with `undefined: newUpdateInfo` and `undefined: buildHelperContainerConfig`

- [ ] **Step 3: Implement digest-based update checks and a panel-image helper-container builder**

```go
func newUpdateInfo(currentImageID string, inspect registry.DistributionInspect) *UpdateInfo {
	latestImageID := inspect.Descriptor.Digest.String()
	return &UpdateInfo{
		Available: currentImageID != latestImageID,
		CurrentID: truncID(currentImageID),
		LatestID:  truncID(latestImageID),
	}
}

func buildHelperContainerConfig(image, swapScript string) (*container.Config, *container.HostConfig) {
	return &container.Config{
			Image:      image,
			Entrypoint: []string{"sh", "-c"},
			Cmd:        []string{swapScript},
		}, &container.HostConfig{
			Binds:      []string{"/var/run/docker.sock:/var/run/docker.sock"},
			AutoRemove: true,
		}
}
```

```go
func CheckForUpdate(ctx context.Context) (*UpdateInfo, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("docker client: %w", err)
	}
	defer cli.Close()

	containerID, err := getContainerID()
	if err != nil {
		return nil, fmt.Errorf("get container ID: %w", err)
	}

	info, err := cli.ContainerInspect(ctx, containerID)
	if err != nil {
		return nil, fmt.Errorf("inspect container: %w", err)
	}

	distInspect, err := cli.DistributionInspect(ctx, DefaultImage, "")
	if err != nil {
		return nil, fmt.Errorf("inspect registry image: %w", err)
	}

	return newUpdateInfo(info.Image, distInspect), nil
}
```

```go
helperCfg, helperHC := buildHelperContainerConfig(DefaultImage, swapScript)
helperResp, err := cli.ContainerCreate(ctx, helperCfg, helperHC, nil, nil, "zenith-updater")
if err != nil {
	cli.ContainerRemove(ctx, resp.ID, types.ContainerRemoveOptions{})
	cli.ContainerRename(ctx, containerID, containerName)
	return fmt.Errorf("create updater helper: %w", err)
}
```

```go
helperCfg, helperHC := buildHelperContainerConfig(newConfig.Image, swapScript)
helperResp, err := cli.ContainerCreate(ctx, helperCfg, helperHC, nil, nil, "zenith-updater")
if err != nil {
	cli.ContainerRemove(ctx, resp.ID, types.ContainerRemoveOptions{})
	cli.ContainerRename(ctx, containerID, containerName)
	return fmt.Errorf("create helper: %w", err)
}
```

- [ ] **Step 4: Run the updater tests and the full backend suite**

Run: `go test ./internal/service/updater -run "TestNewUpdateInfo|TestBuildHelperContainerConfig" -v`

Expected: PASS

Run: `go test ./...`

Expected: PASS

- [ ] **Step 5: Commit the updater hardening change**

```bash
git add backend/internal/service/updater/updater.go backend/internal/service/updater/updater_test.go
git commit -m "fix: harden updater checks and helper containers"
```

### Task 6: Upgrade axios to a non-vulnerable patch line and re-verify the frontend

**Files:**
- Modify: `frontend/package.json`
- Modify: `frontend/package-lock.json`

- [ ] **Step 1: Reproduce the current dependency advisory**

Run: `npm audit --omit=dev`

Expected: FAIL with a moderate vulnerability reported for `axios`

- [ ] **Step 2: Update `axios` to the fixed patch line and refresh the lockfile**

```json
{
  "dependencies": {
    "axios": "^1.15.1"
  }
}
```

Run: `npm install axios@^1.15.1`

Expected: `package.json` and `package-lock.json` update successfully

- [ ] **Step 3: Re-run the targeted frontend test, frontend build, and audit**

Run: `npx tsx --test src/api/session-recovery.test.ts`

Expected: PASS

Run: `npm run build`

Expected: PASS

Run: `npm audit --omit=dev`

Expected: PASS with `found 0 vulnerabilities`

- [ ] **Step 4: Commit the dependency remediation**

```bash
git add frontend/package.json frontend/package-lock.json
git commit -m "chore: upgrade axios to a patched release"
```

## Self-Review

- **Spec coverage:** Phase 1 requirements from `docs/superpowers/specs/2026-04-20-project-hardening-design.md` are mapped to six executable tasks: sandbox, auth/session, honest network check, diagnostics, updater, and axios remediation.
- **Placeholder scan:** No `TODO`, `TBD`, "similar to", or deferred code placeholders remain in the plan.
- **Type consistency:** New helpers are named consistently across tests and implementation (`resolveSandboxPath`, `pathWithinSandbox`, `shouldLogoutOnUnauthorized`, `newUpdateInfo`, `buildHelperContainerConfig`).

