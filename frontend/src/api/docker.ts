import apiClient from './client'

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
