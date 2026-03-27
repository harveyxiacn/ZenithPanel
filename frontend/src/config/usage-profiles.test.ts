import assert from 'node:assert/strict'
import test from 'node:test'

import {
  dashboardViewForProfile,
  navigationForProfile,
  normalizeUsageProfile,
  profileHomeRoute,
} from './usage-profiles'

test('normalizeUsageProfile falls back to mixed for unknown values', () => {
  assert.equal(normalizeUsageProfile(undefined), 'mixed')
  assert.equal(normalizeUsageProfile('weird'), 'mixed')
  assert.equal(normalizeUsageProfile('personal_proxy'), 'personal_proxy')
})

test('profileHomeRoute maps each profile to the expected landing page', () => {
  assert.equal(profileHomeRoute('personal_proxy'), '/nodes')
  assert.equal(profileHomeRoute('vps_ops'), '/servers')
  assert.equal(profileHomeRoute('mixed'), '/dashboard')
})

test('personal_proxy navigation prioritizes proxy routes', () => {
  const items = navigationForProfile('personal_proxy').map((item) => item.href)
  assert.deepEqual(items.slice(0, 3), ['/nodes', '/users', '/dashboard'])
})

test('vps_ops dashboard view emphasizes system health cards', () => {
  const dashboardView = dashboardViewForProfile('vps_ops')
  assert.deepEqual(dashboardView.featuredCardIds, ['cpu', 'memory', 'disk', 'network'])
  assert.equal(dashboardView.primaryAction.href, '/servers')
})
