import apiClient from './client'

export function getSystemMonitor() {
  return apiClient.get('/v1/system/monitor')
}

export function getNetworkDiagnostics() {
  return apiClient.get('/v1/diagnostics/network')
}
