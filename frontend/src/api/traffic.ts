import apiClient from './client'

export interface ProxyUserSample {
  email: string
  upload_rate_bps: number
  download_rate_bps: number
  active_conns: number
  upload_total: number
  download_total: number
  top_targets: string[] | null
  engine?: string
  protocol?: string
  inbound_tag?: string
}

export interface NICSample {
  name: string
  in_rate_bps: number
  out_rate_bps: number
  total_in: number
  total_out: number
}

export interface ProcessSample {
  pid: number
  name: string
  user: string
  command: string
  active_conns: number
  listen_ports: number[] | null
  destinations: string[] | null
}

export interface TrafficSnapshot {
  at: string
  proxy_users: ProxyUserSample[] | null
  nics: NICSample[] | null
  processes: ProcessSample[] | null
  proxy_error?: string
  system_error?: string
}

export function getTrafficLive() {
  return apiClient.get<{ code: number; data: TrafficSnapshot }>('/v1/traffic/live')
}

export function getTrafficHistory(seconds = 120) {
  return apiClient.get<{ code: number; data: TrafficSnapshot[] }>('/v1/traffic/history', {
    params: { seconds },
  })
}

// ---- Egress logger (per-instance / per-destination history) ----

export interface EgressFilterParams {
  start?: number
  end?: number
  instance?: string
  user?: string
  direction?: string
  limit?: number
}

export interface EgressRow {
  bucket: number
  instance: string
  user_email: string
  dest_host: string
  dest_ip: string
  dest_rdns: string
  asn: string
  as_org: string
  country: string
  direction: string
  bytes_up: number
  bytes_down: number
  hits: number
}

export interface EgressSummaryRow {
  key: string
  // "dest" dimension only: domain (sniffed) | rdns (reverse-DNS guess) | ip
  kind?: string
  as_org?: string
  country?: string
  bytes_up: number
  bytes_down: number
  bytes_total: number
  hits: number
}

export interface EgressSeriesPoint {
  bucket: number
  instance?: string
  bytes_up: number
  bytes_down: number
}

export interface EgressCoverage {
  instance: string
  domain: boolean
  per_user: boolean
  bytes: boolean
  source: string
  note: string
}

export type EgressConfig = Record<string, string>

export function getEgressList(params: EgressFilterParams) {
  return apiClient.get<{ code: number; data: EgressRow[] }>('/v1/traffic/egress', { params })
}

export function getEgressSummary(params: EgressFilterParams & { group_by: string }) {
  return apiClient.get<{ code: number; data: EgressSummaryRow[] }>('/v1/traffic/egress/summary', { params })
}

export function getEgressSeries(params: EgressFilterParams & { split?: string }) {
  return apiClient.get<{ code: number; data: EgressSeriesPoint[] }>('/v1/traffic/egress/series', { params })
}

export function getEgressCoverage() {
  return apiClient.get<{ code: number; data: EgressCoverage[] }>('/v1/traffic/egress/coverage')
}

export function getEgressConfig() {
  return apiClient.get<{ code: number; data: EgressConfig }>('/v1/traffic/egress/config')
}

export function updateEgressConfig(body: EgressConfig) {
  return apiClient.put<{ code: number; data: EgressConfig }>('/v1/traffic/egress/config', body)
}
