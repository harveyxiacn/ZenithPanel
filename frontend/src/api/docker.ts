import apiClient from './client'

// ─── Containers ──────────────────────────────────────────────────────────────

export function listContainers() {
  return apiClient.get('/v1/docker/containers')
}

export function startContainer(id: string) {
  return apiClient.post(`/v1/docker/containers/${encodeURIComponent(id)}/start`)
}

export function stopContainer(id: string) {
  return apiClient.post(`/v1/docker/containers/${encodeURIComponent(id)}/stop`)
}

export function restartContainer(id: string) {
  return apiClient.post(`/v1/docker/containers/${encodeURIComponent(id)}/restart`)
}

export function removeContainer(id: string) {
  return apiClient.delete(`/v1/docker/containers/${encodeURIComponent(id)}`)
}

export function getContainerLogs(id: string, tail = 100) {
  return apiClient.get(`/v1/docker/containers/${encodeURIComponent(id)}/logs`, { params: { tail } })
}

export function getContainerStats(id: string) {
  return apiClient.get(`/v1/docker/containers/${encodeURIComponent(id)}/stats`)
}

export function inspectContainer(id: string) {
  return apiClient.get(`/v1/docker/containers/${encodeURIComponent(id)}/inspect`)
}

export interface RunContainerRequest {
  image: string
  name?: string
  ports?: string[]    // ["8080:80/tcp"]
  volumes?: string[]  // ["/host:/container"]
  env?: string[]      // ["KEY=VALUE"]
  cmd?: string[]
  restart_policy?: string  // "always"|"unless-stopped"|"no"
  network_mode?: string    // "bridge"|"host"
}

export function runContainer(req: RunContainerRequest) {
  return apiClient.post('/v1/docker/containers/run', req)
}

// ─── Images ──────────────────────────────────────────────────────────────────

export function listImages() {
  return apiClient.get('/v1/docker/images')
}

export function pullImage(image: string) {
  return apiClient.post('/v1/docker/images/pull', { image })
}

export function removeImage(id: string, force = false) {
  return apiClient.delete(`/v1/docker/images/${encodeURIComponent(id)}`, { params: { force } })
}

// ─── Volumes & Networks ───────────────────────────────────────────────────────

export function listVolumes() {
  return apiClient.get('/v1/docker/volumes')
}

export function listNetworks() {
  return apiClient.get('/v1/docker/networks')
}
