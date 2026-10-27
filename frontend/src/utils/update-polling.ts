import type { UpdateStatus } from '@/api/system'

interface PollOptions {
	isCancelled?: () => boolean
  intervalMs?: number
  maxAttempts?: number
  onProgress?: (status: UpdateStatus) => void
  wait?: (milliseconds: number) => Promise<void>
}

const defaultWait = (milliseconds: number) => new Promise<void>((resolve) => setTimeout(resolve, milliseconds))

export async function waitForUpdateCompletion(
  fetchStatus: () => Promise<UpdateStatus | undefined>,
  options: PollOptions = {},
) {
  const intervalMs = options.intervalMs ?? 2_000
  const maxAttempts = options.maxAttempts ?? 45
  const wait = options.wait ?? defaultWait

  for (let attempt = 0; attempt < maxAttempts; attempt += 1) {
	if (options.isCancelled?.()) throw new Error('Update polling cancelled')
    await wait(intervalMs)
	if (options.isCancelled?.()) throw new Error('Update polling cancelled')
    try {
      const status = await fetchStatus()
      if (options.isCancelled?.()) throw new Error('Update polling cancelled')
      if (!status) continue
      options.onProgress?.(status)
      if (status.state === 'succeeded' || status.state === 'failed' || status.state === 'rolled_back' || status.state === 'rollback_failed') {
        return status
      }
    } catch {
      if (options.isCancelled?.()) throw new Error('Update polling cancelled')
      // Losing the HTTP connection is expected while the old container is
      // stopped. The candidate or rollback container will answer a later poll.
    }
  }

  throw new Error('Timed out waiting for the update result; check update status again.')
}
