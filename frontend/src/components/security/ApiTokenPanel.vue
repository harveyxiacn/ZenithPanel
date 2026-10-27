<script setup lang="ts">
import { onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { KeyIcon } from '@heroicons/vue/24/outline'
import { useApiTokens } from '@/composables/useApiTokens'
import { useConfirm } from '@/composables/useConfirm'
import { useToast } from '@/composables/useToast'

const { t } = useI18n()
const { confirm } = useConfirm()
const toast = useToast()
const {
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
} = useApiTokens({
  translate: (key, params) => t(key, params ?? {}),
  confirm,
  success: toast.success,
  error: toast.error,
})

onMounted(loadTokens)
</script>

<template>
  <section class="bg-white rounded-2xl shadow-sm border border-slate-100 overflow-hidden">
    <div class="p-6 border-b border-slate-100 flex items-center">
      <div class="bg-violet-500/10 text-violet-500 p-2 rounded-lg mr-4">
        <KeyIcon class="h-6 w-6" />
      </div>
      <div>
        <h3 class="text-lg font-medium text-slate-800">{{ $t('security.apiTokens.title') }}</h3>
        <p class="text-sm text-slate-500">{{ $t('security.apiTokens.subtitle') }}</p>
      </div>
    </div>
    <div class="p-6 space-y-5">
      <div v-if="tokenPlaintext" class="rounded-xl border border-emerald-200 bg-emerald-50 p-4">
        <div class="flex items-start justify-between gap-3">
          <div class="flex-1">
            <p class="text-sm font-medium text-emerald-800">{{ $t('security.apiTokens.justCreated', { name: tokenPlaintextName }) }}</p>
            <p class="text-xs text-emerald-700 mt-0.5">{{ $t('security.apiTokens.copyNowWarn') }}</p>
            <code class="block mt-2 font-mono text-xs bg-white border border-emerald-200 rounded-lg p-2 break-all">{{ tokenPlaintext }}</code>
          </div>
          <div class="flex flex-col gap-2">
            <button @click="copyTokenToClipboard" class="bg-emerald-500 hover:bg-emerald-600 text-white text-xs px-3 py-1.5 rounded-lg transition">
              {{ $t('security.apiTokens.copy') }}
            </button>
            <button @click="tokenPlaintext = ''" class="bg-white border border-emerald-200 hover:bg-emerald-100 text-emerald-700 text-xs px-3 py-1.5 rounded-lg transition">
              {{ $t('security.apiTokens.dismiss') }}
            </button>
          </div>
        </div>
      </div>

      <div class="grid grid-cols-1 md:grid-cols-4 gap-3 items-end">
        <div class="md:col-span-2">
          <label class="text-xs font-medium text-slate-500">{{ $t('security.apiTokens.nameLabel') }}</label>
          <input v-model="tokenName" :placeholder="$t('security.apiTokens.namePlaceholder')" class="input-field text-sm mt-1 w-full" />
        </div>
        <div>
          <label class="text-xs font-medium text-slate-500">{{ $t('security.apiTokens.scopesLabel') }}</label>
          <input v-model="tokenScopes" placeholder="read" class="input-field text-sm mt-1 w-full" />
        </div>
        <div>
          <label class="text-xs font-medium text-slate-500">{{ $t('security.apiTokens.expiresLabel') }}</label>
          <input v-model.number="tokenExpiresDays" type="number" min="0" placeholder="0" class="input-field text-sm mt-1 w-full" />
        </div>
      </div>
      <div class="flex justify-end">
        <button @click="onCreateToken" :disabled="tokenCreating || !tokenName.trim()" class="bg-violet-500 hover:bg-violet-600 disabled:opacity-50 text-white px-4 py-2 rounded-lg text-sm font-medium transition">
          {{ tokenCreating ? $t('security.apiTokens.creating') : $t('security.apiTokens.create') }}
        </button>
      </div>

      <div v-if="tokensLoading" class="text-sm text-slate-500">{{ $t('common.loading') }}</div>
      <div v-else-if="tokens.length === 0" class="text-sm text-slate-500">{{ $t('security.apiTokens.empty') }}</div>
      <div v-else class="overflow-x-auto">
        <table class="min-w-full text-sm">
          <thead class="text-xs text-slate-500 uppercase border-b border-slate-100">
            <tr>
              <th class="text-left py-2 pr-3 font-medium">{{ $t('security.apiTokens.colName') }}</th>
              <th class="text-left py-2 pr-3 font-medium">{{ $t('security.apiTokens.colScopes') }}</th>
              <th class="text-left py-2 pr-3 font-medium">{{ $t('security.apiTokens.colExpiry') }}</th>
              <th class="text-left py-2 pr-3 font-medium">{{ $t('security.apiTokens.colLastUsed') }}</th>
              <th class="text-left py-2 pr-3 font-medium">{{ $t('security.apiTokens.colStatus') }}</th>
              <th class="text-right py-2 font-medium">{{ $t('security.apiTokens.colActions') }}</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="row in tokens" :key="row.id" class="border-b border-slate-50 last:border-0">
              <td class="py-2 pr-3 font-mono text-xs text-slate-700">{{ row.name }}</td>
              <td class="py-2 pr-3 text-slate-600">{{ row.scopes }}</td>
              <td class="py-2 pr-3 text-slate-600">{{ row.expires_at ? formatTokenTimestamp(row.expires_at) : '—' }}</td>
              <td class="py-2 pr-3 text-slate-600">{{ formatTokenTimestamp(row.last_used_at) }}</td>
              <td class="py-2 pr-3">
                <span v-if="row.revoked" class="inline-flex items-center px-2 py-0.5 rounded-full text-xs bg-rose-50 text-rose-700">{{ $t('security.apiTokens.revoked') }}</span>
                <span v-else class="inline-flex items-center px-2 py-0.5 rounded-full text-xs bg-emerald-50 text-emerald-700">{{ $t('security.apiTokens.active') }}</span>
              </td>
              <td class="py-2 text-right">
                <button v-if="!row.revoked" @click="onRevokeToken(row)" class="text-xs bg-rose-50 hover:bg-rose-100 text-rose-700 px-2 py-1 rounded-md transition">
                  {{ $t('security.apiTokens.revoke') }}
                </button>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>
  </section>
</template>
