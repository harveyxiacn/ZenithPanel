<script setup lang="ts">
import { ref, computed, onMounted, onBeforeUnmount } from 'vue'
import { SignalIcon, UserIcon, CpuChipIcon, ArrowDownIcon, ArrowUpIcon } from '@heroicons/vue/24/outline'
import { useToast } from '@/composables/useToast'
import { getTrafficLive, getTrafficHistory, type TrafficSnapshot, type NICSample, type ProxyUserSample, type ProcessSample } from '@/api/traffic'

const toast = useToast()

type TabId = 'proxy' | 'system'
const activeTab = ref<TabId>('proxy')

const snapshot = ref<TrafficSnapshot | null>(null)
const history = ref<TrafficSnapshot[]>([])
const loading = ref(false)
const lastErrors = ref<{ proxy?: string; system?: string }>({})

let liveTimer: number | null = null

async function pull() {
  try {
    const res = await getTrafficLive() as any
    if (res?.code === 200 && res?.data) {
      snapshot.value = res.data as TrafficSnapshot
      lastErrors.value = {
        proxy: res.data.proxy_error,
        system: res.data.system_error,
      }
    }
  } catch (e: any) {
    // Network errors are silent — banner already shows the last-known state.
    if (!snapshot.value) toast.error(e?.response?.data?.msg || 'Failed to fetch traffic')
  }
}

async function pullHistory() {
  try {
    const res = await getTrafficHistory(120) as any
    if (res?.code === 200 && Array.isArray(res?.data)) {
      history.value = res.data as TrafficSnapshot[]
    }
  } catch {}
}

onMounted(async () => {
  loading.value = true
  await Promise.all([pull(), pullHistory()])
  loading.value = false
  // The backend samples every 2 s; polling at 2 s aligns the UI to fresh data.
  liveTimer = window.setInterval(async () => {
    await pull()
    await pullHistory()
  }, 2000)
})

onBeforeUnmount(() => {
  if (liveTimer !== null) window.clearInterval(liveTimer)
})

// ---- formatting helpers ----
function fmtBytes(n: number): string {
  if (!n || n < 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let i = 0
  let v = n
  while (v >= 1024 && i < units.length - 1) { v /= 1024; i++ }
  return v.toFixed(v >= 100 ? 0 : v >= 10 ? 1 : 2) + ' ' + units[i]
}

function fmtRate(bps: number): string {
  return fmtBytes(bps) + '/s'
}

const proxyUsers = computed<ProxyUserSample[]>(() => snapshot.value?.proxy_users || [])
const nics = computed<NICSample[]>(() => snapshot.value?.nics || [])
const processes = computed<ProcessSample[]>(() => snapshot.value?.processes || [])

// Total rate banner numbers per tab
const totalProxyUp = computed(() => proxyUsers.value.reduce((s, u) => s + u.upload_rate_bps, 0))
const totalProxyDown = computed(() => proxyUsers.value.reduce((s, u) => s + u.download_rate_bps, 0))
const totalProxyConns = computed(() => proxyUsers.value.reduce((s, u) => s + u.active_conns, 0))
const totalNicIn = computed(() => nics.value.reduce((s, n) => s + n.in_rate_bps, 0))
const totalNicOut = computed(() => nics.value.reduce((s, n) => s + n.out_rate_bps, 0))

// Sparkline series for the system tab — last 60 samples of summed NIC rates.
const nicHistory = computed(() => {
  return history.value.slice(-60).map((snap) => {
    const inSum = (snap.nics || []).reduce((s, n) => s + n.in_rate_bps, 0)
    const outSum = (snap.nics || []).reduce((s, n) => s + n.out_rate_bps, 0)
    return { in: inSum, out: outSum }
  })
})

// Sparkline SVG path for a single series. Range is normalised against the
// max value across both series so the in/out lines share an axis.
function sparklinePath(values: number[], maxOverride?: number): string {
  if (values.length < 2) return ''
  const max = maxOverride ?? Math.max(1, ...values)
  const w = 100
  const h = 32
  const step = w / (values.length - 1)
  return values
    .map((v, i) => {
      const x = i * step
      const y = h - (v / max) * h
      return (i === 0 ? 'M' : 'L') + x.toFixed(1) + ',' + y.toFixed(1)
    })
    .join(' ')
}

const sparkInPath = computed(() => {
  const vals = nicHistory.value.map((p) => p.in)
  const maxAll = Math.max(1, ...nicHistory.value.map((p) => Math.max(p.in, p.out)))
  return sparklinePath(vals, maxAll)
})
const sparkOutPath = computed(() => {
  const vals = nicHistory.value.map((p) => p.out)
  const maxAll = Math.max(1, ...nicHistory.value.map((p) => Math.max(p.in, p.out)))
  return sparklinePath(vals, maxAll)
})
</script>

<template>
  <div class="space-y-6">
    <header class="flex items-center justify-between">
      <div>
        <h1 class="text-2xl font-bold text-slate-800 dark:text-white flex items-center gap-2">
          <SignalIcon class="h-6 w-6 text-primary-500" />
          流量观察
        </h1>
        <p class="text-sm text-slate-500 dark:text-slate-400 mt-1">实时查看代理用户与 VPS 进程占用</p>
      </div>
      <div class="text-xs text-slate-400">
        <span v-if="snapshot">最近更新：{{ new Date(snapshot.at).toLocaleTimeString() }}</span>
        <span v-else>等待数据…</span>
      </div>
    </header>

    <!-- Tab switcher -->
    <div class="inline-flex rounded-lg bg-slate-100 dark:bg-slate-800 p-1 text-sm font-medium">
      <button
        @click="activeTab = 'proxy'"
        :class="['flex items-center gap-2 px-4 py-2 rounded-md transition', activeTab === 'proxy' ? 'bg-white dark:bg-slate-900 text-primary-600 shadow-sm' : 'text-slate-500 hover:text-slate-700']"
      >
        <UserIcon class="h-4 w-4" />代理用户
      </button>
      <button
        @click="activeTab = 'system'"
        :class="['flex items-center gap-2 px-4 py-2 rounded-md transition', activeTab === 'system' ? 'bg-white dark:bg-slate-900 text-primary-600 shadow-sm' : 'text-slate-500 hover:text-slate-700']"
      >
        <CpuChipIcon class="h-4 w-4" />系统进程
      </button>
    </div>

    <!-- Proxy users tab -->
    <section v-if="activeTab === 'proxy'" class="space-y-4">
      <div v-if="lastErrors.proxy" class="rounded-lg border border-amber-200 bg-amber-50 dark:bg-amber-900/20 dark:border-amber-800 px-4 py-3 text-sm text-amber-700 dark:text-amber-300">
        {{ lastErrors.proxy }}
      </div>

      <!-- Summary cards -->
      <div class="grid grid-cols-1 md:grid-cols-3 gap-4">
        <div class="rounded-xl bg-white dark:bg-slate-800 border border-slate-200 dark:border-slate-700 p-4">
          <div class="text-xs text-slate-500 dark:text-slate-400 uppercase tracking-wide">上行总速率</div>
          <div class="text-2xl font-bold text-slate-800 dark:text-white mt-1 flex items-center gap-2">
            <ArrowUpIcon class="h-5 w-5 text-emerald-500" />{{ fmtRate(totalProxyUp) }}
          </div>
        </div>
        <div class="rounded-xl bg-white dark:bg-slate-800 border border-slate-200 dark:border-slate-700 p-4">
          <div class="text-xs text-slate-500 dark:text-slate-400 uppercase tracking-wide">下行总速率</div>
          <div class="text-2xl font-bold text-slate-800 dark:text-white mt-1 flex items-center gap-2">
            <ArrowDownIcon class="h-5 w-5 text-sky-500" />{{ fmtRate(totalProxyDown) }}
          </div>
        </div>
        <div class="rounded-xl bg-white dark:bg-slate-800 border border-slate-200 dark:border-slate-700 p-4">
          <div class="text-xs text-slate-500 dark:text-slate-400 uppercase tracking-wide">活跃连接数</div>
          <div class="text-2xl font-bold text-slate-800 dark:text-white mt-1">{{ totalProxyConns }}</div>
        </div>
      </div>

      <!-- Per-user table -->
      <div class="rounded-xl bg-white dark:bg-slate-800 border border-slate-200 dark:border-slate-700 overflow-hidden">
        <table class="w-full text-sm">
          <thead class="bg-slate-50 dark:bg-slate-900/40 text-slate-500 text-xs uppercase tracking-wide">
            <tr>
              <th class="text-left px-4 py-3">用户 (Email)</th>
              <th class="text-right px-4 py-3">上行</th>
              <th class="text-right px-4 py-3">下行</th>
              <th class="text-right px-4 py-3">连接数</th>
              <th class="text-right px-4 py-3">累计上行</th>
              <th class="text-right px-4 py-3">累计下行</th>
              <th class="text-left px-4 py-3">最近目标</th>
            </tr>
          </thead>
          <tbody>
            <tr v-if="proxyUsers.length === 0">
              <td colspan="7" class="text-center text-slate-400 py-8">
                {{ lastErrors.proxy ? '代理或 Clash API 未启用 — 见上方提示' : '暂无活跃用户' }}
              </td>
            </tr>
            <tr v-for="u in proxyUsers" :key="u.email" class="border-t border-slate-100 dark:border-slate-700/50">
              <td class="px-4 py-3 font-medium text-slate-800 dark:text-slate-100 truncate max-w-[16rem]">{{ u.email }}</td>
              <td class="px-4 py-3 text-right text-emerald-600 dark:text-emerald-400 tabular-nums">{{ fmtRate(u.upload_rate_bps) }}</td>
              <td class="px-4 py-3 text-right text-sky-600 dark:text-sky-400 tabular-nums">{{ fmtRate(u.download_rate_bps) }}</td>
              <td class="px-4 py-3 text-right tabular-nums">{{ u.active_conns }}</td>
              <td class="px-4 py-3 text-right text-slate-500 tabular-nums">{{ fmtBytes(u.upload_total) }}</td>
              <td class="px-4 py-3 text-right text-slate-500 tabular-nums">{{ fmtBytes(u.download_total) }}</td>
              <td class="px-4 py-3 text-xs text-slate-500 truncate max-w-[20rem]">
                <template v-if="u.top_targets && u.top_targets.length">
                  {{ u.top_targets.join(', ') }}
                </template>
                <span v-else class="text-slate-300">—</span>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </section>

    <!-- System processes tab -->
    <section v-if="activeTab === 'system'" class="space-y-4">
      <div v-if="lastErrors.system" class="rounded-lg border border-amber-200 bg-amber-50 dark:bg-amber-900/20 dark:border-amber-800 px-4 py-3 text-sm text-amber-700 dark:text-amber-300">
        {{ lastErrors.system }}
      </div>

      <!-- NIC summary + sparkline -->
      <div class="rounded-xl bg-white dark:bg-slate-800 border border-slate-200 dark:border-slate-700 p-4">
        <div class="flex items-start justify-between gap-6 flex-wrap">
          <div>
            <div class="text-xs text-slate-500 dark:text-slate-400 uppercase tracking-wide">网卡总速率（含全部接口）</div>
            <div class="flex items-baseline gap-6 mt-2">
              <div>
                <span class="text-xs text-emerald-600">入站 </span>
                <span class="text-xl font-bold text-slate-800 dark:text-white tabular-nums">{{ fmtRate(totalNicIn) }}</span>
              </div>
              <div>
                <span class="text-xs text-sky-600">出站 </span>
                <span class="text-xl font-bold text-slate-800 dark:text-white tabular-nums">{{ fmtRate(totalNicOut) }}</span>
              </div>
            </div>
          </div>
          <svg viewBox="0 0 100 32" class="w-64 h-12" preserveAspectRatio="none">
            <path :d="sparkInPath" fill="none" stroke="#10b981" stroke-width="1.5" />
            <path :d="sparkOutPath" fill="none" stroke="#0ea5e9" stroke-width="1.5" />
          </svg>
        </div>

        <!-- Per-NIC rows -->
        <div class="mt-4 space-y-1" v-if="nics.length">
          <div v-for="n in nics" :key="n.name" class="flex items-center justify-between text-sm py-1 border-t border-slate-100 dark:border-slate-700/40 first:border-0">
            <div class="font-mono text-slate-600 dark:text-slate-300">{{ n.name }}</div>
            <div class="flex gap-6 tabular-nums">
              <span class="text-emerald-600 dark:text-emerald-400">{{ fmtRate(n.in_rate_bps) }} ↓</span>
              <span class="text-sky-600 dark:text-sky-400">{{ fmtRate(n.out_rate_bps) }} ↑</span>
              <span class="text-slate-400 text-xs hidden md:inline">总 {{ fmtBytes(n.total_in) }} / {{ fmtBytes(n.total_out) }}</span>
            </div>
          </div>
        </div>
      </div>

      <!-- Processes -->
      <div class="rounded-xl bg-white dark:bg-slate-800 border border-slate-200 dark:border-slate-700 overflow-hidden">
        <div class="px-4 py-3 border-b border-slate-100 dark:border-slate-700/40">
          <h2 class="text-sm font-semibold text-slate-800 dark:text-slate-100">活跃网络进程</h2>
          <p class="text-xs text-slate-500 mt-0.5">按打开 socket 数量排序。每 5 秒刷新一次（独立于网卡速率）。每个进程的实时带宽需要 nethogs/eBPF，本表不直接给。</p>
        </div>
        <table class="w-full text-sm">
          <thead class="bg-slate-50 dark:bg-slate-900/40 text-slate-500 text-xs uppercase tracking-wide">
            <tr>
              <th class="text-left px-4 py-3">PID</th>
              <th class="text-left px-4 py-3">进程</th>
              <th class="text-left px-4 py-3">用户</th>
              <th class="text-right px-4 py-3">连接</th>
              <th class="text-left px-4 py-3">监听端口</th>
              <th class="text-left px-4 py-3">目标</th>
            </tr>
          </thead>
          <tbody>
            <tr v-if="processes.length === 0">
              <td colspan="6" class="text-center text-slate-400 py-8">暂无活跃网络进程</td>
            </tr>
            <tr v-for="p in processes" :key="p.pid" class="border-t border-slate-100 dark:border-slate-700/50">
              <td class="px-4 py-3 font-mono text-xs text-slate-500">{{ p.pid }}</td>
              <td class="px-4 py-3">
                <div class="font-medium text-slate-800 dark:text-slate-100">{{ p.name }}</div>
                <div class="text-xs text-slate-400 font-mono truncate max-w-[20rem]" :title="p.command">{{ p.command }}</div>
              </td>
              <td class="px-4 py-3 text-slate-600 dark:text-slate-300">{{ p.user || '—' }}</td>
              <td class="px-4 py-3 text-right tabular-nums">{{ p.active_conns }}</td>
              <td class="px-4 py-3 text-xs text-slate-500">
                <template v-if="p.listen_ports && p.listen_ports.length">{{ p.listen_ports.join(', ') }}</template>
                <span v-else class="text-slate-300">—</span>
              </td>
              <td class="px-4 py-3 text-xs text-slate-500 truncate max-w-[22rem]">
                <template v-if="p.destinations && p.destinations.length">{{ p.destinations.join(', ') }}</template>
                <span v-else class="text-slate-300">—</span>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </section>
  </div>
</template>
