import apiClient from './client'

export function listInbounds() {
  return apiClient.get('/v1/inbounds')
}

export function createInbound(data: any) {
  return apiClient.post('/v1/inbounds', data)
}

export function updateInbound(id: number, data: any) {
  return apiClient.put(`/v1/inbounds/${id}`, data)
}

export function deleteInbound(id: number) {
  return apiClient.delete(`/v1/inbounds/${id}`)
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

export function testProxyConnection() {
  return apiClient.post('/v1/proxy/test-connection')
}
