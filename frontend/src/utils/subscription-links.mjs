// Builds a client subscription URL.
//
// When the public subscription server is enabled, `subBase` is its base URL
// (e.g. "https://host:2096/<secret>/") and the link is `<subBase><uuid>` —
// reachable by client apps even though the admin panel port is closed.
// Otherwise the link points at the panel's own /api/v1/sub endpoint on
// `origin`.
export function buildSubscriptionLink(origin, uuid, format, subBase, dualStack = false) {
  let url
  if (subBase) {
    const base = String(subBase).replace(/\/*$/, '/')
    url = new URL(`${base}${encodeURIComponent(uuid)}`)
  } else {
    const base = String(origin || '').replace(/\/+$/, '')
    url = new URL(`${base}/api/v1/sub/${encodeURIComponent(uuid)}`)
  }

  if (format === 'clash' || format === 'base64') {
    url.searchParams.set('format', format)
  }
  if (format === 'clash' && dualStack) {
    url.searchParams.set('dual_stack', '1')
  }

  return url.toString()
}
