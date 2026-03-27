import assert from 'node:assert/strict'
import test from 'node:test'

import { buildSetupPayload } from './SetupWizard.helpers'

test('buildSetupPayload includes usage_profile in the setup request', () => {
  const got = buildSetupPayload({
    adminUsername: 'admin',
    newPassword: 'password123',
    customPanelPath: '/zenith',
    usageProfile: 'personal_proxy',
  })

  assert.equal(got.usage_profile, 'personal_proxy')
})

test('buildSetupPayload normalizes invalid usage profiles to mixed', () => {
  const got = buildSetupPayload({
    adminUsername: 'admin',
    newPassword: 'password123',
    customPanelPath: '/zenith',
    usageProfile: 'unexpected',
  })

  assert.equal(got.usage_profile, 'mixed')
})
