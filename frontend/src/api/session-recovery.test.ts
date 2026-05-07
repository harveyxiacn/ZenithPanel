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
