// Smart Deploy types — mirrored from backend/internal/service/deploy/types.go
// and backend/internal/model/deploy.go. Keep the JSON shapes in sync when
// the Go side changes.

export type PresetID = 'stable_egress' | 'speed' | 'combo' | 'weak_network'

export type DeployStatus =
  | 'pending'
  | 'running'
  | 'succeeded'
  | 'failed'
  | 'rolled_back'

export type CertMode = 'reality' | 'acme' | 'self_signed' | 'existing'

export type OpStatus =
  | 'pending'
  | 'applied'
  | 'skipped'
  | 'failed'
  | 'reverted'

export type OpType = 'probe' | 'tune' | 'cert' | 'inbound' | 'firewall'

// ─────────────────────────────────────────────────────────────────────────
// Probe result
// ─────────────────────────────────────────────────────────────────────────

export interface KernelFeatures {
  bbr: boolean
  fq: boolean
  fq_codel: boolean
  cake: boolean
  tfo: boolean
}

export interface ProbeResult {
  root_check: { ok: boolean; uid: number; note?: string }
  kernel: {
    version: string
    major: number
    minor: number
    features: KernelFeatures
  }
  systemd: { present: boolean; version?: string }
  distro: { id: string; version_id: string; pretty_name: string }
  time_sync: { service: string; active: boolean; synced: boolean; error?: string }
  public_ip: { v4: string; v6?: string; error?: string }
  hardware: { cpu_cores: number; ram_bytes: number; swap_bytes: number }
  nic: { primary: string; link_speed_mbps: number }
  port_avail: { ports: Record<number, boolean> }
  inbound_ports: number[]
  firewall: { type: string; active: boolean }
  docker: { present: boolean; running: boolean; version?: string }
  probed_at: string
  duration_ms: number
}

// ─────────────────────────────────────────────────────────────────────────
// Plan
// ─────────────────────────────────────────────────────────────────────────

export interface InboundSpec {
  engine: string
  protocol: string
  tag: string
  listen?: string
  port: number
  network?: string
  settings: Record<string, unknown>
  stream?: Record<string, unknown>
  remark?: string
}

export interface TuneSpec {
  op_name: string
  params?: Record<string, string>
}

export interface CertInput {
  domain?: string
  email?: string
  public_ip?: string
  cert_path?: string
  key_path?: string
}

export interface DeployPlan {
  preset_id: PresetID
  inbounds: InboundSpec[]
  tuning: TuneSpec[]
  cert_mode: CertMode
  cert_input?: CertInput
  firewall_allow_ports?: number[]
  notes?: string[]
}

// ─────────────────────────────────────────────────────────────────────────
// Persistent records
// ─────────────────────────────────────────────────────────────────────────

export interface Deployment {
  id: number
  preset_id: PresetID
  status: DeployStatus
  probe_snapshot: string // JSON-encoded ProbeResult
  plan_snapshot: string // JSON-encoded DeployPlan
  domain?: string
  cert_mode?: CertMode
  inbound_ids: string // JSON-encoded number[]
  error?: string
  created_at: string
  updated_at: string
}

export interface DeploymentOp {
  id: number
  deployment_id: number
  sequence: number
  op_type: OpType
  op_name: string
  pre_value?: string
  post_value?: string
  status: OpStatus
  error?: string
  applied_at?: string
  created_at: string
  updated_at: string
}

// ─────────────────────────────────────────────────────────────────────────
// Request / response
// ─────────────────────────────────────────────────────────────────────────

export interface DeployRequest {
  preset_id: PresetID
  domain?: string
  email?: string
  port_override?: number
  reality_target?: string
  options?: Record<string, unknown>
}

export interface PreviewResponse {
  plan: DeployPlan
  probe: ProbeResult
}

export interface DeploymentDetail {
  deployment: Deployment
  ops: DeploymentOp[]
}

export interface ClientsResponse {
  deployment_id: number
  inbound_ids: string
  note: string
}

// UI helper: preset display metadata used by the wizard cards.
export interface PresetMeta {
  id: PresetID
  displayName: string
  description: string
  recommended: boolean
  color: string
  useCase: string
}

export const PRESETS: PresetMeta[] = [
  {
    id: 'stable_egress',
    displayName: '稳定出口',
    description: 'VLESS + Reality · TCP 443 · 无需域名',
    recommended: true,
    color: 'emerald',
    useCase: '推荐。稳定唯一出口 IP，规避平台风控（银行/交易所/电商）。Reality 伪装成真实 TLS 流量。',
  },
  {
    id: 'speed',
    displayName: '速度优先',
    description: 'Hysteria2 · UDP 443 · 建议配域名',
    recommended: false,
    color: 'indigo',
    useCase: '低延迟、高吞吐。开放网络环境。有域名用 ACME 证书，无域名用自签。',
  },
  {
    id: 'combo',
    displayName: '全能组合',
    description: 'Reality (TCP) + Hysteria2 (UDP)',
    recommended: false,
    color: 'amber',
    useCase: 'TCP/UDP 双入口，客户端按需切换。兼顾稳定性和速度。',
  },
  {
    id: 'weak_network',
    displayName: '移动弱网',
    description: 'Hysteria2 + TUIC · 丢包容忍',
    recommended: false,
    color: 'rose',
    useCase: '手机 4G/5G、丢包高的网络。使用 cake qdisc + UDP 大缓冲。',
  },
]
