declare module '@/utils/subscription-links.mjs' {
  export function buildSubscriptionLink(
    origin: string,
    uuid: string,
    format?: 'clash' | 'base64',
    /** public subscription server base URL; overrides origin when set */
    subBase?: string
  ): string
}
