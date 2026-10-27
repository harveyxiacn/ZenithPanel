declare module '@/utils/subscription-links.mjs' {
  export function buildSubscriptionLink(
    origin: string,
    uuid: string,
    format?: 'clash' | 'base64',
    /** public subscription server base URL; overrides origin when set */
    subBase?: string,
    /** Expand configured IPv4/IPv6 endpoints and include automatic groups (Clash only). */
    dualStack?: boolean
  ): string
}
