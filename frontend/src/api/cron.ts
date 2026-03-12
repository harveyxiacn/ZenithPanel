import apiClient from './client'

export function listCronJobs() {
  return apiClient.get('/v1/cron/jobs')
}

export function createCronJob(data: { name: string; schedule: string; command: string; enable: boolean }) {
  return apiClient.post('/v1/cron/jobs', data)
}

export function deleteCronJob(id: number) {
  return apiClient.delete(`/v1/cron/jobs/${id}`)
}
