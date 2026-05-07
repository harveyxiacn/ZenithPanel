// shouldLogoutOnUnauthorized determines whether a 401 response should trigger
// a logout redirect. Login and setup endpoints return 401 on invalid credentials
// but must NOT trigger a redirect loop back to /login.
export function shouldLogoutOnUnauthorized(status?: number, requestUrl = ''): boolean {
  if (status !== 401) return false
  return !requestUrl.startsWith('/v1/login') && !requestUrl.startsWith('/setup/login')
}
