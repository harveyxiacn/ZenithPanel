import apiClient from './client'

export interface AccessConfigData {
  panel_path?: string
  port?: string
  usage_profile?: string
  ip_whitelist?: string
  your_ip?: string
}

export interface AccessConfigUpdatePayload {
  panel_path?: string
  port?: string
  usage_profile?: string
  ip_whitelist?: string
}

export interface DNSSettings {
  dns_mode?: string
  dns_primary?: string
  dns_secondary?: string
}

export function getSystemMonitor() {
  return apiClient.get('/v1/system/monitor')
}

export function getNetworkHistory() {
  return apiClient.get('/v1/system/network-history')
}

export function getExtendedNetworkHistory(since?: number) {
  return apiClient.get('/v1/system/network-history/extended', { params: since ? { since } : {} })
}

// DNS Configuration (controls Sing-box / Xray DNS — DoH or plain UDP)
export function getDNSSettings() {
  return apiClient.get('/v1/admin/dns')
}

export function updateDNSSettings(data: DNSSettings) {
  return apiClient.put('/v1/admin/dns', data)
}

// Health (unauthenticated, suitable for external monitors)
export function getHealth() {
  return apiClient.get('/v1/health')
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

export function updateAccessConfig(data: AccessConfigUpdatePayload) {
  return apiClient.put('/v1/admin/access', data)
}

export function restartPanel() {
  return apiClient.post('/v1/admin/restart', null, { timeout: 120000 })
}

// Cloudflare Protection
export function getCFProtectionStatus() {
  return apiClient.get('/v1/firewall/cloudflare/status')
}

export function enableCFProtection() {
  return apiClient.post('/v1/firewall/cloudflare/enable')
}

export function disableCFProtection() {
  return apiClient.post('/v1/firewall/cloudflare/disable')
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

// System Optimization - BBR
export function getBBRStatus() {
  return apiClient.get('/v1/system/bbr/status')
}
export function enableBBR() {
  return apiClient.post('/v1/system/bbr/enable')
}
export function disableBBR() {
  return apiClient.post('/v1/system/bbr/disable')
}

// System Optimization - Swap
export function getSwapStatus() {
  return apiClient.get('/v1/system/swap/status')
}
export function createSwap(sizeMB: number) {
  return apiClient.post('/v1/system/swap/create', { size_mb: sizeMB })
}
export function removeSwap() {
  return apiClient.post('/v1/system/swap/remove')
}

// System Optimization - Sysctl Network Tuning
export function getSysctlStatus() {
  return apiClient.get('/v1/system/sysctl/status')
}
export function enableSysctl() {
  return apiClient.post('/v1/system/sysctl/enable')
}
export function disableSysctl() {
  return apiClient.post('/v1/system/sysctl/disable')
}

// System Cleanup
export function getCleanupInfo() {
  return apiClient.get('/v1/system/cleanup/info')
}
export function runCleanup() {
  return apiClient.post('/v1/system/cleanup/run')
}

// Backup / Restore
export function downloadBackup() {
  return apiClient.get('/v1/admin/backup/export', { responseType: 'blob', timeout: 60000 })
}

export function restoreBackup(file: File) {
  return apiClient.post('/v1/admin/backup/restore', file, {
    headers: { 'Content-Type': 'application/zip' },
    timeout: 60000,
  })
}
