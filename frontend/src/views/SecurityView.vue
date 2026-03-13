<script setup lang="ts">
import { ref } from 'vue'
import { KeyIcon, LockClosedIcon, FingerPrintIcon, ShieldCheckIcon, GlobeAltIcon, ArrowPathIcon, ArrowDownTrayIcon } from '@heroicons/vue/24/outline'
import { checkForUpdate, applyUpdate } from '@/api/system'

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
  if (!confirm('This will restart the panel. Continue?')) return
  updateApplying.value = true
  updateError.value = ''
  try {
    const res = await applyUpdate() as any
    if (res.code === 200) {
      // Panel is restarting — show countdown and reload
      let countdown = 15
      updateError.value = ''
      const timer = setInterval(() => {
        countdown--
        updateError.value = `Panel restarting... reloading in ${countdown}s`
        if (countdown <= 0) {
          clearInterval(timer)
          window.location.reload()
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
      <h1 class="text-3xl font-bold text-slate-800 tracking-tight">Security & Settings</h1>
      <p class="text-slate-500 mt-1">Configure panel security, manage updates, and customize settings.</p>
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
              <h3 class="text-lg font-medium text-slate-800">Panel Update</h3>
              <p class="text-sm text-slate-500">Check for and install updates automatically</p>
            </div>
          </div>
          <div class="p-6 space-y-4">
            <div v-if="updateChecked" class="space-y-3">
              <div class="flex items-center justify-between text-sm">
                <span class="text-slate-500">Current Image</span>
                <code class="bg-slate-100 px-2 py-0.5 rounded text-xs text-slate-700">{{ currentImageID }}</code>
              </div>
              <div class="flex items-center justify-between text-sm">
                <span class="text-slate-500">Latest Image</span>
                <code class="bg-slate-100 px-2 py-0.5 rounded text-xs text-slate-700">{{ latestImageID }}</code>
              </div>
              <div v-if="updateAvailable" class="bg-amber-50 border border-amber-200 rounded-lg p-3 text-sm text-amber-700">
                A new version is available. Click "Update Now" to apply.
              </div>
              <div v-else class="bg-emerald-50 border border-emerald-200 rounded-lg p-3 text-sm text-emerald-700">
                You are running the latest version.
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
                {{ updateChecking ? 'Checking...' : 'Check for Updates' }}
              </button>
              <button
                v-if="updateAvailable"
                @click="onApplyUpdate"
                :disabled="updateApplying"
                class="flex items-center bg-primary-600 hover:bg-primary-700 disabled:opacity-50 text-white px-4 py-2 rounded-lg text-sm font-medium transition"
              >
                <ArrowDownTrayIcon class="h-4 w-4 mr-2" />
                {{ updateApplying ? 'Updating...' : 'Update Now' }}
              </button>
            </div>
          </div>
        </div>

        <!-- Panel Settings -->
        <div class="bg-white rounded-2xl shadow-sm border border-slate-100 overflow-hidden">
          <div class="p-6 border-b border-slate-100 flex items-center">
            <div class="bg-indigo-500/10 text-indigo-500 p-2 rounded-lg mr-4">
              <GlobeAltIcon class="h-6 w-6" />
            </div>
            <div>
              <h3 class="text-lg font-medium text-slate-800">Access Configuration</h3>
              <p class="text-sm text-slate-500">Customize how you connect to ZenithPanel</p>
            </div>
          </div>
          <div class="p-6 space-y-4">
            <div>
              <label class="block text-sm font-medium text-slate-700">Security Path Suffix</label>
              <div class="mt-1 flex rounded-md shadow-sm">
                <span class="inline-flex items-center rounded-l-md border border-r-0 border-slate-300 bg-slate-50 px-3 text-slate-500 sm:text-sm">
                  https://ip:port/
                </span>
                <input type="text" value="zenith-secret-path" class="block w-full min-w-0 flex-1 rounded-none rounded-r-md border-slate-300 px-3 py-2 text-slate-900 focus:border-primary-500 focus:ring-primary-500 sm:text-sm" />
              </div>
            </div>

            <div class="flex items-center justify-between pt-4 border-t border-slate-100">
              <div>
                <h4 class="text-sm font-medium text-slate-900">API White-list</h4>
                <p class="text-xs text-slate-500">Restrict backend API access to specific IPs.</p>
              </div>
              <button class="bg-slate-100 hover:bg-slate-200 text-slate-700 px-4 py-2 rounded-lg text-sm font-medium transition">Configure</button>
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
              <h3 class="text-lg font-medium text-slate-800">Authentication</h3>
              <p class="text-sm text-slate-500">Manage your passwords and two-factor auth</p>
            </div>
          </div>
          <div class="p-6 space-y-4">
            <div class="flex items-center justify-between">
              <div class="flex items-center">
                <LockClosedIcon class="h-5 w-5 text-slate-400 mr-3" />
                <div>
                  <h4 class="text-sm font-medium text-slate-900">Panel Password</h4>
                  <p class="text-xs text-slate-500">Change your admin password</p>
                </div>
              </div>
              <button class="bg-slate-100 hover:bg-slate-200 text-slate-700 px-4 py-2 rounded-lg text-sm font-medium transition">Change</button>
            </div>

            <div class="flex items-center justify-between pt-4 border-t border-slate-100">
              <div class="flex items-center">
                <FingerPrintIcon class="h-5 w-5 text-slate-400 mr-3" />
                <div>
                  <h4 class="text-sm font-medium text-slate-900">Two-Factor Authentication (2FA)</h4>
                  <p class="text-xs text-rose-500 font-medium mt-0.5">Not configured</p>
                </div>
              </div>
              <button class="bg-primary-600 hover:bg-primary-700 text-white px-4 py-2 rounded-lg text-sm font-medium transition">Enable Auth</button>
            </div>
          </div>
        </div>

      </div>

      <!-- Right Column: Info -->
      <div class="bg-white rounded-2xl shadow-sm border border-slate-100 overflow-hidden h-fit">
        <div class="p-6 border-b border-slate-100 flex items-center">
          <ShieldCheckIcon class="h-6 w-6 text-emerald-500 mr-2" />
          <h3 class="text-lg font-medium text-slate-800">Security Tips</h3>
        </div>
        <div class="p-6 space-y-4">
          <div class="text-sm text-slate-600 space-y-3">
            <p>Keep your panel secure:</p>
            <ul class="list-disc list-inside space-y-2 text-slate-500">
              <li>Use a strong admin password</li>
              <li>Enable 2FA when available</li>
              <li>Keep the panel updated</li>
              <li>Use HTTPS with a valid certificate</li>
              <li>Restrict API access by IP if possible</li>
            </ul>
          </div>
        </div>
      </div>

    </div>
  </div>
</template>
