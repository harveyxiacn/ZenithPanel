import apiClient from './client'
import type { ApiEnvelope, SessionUser } from './types'

export function setupLogin(initialPassword: string) {
	return apiClient.post<ApiEnvelope>('/setup/login', { password: initialPassword })
}

export function setupComplete(data: {
  username: string
  password: string
  panel_path: string
  usage_profile?: 'personal_proxy' | 'vps_ops' | 'mixed'
}) {
  return apiClient.post('/setup/complete', data)
}

export function login(username: string, password: string, totpCode?: string) {
	return apiClient.post<ApiEnvelope<{ requires_2fa?: boolean; user?: SessionUser }>>('/v1/login', { username, password, totp_code: totpCode })
}

export function createTerminalTicket() {
	return apiClient.post<ApiEnvelope<{ ticket: string; expires_at: string }>>('/v1/terminal/ticket')
}

export function logoutAllSessions() { return apiClient.post<ApiEnvelope>('/v1/auth/logout-all') }
