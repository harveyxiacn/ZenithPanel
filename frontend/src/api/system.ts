import apiClient from './client'
import type { ApiDataEnvelope, ApiEnvelope } from './types'

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

export interface AcmeIssueResult {
  cert_path: string
  key_path: string
  not_after: number
}

export interface NotifyConfig {
  notify_telegram_token: string
  notify_telegram_chat_id: string
  notify_webhook_url: string
  notify_enable_expiring_soon: string
  notify_enable_expired: string
  notify_enable_traffic_limit: string
  notify_enable_proxy_crashed: string
}

export interface NotificationTestPayload {
  channel: 'telegram' | 'webhook'
  token?: string
  chat_id?: string
  url?: string
}

export interface UpdateCheckResult {
  available: boolean
  current_version: string
  current_id: string
  latest_id: string
}

export interface UpdateStatus {
  state: 'idle' | 'running' | 'succeeded' | 'failed' | 'rolled_back' | 'rollback_failed'
  phase?: string
  message?: string
  previous_image?: string
  candidate_image?: string
  rolled_back?: boolean
  updated_at?: string
}

export interface TwoFactorStatus { enabled: boolean }
export interface TwoFactorSetup {
  qr_base64: string
  secret: string
  recovery_codes: string[]
}
export interface EnabledStatus { enabled: boolean }
export interface CloudflareStatus extends EnabledStatus { port: string }
export interface BBRStatus extends EnabledStatus {
  current: string
  available: string
}
export interface SwapStatus extends EnabledStatus {
  total_mb: number
  used_mb: number
  file_path: string
}
export interface CleanupInfo {
  journal_size: string
  package_size: string
  docker_size: string
}
export interface CleanupResult {
  journal_freed: string
  package_freed: string
  docker_freed: string
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
  return apiClient.get<ApiDataEnvelope<DNSSettings>>('/v1/admin/dns')
}

export function updateDNSSettings(data: DNSSettings) {
  return apiClient.put('/v1/admin/dns', data)
}

export function issueAcmeCertificate(domain: string, email: string) {
  return apiClient.post<ApiEnvelope<AcmeIssueResult>, { domain: string; email: string }>(
    '/v1/proxy/tls/issue',
    { domain, email },
  )
}

export function getNotifyConfig() {
  return apiClient.get<ApiEnvelope<NotifyConfig>>('/v1/admin/notify')
}

export function updateNotifyConfig(data: NotifyConfig) {
  return apiClient.put<ApiEnvelope, NotifyConfig>('/v1/admin/notify', data)
}

export function testNotification(data: NotificationTestPayload) {
  return apiClient.post<ApiEnvelope, NotificationTestPayload>('/v1/admin/notify/test', data)
}

// Health (unauthenticated, suitable for external monitors)
export function getHealth() {
  return apiClient.get('/v1/health')
}

export function getNetworkDiagnostics() {
  return apiClient.get('/v1/diagnostics/network')
}

export function checkForUpdate() {
  return apiClient.get<ApiDataEnvelope<UpdateCheckResult>>('/v1/system/update/check', { timeout: 120000 })
}

export function applyUpdate() {
  return apiClient.post<ApiEnvelope>('/v1/system/update/apply', null, { timeout: 120000 })
}

export function getUpdateStatus() {
  return apiClient.get<ApiDataEnvelope<UpdateStatus>>('/v1/system/update/status')
}

export function changePassword(oldPassword: string, newPassword: string) {
  return apiClient.post<ApiEnvelope, { old_password: string; new_password: string }>('/v1/admin/change-password', { old_password: oldPassword, new_password: newPassword })
}

// 2FA
export function get2FAStatus() {
  return apiClient.get<ApiDataEnvelope<TwoFactorStatus>>('/v1/admin/2fa/status')
}

export function setup2FA() {
  return apiClient.post<ApiDataEnvelope<TwoFactorSetup>>('/v1/admin/2fa/setup')
}

export function verify2FA(code: string) {
  return apiClient.post<ApiEnvelope, { code: string }>('/v1/admin/2fa/verify', { code })
}

export function disable2FA(password: string) {
  return apiClient.post<ApiEnvelope, { password: string }>('/v1/admin/2fa/disable', { password })
}

// Access Configuration
export function getAccessConfig() {
  return apiClient.get<ApiDataEnvelope<Required<AccessConfigData>>>('/v1/admin/access')
}

export function updateAccessConfig(data: AccessConfigUpdatePayload) {
  return apiClient.put('/v1/admin/access', data)
}

export function restartPanel() {
  return apiClient.post<ApiEnvelope>('/v1/admin/restart', null, { timeout: 120000 })
}

// Cloudflare Protection
export function getCFProtectionStatus() {
  return apiClient.get<ApiDataEnvelope<CloudflareStatus>>('/v1/firewall/cloudflare/status')
}

export function enableCFProtection() {
  return apiClient.post<ApiEnvelope>('/v1/firewall/cloudflare/enable')
}

export function disableCFProtection() {
  return apiClient.post<ApiEnvelope>('/v1/firewall/cloudflare/disable')
}

// TLS
export function getTLSStatus() {
  return apiClient.get<ApiDataEnvelope<EnabledStatus>>('/v1/admin/tls/status')
}

export function uploadTLSCerts(formData: FormData) {
  return apiClient.post<ApiEnvelope, FormData>('/v1/admin/tls/upload', formData, {
    headers: { 'Content-Type': 'multipart/form-data' },
  })
}

export function removeTLS() {
  return apiClient.delete<ApiEnvelope>('/v1/admin/tls')
}

// System Optimization - BBR
export function getBBRStatus() {
  return apiClient.get<ApiDataEnvelope<BBRStatus>>('/v1/system/bbr/status')
}
export function enableBBR() {
  return apiClient.post<ApiEnvelope>('/v1/system/bbr/enable')
}
export function disableBBR() {
  return apiClient.post<ApiEnvelope>('/v1/system/bbr/disable')
}

// System Optimization - Swap
export function getSwapStatus() {
  return apiClient.get<ApiDataEnvelope<SwapStatus>>('/v1/system/swap/status')
}
export function createSwap(sizeMB: number) {
  return apiClient.post<ApiEnvelope, { size_mb: number }>('/v1/system/swap/create', { size_mb: sizeMB })
}
export function removeSwap() {
  return apiClient.post<ApiEnvelope>('/v1/system/swap/remove')
}

// System Optimization - Sysctl Network Tuning
export function getSysctlStatus() {
  return apiClient.get<ApiDataEnvelope<EnabledStatus>>('/v1/system/sysctl/status')
}
export function enableSysctl() {
  return apiClient.post<ApiEnvelope>('/v1/system/sysctl/enable')
}
export function disableSysctl() {
  return apiClient.post<ApiEnvelope>('/v1/system/sysctl/disable')
}

// System Cleanup
export function getCleanupInfo() {
  return apiClient.get<ApiDataEnvelope<CleanupInfo>>('/v1/system/cleanup/info')
}
export function runCleanup() {
  return apiClient.post<ApiDataEnvelope<CleanupResult>>('/v1/system/cleanup/run')
}

// Backup / Restore
export function downloadBackup() {
  return apiClient.get<Blob>('/v1/admin/backup/export', { responseType: 'blob', timeout: 60000 })
}

export function restoreBackup(file: File) {
  return apiClient.post<ApiEnvelope, File>('/v1/admin/backup/restore', file, {
    headers: { 'Content-Type': 'application/zip' },
    timeout: 60000,
  })
}

// API Tokens — used by the headless CLI (zenithctl) and automation. See
// docs/cli_api_spec.md for the contract.
export interface ApiTokenRow {
  id: number
  name: string
  scopes: string
  expires_at: number
  last_used_at: number
  revoked: boolean
  created_at: string
}

export interface ApiTokenCreatePayload {
  name: string
  scopes?: string
  expires_in_days?: number
}

export interface ApiTokenCreateResponse extends ApiTokenRow {
  token: string // plaintext — shown to user once and never persisted client-side
}

// apiClient already unwraps the outer envelope (see api/client.ts), so the
// generic should describe the wire body (`{code, msg, data: {...}}`); the
// caller then reads `.data` once. Mirrors the typing of the surrounding
// system-settings calls.
export function getAdBlockStatus() {
  return apiClient.get<ApiDataEnvelope<EnabledStatus>>('/v1/admin/adblock')
}

export function setAdBlockEnabled(enabled: boolean) {
  return apiClient.put('/v1/admin/adblock', { enabled })
}

export function listApiTokens() {
  return apiClient.get<{ code: number; msg: string; data: ApiTokenRow[] }>('/v1/admin/api-tokens')
}

export function createApiToken(payload: ApiTokenCreatePayload) {
  return apiClient.post<{ code: number; msg: string; data: ApiTokenCreateResponse }>('/v1/admin/api-tokens', payload)
}

export function revokeApiToken(id: number) {
  return apiClient.delete(`/v1/admin/api-tokens/${id}`)
}
