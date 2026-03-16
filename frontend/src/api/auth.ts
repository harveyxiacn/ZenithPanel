import apiClient from './client'

export function setupLogin(initialPassword: string) {
  return apiClient.post('/setup/login', { password: initialPassword })
}

export function setupComplete(data: {
  username: string
  password: string
  panel_path: string
}) {
  return apiClient.post('/setup/complete', data)
}

export function login(username: string, password: string, totpCode?: string) {
  return apiClient.post('/v1/login', { username, password, totp_code: totpCode })
}
