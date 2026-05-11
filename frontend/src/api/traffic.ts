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
