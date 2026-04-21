import apiClient from './client'
import type {
  ClientsResponse,
  Deployment,
  DeploymentDetail,
  DeployRequest,
  PreviewResponse,
  ProbeResult,
} from '@/types/deploy'

// Raw API envelope — the response interceptor unwraps Axios's `.data` so we
// receive `{code, msg, data}` directly. Each typed helper returns only the
// `.data` field.

interface Envelope<T> {
  code: number
  msg?: string
  data: T
}

export async function deployProbe(): Promise<ProbeResult> {
  const res = (await apiClient.post('/v1/deploy/probe')) as Envelope<ProbeResult>
  return res.data
}

export async function deployPreview(req: DeployRequest): Promise<PreviewResponse> {
  const res = (await apiClient.post('/v1/deploy/preview', req)) as Envelope<PreviewResponse>
  return res.data
}

export async function deployApply(req: DeployRequest, timeoutMs = 120_000): Promise<Deployment> {
  const res = (await apiClient.post('/v1/deploy/apply', req, { timeout: timeoutMs })) as Envelope<Deployment>
  return res.data
}

export async function deployList(limit = 20, offset = 0): Promise<Deployment[]> {
  const res = (await apiClient.get('/v1/deploy', { params: { limit, offset } })) as Envelope<Deployment[]>
  return res.data
}

export async function deployGet(id: number): Promise<DeploymentDetail> {
  const res = (await apiClient.get(`/v1/deploy/${id}`)) as Envelope<DeploymentDetail>
  return res.data
}

export async function deployRollback(id: number): Promise<Deployment> {
  const res = (await apiClient.post(`/v1/deploy/${id}/rollback`)) as Envelope<Deployment>
  return res.data
}

export async function deployClients(id: number): Promise<ClientsResponse> {
  const res = (await apiClient.get(`/v1/deploy/${id}/clients`)) as Envelope<ClientsResponse>
  return res.data
}
