import test from 'node:test'
import assert from 'node:assert/strict'

import { buildSubscriptionLink } from './subscription-links.mjs'

test('buildSubscriptionLink returns generic subscription URL when no format is given', () => {
  const got = buildSubscriptionLink('https://panel.example.com/', 'abc-123')
  assert.equal(got, 'https://panel.example.com/api/v1/sub/abc-123')
})

test('buildSubscriptionLink appends explicit clash format', () => {
  const got = buildSubscriptionLink('https://panel.example.com', 'abc-123', 'clash')
  assert.equal(got, 'https://panel.example.com/api/v1/sub/abc-123?format=clash')
})

test('buildSubscriptionLink appends explicit base64 format', () => {
  const got = buildSubscriptionLink('https://panel.example.com', 'abc-123', 'base64')
  assert.equal(got, 'https://panel.example.com/api/v1/sub/abc-123?format=base64')
})

test('buildSubscriptionLink URL-encodes UUID-like values safely', () => {
  const got = buildSubscriptionLink('https://panel.example.com', 'abc 123', 'base64')
  assert.equal(got, 'https://panel.example.com/api/v1/sub/abc%20123?format=base64')
})
