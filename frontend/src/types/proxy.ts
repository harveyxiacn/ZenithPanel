export interface Inbound {
  id: number
  tag: string
  protocol: string
  listen: string
  server_address: string
  port: number
  network: string
  settings: string
  stream: string
  enable: boolean
  remark: string
  created_at: string
  updated_at: string
}

type MutableInboundFields = Omit<Inbound, 'id' | 'created_at' | 'updated_at'>
export type InboundPayload = Pick<Inbound, 'tag' | 'protocol' | 'port' | 'settings' | 'stream'> &
  Partial<Omit<MutableInboundFields, 'tag' | 'protocol' | 'port' | 'settings' | 'stream'>>

export interface ProxyClient {
  id: number
  inbound_id: number
  email: string
  uuid: string
  enable: boolean
  up_load: number
  down_load: number
  total: number
  expiry_time: number
  speed_limit: number
  reset_day: number
  remark: string
  created_at: string
  updated_at: string
}

export type ProxyClientPayload = Partial<Omit<ProxyClient, 'id' | 'created_at' | 'updated_at'>> &
  Pick<ProxyClient, 'inbound_id' | 'email'>

export interface RoutingRule {
  id: number
  rule_tag: string
  domain: string
  ip: string
  port: string
  outbound_tag: string
  enable: boolean
  created_at: string
  updated_at: string
}

export type RoutingRulePayload = Pick<RoutingRule, 'rule_tag' | 'outbound_tag' | 'enable'> &
  Partial<Pick<RoutingRule, 'domain' | 'ip' | 'port'>>

export interface Outbound {
  id: number
  tag: string
  protocol: string
  config: string
  description: string
  enable: boolean
  created_at: string
  updated_at: string
}

export type OutboundPayload = Omit<Outbound, 'id' | 'created_at' | 'updated_at'>

export interface RealityKeys {
  private_key: string
  public_key: string
  short_id: string
}

export interface ProxyStatus {
  status?: string
  xray_running?: boolean
  singbox_running?: boolean
  xray_last_error?: string
  singbox_last_error?: string
  [key: string]: unknown
}

export interface PublicNetworkCheck {
  success?: boolean
  ip?: string
  message?: string
  [key: string]: unknown
}

export interface ClashConnection {
  id?: string
  metadata?: Record<string, unknown>
  upload?: number
  download?: number
  start?: string
  chains?: string[]
  rule?: string
  rulePayload?: string
  [key: string]: unknown
}

export interface ClashApiStatus {
  enabled: boolean
  running?: boolean
  [key: string]: unknown
}
