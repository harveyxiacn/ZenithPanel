import apiClient from './client'

export function listDirectory(path: string) {
  return apiClient.get('/v1/fs/list', { params: { path } })
}

export function readFile(path: string) {
  return apiClient.get('/v1/fs/read', { params: { path } })
}

export function writeFile(path: string, content: string) {
  return apiClient.post('/v1/fs/write', { path, content })
}
