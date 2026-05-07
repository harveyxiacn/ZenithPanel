<script setup lang="ts">
import { ref, reactive, onMounted, onUnmounted, watch, computed } from 'vue'
import type { Component } from 'vue'
import {
  ServerStackIcon,
  CpuChipIcon,
  ServerIcon,
  SignalIcon,
  Cog6ToothIcon,
  EyeIcon,
  EyeSlashIcon,
  ArrowRightIcon,
  HomeIcon,
  ShieldCheckIcon,
  GlobeAltIcon,
  UsersIcon,
} from '@heroicons/vue/24/outline'

import { getSystemMonitor } from '@/api/system'
import { dashboardViewForProfile, type DashboardCardId, type NavigationIconKey } from '@/config/usage-profiles'
import { useUsageProfile } from '@/composables/useUsageProfile'
import { useI18n } from 'vue-i18n'
import { useToast } from '../composables/useToast'

interface DashboardCardOption {
  id: DashboardCardId
  icon: string
  color: string
}

interface MetricCard {
  id: 'cpu' | 'memory' | 'disk' | 'network'
  title: string
  value: string
  detail?: string
  icon: Component
  iconClasses: string
}

const { t } = useI18n()
const toast = useToast()
const { usageProfile, loadUsageProfile } = useUsageProfile()

const actionIcons: Record<NavigationIconKey, Component> = {
  dashboard: HomeIcon,
  servers: ServerIcon,
  nodes: GlobeAltIcon,
  users: UsersIcon,
  security: ShieldCheckIcon,
}

const allCards: DashboardCardOption[] = [
  { id: 'cpu', icon: 'cpu', color: 'sky' },
  { id: 'memory', icon: 'memory', color: 'primary' },
  { id: 'disk', icon: 'disk', color: 'indigo' },
  { id: 'network', icon: 'network', color: 'emerald' },
  { id: 'systemInfo', icon: 'info', color: '' },
  { id: 'quickStats', icon: 'stats', color: '' },
]

function loadCardVisibility(): Record<string, boolean> {
  try {
    const saved = localStorage.getItem('zenith_dashboard_cards')
    if (saved) return JSON.parse(saved)
  } catch {
    // localStorage parse error, use defaults
  }

  return Object.fromEntries(allCards.map((card) => [card.id, true]))
}

const cardVisibility = reactive(loadCardVisibility())
const showCardSettings = ref(false)
const dashboardView = computed(() => dashboardViewForProfile(usageProfile.value))

const orderedCardSettings = computed(() => {
  const preferredOrder = [
    ...dashboardView.value.featuredCardIds,
    ...dashboardView.value.secondaryCardIds,
    ...allCards.map((card) => card.id),
  ]

  return Array.from(new Set(preferredOrder))
    .map((id) => allCards.find((card) => card.id === id))
    .filter((card): card is DashboardCardOption => Boolean(card))
})

function toggleCard(id: string) {
  cardVisibility[id] = !cardVisibility[id]
}

watch(cardVisibility, (val) => {
  localStorage.setItem('zenith_dashboard_cards', JSON.stringify(val))
}, { deep: true })

function cardLabel(id: string): string {
  const map: Record<string, string> = {
    cpu: t('dashboard.cpuUsage'),
    memory: t('dashboard.memory'),
    disk: t('dashboard.diskUsage'),
    network: t('dashboard.network'),
    systemInfo: t('dashboard.systemInfo'),
    quickStats: t('dashboard.quickStats'),
  }
  return map[id] || id
}

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

const metricCards = computed<Record<MetricCard['id'], MetricCard>>(() => ({
  cpu: {
    id: 'cpu',
    title: t('dashboard.cpuUsage'),
    value: `${cpuPercent.value}%`,
    icon: CpuChipIcon,
    iconClasses: 'bg-sky-500/10 text-sky-500',
  },
  memory: {
    id: 'memory',
    title: t('dashboard.memory'),
    value: `${memPercent.value}%`,
    detail: `${memUsed.value} / ${memTotal.value}`,
    icon: ServerStackIcon,
    iconClasses: 'bg-primary-500/10 text-primary-500',
  },
  disk: {
    id: 'disk',
    title: t('dashboard.diskUsage'),
    value: `${diskPercent.value}%`,
    icon: ServerIcon,
    iconClasses: 'bg-indigo-500/10 text-indigo-500',
  },
  network: {
    id: 'network',
    title: t('dashboard.network'),
    value: `${formatBytes(netIn.value)}/s`,
    detail: `${t('dashboard.up')}: ${formatBytes(netOut.value)}/s`,
    icon: SignalIcon,
    iconClasses: 'bg-emerald-500/10 text-emerald-500',
  },
}))

const featuredCards = computed(() => {
  return dashboardView.value.featuredCardIds
    .map((cardId) => metricCards.value[cardId as MetricCard['id']])
    .filter((card): card is MetricCard => Boolean(card) && Boolean(cardVisibility[card.id]))
})

const detailSectionIds = computed(() => {
  return dashboardView.value.secondaryCardIds.filter((cardId) => {
    return (cardId === 'systemInfo' || cardId === 'quickStats') && Boolean(cardVisibility[cardId])
  })
})

const dashboardActions = computed(() => {
  const actions = [dashboardView.value.primaryAction, ...dashboardView.value.secondaryActions]
  return actions.map((action, index) => ({
    ...action,
    iconComponent: actionIcons[action.icon],
    featured: index === 0,
  }))
})

const primaryDashboardAction = computed(() => dashboardActions.value[0] ?? null)
const secondaryDashboardActions = computed(() => dashboardActions.value.slice(1))

// Rolling 60-sample (5-min) history for the traffic sparkline
const SPARKLINE_MAX = 60
const netInHistory = ref<number[]>([])
const netOutHistory = ref<number[]>([])

function sparklinePath(data: number[], width = 120, height = 32): string {
  if (data.length < 2) return ''
  const max = Math.max(...data, 1)
  const step = width / (data.length - 1)
  return data.map((v, i) => {
    const x = i * step
    const y = height - (v / max) * height
    return `${i === 0 ? 'M' : 'L'}${x.toFixed(1)},${y.toFixed(1)}`
  }).join(' ')
}

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

      const now = Date.now()
      const rawIn = d.net_in ?? 0
      const rawOut = d.net_out ?? 0
      if (lastFetchTime > 0) {
        const elapsed = (now - lastFetchTime) / 1000
        if (elapsed > 0) {
          netIn.value = Math.max(0, Math.round((rawIn - lastNetIn) / elapsed))
          netOut.value = Math.max(0, Math.round((rawOut - lastNetOut) / elapsed))
          // Append to sparkline history (keep last SPARKLINE_MAX points)
          netInHistory.value = [...netInHistory.value.slice(-(SPARKLINE_MAX - 1)), netIn.value]
          netOutHistory.value = [...netOutHistory.value.slice(-(SPARKLINE_MAX - 1)), netOut.value]
        }
      }
      lastNetIn = rawIn
      lastNetOut = rawOut
      lastFetchTime = now
    }
  } catch {
    toast.error(t('common.errorOccurred'))
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
  void loadUsageProfile()
  fetchStats()
  pollTimer = setInterval(fetchStats, 5000)
})

onUnmounted(() => {
  if (pollTimer) clearInterval(pollTimer)
})
</script>

<template>
  <div class="py-2">
    <div class="mb-8 flex flex-col gap-4 xl:flex-row xl:items-start xl:justify-between">
      <div>
        <div class="inline-flex items-center rounded-full border border-primary-200/60 bg-primary-50 px-3 py-1 text-xs font-semibold uppercase tracking-[0.24em] text-primary-700">
          {{ $t(dashboardView.badgeKey) }}
        </div>
        <h1 class="mt-4 text-3xl font-bold text-slate-800 dark:text-white tracking-tight">{{ $t(dashboardView.titleKey) }}</h1>
        <p class="mt-2 max-w-3xl text-slate-500 dark:text-slate-400">{{ $t(dashboardView.descriptionKey) }}</p>
      </div>
      <div class="flex items-center space-x-3">
        <div class="relative">
          <button @click="showCardSettings = !showCardSettings" class="bg-white dark:bg-slate-800 rounded-full p-2 shadow-sm border border-slate-100 dark:border-slate-700 text-slate-400 hover:text-slate-600 transition" :title="$t('dashboard.customizeCards')">
            <Cog6ToothIcon class="h-5 w-5" />
          </button>
          <div v-if="showCardSettings" class="absolute right-0 mt-2 w-56 bg-white dark:bg-slate-800 rounded-xl shadow-lg border border-slate-100 dark:border-slate-700 py-2 z-30">
            <p class="px-4 py-1.5 text-xs font-medium text-slate-400 uppercase">{{ $t('dashboard.customizeCards') }}</p>
            <button
              v-for="card in orderedCardSettings"
              :key="card.id"
              @click="toggleCard(card.id)"
              class="w-full flex items-center justify-between px-4 py-2 text-sm hover:bg-slate-50 dark:hover:bg-slate-700 transition"
            >
              <span class="text-slate-700 dark:text-slate-200">{{ cardLabel(card.id) }}</span>
              <EyeIcon v-if="cardVisibility[card.id]" class="h-4 w-4 text-emerald-500" />
              <EyeSlashIcon v-else class="h-4 w-4 text-slate-300" />
            </button>
          </div>
        </div>
        <div class="bg-white dark:bg-slate-800 rounded-full px-4 py-2 shadow-sm border border-slate-100 dark:border-slate-700 flex items-center space-x-2">
          <span class="h-2.5 w-2.5 rounded-full bg-emerald-500 animate-pulse"></span>
          <span class="text-sm font-medium text-slate-600 dark:text-slate-300">{{ hostname || $t('common.loading') }}</span>
        </div>
      </div>
    </div>

    <div class="mb-8 grid gap-6 xl:grid-cols-[minmax(0,1.35fr)_minmax(0,0.95fr)]">
      <section class="overflow-hidden rounded-3xl border border-primary-200/40 bg-[radial-gradient(circle_at_top_left,_rgba(16,185,129,0.22),_transparent_45%),linear-gradient(135deg,_rgba(255,255,255,0.96),_rgba(241,245,249,0.92))] p-6 shadow-sm dark:border-primary-500/20 dark:bg-[radial-gradient(circle_at_top_left,_rgba(16,185,129,0.24),_transparent_38%),linear-gradient(135deg,_rgba(15,23,42,0.92),_rgba(15,23,42,0.82))]">
        <div class="flex flex-col gap-5 lg:flex-row lg:items-end lg:justify-between">
          <div class="max-w-2xl">
            <p class="text-sm font-medium text-slate-600 dark:text-slate-300">{{ $t('dashboard.profileOverview') }}</p>
            <p class="mt-2 text-sm leading-7 text-slate-600 dark:text-slate-300">{{ $t('dashboard.profileHint') }}</p>
          </div>
          <router-link
            v-if="primaryDashboardAction"
            :to="primaryDashboardAction.href"
            class="inline-flex items-center justify-center rounded-2xl bg-slate-900 px-5 py-3 text-sm font-semibold text-white shadow-lg shadow-slate-900/15 transition hover:-translate-y-0.5 hover:bg-slate-800 dark:bg-primary-500 dark:text-slate-950 dark:hover:bg-primary-400"
          >
            <component :is="primaryDashboardAction.iconComponent" class="mr-2 h-5 w-5" />
            {{ $t(primaryDashboardAction.labelKey) }}
            <ArrowRightIcon class="ml-2 h-4 w-4" />
          </router-link>
        </div>
      </section>

      <section class="rounded-3xl border border-slate-200 bg-white/90 p-6 shadow-sm backdrop-blur dark:border-slate-700 dark:bg-slate-800/90">
        <p class="text-xs font-semibold uppercase tracking-[0.28em] text-slate-400">{{ $t('dashboard.quickActions') }}</p>
        <div class="mt-4 space-y-3">
          <router-link
            v-for="action in secondaryDashboardActions"
            :key="action.href"
            :to="action.href"
            :class="[
              'group flex items-start rounded-2xl border px-4 py-3 transition-all',
              'border-slate-200 hover:border-slate-300 hover:bg-slate-50 dark:border-slate-700 dark:hover:bg-slate-700/70',
            ]"
          >
            <div :class="[
              'mr-3 rounded-xl p-2 transition-transform group-hover:scale-105',
              'bg-slate-100 text-slate-500 dark:bg-slate-700 dark:text-slate-300',
            ]">
              <component :is="action.iconComponent" class="h-5 w-5" />
            </div>
            <div class="min-w-0 flex-1">
              <p class="text-sm font-semibold text-slate-800 dark:text-white">{{ $t(action.labelKey) }}</p>
              <p class="mt-1 text-sm text-slate-500 dark:text-slate-400">{{ $t(action.descriptionKey) }}</p>
            </div>
            <ArrowRightIcon class="ml-3 h-4 w-4 shrink-0 text-slate-300 transition group-hover:translate-x-0.5 group-hover:text-slate-500 dark:text-slate-500" />
          </router-link>
        </div>
      </section>
    </div>

    <div v-if="loading" class="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-6 mb-8">
      <div v-for="i in 4" :key="i" class="glass-panel p-6 rounded-2xl bg-white dark:bg-slate-800 animate-pulse">
        <div class="flex items-center">
          <div class="bg-slate-200 dark:bg-slate-700 p-3 rounded-xl mr-4 h-12 w-12"></div>
          <div>
            <div class="h-3 bg-slate-200 dark:bg-slate-600 rounded w-20 mb-2"></div>
            <div class="h-6 bg-slate-200 dark:bg-slate-600 rounded w-16"></div>
          </div>
        </div>
      </div>
    </div>

    <div v-else class="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-6 mb-8">
      <div
        v-for="card in featuredCards"
        :key="card.id"
        class="glass-panel p-6 rounded-2xl bg-white dark:bg-slate-800 hover:shadow-lg transition-all duration-300 transform hover:-translate-y-1"
      >
        <div class="flex items-center">
          <div :class="['p-3 rounded-xl mr-4', card.iconClasses]">
            <component :is="card.icon" class="h-6 w-6" />
          </div>
          <div>
            <p class="text-sm font-medium text-slate-500 dark:text-slate-400">{{ card.title }}</p>
            <p class="text-2xl font-bold text-slate-800 dark:text-white mt-1">{{ card.value }}</p>
            <p v-if="card.detail" class="text-xs text-slate-400 dark:text-slate-500">{{ card.detail }}</p>
          </div>
        </div>
        <!-- Traffic sparkline — only shown on the network card -->
        <template v-if="card.id === 'network' && netInHistory.length > 1">
          <div class="mt-3 relative overflow-hidden rounded-lg" style="height:36px">
            <svg viewBox="0 0 120 32" preserveAspectRatio="none" class="w-full h-full" aria-hidden="true">
              <!-- Download (in) — emerald fill -->
              <path
                :d="sparklinePath(netInHistory) + ' L120,32 L0,32 Z'"
                fill="rgba(16,185,129,0.15)"
              />
              <path
                :d="sparklinePath(netInHistory)"
                fill="none"
                stroke="rgba(16,185,129,0.8)"
                stroke-width="1.5"
                stroke-linecap="round"
                stroke-linejoin="round"
              />
              <!-- Upload (out) — sky -->
              <path
                :d="sparklinePath(netOutHistory)"
                fill="none"
                stroke="rgba(14,165,233,0.7)"
                stroke-width="1"
                stroke-linecap="round"
                stroke-linejoin="round"
                stroke-dasharray="3 2"
              />
            </svg>
            <p class="absolute bottom-0.5 right-1 text-[9px] text-slate-400 leading-none">5 min</p>
          </div>
        </template>
      </div>
    </div>

    <div v-if="detailSectionIds.length" class="grid grid-cols-1 lg:grid-cols-3 gap-6">
      <template v-for="sectionId in detailSectionIds" :key="sectionId">
        <div
          v-if="sectionId === 'systemInfo'"
          :class="[
            detailSectionIds.includes('quickStats') ? 'lg:col-span-2' : 'lg:col-span-3',
            'glass-panel p-6 rounded-2xl bg-white dark:bg-slate-800 min-h-[300px]'
          ]"
        >
          <h3 class="text-lg font-semibold text-slate-800 dark:text-white mb-4 border-b border-slate-100 dark:border-slate-700 pb-3">{{ $t('dashboard.systemInfo') }}</h3>
          <div class="space-y-3">
            <div class="flex justify-between text-sm">
              <span class="text-slate-500">{{ $t('dashboard.uptime') }}</span>
              <span class="text-slate-800 dark:text-slate-200 font-medium">{{ uptime || '-' }}</span>
            </div>
            <div class="flex justify-between text-sm">
              <span class="text-slate-500">{{ $t('dashboard.loadAverage') }}</span>
              <span class="text-slate-800 dark:text-slate-200 font-medium">{{ loadAvg || '-' }}</span>
            </div>
            <div class="flex justify-between text-sm">
              <span class="text-slate-500">{{ $t('dashboard.hostname') }}</span>
              <span class="text-slate-800 dark:text-slate-200 font-medium">{{ hostname || '-' }}</span>
            </div>
          </div>
        </div>

        <div
          v-else-if="sectionId === 'quickStats'"
          :class="[
            detailSectionIds.includes('systemInfo') ? '' : 'lg:col-span-3',
            'glass-panel p-6 rounded-2xl bg-white dark:bg-slate-800 min-h-[300px]'
          ]"
        >
          <h3 class="text-lg font-semibold text-slate-800 dark:text-white mb-4 border-b border-slate-100 dark:border-slate-700 pb-3">{{ $t('dashboard.quickStats') }}</h3>
          <div class="space-y-4">
            <div>
              <div class="flex justify-between text-sm mb-1">
                <span class="text-slate-500">{{ $t('dashboard.cpu') }}</span>
                <span class="text-slate-700 dark:text-slate-300">{{ cpuPercent }}%</span>
              </div>
              <div class="w-full bg-slate-200 dark:bg-slate-700 rounded-full h-2">
                <div class="bg-sky-500 h-2 rounded-full transition-all duration-500" :style="{ width: cpuPercent + '%' }"></div>
              </div>
            </div>
            <div>
              <div class="flex justify-between text-sm mb-1">
                <span class="text-slate-500">{{ $t('dashboard.memory') }}</span>
                <span class="text-slate-700 dark:text-slate-300">{{ memPercent }}%</span>
              </div>
              <div class="w-full bg-slate-200 dark:bg-slate-700 rounded-full h-2">
                <div class="bg-primary-500 h-2 rounded-full transition-all duration-500" :style="{ width: memPercent + '%' }"></div>
              </div>
            </div>
            <div>
              <div class="flex justify-between text-sm mb-1">
                <span class="text-slate-500">{{ $t('dashboard.disk') }}</span>
                <span class="text-slate-700 dark:text-slate-300">{{ diskPercent }}%</span>
              </div>
              <div class="w-full bg-slate-200 dark:bg-slate-700 rounded-full h-2">
                <div class="bg-indigo-500 h-2 rounded-full transition-all duration-500" :style="{ width: diskPercent + '%' }"></div>
              </div>
            </div>
          </div>
        </div>
      </template>
    </div>
  </div>
</template>
