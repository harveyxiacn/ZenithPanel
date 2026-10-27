import assert from 'node:assert/strict'
import test from 'node:test'
import { waitForUpdateCompletion } from './update-polling'
import type { UpdateStatus } from '../api/system'

const noWait = async () => {}

test('update polling tolerates container downtime and returns committed state', async () => {
  let calls = 0
  const progress: string[] = []
  const status = await waitForUpdateCompletion(async () => {
    calls += 1
    if (calls === 1) throw new Error('connection reset')
    if (calls === 2) return { state: 'running', phase: 'health_check' }
    return { state: 'succeeded', phase: 'commit' }
  }, { wait: noWait, maxAttempts: 4, onProgress: (value) => progress.push(value.state) })

  assert.equal(status.state, 'succeeded')
  assert.deepEqual(progress, ['running', 'succeeded'])
})

test('update polling surfaces durable rollback state', async () => {
  const rolledBack: UpdateStatus = { state: 'rolled_back', rolled_back: true, message: 'previous container restored' }
  const status = await waitForUpdateCompletion(async () => rolledBack, { wait: noWait })
  assert.deepEqual(status, rolledBack)
})

test('update polling fails after its bounded retry budget', async () => {
  await assert.rejects(
    waitForUpdateCompletion(async () => undefined, { wait: noWait, maxAttempts: 2 }),
    /Timed out/,
  )
})


test('update polling stops before fetching after cancellation', async () => {
 let cancelled = false
 let calls = 0
 await assert.rejects(waitForUpdateCompletion(async () => { calls++;return {state: 'running'} }, {isCancelled: () => cancelled, wait: async () => {cancelled = true}}), /cancelled/)
 assert.equal(calls, 0)
})

test('update polling returns rollback failure without retrying forever', async () => {
 const status = await waitForUpdateCompletion(async () => ({state: 'rollback_failed'}), {wait: noWait})
 assert.equal(status.state, 'rollback_failed')
})
