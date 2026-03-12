import apiClient from './client'

export function listFirewallRules() {
  return apiClient.get('/v1/firewall/rules')
}

export function addFirewallRule(rule: {
  protocol: string
  port: string
  action: string
  source?: string
  comment?: string
}) {
  return apiClient.post('/v1/firewall/rules', rule)
}

export function deleteFirewallRule(num: string) {
  return apiClient.delete('/v1/firewall/rules', { data: { num } })
}
