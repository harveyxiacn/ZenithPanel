export function buildSubscriptionLink(origin, uuid, format) {
  const base = String(origin || '').replace(/\/+$/, '')
  const url = new URL(`${base}/api/v1/sub/${encodeURIComponent(uuid)}`)

  if (format === 'clash' || format === 'base64') {
    url.searchParams.set('format', format)
  }

  return url.toString()
}
