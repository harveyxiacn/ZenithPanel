<script setup lang="ts">
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import { useAuthStore } from '@/store/auth'
import { login } from '@/api/auth'

const router = useRouter()
const authStore = useAuthStore()

const username = ref('')
const password = ref('')
const loading = ref(false)
const errorMsg = ref('')

const handleLogin = async () => {
  errorMsg.value = ''

  if (!username.value || !password.value) {
    errorMsg.value = 'Please enter both username and password'
    return
  }

  loading.value = true

  try {
    const res: any = await login(username.value, password.value)
    if (res.code === 200 && res.data?.token) {
      authStore.setToken(res.data.token)
      router.push('/dashboard')
    } else {
      errorMsg.value = res.msg || 'Login failed'
    }
  } catch (err: any) {
    if (err.response?.status === 429) {
      errorMsg.value = 'Too many login attempts. Please try again later.'
    } else {
      errorMsg.value = err.response?.data?.msg || err.message || 'Network error'
    }
  } finally {
    loading.value = false
  }
}
</script>

<template>
  <div class="min-h-screen flex items-center justify-center bg-slate-50 p-4">
    <div class="glass-panel w-full max-w-md p-8 rounded-2xl shadow-xl bg-white">
      <div class="text-center mb-8">
        <h1 class="text-2xl font-bold text-slate-800 tracking-tight">Login to Zenith</h1>
      </div>

      <!-- Error Alert -->
      <div v-if="errorMsg" class="mb-6 p-4 rounded-xl bg-red-50 border border-red-200 text-red-600 text-sm flex items-start">
        <svg class="w-5 h-5 mr-2 shrink-0" fill="currentColor" viewBox="0 0 20 20"><path fill-rule="evenodd" d="M18 10a8 8 0 11-16 0 8 8 0 0116 0zm-7 4a1 1 0 11-2 0 1 1 0 012 0zm-1-9a1 1 0 00-1 1v4a1 1 0 102 0V6a1 1 0 00-1-1z" clip-rule="evenodd"></path></svg>
        <span>{{ errorMsg }}</span>
      </div>

      <form @submit.prevent="handleLogin" class="space-y-4">
        <div>
          <label class="block text-sm font-medium text-slate-600 mb-1">Username</label>
          <input
            type="text"
            v-model="username"
            class="input-field"
            placeholder="admin"
            :disabled="loading"
          />
        </div>
        <div>
          <label class="block text-sm font-medium text-slate-600 mb-1">Password</label>
          <input
            type="password"
            v-model="password"
            class="input-field"
            placeholder="••••••••"
            :disabled="loading"
          />
        </div>
        <button
          type="submit"
          :disabled="loading"
          class="btn-primary w-full mt-2 flex justify-center items-center"
        >
          <span v-if="loading" class="w-5 h-5 border-2 border-white/30 border-t-white rounded-full animate-spin"></span>
          <span v-else>Login</span>
        </button>
      </form>
    </div>
  </div>
</template>
