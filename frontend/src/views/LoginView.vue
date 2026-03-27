<script setup lang="ts">
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import { useAuthStore } from '@/store/auth'
import { login } from '@/api/auth'
import { useI18n } from 'vue-i18n'

const router = useRouter()
const authStore = useAuthStore()
const { t } = useI18n()

const username = ref('')
const password = ref('')
const totpCode = ref('')
const showTOTP = ref(false)
const loading = ref(false)
const errorMsg = ref('')

const handleLogin = async () => {
  errorMsg.value = ''

  if (!username.value || !password.value) {
    errorMsg.value = t('login.errorEmpty')
    return
  }

  if (showTOTP.value && !totpCode.value) {
    errorMsg.value = t('login.enterTOTP')
    return
  }

  loading.value = true

  try {
    const res: any = await login(username.value, password.value, showTOTP.value ? totpCode.value : undefined)
    if (res.code === 200 && res.data?.requires_2fa) {
      showTOTP.value = true
      loading.value = false
      return
    }
    if (res.code === 200 && res.data?.token) {
      authStore.setToken(res.data.token)
      router.push('/')
    } else {
      errorMsg.value = res.msg || 'Login failed'
    }
  } catch (err: any) {
    if (err.response?.status === 429) {
      const data = err.response?.data?.data
      if (data?.locked) {
        errorMsg.value = t('login.errorLocked', { n: data.minutes })
      } else {
        errorMsg.value = t('login.errorRateLimit')
      }
    } else {
      errorMsg.value = err.response?.data?.msg || err.message || 'Network error'
    }
  } finally {
    loading.value = false
  }
}
</script>

<template>
  <div class="min-h-screen flex items-center justify-center bg-slate-50 dark:bg-slate-900 p-4">
    <div class="glass-panel w-full max-w-md p-8 rounded-2xl shadow-xl bg-white dark:bg-slate-800">
      <div class="text-center mb-8">
        <h1 class="text-2xl font-bold text-slate-800 dark:text-white tracking-tight">{{ $t('login.title') }}</h1>
      </div>

      <!-- Error Alert -->
      <div v-if="errorMsg" class="mb-6 p-4 rounded-xl bg-red-50 border border-red-200 text-red-600 text-sm flex items-start">
        <svg class="w-5 h-5 mr-2 shrink-0" fill="currentColor" viewBox="0 0 20 20"><path fill-rule="evenodd" d="M18 10a8 8 0 11-16 0 8 8 0 0116 0zm-7 4a1 1 0 11-2 0 1 1 0 012 0zm-1-9a1 1 0 00-1 1v4a1 1 0 102 0V6a1 1 0 00-1-1z" clip-rule="evenodd"></path></svg>
        <span>{{ errorMsg }}</span>
      </div>

      <form @submit.prevent="handleLogin" class="space-y-4">
        <div>
          <label class="block text-sm font-medium text-slate-600 dark:text-slate-300 mb-1">{{ $t('login.username') }}</label>
          <input
            type="text"
            v-model="username"
            class="input-field"
            placeholder="admin"
            :disabled="loading || showTOTP"
          />
        </div>
        <div>
          <label class="block text-sm font-medium text-slate-600 dark:text-slate-300 mb-1">{{ $t('login.password') }}</label>
          <input
            type="password"
            v-model="password"
            class="input-field"
            placeholder="••••••••"
            :disabled="loading || showTOTP"
          />
        </div>

        <!-- TOTP Input (shown after password verified) -->
        <div v-if="showTOTP" class="pt-2">
          <label class="block text-sm font-medium text-slate-600 dark:text-slate-300 mb-1">{{ $t('login.totpCode') }}</label>
          <input
            type="text"
            v-model="totpCode"
            class="input-field text-center text-lg tracking-widest"
            :placeholder="$t('login.totpPlaceholder')"
            inputmode="numeric"
            maxlength="8"
            autocomplete="one-time-code"
            :disabled="loading"
          />
          <p class="text-xs text-slate-400 mt-1">{{ $t('login.totpHint') }}</p>
        </div>

        <button
          type="submit"
          :disabled="loading"
          class="btn-primary w-full mt-2 flex justify-center items-center"
        >
          <span v-if="loading" class="w-5 h-5 border-2 border-white/30 border-t-white rounded-full animate-spin"></span>
          <span v-else>{{ $t('login.submit') }}</span>
        </button>
      </form>
    </div>
  </div>
</template>
