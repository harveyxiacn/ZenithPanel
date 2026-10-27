import { ref } from 'vue'
import { createApiToken, listApiTokens, revokeApiToken, type ApiTokenRow } from '@/api/system'

interface TokenUiDependencies {
  translate: (key: string, params?: Record<string, unknown>) => string
  confirm: (options: {
    title: string
    message: string
    confirmText?: string
    cancelText?: string
    variant?: 'danger' | 'warning' | 'default'
  }) => Promise<boolean>
  success: (message: string) => void
  error: (message: string) => void
}

function apiErrorMessage(error: unknown, fallback: string) {
  if (typeof error !== 'object' || error === null) return fallback
  const response = 'response' in error ? error.response : undefined
  if (typeof response !== 'object' || response === null || !('data' in response)) return fallback
  const data = response.data
  if (typeof data !== 'object' || data === null || !('msg' in data)) return fallback
  return typeof data.msg === 'string' ? data.msg : fallback
}

export function useApiTokens(dependencies: TokenUiDependencies) {
  const { translate: t, confirm, success, error } = dependencies
  const tokens = ref<ApiTokenRow[]>([])
  const tokensLoading = ref(false)
  const tokenName = ref('')
  const tokenScopes = ref('read')
  const tokenExpiresDays = ref<number | null>(null)
  const tokenCreating = ref(false)
  const tokenPlaintext = ref('')
  const tokenPlaintextName = ref('')

  async function loadTokens() {
    tokensLoading.value = true
    try {
      const response = await listApiTokens()
      tokens.value = response.data ?? []
    } catch {
      error(t('common.errorOccurred'))
    } finally {
      tokensLoading.value = false
    }
  }

  async function onCreateToken() {
    if (!tokenName.value.trim()) {
      error(t('security.apiTokens.errorNameRequired'))
      return
    }
    tokenCreating.value = true
    try {
      const response = await createApiToken({
        name: tokenName.value.trim(),
        scopes: tokenScopes.value.trim() || 'read',
        expires_in_days: tokenExpiresDays.value ?? 0,
      })
      if (!response.data?.token) {
        error(response.msg || t('common.errorOccurred'))
        return
      }
      tokenPlaintext.value = response.data.token
      tokenPlaintextName.value = response.data.name
      tokenName.value = ''
      tokenExpiresDays.value = null
      await loadTokens()
    } catch (caught: unknown) {
      error(apiErrorMessage(caught, t('common.errorOccurred')))
    } finally {
      tokenCreating.value = false
    }
  }

  async function onRevokeToken(row: ApiTokenRow) {
    const accepted = await confirm({
      title: t('security.apiTokens.revokeConfirmTitle'),
      message: t('security.apiTokens.revokeConfirmBody', { name: row.name }),
      confirmText: t('common.confirm'),
      cancelText: t('common.cancel'),
      variant: 'danger',
    })
    if (!accepted) return
    try {
      await revokeApiToken(row.id)
      await loadTokens()
      success(t('security.apiTokens.revokedToast'))
    } catch {
      error(t('common.errorOccurred'))
    }
  }

  async function copyTokenToClipboard() {
    if (!tokenPlaintext.value) return
    try {
      await navigator.clipboard.writeText(tokenPlaintext.value)
      success(t('security.apiTokens.copied'))
    } catch {
      error(t('common.errorOccurred'))
    }
  }

  function formatTokenTimestamp(timestamp: number) {
    return timestamp ? new Date(timestamp * 1000).toLocaleString() : '—'
  }

  return {
    tokens,
    tokensLoading,
    tokenName,
    tokenScopes,
    tokenExpiresDays,
    tokenCreating,
    tokenPlaintext,
    tokenPlaintextName,
    loadTokens,
    onCreateToken,
    onRevokeToken,
    copyTokenToClipboard,
    formatTokenTimestamp,
  }
}
