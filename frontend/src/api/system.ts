import apiClient from './client'

export function getSystemMonitor() {
  return apiClient.get('/v1/system/monitor')
}

export function getNetworkDiagnostics() {
  return apiClient.get('/v1/diagnostics/network')
}

export function checkForUpdate() {
  return apiClient.get('/v1/system/update/check')
}

export function applyUpdate() {
  return apiClient.post('/v1/system/update/apply')
}
