import apiClient from './client'
import type { ApiEnvelope } from './types'
import type {
  ClashApiStatus,
  ClashConnection,
  Inbound,
  InboundPayload,
  Outbound,
  OutboundPayload,
  ProxyClient,
  ProxyClientPayload,
  ProxyStatus,
  PublicNetworkCheck,
  RealityKeys,
  RoutingRule,
  RoutingRulePayload,
} from '@/types/proxy'

export function listInbounds() { return apiClient.get<ApiEnvelope<Inbound[]>>('/v1/inbounds') }
export function createInbound(data: InboundPayload) { return apiClient.post<ApiEnvelope<Inbound>, InboundPayload>('/v1/inbounds', data) }
export function importThreeXUIInbounds(data: unknown) { return apiClient.post<ApiEnvelope, unknown>('/v1/inbounds/import-3xui', data) }
export function updateInbound(id: number, data: InboundPayload) { return apiClient.put<ApiEnvelope<Inbound>, InboundPayload>(`/v1/inbounds/${id}`, data) }
export function deleteInbound(id: number) { return apiClient.delete(`/v1/inbounds/${id}`) }
export function exportThreeXUIInbound(id: number) { return apiClient.get(`/v1/inbounds/${id}/export-3xui`) }
export function listClients(inboundId?: number) { return apiClient.get<ApiEnvelope<ProxyClient[]>>('/v1/clients', { params: inboundId ? { inbound_id: inboundId } : {} }) }
export function createClient(data: ProxyClientPayload) { return apiClient.post<ApiEnvelope<ProxyClient>, ProxyClientPayload>('/v1/clients', data) }
export function updateClient(id: number, data: ProxyClientPayload) { return apiClient.put<ApiEnvelope<ProxyClient>, ProxyClientPayload>(`/v1/clients/${id}`, data) }
export function deleteClient(id: number) { return apiClient.delete(`/v1/clients/${id}`) }
export function listRoutingRules() { return apiClient.get<ApiEnvelope<RoutingRule[]>>('/v1/routing-rules') }
export function createRoutingRule(data: RoutingRulePayload) { return apiClient.post<ApiEnvelope<RoutingRule>, RoutingRulePayload>('/v1/routing-rules', data) }
export function updateRoutingRule(id: number, data: RoutingRulePayload) { return apiClient.put<ApiEnvelope<RoutingRule>, RoutingRulePayload>(`/v1/routing-rules/${id}`, data) }
export function deleteRoutingRule(id: number) { return apiClient.delete(`/v1/routing-rules/${id}`) }
export function generateRealityKeys() { return apiClient.post<ApiEnvelope<RealityKeys>>('/v1/proxy/generate-reality-keys') }

/** Omitting engine invokes the server's safe dual-engine auto mode. */
export function applyProxyConfig(engine?: 'xray' | 'singbox') {
  const suffix = engine ? `?engine=${encodeURIComponent(engine)}` : ''
  return apiClient.post(`/v1/proxy/apply${suffix}`)
}
export function getProxyStatus() { return apiClient.get<ApiEnvelope<ProxyStatus>>('/v1/proxy/status') }
export function checkServerPublicNetwork() { return apiClient.post<ApiEnvelope<PublicNetworkCheck>>('/v1/proxy/test-connection') }
export function listOutbounds() { return apiClient.get<ApiEnvelope<Outbound[]>>('/v1/outbounds') }
export function createOutbound(data: OutboundPayload) { return apiClient.post<ApiEnvelope<Outbound>, OutboundPayload>('/v1/outbounds', data) }
export function updateOutbound(id: number, data: OutboundPayload) { return apiClient.put<ApiEnvelope<Outbound>, OutboundPayload>(`/v1/outbounds/${id}`, data) }
export function deleteOutbound(id: number) { return apiClient.delete(`/v1/outbounds/${id}`) }
export function fetchWARPConfig(accountId: string, token: string) { return apiClient.post('/v1/outbounds/warp/fetch', { account_id: accountId, token }) }
export function bulkClientAction(action: string, ids: number[]) { return apiClient.post('/v1/clients/bulk', { action, ids }) }
export function getActiveConnections() { return apiClient.get<ApiEnvelope<ClashConnection[]>>('/v1/proxy/connections') }
export function getClashApiStatus() { return apiClient.get<ApiEnvelope<ClashApiStatus>>('/v1/proxy/clash-api/status') }
export function enableClashApi() { return apiClient.post('/v1/proxy/clash-api/enable') }
export function disableClashApi() { return apiClient.post('/v1/proxy/clash-api/disable') }

export interface SubscriptionServerStatus {
  enabled: boolean
  port: number
  secret: string
  public_host: string
  cert_domain: string
  running: boolean
  tls: boolean
  base_url?: string
  error?: string
}

export function getSubscriptionServer() {
  return apiClient.get('/v1/admin/subscription')
}

export function updateSubscriptionServer(body: { enabled: boolean; port?: number; public_host?: string; cert_domain?: string; regenerate_secret?: boolean }) {
  return apiClient.put('/v1/admin/subscription', body)
}
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
  /** end-to-end traffic test: passed | failed | skipped */
  e2e?: 'passed' | 'failed' | 'skipped'
  exit_ip?: string
  e2e_latency_ms?: number
}
export function probeInbound(id: number) { return apiClient.get<ApiEnvelope<InboundProbeResult>>(`/v1/proxy/test/${id}`) }
