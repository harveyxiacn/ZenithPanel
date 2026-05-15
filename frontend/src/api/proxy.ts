import apiClient from './client'

export function listInbounds() {
  return apiClient.get('/v1/inbounds')
}

export function createInbound(data: any) {
  return apiClient.post('/v1/inbounds', data)
}

export function importThreeXUIInbounds(data: any) {
  return apiClient.post('/v1/inbounds/import-3xui', data)
}

export function updateInbound(id: number, data: any) {
  return apiClient.put(`/v1/inbounds/${id}`, data)
}

export function deleteInbound(id: number) {
  return apiClient.delete(`/v1/inbounds/${id}`)
}

export function exportThreeXUIInbound(id: number) {
  return apiClient.get(`/v1/inbounds/${id}/export-3xui`)
}

export function listClients(inboundId?: number) {
  const params = inboundId ? { inbound_id: inboundId } : {}
  return apiClient.get('/v1/clients', { params })
}

export function createClient(data: any) {
  return apiClient.post('/v1/clients', data)
}

export function updateClient(id: number, data: any) {
  return apiClient.put(`/v1/clients/${id}`, data)
}

export function deleteClient(id: number) {
  return apiClient.delete(`/v1/clients/${id}`)
}

export function listRoutingRules() {
  return apiClient.get('/v1/routing-rules')
}

export function createRoutingRule(data: any) {
  return apiClient.post('/v1/routing-rules', data)
}

export function updateRoutingRule(id: number, data: any) {
  return apiClient.put(`/v1/routing-rules/${id}`, data)
}

export function deleteRoutingRule(id: number) {
  return apiClient.delete(`/v1/routing-rules/${id}`)
}

export function generateRealityKeys() {
  return apiClient.post('/v1/proxy/generate-reality-keys')
}

export function applyProxyConfig(engine = 'xray') {
  return apiClient.post(`/v1/proxy/apply?engine=${encodeURIComponent(engine)}`)
}

export function getProxyStatus() {
  return apiClient.get('/v1/proxy/status')
}

export function checkServerPublicNetwork() {
  return apiClient.post('/v1/proxy/test-connection')
}

// ─── Outbounds ───────────────────────────────────────────────────────────────

export function listOutbounds() {
  return apiClient.get('/v1/outbounds')
}

export function createOutbound(data: any) {
  return apiClient.post('/v1/outbounds', data)
}

export function updateOutbound(id: number, data: any) {
  return apiClient.put(`/v1/outbounds/${id}`, data)
}

export function deleteOutbound(id: number) {
  return apiClient.delete(`/v1/outbounds/${id}`)
}

export function fetchWARPConfig(accountId: string, token: string) {
  return apiClient.post('/v1/outbounds/warp/fetch', { account_id: accountId, token })
}

// ─── Bulk Client Operations ──────────────────────────────────────────────────

export function bulkClientAction(action: string, ids: number[]) {
  return apiClient.post('/v1/clients/bulk', { action, ids })
}

// ─── Clash API (real-time connections) ───────────────────────────────────────

export function getActiveConnections() {
  return apiClient.get('/v1/proxy/connections')
}

export function getClashApiStatus() {
  return apiClient.get('/v1/proxy/clash-api/status')
}

export function enableClashApi() {
  return apiClient.post('/v1/proxy/clash-api/enable')
}

export function disableClashApi() {
  return apiClient.post('/v1/proxy/clash-api/disable')
}

// Inbound connectivity probe — calls the server-side prober. See
// docs/cli_api_spec.md §2.5 and service/diagnostic.ProbeInbound for the
// shape of `data`.
export interface InboundProbeResult {
  inbound_id: number
  tag: string
  protocol: string
  transport: string
  port: number
  ok: boolean
  stage?: string
  elapsed_ms: number
  err?: string
}

export function probeInbound(id: number) {
  return apiClient.get<{ code: number; msg: string; data: InboundProbeResult }>(`/v1/proxy/test/${id}`)
}
