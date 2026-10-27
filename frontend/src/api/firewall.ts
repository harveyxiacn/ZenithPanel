import apiClient from './client'

export type FirewallFamily = 'ipv4' | 'ipv6'
export interface FirewallRule {
  family?: FirewallFamily
  num: string
  target: string
  protocol: string
  source: string
  destination: string
  port: string
}

export function listFirewallRules(family: FirewallFamily = 'ipv4') {
  return apiClient.get('/v1/firewall/rules', { params: { family } })
}

export function addFirewallRule(rule: {
  family?: FirewallFamily
  protocol: string
  port: string
  action: string
  source?: string
  comment?: string
}) {
  return apiClient.post('/v1/firewall/rules', rule)
}

export function deleteFirewallRule(rule: FirewallRule) {
  return apiClient.delete('/v1/firewall/rules', { data: rule })
}
