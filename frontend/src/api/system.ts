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
