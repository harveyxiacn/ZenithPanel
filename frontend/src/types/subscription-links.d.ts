declare module '@/utils/subscription-links.mjs' {
  export function buildSubscriptionLink(
    origin: string,
    uuid: string,
    format?: 'clash' | 'base64'
  ): string
}
