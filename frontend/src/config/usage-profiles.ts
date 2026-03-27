export type UsageProfile = 'personal_proxy' | 'vps_ops' | 'mixed'

export type UsageProfileOptionTone = 'emerald' | 'sky' | 'amber'
export type NavigationIconKey = 'dashboard' | 'servers' | 'nodes' | 'users' | 'security'
export type DashboardCardId = 'cpu' | 'memory' | 'disk' | 'network' | 'systemInfo' | 'quickStats'

export interface UsageProfileOption {
  value: UsageProfile
  labelKey: string
  descriptionKey: string
  emphasisKey: string
  tone: UsageProfileOptionTone
}

export interface ProfileNavigationItem {
  id: 'dashboard' | 'servers' | 'nodes' | 'users' | 'security'
  labelKey: string
  href: string
  icon: NavigationIconKey
}

export interface DashboardAction {
  labelKey: string
  descriptionKey: string
  href: string
  icon: NavigationIconKey
}

export interface DashboardViewConfig {
  badgeKey: string
  titleKey: string
  descriptionKey: string
  featuredCardIds: DashboardCardId[]
  secondaryCardIds: DashboardCardId[]
  primaryAction: DashboardAction
  secondaryActions: DashboardAction[]
}

const baseNavigation: Record<ProfileNavigationItem['id'], ProfileNavigationItem> = {
  dashboard: { id: 'dashboard', labelKey: 'nav.dashboard', href: '/dashboard', icon: 'dashboard' },
  servers: { id: 'servers', labelKey: 'nav.servers', href: '/servers', icon: 'servers' },
  nodes: { id: 'nodes', labelKey: 'nav.proxyNodes', href: '/nodes', icon: 'nodes' },
  users: { id: 'users', labelKey: 'nav.usersSubs', href: '/users', icon: 'users' },
  security: { id: 'security', labelKey: 'nav.security', href: '/security', icon: 'security' },
}

const usageProfileMap: Record<string, UsageProfile> = {
  personal_proxy: 'personal_proxy',
  vps_ops: 'vps_ops',
  mixed: 'mixed',
}

const navigationOrder: Record<UsageProfile, ProfileNavigationItem['id'][]> = {
  personal_proxy: ['nodes', 'users', 'dashboard', 'security', 'servers'],
  vps_ops: ['servers', 'dashboard', 'security', 'nodes', 'users'],
  mixed: ['dashboard', 'servers', 'nodes', 'users', 'security'],
}

const dashboardViews: Record<UsageProfile, DashboardViewConfig> = {
  personal_proxy: {
    badgeKey: 'usageProfile.personalProxy.label',
    titleKey: 'dashboard.profile.personalProxy.title',
    descriptionKey: 'dashboard.profile.personalProxy.description',
    featuredCardIds: ['network', 'cpu', 'memory', 'disk'],
    secondaryCardIds: ['quickStats', 'systemInfo'],
    primaryAction: {
      labelKey: 'dashboard.profile.personalProxy.primaryAction',
      descriptionKey: 'dashboard.profile.personalProxy.primaryActionHint',
      href: '/nodes',
      icon: 'nodes',
    },
    secondaryActions: [
      {
        labelKey: 'dashboard.profile.personalProxy.secondaryActionClients',
        descriptionKey: 'dashboard.profile.personalProxy.secondaryActionClientsHint',
        href: '/users',
        icon: 'users',
      },
      {
        labelKey: 'dashboard.profile.personalProxy.secondaryActionSecurity',
        descriptionKey: 'dashboard.profile.personalProxy.secondaryActionSecurityHint',
        href: '/security',
        icon: 'security',
      },
    ],
  },
  vps_ops: {
    badgeKey: 'usageProfile.vpsOps.label',
    titleKey: 'dashboard.profile.vpsOps.title',
    descriptionKey: 'dashboard.profile.vpsOps.description',
    featuredCardIds: ['cpu', 'memory', 'disk', 'network'],
    secondaryCardIds: ['systemInfo', 'quickStats'],
    primaryAction: {
      labelKey: 'dashboard.profile.vpsOps.primaryAction',
      descriptionKey: 'dashboard.profile.vpsOps.primaryActionHint',
      href: '/servers',
      icon: 'servers',
    },
    secondaryActions: [
      {
        labelKey: 'dashboard.profile.vpsOps.secondaryActionSecurity',
        descriptionKey: 'dashboard.profile.vpsOps.secondaryActionSecurityHint',
        href: '/security',
        icon: 'security',
      },
      {
        labelKey: 'dashboard.profile.vpsOps.secondaryActionProxy',
        descriptionKey: 'dashboard.profile.vpsOps.secondaryActionProxyHint',
        href: '/nodes',
        icon: 'nodes',
      },
    ],
  },
  mixed: {
    badgeKey: 'usageProfile.mixed.label',
    titleKey: 'dashboard.profile.mixed.title',
    descriptionKey: 'dashboard.profile.mixed.description',
    featuredCardIds: ['cpu', 'network', 'memory', 'disk'],
    secondaryCardIds: ['quickStats', 'systemInfo'],
    primaryAction: {
      labelKey: 'dashboard.profile.mixed.primaryAction',
      descriptionKey: 'dashboard.profile.mixed.primaryActionHint',
      href: '/dashboard',
      icon: 'dashboard',
    },
    secondaryActions: [
      {
        labelKey: 'dashboard.profile.mixed.secondaryActionServers',
        descriptionKey: 'dashboard.profile.mixed.secondaryActionServersHint',
        href: '/servers',
        icon: 'servers',
      },
      {
        labelKey: 'dashboard.profile.mixed.secondaryActionNodes',
        descriptionKey: 'dashboard.profile.mixed.secondaryActionNodesHint',
        href: '/nodes',
        icon: 'nodes',
      },
    ],
  },
}

export const usageProfileOptions: UsageProfileOption[] = [
  {
    value: 'personal_proxy',
    labelKey: 'usageProfile.personalProxy.label',
    descriptionKey: 'usageProfile.personalProxy.description',
    emphasisKey: 'usageProfile.personalProxy.emphasis',
    tone: 'emerald',
  },
  {
    value: 'vps_ops',
    labelKey: 'usageProfile.vpsOps.label',
    descriptionKey: 'usageProfile.vpsOps.description',
    emphasisKey: 'usageProfile.vpsOps.emphasis',
    tone: 'sky',
  },
  {
    value: 'mixed',
    labelKey: 'usageProfile.mixed.label',
    descriptionKey: 'usageProfile.mixed.description',
    emphasisKey: 'usageProfile.mixed.emphasis',
    tone: 'amber',
  },
]

export function normalizeUsageProfile(raw?: string | null): UsageProfile {
  if (!raw) {
    return 'mixed'
  }

  return usageProfileMap[raw.trim().toLowerCase()] ?? 'mixed'
}

export function profileHomeRoute(profile: UsageProfile): string {
  switch (profile) {
    case 'personal_proxy':
      return '/nodes'
    case 'vps_ops':
      return '/servers'
    default:
      return '/dashboard'
  }
}

export function navigationForProfile(profile: UsageProfile): ProfileNavigationItem[] {
  return navigationOrder[profile].map((itemId) => baseNavigation[itemId])
}

export function dashboardViewForProfile(profile: UsageProfile): DashboardViewConfig {
  return dashboardViews[profile]
}
