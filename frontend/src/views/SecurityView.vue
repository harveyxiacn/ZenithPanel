<script setup lang="ts">
import { ref, onMounted, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { KeyIcon, LockClosedIcon, FingerPrintIcon, ShieldCheckIcon, ArrowPathIcon, ArrowDownTrayIcon, GlobeAltIcon, Cog6ToothIcon } from '@heroicons/vue/24/outline'
import { checkForUpdate, applyUpdate, changePassword, get2FAStatus, setup2FA, verify2FA, disable2FA, getTLSStatus, uploadTLSCerts, removeTLS, getAccessConfig, updateAccessConfig, restartPanel } from '@/api/system'

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

// ---- 2FA ----
const twoFAEnabled = ref(false)
const twoFALoading = ref(false)
const twoFAStep = ref<'idle' | 'setup' | 'verify' | 'codes'>('idle')
const twoFAQR = ref('')
const twoFASecret = ref('')
const twoFARecoveryCodes = ref<string[]>([])
const twoFACode = ref('')
const twoFAMsg = ref('')
const twoFADisablePassword = ref('')
const showDisable2FA = ref(false)
const codesSaved = ref(false)

async function load2FAStatus() {
  try {
    const res = await get2FAStatus() as any
    if (res.code === 200) twoFAEnabled.value = res.data.enabled
  } catch { /* ignore */ }
}

async function onSetup2FA() {
  twoFALoading.value = true
  twoFAMsg.value = ''
  try {
    const res = await setup2FA() as any
    if (res.code === 200) {
      twoFAQR.value = res.data.qr_base64
      twoFASecret.value = res.data.secret
      twoFARecoveryCodes.value = res.data.recovery_codes
      twoFAStep.value = 'setup'
    } else {
      twoFAMsg.value = res.msg || 'Failed'
    }
  } catch (e: any) {
    twoFAMsg.value = e?.response?.data?.msg || 'Failed'
  }
  twoFALoading.value = false
}

async function onVerify2FA() {
  if (!twoFACode.value) return
  twoFALoading.value = true
  twoFAMsg.value = ''
  try {
    const res = await verify2FA(twoFACode.value) as any
    if (res.code === 200) {
      twoFAStep.value = 'codes'
      twoFAEnabled.value = true
    } else {
      twoFAMsg.value = res.msg || 'Invalid code'
    }
  } catch (e: any) {
    twoFAMsg.value = e?.response?.data?.msg || 'Invalid code'
  }
  twoFALoading.value = false
}

async function onDisable2FA() {
  if (!twoFADisablePassword.value) return
  twoFALoading.value = true
  twoFAMsg.value = ''
  try {
    const res = await disable2FA(twoFADisablePassword.value) as any
    if (res.code === 200) {
      twoFAEnabled.value = false
      showDisable2FA.value = false
      twoFADisablePassword.value = ''
      twoFAStep.value = 'idle'
    } else {
      twoFAMsg.value = res.msg || 'Failed'
    }
  } catch (e: any) {
    twoFAMsg.value = e?.response?.data?.msg || 'Failed'
  }
  twoFALoading.value = false
}

function finish2FASetup() {
  twoFAStep.value = 'idle'
  twoFACode.value = ''
  codesSaved.value = false
}

// ---- TLS ----
const tlsEnabled = ref(false)
const tlsLoading = ref(false)
const tlsMsg = ref('')
const tlsMsgType = ref<'success' | 'error'>('success')
const certFile = ref<File | null>(null)
const keyFile = ref<File | null>(null)
const showCFGuide = ref(false)

async function loadTLSStatus() {
  try {
    const res = await getTLSStatus() as any
    if (res.code === 200) tlsEnabled.value = res.data.enabled
  } catch { /* ignore */ }
}

async function onUploadTLS() {
  if (!certFile.value || !keyFile.value) {
    tlsMsg.value = t('security.tls.selectFiles')
    tlsMsgType.value = 'error'
    return
  }
  tlsLoading.value = true
  tlsMsg.value = ''
  const fd = new FormData()
  fd.append('cert', certFile.value)
  fd.append('key', keyFile.value)
  try {
    const res = await uploadTLSCerts(fd) as any
    if (res.code === 200) {
      tlsMsg.value = res.msg
      tlsMsgType.value = 'success'
      tlsEnabled.value = true
    } else {
      tlsMsg.value = res.msg || 'Upload failed'
      tlsMsgType.value = 'error'
    }
  } catch (e: any) {
    tlsMsg.value = e?.response?.data?.msg || 'Upload failed'
    tlsMsgType.value = 'error'
  }
  tlsLoading.value = false
}

async function onRemoveTLS() {
  if (!confirm(t('security.tls.confirmRemove'))) return
  tlsLoading.value = true
  try {
    const res = await removeTLS() as any
    if (res.code === 200) {
      tlsEnabled.value = false
      tlsMsg.value = res.msg
      tlsMsgType.value = 'success'
    }
  } catch (e: any) {
    tlsMsg.value = e?.response?.data?.msg || 'Failed'
    tlsMsgType.value = 'error'
  }
  tlsLoading.value = false
}

// ---- Access Configuration ----
const accessPath = ref('')
const accessPort = ref('')
const accessLoading = ref(false)
const accessMsg = ref('')
const accessMsgType = ref<'success' | 'error'>('success')

async function loadAccessConfig() {
  try {
    const res = await getAccessConfig() as any
    if (res.code === 200) {
      accessPath.value = res.data.panel_path || ''
      accessPort.value = res.data.port || ''
      accessOriginalPort.value = res.data.port || ''
    }
  } catch { /* ignore */ }
}

const accessRestarting = ref(false)
const accessOriginalPort = ref('')

async function onSaveAccess() {
  accessLoading.value = true
  accessMsg.value = ''
  try {
    const res = await updateAccessConfig({
      panel_path: accessPath.value,
      port: accessPort.value,
    }) as any
    if (res.code === 200) {
      accessMsg.value = res.msg
      accessMsgType.value = 'success'
    } else {
      accessMsg.value = res.msg || 'Failed'
      accessMsgType.value = 'error'
    }
  } catch (e: any) {
    accessMsg.value = e?.response?.data?.msg || 'Failed to save'
    accessMsgType.value = 'error'
  }
  accessLoading.value = false
}

const accessNeedsRestart = computed(() => accessPort.value !== accessOriginalPort.value && accessPort.value !== '')

async function onRestartPanel() {
  const newPort = accessPort.value
  const newPath = accessPath.value
  if (!confirm(t('security.access.confirmRestart'))) return
  accessRestarting.value = true
  accessMsg.value = ''
  try {
    // Save first, then restart
    await updateAccessConfig({ panel_path: newPath, port: newPort })
    const res = await restartPanel() as any
    if (res.code === 200) {
      let countdown = 10
      const timer = setInterval(() => {
        countdown--
        accessMsg.value = t('security.update.restarting', { n: countdown })
        if (countdown <= 0) {
          clearInterval(timer)
          // Build new URL with potentially new port
          const proto = window.location.protocol
          const host = window.location.hostname
          const path = newPath ? '/' + newPath + '/' : '/'
          const newUrl = `${proto}//${host}:${newPort}${path}`
          const poll = setInterval(async () => {
            try {
              const r = await fetch(`${proto}//${host}:${newPort}/api/v1/ping`, { signal: AbortSignal.timeout(3000) })
              if (r.ok) { clearInterval(poll); window.location.href = newUrl }
            } catch { /* still restarting */ }
          }, 2000)
        }
      }, 1000)
    } else {
      accessMsg.value = res.msg || 'Restart failed'
      accessMsgType.value = 'error'
      accessRestarting.value = false
    }
  } catch (e: any) {
    accessMsg.value = e?.response?.data?.msg || 'Restart failed'
    accessMsgType.value = 'error'
    accessRestarting.value = false
  }
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
      let countdown = 10
      updateError.value = ''
      const timer = setInterval(() => {
        countdown--
        updateError.value = t('security.update.restarting', { n: countdown })
        if (countdown <= 0) {
          clearInterval(timer)
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

// ---- Port Security ----
const panelPort = ref(window.location.port || (window.location.protocol === 'https:' ? '443' : '80'))
const commonPorts = [80, 443, 8080, 8443, 8888, 2053, 2083, 2087, 2096, 3000, 5000]
const isCommonPort = computed(() => commonPorts.includes(Number(panelPort.value)))

onMounted(() => {
  load2FAStatus()
  loadTLSStatus()
  loadAccessConfig()
})
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
              <button @click="onCheckUpdate" :disabled="updateChecking || updateApplying" class="flex items-center bg-slate-100 hover:bg-slate-200 disabled:opacity-50 text-slate-700 px-4 py-2 rounded-lg text-sm font-medium transition">
                <ArrowPathIcon class="h-4 w-4 mr-2" :class="{ 'animate-spin': updateChecking }" />
                {{ updateChecking ? $t('security.update.checking') : $t('security.update.checkForUpdates') }}
              </button>
              <button v-if="updateAvailable" @click="onApplyUpdate" :disabled="updateApplying" class="flex items-center bg-primary-600 hover:bg-primary-700 disabled:opacity-50 text-white px-4 py-2 rounded-lg text-sm font-medium transition">
                <ArrowDownTrayIcon class="h-4 w-4 mr-2" />
                {{ updateApplying ? $t('security.update.updating') : $t('security.update.updateNow') }}
              </button>
            </div>
          </div>
        </div>

        <!-- HTTPS / TLS Configuration -->
        <div class="bg-white rounded-2xl shadow-sm border border-slate-100 overflow-hidden">
          <div class="p-6 border-b border-slate-100 flex items-center justify-between">
            <div class="flex items-center">
              <div class="bg-blue-500/10 text-blue-500 p-2 rounded-lg mr-4">
                <GlobeAltIcon class="h-6 w-6" />
              </div>
              <div>
                <h3 class="text-lg font-medium text-slate-800">{{ $t('security.tls.title') }}</h3>
                <p class="text-sm text-slate-500">{{ $t('security.tls.subtitle') }}</p>
              </div>
            </div>
            <span :class="['px-2.5 py-0.5 rounded-full text-xs font-medium', tlsEnabled ? 'bg-emerald-100 text-emerald-700' : 'bg-amber-100 text-amber-700']">
              {{ tlsEnabled ? $t('security.tls.statusEnabled') : $t('security.tls.statusDisabled') }}
            </span>
          </div>
          <div class="p-6 space-y-4">
            <!-- Upload Certs -->
            <div class="space-y-3">
              <div>
                <label class="block text-sm font-medium text-slate-700 mb-1">{{ $t('security.tls.uploadCert') }}</label>
                <input type="file" accept=".pem,.crt,.cer" @change="certFile = ($event.target as HTMLInputElement).files?.[0] || null" class="w-full text-sm text-slate-500 file:mr-3 file:py-1.5 file:px-3 file:rounded-lg file:border-0 file:text-sm file:font-medium file:bg-slate-100 file:text-slate-700 hover:file:bg-slate-200" />
              </div>
              <div>
                <label class="block text-sm font-medium text-slate-700 mb-1">{{ $t('security.tls.uploadKey') }}</label>
                <input type="file" accept=".pem,.key" @change="keyFile = ($event.target as HTMLInputElement).files?.[0] || null" class="w-full text-sm text-slate-500 file:mr-3 file:py-1.5 file:px-3 file:rounded-lg file:border-0 file:text-sm file:font-medium file:bg-slate-100 file:text-slate-700 hover:file:bg-slate-200" />
              </div>
              <div class="flex items-center space-x-3">
                <button @click="onUploadTLS" :disabled="tlsLoading" class="bg-primary-600 hover:bg-primary-700 disabled:opacity-50 text-white px-4 py-2 rounded-lg text-sm font-medium transition">
                  {{ $t('security.tls.uploadBtn') }}
                </button>
                <button v-if="tlsEnabled" @click="onRemoveTLS" :disabled="tlsLoading" class="bg-rose-100 hover:bg-rose-200 text-rose-700 px-4 py-2 rounded-lg text-sm font-medium transition">
                  {{ $t('security.tls.removeBtn') }}
                </button>
              </div>
              <p class="text-xs text-slate-400">{{ $t('security.tls.restartNote') }}</p>
            </div>

            <div v-if="tlsMsg" :class="['text-sm p-2 rounded-lg', tlsMsgType === 'success' ? 'bg-emerald-50 text-emerald-700' : 'bg-rose-50 text-rose-700']">{{ tlsMsg }}</div>

            <!-- Cloudflare Guide -->
            <div class="border-t border-slate-100 pt-4">
              <button @click="showCFGuide = !showCFGuide" class="flex items-center text-sm font-medium text-blue-600 hover:text-blue-700">
                <span class="mr-1">{{ showCFGuide ? '▼' : '▶' }}</span>
                {{ $t('security.tls.cfGuideTitle') }}
              </button>
              <div v-if="showCFGuide" class="mt-3 space-y-2 text-sm text-slate-600 bg-slate-50 rounded-lg p-4">
                <p class="font-medium text-slate-700">{{ $t('security.tls.cfGuideIntro') }}</p>
                <ol class="list-decimal list-inside space-y-1.5">
                  <li>{{ $t('security.tls.cfStep1') }}</li>
                  <li>{{ $t('security.tls.cfStep2') }}</li>
                  <li>{{ $t('security.tls.cfStep3') }}</li>
                  <li>{{ $t('security.tls.cfStep4') }}</li>
                  <li>{{ $t('security.tls.cfStep5') }}</li>
                </ol>
                <p class="text-xs text-slate-400 mt-2">{{ $t('security.tls.cfNote') }}</p>
              </div>
            </div>
          </div>
        </div>

        <!-- Access Configuration -->
        <div class="bg-white rounded-2xl shadow-sm border border-slate-100 overflow-hidden">
          <div class="p-6 border-b border-slate-100 flex items-center">
            <div class="bg-violet-500/10 text-violet-500 p-2 rounded-lg mr-4">
              <Cog6ToothIcon class="h-6 w-6" />
            </div>
            <div>
              <h3 class="text-lg font-medium text-slate-800">{{ $t('security.access.title') }}</h3>
              <p class="text-sm text-slate-500">{{ $t('security.access.subtitle') }}</p>
            </div>
          </div>
          <div class="p-6 space-y-4">
            <div>
              <label class="block text-sm font-medium text-slate-700 mb-1">{{ $t('security.access.panelPort') }}</label>
              <div class="flex items-center space-x-2">
                <input v-model="accessPort" type="text" inputmode="numeric" maxlength="5" class="w-32 border border-slate-300 rounded-lg px-3 py-2 text-sm focus:border-primary-500 focus:ring-primary-500" />
                <span class="text-xs text-slate-400">{{ $t('security.access.portHint') }}</span>
              </div>
            </div>
            <div>
              <label class="block text-sm font-medium text-slate-700 mb-1">{{ $t('security.access.securityPath') }}</label>
              <div class="flex items-center space-x-2">
                <span class="text-sm text-slate-400">/</span>
                <input v-model="accessPath" type="text" :placeholder="'my-secret-path'" class="flex-1 border border-slate-300 rounded-lg px-3 py-2 text-sm focus:border-primary-500 focus:ring-primary-500" />
              </div>
              <p class="text-xs text-slate-400 mt-1">{{ $t('security.access.pathHint') }}</p>
            </div>
            <div v-if="accessMsg" :class="['text-sm p-2 rounded-lg', accessMsgType === 'success' ? 'bg-emerald-50 text-emerald-700' : 'bg-rose-50 text-rose-700']">{{ accessMsg }}</div>
            <div class="flex items-center space-x-3">
              <button @click="onSaveAccess" :disabled="accessLoading || accessRestarting" class="bg-primary-600 hover:bg-primary-700 disabled:opacity-50 text-white px-4 py-2 rounded-lg text-sm font-medium transition">
                {{ accessLoading ? $t('common.loading') : $t('common.save') }}
              </button>
              <button v-if="accessNeedsRestart" @click="onRestartPanel" :disabled="accessRestarting" class="bg-amber-500 hover:bg-amber-600 disabled:opacity-50 text-white px-4 py-2 rounded-lg text-sm font-medium transition">
                {{ accessRestarting ? $t('security.update.restarting', { n: '...' }) : $t('security.access.applyRestart') }}
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
            <!-- Password -->
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

            <div v-if="showPasswordForm" class="pt-3 space-y-3">
              <input v-model="oldPassword" type="password" :placeholder="$t('security.auth.oldPassword')" class="w-full border border-slate-300 rounded-lg px-3 py-2 text-sm focus:border-primary-500 focus:ring-primary-500" />
              <input v-model="newPassword" type="password" :placeholder="$t('security.auth.newPasswordField')" class="w-full border border-slate-300 rounded-lg px-3 py-2 text-sm focus:border-primary-500 focus:ring-primary-500" />
              <input v-model="confirmNewPassword" type="password" :placeholder="$t('security.auth.confirmNewPassword')" class="w-full border border-slate-300 rounded-lg px-3 py-2 text-sm focus:border-primary-500 focus:ring-primary-500" />
              <div v-if="passwordMsg" :class="['text-sm p-2 rounded-lg', passwordMsgType === 'success' ? 'bg-emerald-50 text-emerald-700' : 'bg-rose-50 text-rose-700']">{{ passwordMsg }}</div>
              <button @click="onChangePassword" :disabled="passwordChanging" class="bg-primary-600 hover:bg-primary-700 disabled:opacity-50 text-white px-4 py-2 rounded-lg text-sm font-medium transition">
                {{ passwordChanging ? $t('common.loading') : $t('security.auth.savePassword') }}
              </button>
            </div>

            <!-- 2FA -->
            <div class="pt-4 border-t border-slate-100">
              <div class="flex items-center justify-between">
                <div class="flex items-center">
                  <FingerPrintIcon class="h-5 w-5 text-slate-400 mr-3" />
                  <div>
                    <h4 class="text-sm font-medium text-slate-900">{{ $t('security.auth.twoFactor') }}</h4>
                    <p class="text-xs" :class="twoFAEnabled ? 'text-emerald-500 font-medium' : 'text-slate-400'">
                      {{ twoFAEnabled ? $t('security.auth.twoFactorEnabled') : $t('security.auth.twoFactorDisabled') }}
                    </p>
                  </div>
                </div>
                <button v-if="!twoFAEnabled && twoFAStep === 'idle'" @click="onSetup2FA" :disabled="twoFALoading" class="bg-primary-600 hover:bg-primary-700 disabled:opacity-50 text-white px-4 py-2 rounded-lg text-sm font-medium transition">
                  {{ $t('security.auth.enable2fa') }}
                </button>
                <button v-else-if="twoFAEnabled && twoFAStep === 'idle'" @click="showDisable2FA = !showDisable2FA" class="bg-rose-100 hover:bg-rose-200 text-rose-700 px-4 py-2 rounded-lg text-sm font-medium transition">
                  {{ $t('security.auth.disable2fa') }}
                </button>
              </div>

              <!-- 2FA Setup Flow -->
              <div v-if="twoFAStep === 'setup'" class="mt-4 space-y-4 bg-slate-50 rounded-lg p-4">
                <p class="text-sm font-medium text-slate-700">{{ $t('security.auth.scanQR') }}</p>
                <div class="flex justify-center">
                  <img :src="'data:image/png;base64,' + twoFAQR" alt="QR Code" class="w-48 h-48 rounded-lg border border-slate-200" />
                </div>
                <div class="text-center">
                  <p class="text-xs text-slate-500 mb-1">{{ $t('security.auth.manualEntry') }}</p>
                  <code class="text-xs bg-white px-3 py-1 rounded border border-slate-200 select-all">{{ twoFASecret }}</code>
                </div>
                <div>
                  <label class="block text-sm font-medium text-slate-700 mb-1">{{ $t('security.auth.enterCode') }}</label>
                  <input v-model="twoFACode" type="text" inputmode="numeric" maxlength="6" class="w-full border border-slate-300 rounded-lg px-3 py-2 text-sm text-center tracking-widest" :placeholder="'000000'" />
                </div>
                <div v-if="twoFAMsg" class="text-sm p-2 rounded-lg bg-rose-50 text-rose-700">{{ twoFAMsg }}</div>
                <button @click="onVerify2FA" :disabled="twoFALoading || !twoFACode" class="w-full bg-primary-600 hover:bg-primary-700 disabled:opacity-50 text-white px-4 py-2 rounded-lg text-sm font-medium transition">
                  {{ $t('security.auth.verifyCode') }}
                </button>
              </div>

              <!-- Recovery Codes -->
              <div v-if="twoFAStep === 'codes'" class="mt-4 space-y-4 bg-amber-50 border border-amber-200 rounded-lg p-4">
                <p class="text-sm font-medium text-amber-800">{{ $t('security.auth.recoveryCodesTitle') }}</p>
                <p class="text-xs text-amber-700">{{ $t('security.auth.recoveryCodesDesc') }}</p>
                <div class="grid grid-cols-2 gap-2">
                  <code v-for="code in twoFARecoveryCodes" :key="code" class="bg-white px-3 py-1.5 rounded text-xs text-center font-mono border border-amber-200 select-all">{{ code }}</code>
                </div>
                <label class="flex items-center text-sm text-amber-800">
                  <input type="checkbox" v-model="codesSaved" class="mr-2 rounded border-amber-300" />
                  {{ $t('security.auth.recoveryCodesSaved') }}
                </label>
                <button @click="finish2FASetup" :disabled="!codesSaved" class="w-full bg-emerald-600 hover:bg-emerald-700 disabled:opacity-50 text-white px-4 py-2 rounded-lg text-sm font-medium transition">
                  {{ $t('common.done') }}
                </button>
              </div>

              <!-- Disable 2FA -->
              <div v-if="showDisable2FA" class="mt-4 space-y-3 bg-rose-50 rounded-lg p-4">
                <p class="text-sm text-rose-700">{{ $t('security.auth.confirmDisable2fa') }}</p>
                <input v-model="twoFADisablePassword" type="password" :placeholder="$t('security.auth.oldPassword')" class="w-full border border-rose-200 rounded-lg px-3 py-2 text-sm" />
                <div v-if="twoFAMsg" class="text-sm p-2 rounded-lg bg-rose-100 text-rose-700">{{ twoFAMsg }}</div>
                <button @click="onDisable2FA" :disabled="twoFALoading" class="bg-rose-600 hover:bg-rose-700 disabled:opacity-50 text-white px-4 py-2 rounded-lg text-sm font-medium transition">
                  {{ $t('security.auth.disable2fa') }}
                </button>
              </div>
            </div>
          </div>
        </div>

      </div>

      <!-- Right Column: Security Tips -->
      <div class="space-y-6">
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

            <!-- Port Security -->
            <div class="border-t border-slate-100 pt-4">
              <div class="flex items-center justify-between text-sm mb-2">
                <span class="text-slate-500">{{ $t('security.tips.portLabel') }}</span>
                <code class="bg-slate-100 px-2 py-0.5 rounded text-xs font-medium text-slate-700">{{ panelPort }}</code>
              </div>
              <div v-if="isCommonPort" class="bg-amber-50 border border-amber-200 rounded-lg p-3 text-xs text-amber-700">
                {{ $t('security.tips.portWarning') }}
              </div>
              <div v-else class="bg-emerald-50 border border-emerald-200 rounded-lg p-3 text-xs text-emerald-700">
                {{ $t('security.tips.portGood') }}
              </div>
            </div>
          </div>
        </div>
      </div>

    </div>
  </div>
</template>
