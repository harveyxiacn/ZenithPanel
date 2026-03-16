<script setup lang="ts">
import { ref, onMounted, onUnmounted } from 'vue'
import { ServerStackIcon, CpuChipIcon, ServerIcon, SignalIcon } from '@heroicons/vue/24/outline'
import { getSystemMonitor } from '@/api/system'
import { useI18n } from 'vue-i18n'

const { t } = useI18n()

const loading = ref(true)
const cpuPercent = ref(0)
const memPercent = ref(0)
const memUsed = ref('')
const memTotal = ref('')
const diskPercent = ref(0)
const uptime = ref('')
const hostname = ref('')
const loadAvg = ref('')
const netIn = ref(0)
const netOut = ref(0)

let pollTimer: ReturnType<typeof setInterval> | null = null
let lastNetIn = 0
let lastNetOut = 0
let lastFetchTime = 0

async function fetchStats() {
  try {
    const res = await getSystemMonitor() as any
    if (res.code === 200 && res.data) {
      const d = res.data
      cpuPercent.value = Math.round(d.cpu_percent ?? 0)
      memPercent.value = Math.round(d.mem_percent ?? 0)
      memUsed.value = formatBytes(d.mem_used ?? 0)
      memTotal.value = formatBytes(d.mem_total ?? 0)
      diskPercent.value = Math.round(d.disk_percent ?? 0)
      uptime.value = formatUptime(d.uptime_seconds ?? 0)
      hostname.value = d.hostname ?? ''
      loadAvg.value = (d.load_avg ?? []).map((v: number) => v.toFixed(2)).join(' / ')

      // Compute network rate from cumulative counters
      const now = Date.now()
      const rawIn = d.net_in ?? 0
      const rawOut = d.net_out ?? 0
      if (lastFetchTime > 0) {
        const elapsed = (now - lastFetchTime) / 1000
        if (elapsed > 0) {
          netIn.value = Math.round((rawIn - lastNetIn) / elapsed)
          netOut.value = Math.round((rawOut - lastNetOut) / elapsed)
        }
      }
      lastNetIn = rawIn
      lastNetOut = rawOut
      lastFetchTime = now
    }
  } catch {
    // silently fail, keep polling
  } finally {
    loading.value = false
  }
}

function formatBytes(bytes: number): string {
  if (bytes === 0) return '0 B'
  const k = 1024
  const sizes = ['B', 'KB', 'MB', 'GB', 'TB']
  const i = Math.floor(Math.log(bytes) / Math.log(k))
  return parseFloat((bytes / Math.pow(k, i)).toFixed(1)) + ' ' + sizes[i]
}

function formatUptime(seconds: number): string {
  const d = Math.floor(seconds / 86400)
  const h = Math.floor((seconds % 86400) / 3600)
  const m = Math.floor((seconds % 3600) / 60)
  if (d > 0) return `${d}d ${h}h ${m}m`
  if (h > 0) return `${h}h ${m}m`
  return `${m}m`
}

onMounted(() => {
  fetchStats()
  pollTimer = setInterval(fetchStats, 5000)
})

onUnmounted(() => {
  if (pollTimer) clearInterval(pollTimer)
})
</script>

<template>
  <div class="py-2">
    <!-- Header -->
    <div class="mb-8 flex items-center justify-between">
      <div>
        <h1 class="text-3xl font-bold text-slate-800 tracking-tight">{{ $t('dashboard.title') }}</h1>
        <p class="text-slate-500 mt-1">{{ $t('dashboard.subtitle') }}</p>
      </div>
      <div class="bg-white rounded-full px-4 py-2 shadow-sm border border-slate-100 flex items-center space-x-2">
         <span class="h-2.5 w-2.5 rounded-full bg-emerald-500 animate-pulse"></span>
         <span class="text-sm font-medium text-slate-600">{{ hostname || $t('common.loading') }}</span>
      </div>
    </div>

    <!-- Loading Skeleton -->
    <div v-if="loading" class="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-6 mb-8">
      <div v-for="i in 4" :key="i" class="glass-panel p-6 rounded-2xl bg-white animate-pulse">
        <div class="flex items-center">
          <div class="bg-slate-200 p-3 rounded-xl mr-4 h-12 w-12"></div>
          <div>
            <div class="h-3 bg-slate-200 rounded w-20 mb-2"></div>
            <div class="h-6 bg-slate-200 rounded w-16"></div>
          </div>
        </div>
      </div>
    </div>

    <!-- Stats Grid -->
    <div v-else class="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-6 mb-8">
      <div class="glass-panel p-6 rounded-2xl bg-white hover:shadow-lg transition-all duration-300 transform hover:-translate-y-1">
        <div class="flex items-center">
          <div class="bg-sky-500/10 text-sky-500 p-3 rounded-xl mr-4">
            <CpuChipIcon class="h-6 w-6" />
          </div>
          <div>
            <p class="text-sm font-medium text-slate-500">{{ $t('dashboard.cpuUsage') }}</p>
            <p class="text-2xl font-bold text-slate-800 mt-1">{{ cpuPercent }}%</p>
          </div>
        </div>
      </div>

      <div class="glass-panel p-6 rounded-2xl bg-white hover:shadow-lg transition-all duration-300 transform hover:-translate-y-1">
        <div class="flex items-center">
          <div class="bg-primary-500/10 text-primary-500 p-3 rounded-xl mr-4">
            <ServerStackIcon class="h-6 w-6" />
          </div>
          <div>
            <p class="text-sm font-medium text-slate-500">{{ $t('dashboard.memory') }}</p>
            <p class="text-2xl font-bold text-slate-800 mt-1">{{ memPercent }}%</p>
            <p class="text-xs text-slate-400">{{ memUsed }} / {{ memTotal }}</p>
          </div>
        </div>
      </div>

      <div class="glass-panel p-6 rounded-2xl bg-white hover:shadow-lg transition-all duration-300 transform hover:-translate-y-1">
        <div class="flex items-center">
          <div class="bg-indigo-500/10 text-indigo-500 p-3 rounded-xl mr-4">
            <ServerIcon class="h-6 w-6" />
          </div>
          <div>
            <p class="text-sm font-medium text-slate-500">{{ $t('dashboard.diskUsage') }}</p>
            <p class="text-2xl font-bold text-slate-800 mt-1">{{ diskPercent }}%</p>
          </div>
        </div>
      </div>

      <div class="glass-panel p-6 rounded-2xl bg-white hover:shadow-lg transition-all duration-300 transform hover:-translate-y-1">
        <div class="flex items-center">
          <div class="bg-emerald-500/10 text-emerald-500 p-3 rounded-xl mr-4">
            <SignalIcon class="h-6 w-6" />
          </div>
          <div>
            <p class="text-sm font-medium text-slate-500">{{ $t('dashboard.network') }}</p>
            <p class="text-lg font-bold text-slate-800 mt-1">{{ formatBytes(netIn) }}/s</p>
            <p class="text-xs text-slate-400">{{ $t('dashboard.up') }}: {{ formatBytes(netOut) }}/s</p>
          </div>
        </div>
      </div>
    </div>

    <!-- Large Content Area -->
    <div class="grid grid-cols-1 lg:grid-cols-3 gap-6">
      <div class="lg:col-span-2 glass-panel p-6 rounded-2xl bg-white min-h-[300px]">
        <h3 class="text-lg font-semibold text-slate-800 mb-4 border-b border-slate-100 pb-3">{{ $t('dashboard.systemInfo') }}</h3>
        <div class="space-y-3">
          <div class="flex justify-between text-sm">
            <span class="text-slate-500">{{ $t('dashboard.uptime') }}</span>
            <span class="text-slate-800 font-medium">{{ uptime || '-' }}</span>
          </div>
          <div class="flex justify-between text-sm">
            <span class="text-slate-500">{{ $t('dashboard.loadAverage') }}</span>
            <span class="text-slate-800 font-medium">{{ loadAvg || '-' }}</span>
          </div>
          <div class="flex justify-between text-sm">
            <span class="text-slate-500">{{ $t('dashboard.hostname') }}</span>
            <span class="text-slate-800 font-medium">{{ hostname || '-' }}</span>
          </div>
        </div>
      </div>

      <div class="glass-panel p-6 rounded-2xl bg-white min-h-[300px]">
        <h3 class="text-lg font-semibold text-slate-800 mb-4 border-b border-slate-100 pb-3">{{ $t('dashboard.quickStats') }}</h3>
        <div class="space-y-4">
          <div>
            <div class="flex justify-between text-sm mb-1">
              <span class="text-slate-500">{{ $t('dashboard.cpu') }}</span>
              <span class="text-slate-700">{{ cpuPercent }}%</span>
            </div>
            <div class="w-full bg-slate-200 rounded-full h-2">
              <div class="bg-sky-500 h-2 rounded-full transition-all duration-500" :style="{ width: cpuPercent + '%' }"></div>
            </div>
          </div>
          <div>
            <div class="flex justify-between text-sm mb-1">
              <span class="text-slate-500">{{ $t('dashboard.memory') }}</span>
              <span class="text-slate-700">{{ memPercent }}%</span>
            </div>
            <div class="w-full bg-slate-200 rounded-full h-2">
              <div class="bg-primary-500 h-2 rounded-full transition-all duration-500" :style="{ width: memPercent + '%' }"></div>
            </div>
          </div>
          <div>
            <div class="flex justify-between text-sm mb-1">
              <span class="text-slate-500">{{ $t('dashboard.disk') }}</span>
              <span class="text-slate-700">{{ diskPercent }}%</span>
            </div>
            <div class="w-full bg-slate-200 rounded-full h-2">
              <div class="bg-indigo-500 h-2 rounded-full transition-all duration-500" :style="{ width: diskPercent + '%' }"></div>
            </div>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>
