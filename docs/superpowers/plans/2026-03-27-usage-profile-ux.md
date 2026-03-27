# Usage Profile UX Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a persisted usage-profile setting that changes setup defaults, navigation order, default landing route, and dashboard emphasis without splitting ZenithPanel into separate products.

**Architecture:** Keep one shared backend and one shared route tree. Store a normalized `usage_profile` in panel settings, expose it through existing setup/settings APIs, and let the frontend consume it through a single profile store/composable that drives routing, navigation, and dashboard composition.

**Tech Stack:** Go + Gin + GORM + Vue 3 + TypeScript + Pinia-style state patterns already used in the repo + Vue Router + vue-i18n

---

### Task 1: Persist `usage_profile` In Backend APIs

**Files:**
- Modify: `backend/internal/api/router.go`
- Modify: `backend/internal/config/db.go`
- Test: `backend/internal/api/router_validation_test.go`

- [ ] **Step 1: Write the failing tests**

Add tests that verify setup completion and access settings support the new normalized field:

```go
func TestNormalizeUsageProfileDefaultsToMixed(t *testing.T) {
	cases := map[string]string{
		"":               "mixed",
		"personal_proxy": "personal_proxy",
		"vps_ops":        "vps_ops",
		"mixed":          "mixed",
		"weird":          "mixed",
	}

	for input, want := range cases {
		if got := normalizeUsageProfile(input); got != want {
			t.Fatalf("normalizeUsageProfile(%q) = %q, want %q", input, got, want)
		}
	}
}
```

```go
func TestApplySetupCompletePersistsUsageProfile(t *testing.T) {
	// Use httptest + setup route group, submit usage_profile = personal_proxy,
	// then assert config.GetSetting("usage_profile") == "personal_proxy".
}
```

```go
func TestAdminAccessConfigRoundTripsUsageProfile(t *testing.T) {
	// Seed usage_profile, GET /api/v1/admin/access, assert it is returned.
	// PUT /api/v1/admin/access with usage_profile = vps_ops, assert persisted value.
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
go test ./internal/api -run "TestNormalizeUsageProfileDefaultsToMixed|TestApplySetupCompletePersistsUsageProfile|TestAdminAccessConfigRoundTripsUsageProfile"
```

Expected:

- FAIL because `normalizeUsageProfile` does not exist
- FAIL because setup/admin access routes do not accept or return `usage_profile`

- [ ] **Step 3: Write minimal implementation**

In `backend/internal/api/router.go`, add:

```go
func normalizeUsageProfile(raw string) string {
	switch strings.TrimSpace(strings.ToLower(raw)) {
	case "personal_proxy":
		return "personal_proxy"
	case "vps_ops":
		return "vps_ops"
	case "mixed":
		return "mixed"
	default:
		return "mixed"
	}
}
```

Extend the setup completion request and persistence:

```go
var req struct {
	Username     string `json:"username"`
	Password     string `json:"password"`
	PanelPath    string `json:"panel_path"`
	UsageProfile string `json:"usage_profile"`
}
```

Inside the transaction:

```go
usageProfile := normalizeUsageProfile(req.UsageProfile)
if err := tx.Where("`key` = ?", "usage_profile").
	Assign(model.Setting{Key: "usage_profile", Value: usageProfile}).
	FirstOrCreate(&model.Setting{}).Error; err != nil {
	return err
}
```

Extend `GET /api/v1/admin/access` response:

```go
"usage_profile": normalizeUsageProfile(config.GetSetting("usage_profile")),
```

Extend `PUT /api/v1/admin/access` request and persistence:

```go
UsageProfile *string `json:"usage_profile"`
```

```go
if req.UsageProfile != nil {
	config.SetSetting("usage_profile", normalizeUsageProfile(*req.UsageProfile))
	changed = true
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run:

```bash
go test ./internal/api -run "TestNormalizeUsageProfileDefaultsToMixed|TestApplySetupCompletePersistsUsageProfile|TestAdminAccessConfigRoundTripsUsageProfile"
```

Expected:

- PASS

- [ ] **Step 5: Commit**

```bash
git add backend/internal/api/router.go backend/internal/api/router_validation_test.go
git commit -m "feat: persist usage profile in setup and admin settings"
```

### Task 2: Add Frontend Usage Profile State And API Integration

**Files:**
- Create: `frontend/src/composables/useUsageProfile.ts`
- Modify: `frontend/src/api/system.ts`
- Test: `frontend/src/utils/subscription-links.test.mjs`

- [ ] **Step 1: Write the failing test**

Create a tiny logic test for normalization/mapping in the composable helper:

```ts
import test from 'node:test'
import assert from 'node:assert/strict'
import { normalizeUsageProfile, profileHomeRoute } from '../composables/useUsageProfile'

test('normalizeUsageProfile falls back to mixed', () => {
  assert.equal(normalizeUsageProfile('weird'), 'mixed')
})

test('profileHomeRoute maps profile to landing page', () => {
  assert.equal(profileHomeRoute('personal_proxy'), '/nodes')
  assert.equal(profileHomeRoute('vps_ops'), '/servers')
  assert.equal(profileHomeRoute('mixed'), '/dashboard')
})
```

- [ ] **Step 2: Run test to verify it fails**

Run:

```bash
node --test frontend/src/composables/useUsageProfile.test.mjs
```

Expected:

- FAIL because the module does not exist

- [ ] **Step 3: Write minimal implementation**

In `frontend/src/api/system.ts`, add:

```ts
export function getAccessConfig() {
  return apiClient.get('/v1/admin/access')
}

export function updateAccessConfig(data: any) {
  return apiClient.put('/v1/admin/access', data)
}
```

Create `frontend/src/composables/useUsageProfile.ts`:

```ts
import { computed, ref } from 'vue'
import { getAccessConfig, updateAccessConfig } from '@/api/system'

export type UsageProfile = 'personal_proxy' | 'vps_ops' | 'mixed'

export function normalizeUsageProfile(raw?: string): UsageProfile {
  switch (raw) {
    case 'personal_proxy':
    case 'vps_ops':
    case 'mixed':
      return raw
    default:
      return 'mixed'
  }
}

export function profileHomeRoute(profile: UsageProfile): string {
  switch (profile) {
    case 'personal_proxy':
      return '/nodes'
    case 'vps_ops':
      return '/servers'
    default:
      return '/dashboard'
  }
}

const usageProfile = ref<UsageProfile>('mixed')
const usageProfileLoaded = ref(false)

export function useUsageProfile() {
  async function loadUsageProfile() {
    const res: any = await getAccessConfig()
    usageProfile.value = normalizeUsageProfile(res?.data?.usage_profile)
    usageProfileLoaded.value = true
  }

  async function saveUsageProfile(nextProfile: UsageProfile) {
    await updateAccessConfig({ usage_profile: nextProfile })
    usageProfile.value = nextProfile
  }

  return {
    usageProfile,
    usageProfileLoaded,
    homeRoute: computed(() => profileHomeRoute(usageProfile.value)),
    loadUsageProfile,
    saveUsageProfile,
  }
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run:

```bash
node --test frontend/src/composables/useUsageProfile.test.mjs
```

Expected:

- PASS

- [ ] **Step 5: Commit**

```bash
git add frontend/src/api/system.ts frontend/src/composables/useUsageProfile.ts frontend/src/composables/useUsageProfile.test.mjs
git commit -m "feat: add frontend usage profile state"
```

### Task 3: Add Usage Profile To Setup And Security Preferences

**Files:**
- Modify: `frontend/src/views/SetupWizard.vue`
- Modify: `frontend/src/views/SecurityView.vue`
- Modify: `frontend/src/i18n/locales/en.ts`
- Modify: `frontend/src/i18n/locales/zh-CN.ts`
- Modify: `frontend/src/i18n/locales/zh-TW.ts`
- Modify: `frontend/src/i18n/locales/ja.ts`

- [ ] **Step 1: Write the failing test**

Add a helper test for setup payload shape:

```ts
import test from 'node:test'
import assert from 'node:assert/strict'
import { buildSetupPayload } from '../views/SetupWizard.helpers'

test('buildSetupPayload includes usage_profile', () => {
  const got = buildSetupPayload({
    adminUsername: 'admin',
    newPassword: 'password123',
    customPanelPath: '/zenith',
    usageProfile: 'personal_proxy',
  })

  assert.equal(got.usage_profile, 'personal_proxy')
})
```

- [ ] **Step 2: Run test to verify it fails**

Run:

```bash
node --test frontend/src/views/SetupWizard.helpers.test.mjs
```

Expected:

- FAIL because helper/module does not exist

- [ ] **Step 3: Write minimal implementation**

Update `SetupWizard.vue` form state:

```ts
const form = reactive({
  initialPassword: '',
  adminUsername: '',
  newPassword: '',
  confirmPassword: '',
  customPanelPath: '/zenith',
  customSshPort: 22,
  enable2FA: false,
  usageProfile: 'mixed' as UsageProfile,
})
```

Submit:

```ts
await setupComplete({
  username: form.adminUsername,
  password: form.newPassword,
  panel_path: form.customPanelPath,
  usage_profile: form.usageProfile,
})
```

Add a select/radio UI in setup step 2 and a matching preferences card in `SecurityView.vue` that uses `useUsageProfile()` to load/save the setting.

- [ ] **Step 4: Run tests to verify they pass**

Run:

```bash
npm run build
```

Expected:

- PASS

- [ ] **Step 5: Commit**

```bash
git add frontend/src/views/SetupWizard.vue frontend/src/views/SecurityView.vue frontend/src/i18n/locales/en.ts frontend/src/i18n/locales/zh-CN.ts frontend/src/i18n/locales/zh-TW.ts frontend/src/i18n/locales/ja.ts
git commit -m "feat: expose usage profile in setup and security settings"
```

### Task 4: Make Router, Sidebar, And Dashboard Profile-Aware

**Files:**
- Modify: `frontend/src/router/index.ts`
- Modify: `frontend/src/components/layout/MainLayout.vue`
- Modify: `frontend/src/views/DashboardView.vue`
- Create: `frontend/src/config/usage-profiles.ts`

- [ ] **Step 1: Write the failing test**

Create a config test:

```ts
import test from 'node:test'
import assert from 'node:assert/strict'
import { navigationForProfile } from './usage-profiles'

test('personal_proxy prioritizes proxy routes', () => {
  const items = navigationForProfile('personal_proxy').map(item => item.href)
  assert.deepEqual(items.slice(0, 3), ['/nodes', '/users', '/dashboard'])
})

test('vps_ops prioritizes server routes', () => {
  const items = navigationForProfile('vps_ops').map(item => item.href)
  assert.deepEqual(items.slice(0, 3), ['/servers', '/dashboard', '/nodes'])
})
```

- [ ] **Step 2: Run test to verify it fails**

Run:

```bash
node --test frontend/src/config/usage-profiles.test.mjs
```

Expected:

- FAIL because config module does not exist

- [ ] **Step 3: Write minimal implementation**

Create `frontend/src/config/usage-profiles.ts`:

```ts
import { HomeIcon, ServerIcon, ShieldCheckIcon, GlobeAltIcon, UsersIcon } from '@heroicons/vue/24/outline'

export function navigationForProfile(profile: 'personal_proxy' | 'vps_ops' | 'mixed') {
  const items = {
    dashboard: { key: 'dashboard', href: '/dashboard', icon: HomeIcon },
    servers: { key: 'servers', href: '/servers', icon: ServerIcon },
    nodes: { key: 'proxyNodes', href: '/nodes', icon: GlobeAltIcon },
    users: { key: 'usersSubs', href: '/users', icon: UsersIcon },
    security: { key: 'security', href: '/security', icon: ShieldCheckIcon },
  }

  switch (profile) {
    case 'personal_proxy':
      return [items.nodes, items.users, items.dashboard, items.security, items.servers]
    case 'vps_ops':
      return [items.servers, items.dashboard, items.security, items.nodes, items.users]
    default:
      return [items.dashboard, items.servers, items.nodes, items.users, items.security]
  }
}
```

Use it in `MainLayout.vue`, and in `router/index.ts` change the root redirect to a lightweight redirect component or async hook that resolves `profileHomeRoute`.

In `DashboardView.vue`, branch the visible card groups and quick actions based on `usageProfile.value`.

- [ ] **Step 4: Run tests to verify they pass**

Run:

```bash
node --test frontend/src/config/usage-profiles.test.mjs
npm run build
```

Expected:

- PASS

- [ ] **Step 5: Commit**

```bash
git add frontend/src/router/index.ts frontend/src/components/layout/MainLayout.vue frontend/src/views/DashboardView.vue frontend/src/config/usage-profiles.ts frontend/src/config/usage-profiles.test.mjs
git commit -m "feat: add profile-aware navigation and dashboard"
```

### Task 5: Full Verification And Embedded Frontend Sync

**Files:**
- Modify: `backend/internal/api/dist/*` (generated embed assets)

- [ ] **Step 1: Build frontend**

Run:

```bash
npm run build
```

Expected:

- PASS

- [ ] **Step 2: Sync frontend dist into backend embed directory**

Run:

```bash
rm -rf backend/internal/api/dist
mkdir -p backend/internal/api/dist
cp -R frontend/dist/. backend/internal/api/dist/
```

- [ ] **Step 3: Run full backend verification**

Run:

```bash
go test ./...
```

Expected:

- PASS

- [ ] **Step 4: Run full frontend verification**

Run:

```bash
npm run build
```

Expected:

- PASS

- [ ] **Step 5: Commit**

```bash
git add backend/internal/api/dist
git commit -m "build: refresh embedded frontend assets for usage profiles"
```
