import apiClient from './client'

export interface Site {
  id?: number
  name: string
  domain: string
  type: string           // "reverse_proxy" | "static" | "redirect"
  upstream_url?: string
  root_path?: string
  redirect_url?: string
  tls_mode?: string      // "none" | "acme" | "custom"
  cert_path?: string
  key_path?: string
  tls_email?: string
  custom_headers?: string // JSON [{key,value}]
  enable?: boolean
}

export function listSites() {
  return apiClient.get('/v1/sites')
}

export function createSite(data: Site) {
  return apiClient.post('/v1/sites', data)
}

export function updateSite(id: number, data: Partial<Site>) {
  return apiClient.put(`/v1/sites/${id}`, data)
}

export function deleteSite(id: number) {
  return apiClient.delete(`/v1/sites/${id}`)
}

export function toggleSite(id: number) {
  return apiClient.post(`/v1/sites/${id}/enable`)
}

export function issueSiteCert(id: number) {
  return apiClient.post(`/v1/sites/${id}/cert`)
}
