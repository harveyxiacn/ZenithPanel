import apiClient from './client'

export function getSystemMonitor() {
  return apiClient.get('/v1/system/monitor')
}

export function getNetworkDiagnostics() {
  return apiClient.get('/v1/diagnostics/network')
}

export function checkForUpdate() {
  return apiClient.get('/v1/system/update/check', { timeout: 120000 })
}

export function applyUpdate() {
  return apiClient.post('/v1/system/update/apply', null, { timeout: 120000 })
}

export function changePassword(oldPassword: string, newPassword: string) {
  return apiClient.post('/v1/admin/change-password', { old_password: oldPassword, new_password: newPassword })
}

// 2FA
export function get2FAStatus() {
  return apiClient.get('/v1/admin/2fa/status')
}

export function setup2FA() {
  return apiClient.post('/v1/admin/2fa/setup')
}

export function verify2FA(code: string) {
  return apiClient.post('/v1/admin/2fa/verify', { code })
}

export function disable2FA(password: string) {
  return apiClient.post('/v1/admin/2fa/disable', { password })
}

// Access Configuration
export function getAccessConfig() {
  return apiClient.get('/v1/admin/access')
}

export function updateAccessConfig(data: { panel_path?: string; port?: string }) {
  return apiClient.put('/v1/admin/access', data)
}

export function restartPanel() {
  return apiClient.post('/v1/admin/restart', null, { timeout: 120000 })
}

// TLS
export function getTLSStatus() {
  return apiClient.get('/v1/admin/tls/status')
}

export function uploadTLSCerts(formData: FormData) {
  return apiClient.post('/v1/admin/tls/upload', formData, {
    headers: { 'Content-Type': 'multipart/form-data' },
  })
}

export function removeTLS() {
  return apiClient.delete('/v1/admin/tls')
}
