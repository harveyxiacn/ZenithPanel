<script setup lang="ts">
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { KeyIcon, LockClosedIcon, FingerPrintIcon, ShieldCheckIcon, ArrowPathIcon, ArrowDownTrayIcon } from '@heroicons/vue/24/outline'
import { checkForUpdate, applyUpdate, changePassword } from '@/api/system'

const { t } = useI18n()

// ---- Change Password ----
const showPasswordForm = ref(false)
const oldPassword = ref('')
const newPassword = ref('')
const confirmNewPassword = ref('')
const passwordChanging = ref(false)
const passwordMsg = ref('')
const passwordMsgType = ref<'success' | 'error'>('success')

async function onChangePassword() {
  passwordMsg.value = ''
  if (!oldPassword.value || !newPassword.value) {
    passwordMsg.value = t('security.auth.errorEmpty')
    passwordMsgType.value = 'error'
    return
  }
  if (newPassword.value !== confirmNewPassword.value) {
    passwordMsg.value = t('security.auth.errorMismatch')
    passwordMsgType.value = 'error'
    return
  }
  if (newPassword.value.length < 8) {
    passwordMsg.value = t('security.auth.errorShort')
    passwordMsgType.value = 'error'
    return
  }
  passwordChanging.value = true
  try {
    const res = await changePassword(oldPassword.value, newPassword.value) as any
    if (res.code === 200) {
      passwordMsg.value = t('security.auth.passwordChanged')
      passwordMsgType.value = 'success'
      oldPassword.value = ''
      newPassword.value = ''
      confirmNewPassword.value = ''
      showPasswordForm.value = false
    } else {
      passwordMsg.value = res.msg || 'Failed'
      passwordMsgType.value = 'error'
    }
  } catch (e: any) {
    passwordMsg.value = e?.response?.data?.msg || 'Failed to change password'
    passwordMsgType.value = 'error'
  }
  passwordChanging.value = false
}

// ---- Update ----
const updateChecking = ref(false)
const updateAvailable = ref(false)
const updateApplying = ref(false)
const currentImageID = ref('')
const latestImageID = ref('')
const updateError = ref('')
const updateChecked = ref(false)

async function onCheckUpdate() {
  updateChecking.value = true
  updateError.value = ''
  try {
    const res = await checkForUpdate() as any
    if (res.code === 200 && res.data) {
      updateAvailable.value = res.data.available
      currentImageID.value = res.data.current_id
      latestImageID.value = res.data.latest_id
      updateChecked.value = true
    } else {
      updateError.value = res.msg || 'Check failed'
    }
  } catch (e: any) {
    updateError.value = e?.response?.data?.msg || 'Failed to check for updates'
  }
  updateChecking.value = false
}

async function onApplyUpdate() {
  if (!confirm(t('security.update.confirmRestart'))) return
  updateApplying.value = true
  updateError.value = ''
  try {
    const res = await applyUpdate() as any
    if (res.code === 200) {
      // Panel is restarting via helper container — show countdown then poll until ready
      let countdown = 10
      updateError.value = ''
      const timer = setInterval(() => {
        countdown--
        updateError.value = t('security.update.restarting', { n: countdown })
        if (countdown <= 0) {
          clearInterval(timer)
          // Poll until the new panel is up, then reload
          const poll = setInterval(async () => {
            try {
              const r = await fetch('/api/v1/ping', { signal: AbortSignal.timeout(3000) })
              if (r.ok) { clearInterval(poll); window.location.reload() }
            } catch { /* still restarting */ }
          }, 2000)
        }
      }, 1000)
    } else {
      updateError.value = res.msg || 'Update failed'
      updateApplying.value = false
    }
  } catch (e: any) {
    updateError.value = e?.response?.data?.msg || 'Update request failed'
    updateApplying.value = false
  }
}
</script>

<template>
  <div class="py-2">
    <!-- Header -->
    <div class="mb-8">
      <h1 class="text-3xl font-bold text-slate-800 tracking-tight">{{ $t('security.title') }}</h1>
      <p class="text-slate-500 mt-1">{{ $t('security.subtitle') }}</p>
    </div>

    <div class="grid grid-cols-1 lg:grid-cols-3 gap-8">
      <!-- Left Column: Settings -->
      <div class="lg:col-span-2 space-y-6">

        <!-- Panel Update -->
        <div class="bg-white rounded-2xl shadow-sm border border-slate-100 overflow-hidden">
          <div class="p-6 border-b border-slate-100 flex items-center">
            <div class="bg-emerald-500/10 text-emerald-500 p-2 rounded-lg mr-4">
              <ArrowDownTrayIcon class="h-6 w-6" />
            </div>
            <div>
              <h3 class="text-lg font-medium text-slate-800">{{ $t('security.update.title') }}</h3>
              <p class="text-sm text-slate-500">{{ $t('security.update.subtitle') }}</p>
            </div>
          </div>
          <div class="p-6 space-y-4">
            <div v-if="updateChecked" class="space-y-3">
              <div class="flex items-center justify-between text-sm">
                <span class="text-slate-500">{{ $t('security.update.currentImage') }}</span>
                <code class="bg-slate-100 px-2 py-0.5 rounded text-xs text-slate-700">{{ currentImageID }}</code>
              </div>
              <div class="flex items-center justify-between text-sm">
                <span class="text-slate-500">{{ $t('security.update.latestImage') }}</span>
                <code class="bg-slate-100 px-2 py-0.5 rounded text-xs text-slate-700">{{ latestImageID }}</code>
              </div>
              <div v-if="updateAvailable" class="bg-amber-50 border border-amber-200 rounded-lg p-3 text-sm text-amber-700">
                {{ $t('security.update.available') }}
              </div>
              <div v-else class="bg-emerald-50 border border-emerald-200 rounded-lg p-3 text-sm text-emerald-700">
                {{ $t('security.update.upToDate') }}
              </div>
            </div>

            <div v-if="updateError" class="bg-rose-50 border border-rose-200 rounded-lg p-3 text-sm text-rose-700">
              {{ updateError }}
            </div>

            <div class="flex items-center space-x-3 pt-2">
              <button
                @click="onCheckUpdate"
                :disabled="updateChecking || updateApplying"
                class="flex items-center bg-slate-100 hover:bg-slate-200 disabled:opacity-50 text-slate-700 px-4 py-2 rounded-lg text-sm font-medium transition"
              >
                <ArrowPathIcon class="h-4 w-4 mr-2" :class="{ 'animate-spin': updateChecking }" />
                {{ updateChecking ? $t('security.update.checking') : $t('security.update.checkForUpdates') }}
              </button>
              <button
                v-if="updateAvailable"
                @click="onApplyUpdate"
                :disabled="updateApplying"
                class="flex items-center bg-primary-600 hover:bg-primary-700 disabled:opacity-50 text-white px-4 py-2 rounded-lg text-sm font-medium transition"
              >
                <ArrowDownTrayIcon class="h-4 w-4 mr-2" />
                {{ updateApplying ? $t('security.update.updating') : $t('security.update.updateNow') }}
              </button>
            </div>
          </div>
        </div>

        <!-- Authentication Settings -->
        <div class="bg-white rounded-2xl shadow-sm border border-slate-100 overflow-hidden">
          <div class="p-6 border-b border-slate-100 flex items-center">
            <div class="bg-rose-500/10 text-rose-500 p-2 rounded-lg mr-4">
              <KeyIcon class="h-6 w-6" />
            </div>
            <div>
              <h3 class="text-lg font-medium text-slate-800">{{ $t('security.auth.title') }}</h3>
              <p class="text-sm text-slate-500">{{ $t('security.auth.subtitle') }}</p>
            </div>
          </div>
          <div class="p-6 space-y-4">
            <div class="flex items-center justify-between">
              <div class="flex items-center">
                <LockClosedIcon class="h-5 w-5 text-slate-400 mr-3" />
                <div>
                  <h4 class="text-sm font-medium text-slate-900">{{ $t('security.auth.panelPassword') }}</h4>
                  <p class="text-xs text-slate-500">{{ $t('security.auth.panelPasswordDesc') }}</p>
                </div>
              </div>
              <button @click="showPasswordForm = !showPasswordForm" class="bg-slate-100 hover:bg-slate-200 text-slate-700 px-4 py-2 rounded-lg text-sm font-medium transition">{{ $t('common.change') }}</button>
            </div>

            <!-- Password Change Form -->
            <div v-if="showPasswordForm" class="pt-3 space-y-3">
              <input v-model="oldPassword" type="password" :placeholder="$t('security.auth.oldPassword')" class="w-full border border-slate-300 rounded-lg px-3 py-2 text-sm focus:border-primary-500 focus:ring-primary-500" />
              <input v-model="newPassword" type="password" :placeholder="$t('security.auth.newPasswordField')" class="w-full border border-slate-300 rounded-lg px-3 py-2 text-sm focus:border-primary-500 focus:ring-primary-500" />
              <input v-model="confirmNewPassword" type="password" :placeholder="$t('security.auth.confirmNewPassword')" class="w-full border border-slate-300 rounded-lg px-3 py-2 text-sm focus:border-primary-500 focus:ring-primary-500" />
              <div v-if="passwordMsg" :class="['text-sm p-2 rounded-lg', passwordMsgType === 'success' ? 'bg-emerald-50 text-emerald-700' : 'bg-rose-50 text-rose-700']">{{ passwordMsg }}</div>
              <button @click="onChangePassword" :disabled="passwordChanging" class="bg-primary-600 hover:bg-primary-700 disabled:opacity-50 text-white px-4 py-2 rounded-lg text-sm font-medium transition">
                {{ passwordChanging ? $t('common.loading') : $t('security.auth.savePassword') }}
              </button>
            </div>

            <div class="flex items-center justify-between pt-4 border-t border-slate-100">
              <div class="flex items-center">
                <FingerPrintIcon class="h-5 w-5 text-slate-400 mr-3" />
                <div>
                  <h4 class="text-sm font-medium text-slate-900">{{ $t('security.auth.twoFactor') }}</h4>
                  <p class="text-xs text-slate-400 font-medium mt-0.5">{{ $t('security.auth.comingSoon') }}</p>
                </div>
              </div>
            </div>
          </div>
        </div>

      </div>

      <!-- Right Column: Info -->
      <div class="bg-white rounded-2xl shadow-sm border border-slate-100 overflow-hidden h-fit">
        <div class="p-6 border-b border-slate-100 flex items-center">
          <ShieldCheckIcon class="h-6 w-6 text-emerald-500 mr-2" />
          <h3 class="text-lg font-medium text-slate-800">{{ $t('security.tips.title') }}</h3>
        </div>
        <div class="p-6 space-y-4">
          <div class="text-sm text-slate-600 space-y-3">
            <p>{{ $t('security.tips.intro') }}</p>
            <ul class="list-disc list-inside space-y-2 text-slate-500">
              <li>{{ $t('security.tips.strongPassword') }}</li>
              <li>{{ $t('security.tips.enable2fa') }}</li>
              <li>{{ $t('security.tips.keepUpdated') }}</li>
              <li>{{ $t('security.tips.useHttps') }}</li>
              <li>{{ $t('security.tips.restrictApi') }}</li>
            </ul>
          </div>
        </div>
      </div>

    </div>
  </div>
</template>
