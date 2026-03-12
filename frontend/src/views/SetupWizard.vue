<script setup lang="ts">
import { ref, reactive } from 'vue'
import { useRouter } from 'vue-router'
import { useAuthStore } from '@/store/auth'
import { setupLogin, setupComplete } from '@/api/auth'
import { CheckCircleIcon, ShieldCheckIcon, CubeTransparentIcon, ArrowRightIcon } from '@heroicons/vue/24/outline'

const router = useRouter()
const authStore = useAuthStore()

const step = ref(1)
const loading = ref(false)
const origin = window.location.origin
const errorMsg = ref('')

const form = reactive({
  initialPassword: '',
  newPassword: '',
  confirmPassword: '',
  customPanelPath: '/zenith',
  customSshPort: 22,
  enable2FA: false
})

const handleInitialLogin = async () => {
  errorMsg.value = ''
  if (!form.initialPassword) {
    errorMsg.value = 'Please enter the initial password printed in your console'
    return
  }
  loading.value = true

  try {
    const res: any = await setupLogin(form.initialPassword)
    if (res.code === 200 && res.data?.token) {
      authStore.setToken(res.data.token)
      step.value = 2
    } else {
      errorMsg.value = res.msg || 'Verification failed'
    }
  } catch (err: any) {
    errorMsg.value = err.response?.data?.msg || err.message || 'Network error'
  } finally {
    loading.value = false
  }
}

const handleCompleteSetup = async () => {
  errorMsg.value = ''
  if (form.newPassword !== form.confirmPassword) {
    errorMsg.value = 'Passwords do not match'
    return
  }
  if (form.newPassword.length < 8) {
    errorMsg.value = 'New password must be at least 8 characters'
    return
  }

  loading.value = true

  try {
    const res: any = await setupComplete({
      username: 'admin',
      password: form.newPassword,
      panel_path: form.customPanelPath
    })
    if (res.code === 200) {
      step.value = 3
    } else {
      errorMsg.value = res.msg || 'Setup failed'
    }
  } catch (err: any) {
    errorMsg.value = err.response?.data?.msg || err.message || 'Network error'
  } finally {
    loading.value = false
  }
}

const goToLogin = () => {
  router.push('/login')
}
</script>

<template>
  <div class="min-h-screen flex items-center justify-center bg-gray-900 bg-[url('https://www.transparenttextures.com/patterns/stardust.png')] p-4">
    <!-- Backdrop blur effect for modern UI -->
    <div class="absolute inset-0 bg-gradient-to-br from-primary-900/40 via-gray-900/80 to-gray-900 pointer-events-none"></div>

    <div class="glass-panel w-full max-w-md p-8 relative z-10 animate-slide-up rounded-2xl border border-white/10 shadow-2xl bg-gray-800/60 backdrop-blur-xl">
      <!-- Header -->
      <div class="text-center mb-8">
        <div class="inline-flex items-center justify-center w-16 h-16 rounded-2xl bg-gradient-to-tr from-primary-500 to-primary-300 shadow-lg mb-4">
          <CubeTransparentIcon class="w-8 h-8 text-white" />
        </div>
        <h1 class="text-3xl font-bold text-white tracking-tight">ZenithPanel</h1>
        <p class="text-primary-200 mt-2 font-medium">Secure Initialization Wizard</p>
      </div>

      <!-- Error Alert -->
      <div v-if="errorMsg" class="mb-6 p-4 rounded-xl bg-red-500/10 border border-red-500/20 text-red-200 text-sm flex items-start animate-fade-in">
        <svg class="w-5 h-5 mr-2 shrink-0" fill="currentColor" viewBox="0 0 20 20"><path fill-rule="evenodd" d="M18 10a8 8 0 11-16 0 8 8 0 0116 0zm-7 4a1 1 0 11-2 0 1 1 0 012 0zm-1-9a1 1 0 00-1 1v4a1 1 0 102 0V6a1 1 0 00-1-1z" clip-rule="evenodd"></path></svg>
        <span>{{ errorMsg }}</span>
      </div>

      <!-- Step 1: Verify Access -->
      <div v-if="step === 1" class="animate-fade-in space-y-5">
        <div class="bg-primary-500/10 border border-primary-500/20 rounded-xl p-4 text-sm text-primary-100 mb-6 leading-relaxed">
          Welcome to your new instance! To protect against unauthorized scanning, please enter the temporary password generated in your server console.
        </div>

        <div>
          <label class="block text-sm font-medium text-gray-300 mb-2">Initial One-Time Password</label>
          <input
            type="password"
            v-model="form.initialPassword"
            class="w-full bg-gray-900/50 border border-gray-600 text-white rounded-xl px-4 py-3 outline-none focus:border-primary-500 focus:ring-2 focus:ring-primary-500/20 transition-all font-mono placeholder:font-sans placeholder-gray-500"
            placeholder="e.g. j9Xm2PqL8vK4wHzC"
            :disabled="loading"
            @keyup.enter="handleInitialLogin"
          />
        </div>

        <button
          @click="handleInitialLogin"
          :disabled="loading"
          class="w-full relative overflow-hidden bg-primary-600 text-white font-medium py-3 px-4 rounded-xl transition-all duration-300 hover:bg-primary-500 hover:shadow-[0_0_20px_rgba(34,197,94,0.4)] active:scale-[0.98] disabled:opacity-70 flex justify-center items-center mt-6"
        >
          <span v-if="loading" class="w-5 h-5 border-2 border-white/30 border-t-white rounded-full animate-spin"></span>
          <span v-else class="flex items-center">Verify Identity <ArrowRightIcon class="w-4 h-4 ml-2" /></span>
        </button>
      </div>

      <!-- Step 2: Configure System -->
      <div v-else-if="step === 2" class="animate-fade-in space-y-5">
        <div class="flex items-center space-x-2 text-green-400 mb-6">
          <ShieldCheckIcon class="w-5 h-5" />
          <span class="font-medium text-sm">Identity Verified. Configure Panel Security.</span>
        </div>

        <div>
          <label class="block text-sm font-medium text-gray-300 mb-2">New Admin Password</label>
          <input
            type="password"
            v-model="form.newPassword"
            class="w-full bg-gray-900/50 border border-gray-600 text-white rounded-xl px-4 py-3 outline-none focus:border-primary-500 transition-all"
            placeholder="Minimum 8 characters"
            :disabled="loading"
          />
        </div>

        <div>
          <label class="block text-sm font-medium text-gray-300 mb-2">Confirm Password</label>
          <input
            type="password"
            v-model="form.confirmPassword"
            class="w-full bg-gray-900/50 border border-gray-600 text-white rounded-xl px-4 py-3 outline-none focus:border-primary-500 transition-all"
            placeholder="Re-type new password"
            :disabled="loading"
          />
        </div>

        <div class="h-px bg-gray-700/50 my-6"></div>

        <div>
          <label class="block text-sm font-medium text-gray-300 mb-2">Custom Panel URL Path</label>
          <div class="flex items-center rounded-xl bg-gray-900/50 border border-gray-600 focus-within:border-primary-500 transition-all overflow-hidden p-1">
            <span class="pl-3 pr-2 text-gray-500 font-mono text-sm leading-none pt-1">/</span>
            <input
              type="text"
              v-model="form.customPanelPath"
              class="w-full bg-transparent text-white border-none py-2 px-1 outline-none text-sm placeholder-gray-500 font-mono"
              placeholder="my-secret-panel"
              :disabled="loading"
            />
          </div>
          <p class="text-xs text-gray-400 mt-2">Change to prevent automated scanning.</p>
        </div>

        <button
          @click="handleCompleteSetup"
          :disabled="loading"
          class="w-full relative overflow-hidden bg-primary-600 text-white font-medium py-3 px-4 rounded-xl transition-all duration-300 hover:bg-primary-500 hover:shadow-[0_0_20px_rgba(34,197,94,0.4)] active:scale-[0.98] disabled:opacity-70 flex justify-center items-center mt-8"
        >
          <span v-if="loading" class="w-5 h-5 border-2 border-white/30 border-t-white rounded-full animate-spin"></span>
          <span v-else class="flex items-center">Apply & Initialize Zenith</span>
        </button>
      </div>

      <!-- Step 3: Success -->
      <div v-else-if="step === 3" class="animate-fade-in text-center py-6">
        <CheckCircleIcon class="w-20 h-20 text-green-400 mx-auto mb-6 drop-shadow-[0_0_15px_rgba(74,222,128,0.5)]" />
        <h2 class="text-2xl font-bold text-white mb-2">Initialization Complete!</h2>
        <p class="text-gray-300 text-sm mb-6 leading-relaxed">
          The temporary setup token has been destroyed. Your panel is now securely locked.
          <br/><br/>
          Please bookmark your new panel URL:<br/>
          <strong class="text-primary-400 select-all p-2 rounded bg-gray-900 inline-block mt-2 font-mono border border-gray-700">{{ origin }}{{ form.customPanelPath.startsWith('/') ? form.customPanelPath : '/' + form.customPanelPath }}</strong>
        </p>

        <button
          @click="goToLogin"
          class="w-full bg-white text-gray-900 font-bold py-3 px-4 rounded-xl transition-all hover:bg-gray-100 active:scale-[0.98]"
        >
          Go to Login
        </button>
      </div>

    </div>
  </div>
</template>

<style scoped>
/* Scoped styles kept minimal due to Tailwind usage */
</style>
