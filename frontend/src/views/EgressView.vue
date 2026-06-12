<script setup lang="ts">
import { ref, computed, onMounted, onBeforeUnmount } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  ArrowTrendingUpIcon, ArrowUpIcon, ArrowDownIcon, GlobeAltIcon,
  Cog6ToothIcon, ArrowPathIcon, ServerStackIcon, ArrowDownTrayIcon,
} from '@heroicons/vue/24/outline'
import { useToast } from '@/composables/useToast'
import StatBar from '@/components/StatBar.vue'
import {
  getEgressSeries, getEgressSummary, getEgressCoverage, getEgressList,
  getEgressConfig, updateEgressConfig, downloadEgressCSV,
  type EgressSeriesPoint, type EgressSummaryRow, type EgressCoverage,
  type EgressRow, type EgressConfig,
} from '@/api/traffic'
import { triggerBlobDownload, fileStamp } from '@/utils/csv'

const toast = useToast()
const { t } = useI18n()

// ---- filters ----
type RangeKey = '1h' | '6h' | '24h' | '7d' | '30d'
const rangePresets: { key: RangeKey; secs: number }[] = [
  { key: '1h', secs: 3600 },
  { key: '6h', secs: 6 * 3600 },
  { key: '24h', secs: 24 * 3600 },
  { key: '7d', secs: 7 * 86400 },
  { key: '30d', secs: 30 * 86400 },
]
const range = ref<RangeKey>('6h')
const instance = ref<string>('')
const direction = ref<string>('egress')
const userSearch = ref<string>('')
const directionOptions: { val: string }[] = [
  { val: 'egress' },
  { val: 'return' },
  { val: '' },
]

const loading = ref(false)
const lastUpdated = ref<Date | null>(null)
let timer: number | null = null

const series = ref<EgressSeriesPoint[]>([])
const topDests = ref<EgressSummaryRow[]>([])
const topAsns = ref<EgressSummaryRow[]>([])
const byInstance = ref<EgressSummaryRow[]>([])
const byUser = ref<EgressSummaryRow[]>([])
const coverage = ref<EgressCoverage[]>([])
const rows = ref<EgressRow[]>([])

function curWindow(): { start: number; end: number } {
  const end = Math.floor(Date.now() / 1000)
  const secs = rangePresets.find((r) => r.key === range.value)?.secs ?? 6 * 3600
  return { start: end - secs, end }
}

function commonParams() {
  const { start, end } = curWindow()
  const p: Record<string, string | number> = { start, end }
  if (instance.value) p.instance = instance.value
  if (direction.value) p.direction = direction.value
  if (userSearch.value.trim()) p.user = userSearch.value.trim()
  return p
}

async function fetchAll() {
  try {
    const [s, d, a, ins, usr, cov, list] = await Promise.all([
      getEgressSeries({ ...commonParams(), split: 'instance' }) as any,
      getEgressSummary({ ...commonParams(), group_by: 'dest' }) as any,
      getEgressSummary({ ...commonParams(), group_by: 'asn' }) as any,
      getEgressSummary({ ...commonParams(), group_by: 'instance' }) as any,
      getEgressSummary({ ...commonParams(), group_by: 'user' }) as any,
      getEgressCoverage() as any,
      getEgressList({ ...commonParams(), limit: 200 }) as any,
    ])
    if (s?.code === 200) series.value = s.data || []
    if (d?.code === 200) topDests.value = d.data || []
    if (a?.code === 200) topAsns.value = (a.data || []).filter((r: EgressSummaryRow) => r.key)
    if (ins?.code === 200) byInstance.value = ins.data || []
    if (usr?.code === 200) byUser.value = (usr.data || []).filter((r: EgressSummaryRow) => r.key)
    if (cov?.code === 200) coverage.value = cov.data || []
    if (list?.code === 200) rows.value = list.data || []
    lastUpdated.value = new Date()
  } catch (e: any) {
    toast.error(e?.response?.data?.msg || t('egress.loadFailed'))
  }
}

async function refresh() {
  loading.value = true
  await fetchAll()
  loading.value = false
}

onMounted(async () => {
  await refresh()
  await loadConfig()
  // Refresh every 30s — matches the backend's flush cadence.
  timer = window.setInterval(fetchAll, 30000)
})
onBeforeUnmount(() => {
  if (timer !== null) window.clearInterval(timer)
})

// ---- formatting ----
function fmtBytes(n: number): string {
  if (!n || n < 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB', 'PB']
  let i = 0, v = n
  while (v >= 1024 && i < units.length - 1) { v /= 1024; i++ }
  return v.toFixed(v >= 100 ? 0 : v >= 10 ? 1 : 2) + ' ' + units[i]
}
function fmtBucket(b: number): string {
  const d = new Date(b * 1000)
  const longRange = range.value === '7d' || range.value === '30d'
  const pad = (x: number) => String(x).padStart(2, '0')
  return longRange
    ? `${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:00`
    : `${pad(d.getHours())}:${pad(d.getMinutes())}`
}

// ---- instance colors ----
// Nice fixed hues for common engines; every other auto-discovered process gets
// a stable hashed color. No behavior is keyed to these names — colors only.
const palette: Record<string, string> = {
  'sing-box': '#10b981',
  'xray': '#6366f1',
  'wireproxy': '#ec4899',
}
function instColor(name: string): string {
  if (palette[name]) return palette[name]
  let h = 0
  for (let i = 0; i < name.length; i++) h = (h * 31 + name.charCodeAt(i)) % 360
  return `hsl(${h},65%,55%)`
}

// ---- stacked time-series model ----
interface Column { bucket: number; total: number; segs: { instance: string; value: number; color: string }[] }
const columns = computed<Column[]>(() => {
  const byBucket = new Map<number, Map<string, number>>()
  for (const p of series.value) {
    const b = p.bucket
    if (!byBucket.has(b)) byBucket.set(b, new Map())
    const m = byBucket.get(b)!
    const inst = p.instance || '(other)'
    m.set(inst, (m.get(inst) || 0) + p.bytes_up + p.bytes_down)
  }
  const out: Column[] = []
  for (const [bucket, m] of Array.from(byBucket.entries()).sort((x, y) => x[0] - y[0])) {
    const segs = Array.from(m.entries())
      .map(([inst, value]) => ({ instance: inst, value, color: instColor(inst) }))
      .sort((a, b) => b.value - a.value)
    out.push({ bucket, total: segs.reduce((s, x) => s + x.value, 0), segs })
  }
  return out
})
const colMax = computed(() => Math.max(1, ...columns.value.map((c) => c.total)))
const firstBucketLabel = computed(() => (columns.value.length ? fmtBucket(columns.value[0]!.bucket) : ''))
const lastBucketLabel = computed(() => {
  const c = columns.value
  return c.length ? fmtBucket(c[c.length - 1]!.bucket) : ''
})
const seriesInstances = computed(() => {
  const set = new Set<string>()
  series.value.forEach((p) => set.add(p.instance || '(other)'))
  return Array.from(set)
})

// ---- summary cards ----
const totalBytes = computed(() => byInstance.value.reduce((s, r) => s + r.bytes_total, 0))
const totalUp = computed(() => byInstance.value.reduce((s, r) => s + r.bytes_up, 0))
const totalDown = computed(() => byInstance.value.reduce((s, r) => s + r.bytes_down, 0))
const distinctDests = computed(() => topDests.value.filter((r) => r.key).length)

const instanceOptions = computed(() => coverage.value.map((c) => c.instance))

function barPct(v: number, list: EgressSummaryRow[]): number {
  const max = Math.max(1, ...list.map((r) => r.bytes_total))
  return Math.max(1.5, (v / max) * 100)
}

// ---- config panel ----
const showConfig = ref(false)
const cfg = ref<EgressConfig>({})
const cfgSaving = ref(false)
async function loadConfig() {
  try {
    const res = await getEgressConfig() as any
    if (res?.code === 200) cfg.value = res.data || {}
  } catch { /* non-fatal */ }
}
async function saveConfig() {
  cfgSaving.value = true
  try {
    const res = await updateEgressConfig(cfg.value) as any
    if (res?.code === 200) {
      cfg.value = res.data || cfg.value
      toast.success(t('egress.config.saved'))
    }
  } catch (e: any) {
    toast.error(e?.response?.data?.msg || t('egress.config.saveFailed'))
  } finally {
    cfgSaving.value = false
  }
}
function cfgBool(key: string): boolean {
  const v = cfg.value[key]
  return v === '' || v === undefined ? true : (v === 'true' || v === '1' || v === 'on')
}
function setCfgBool(key: string, val: boolean) { cfg.value[key] = val ? 'true' : 'false' }

function destLabel(r: EgressSummaryRow): string { return r.key || t('egress.topDests.unresolved') }

// ---- CSV export ----
const exporting = ref(false)
async function exportCsv() {
  exporting.value = true
  try {
    const res: any = await downloadEgressCSV({ ...commonParams(), scope: 'detail' })
    const blob = res instanceof Blob ? res : res?.data
    if (!(blob instanceof Blob)) throw new Error('bad body')
    triggerBlobDownload(t('egress.csvFilePrefix') + '-detail-' + fileStamp() + '.csv', blob)
    toast.success(t('common.exported'))
  } catch (e: any) {
    toast.error(e?.response?.data?.msg || t('common.exportFailed'))
  } finally {
    exporting.value = false
  }
}
</script>

<template>
  <div class="space-y-6">
    <header class="flex items-center justify-between flex-wrap gap-3">
      <div>
        <h1 class="text-2xl font-bold text-slate-800 dark:text-white flex items-center gap-2">
          <ArrowTrendingUpIcon class="h-6 w-6 text-primary-500" />
          {{ t('egress.title') }}
        </h1>
        <p class="text-sm text-slate-500 dark:text-slate-400 mt-1">
          {{ t('egress.subtitle') }}
        </p>
      </div>
      <div class="flex items-center gap-3">
        <span v-if="lastUpdated" class="text-xs text-slate-400">{{ t('egress.updatedAt', { time: lastUpdated.toLocaleTimeString() }) }}</span>
        <button @click="exportCsv" :disabled="exporting"
          class="inline-flex items-center gap-1.5 rounded-lg border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-800 px-3 py-1.5 text-sm text-slate-600 dark:text-slate-300 hover:bg-slate-50 disabled:opacity-50">
          <ArrowDownTrayIcon class="h-4 w-4" />{{ t('common.exportCsv') }}
        </button>
        <button @click="refresh" :disabled="loading"
          class="inline-flex items-center gap-1.5 rounded-lg border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-800 px-3 py-1.5 text-sm text-slate-600 dark:text-slate-300 hover:bg-slate-50 disabled:opacity-50">
          <ArrowPathIcon class="h-4 w-4" :class="loading ? 'animate-spin' : ''" />{{ t('common.refresh') }}
        </button>
        <button @click="showConfig = !showConfig"
          class="inline-flex items-center gap-1.5 rounded-lg border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-800 px-3 py-1.5 text-sm text-slate-600 dark:text-slate-300 hover:bg-slate-50">
          <Cog6ToothIcon class="h-4 w-4" />{{ t('egress.settings') }}
        </button>
      </div>
    </header>

    <!-- Filters -->
    <div class="flex flex-wrap items-center gap-3 text-sm">
      <div class="inline-flex rounded-lg bg-slate-100 dark:bg-slate-800 p-1">
        <button v-for="r in rangePresets" :key="r.key" @click="range = r.key; refresh()"
          :class="['px-3 py-1.5 rounded-md transition text-sm', range === r.key ? 'bg-white dark:bg-slate-900 text-primary-600 shadow-sm' : 'text-slate-500 hover:text-slate-700']">
          {{ t('egress.ranges.' + r.key) }}
        </button>
      </div>
      <div class="inline-flex rounded-lg bg-slate-100 dark:bg-slate-800 p-1">
        <button v-for="d in directionOptions" :key="d.val" @click="direction = d.val; refresh()"
          :class="['px-3 py-1.5 rounded-md transition text-sm', direction === d.val ? 'bg-white dark:bg-slate-900 text-primary-600 shadow-sm' : 'text-slate-500 hover:text-slate-700']">
          {{ t('egress.directions.' + (d.val || 'all')) }}
        </button>
      </div>
      <select v-model="instance" @change="refresh"
        class="rounded-md border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-800 px-2 py-1.5 text-sm">
        <option value="">{{ t('egress.allInstances') }}</option>
        <option v-for="i in instanceOptions" :key="i" :value="i">{{ i }}</option>
      </select>
      <input v-model="userSearch" @keyup.enter="refresh" :placeholder="t('egress.filterUser')"
        class="rounded-md border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-800 px-2 py-1.5 text-sm w-40" />
    </div>

    <!-- Config panel -->
    <section v-if="showConfig" class="rounded-xl bg-white dark:bg-slate-800 border border-slate-200 dark:border-slate-700 p-5 space-y-4">
      <h2 class="text-sm font-semibold text-slate-700 dark:text-slate-200 flex items-center gap-2">
        <Cog6ToothIcon class="h-4 w-4" />{{ t('egress.config.title') }}
      </h2>
      <div class="grid grid-cols-1 md:grid-cols-2 gap-4 text-sm">
        <label class="flex items-center justify-between gap-3">
          <span>{{ t('egress.config.enabled') }}</span>
          <input type="checkbox" :checked="cfgBool('traffic_egress_enabled')" @change="setCfgBool('traffic_egress_enabled', ($event.target as HTMLInputElement).checked)" />
        </label>
        <label class="flex items-center justify-between gap-3">
          <span>{{ t('egress.config.socketSampler') }}</span>
          <input type="checkbox" :checked="cfgBool('traffic_egress_socket_sampler')" @change="setCfgBool('traffic_egress_socket_sampler', ($event.target as HTMLInputElement).checked)" />
        </label>
        <label class="flex items-center justify-between gap-3">
          <span>{{ t('egress.config.asn') }}</span>
          <input type="checkbox" :checked="cfgBool('traffic_egress_asn_enabled')" @change="setCfgBool('traffic_egress_asn_enabled', ($event.target as HTMLInputElement).checked)" />
        </label>
        <label class="flex items-center justify-between gap-3">
          <span>{{ t('egress.config.rdns') }}</span>
          <input type="checkbox" :checked="cfgBool('traffic_egress_rdns_enabled')" @change="setCfgBool('traffic_egress_rdns_enabled', ($event.target as HTMLInputElement).checked)" />
        </label>
        <label class="flex items-center justify-between gap-3">
          <span>{{ t('egress.config.retentionDays') }}</span>
          <input v-model="cfg['traffic_egress_retention_days']" placeholder="7" class="w-20 rounded border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-900 px-2 py-1 text-right" />
        </label>
        <label class="flex items-center justify-between gap-3">
          <span>{{ t('egress.config.hourlyRetentionDays') }}</span>
          <input v-model="cfg['traffic_egress_hourly_retention_days']" placeholder="90" class="w-20 rounded border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-900 px-2 py-1 text-right" />
        </label>
        <label class="flex items-center justify-between gap-3">
          <span>{{ t('egress.config.pruneHour') }}</span>
          <input v-model="cfg['traffic_egress_prune_hour']" placeholder="5" class="w-20 rounded border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-900 px-2 py-1 text-right" />
        </label>
        <label class="flex items-center justify-between gap-3 md:col-span-2">
          <span class="whitespace-nowrap">{{ t('egress.config.xrayAccessPath') }}</span>
          <input v-model="cfg['traffic_egress_xray_access_path']" placeholder="/var/log/xray/access.log" class="flex-1 rounded border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-900 px-2 py-1 font-mono text-xs" />
        </label>
      </div>
      <div class="flex justify-end">
        <button @click="saveConfig" :disabled="cfgSaving"
          class="rounded-lg bg-primary-600 text-white px-4 py-1.5 text-sm hover:bg-primary-700 disabled:opacity-50">{{ t('common.save') }}</button>
      </div>
    </section>

    <!-- Summary cards -->
    <div class="grid grid-cols-2 md:grid-cols-4 gap-4">
      <div class="rounded-xl bg-white dark:bg-slate-800 border border-slate-200 dark:border-slate-700 p-4">
        <div class="text-xs text-slate-500 uppercase tracking-wide">{{ t('egress.cards.total') }}</div>
        <div class="text-2xl font-bold text-slate-800 dark:text-white mt-1">{{ fmtBytes(totalBytes) }}</div>
      </div>
      <div class="rounded-xl bg-white dark:bg-slate-800 border border-slate-200 dark:border-slate-700 p-4">
        <div class="text-xs text-slate-500 uppercase tracking-wide">{{ t('egress.cards.upDown') }}</div>
        <div class="text-base font-semibold text-slate-800 dark:text-white mt-1 flex items-center gap-2">
          <ArrowUpIcon class="h-4 w-4 text-emerald-500" />{{ fmtBytes(totalUp) }}
          <ArrowDownIcon class="h-4 w-4 text-sky-500 ml-1" />{{ fmtBytes(totalDown) }}
        </div>
      </div>
      <div class="rounded-xl bg-white dark:bg-slate-800 border border-slate-200 dark:border-slate-700 p-4">
        <div class="text-xs text-slate-500 uppercase tracking-wide">{{ t('egress.cards.dests') }}</div>
        <div class="text-2xl font-bold text-slate-800 dark:text-white mt-1">{{ distinctDests }}</div>
      </div>
      <div class="rounded-xl bg-white dark:bg-slate-800 border border-slate-200 dark:border-slate-700 p-4">
        <div class="text-xs text-slate-500 uppercase tracking-wide">{{ t('egress.cards.activeInstances') }}</div>
        <div class="text-2xl font-bold text-slate-800 dark:text-white mt-1">{{ byInstance.length }}</div>
      </div>
    </div>

    <!-- Time series (stacked by instance) -->
    <section class="rounded-xl bg-white dark:bg-slate-800 border border-slate-200 dark:border-slate-700 p-5">
      <div class="flex items-center justify-between mb-3">
        <h2 class="text-sm font-semibold text-slate-700 dark:text-slate-200">{{ t('egress.series.title') }}</h2>
        <div class="flex flex-wrap gap-3 text-xs">
          <span v-for="i in seriesInstances" :key="i" class="inline-flex items-center gap-1 text-slate-500">
            <span class="inline-block w-2.5 h-2.5 rounded-sm" :style="{ background: instColor(i) }"></span>{{ i }}
          </span>
        </div>
      </div>
      <div v-if="columns.length === 0" class="text-center text-slate-400 py-12 text-sm">{{ t('egress.series.empty') }}</div>
      <div v-else class="flex items-end gap-px h-48 overflow-x-auto">
        <div v-for="c in columns" :key="c.bucket" class="flex flex-col-reverse min-w-[6px] flex-1 group relative" :title="fmtBucket(c.bucket) + ' · ' + fmtBytes(c.total)">
          <div v-for="seg in c.segs" :key="seg.instance"
            :style="{ height: (seg.value / colMax * 192) + 'px', background: seg.color }"
            class="w-full"></div>
        </div>
      </div>
      <div v-if="columns.length" class="flex justify-between text-[10px] text-slate-400 mt-1">
        <span>{{ firstBucketLabel }}</span>
        <span>{{ lastBucketLabel }}</span>
      </div>
    </section>

    <!-- Coverage badges -->
    <section class="rounded-xl bg-white dark:bg-slate-800 border border-slate-200 dark:border-slate-700 p-5">
      <h2 class="text-sm font-semibold text-slate-700 dark:text-slate-200 mb-3 flex items-center gap-2">
        <ServerStackIcon class="h-4 w-4" />{{ t('egress.coverage.title') }}
      </h2>
      <div class="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-3">
        <div v-for="c in coverage" :key="c.instance" class="rounded-lg border border-slate-100 dark:border-slate-700/60 p-3">
          <div class="flex items-center gap-2">
            <span class="inline-block w-2.5 h-2.5 rounded-sm" :style="{ background: instColor(c.instance) }"></span>
            <span class="font-medium text-slate-700 dark:text-slate-200 text-sm">{{ c.instance }}</span>
          </div>
          <div class="flex gap-1.5 mt-2">
            <span :class="['px-1.5 py-0.5 rounded text-[10px]', c.domain ? 'bg-emerald-100 text-emerald-700 dark:bg-emerald-900/40 dark:text-emerald-300' : 'bg-slate-100 text-slate-400 dark:bg-slate-700']">{{ t('egress.coverage.domain') }}</span>
            <span :class="['px-1.5 py-0.5 rounded text-[10px]', c.per_user ? 'bg-emerald-100 text-emerald-700 dark:bg-emerald-900/40 dark:text-emerald-300' : 'bg-slate-100 text-slate-400 dark:bg-slate-700']">{{ t('egress.coverage.user') }}</span>
            <span :class="['px-1.5 py-0.5 rounded text-[10px]', c.bytes ? 'bg-emerald-100 text-emerald-700 dark:bg-emerald-900/40 dark:text-emerald-300' : 'bg-slate-100 text-slate-400 dark:bg-slate-700']">{{ t('egress.coverage.bytes') }}</span>
          </div>
          <p class="text-[11px] text-slate-400 mt-2 leading-snug">{{ c.note }}</p>
        </div>
      </div>
    </section>

    <!-- Top destinations -->
    <section class="rounded-xl bg-white dark:bg-slate-800 border border-slate-200 dark:border-slate-700 p-5">
      <h2 class="text-sm font-semibold text-slate-700 dark:text-slate-200 mb-3 flex items-center gap-2">
        <GlobeAltIcon class="h-4 w-4" />{{ t('egress.topDests.title') }}
      </h2>
      <div v-if="topDests.length === 0" class="text-slate-400 text-sm py-6 text-center">{{ t('egress.topDests.empty') }}</div>
      <div v-else class="grid grid-cols-1 sm:grid-cols-2 xl:grid-cols-3 gap-2.5">
        <StatBar v-for="r in topDests.slice(0, 15)" :key="r.key || 'na'" :label="destLabel(r)" mono
          :value="fmtBytes(r.bytes_total)" :pct="barPct(r.bytes_total, topDests)"
          :badge="r.kind === 'rdns' ? 'rDNS' : undefined"
          :title="r.key + (r.kind === 'rdns' ? t('egress.topDests.rdnsHint') : '')" />
      </div>
    </section>

    <!-- Top ASNs -->
    <section class="rounded-xl bg-white dark:bg-slate-800 border border-slate-200 dark:border-slate-700 p-5">
      <h2 class="text-sm font-semibold text-slate-700 dark:text-slate-200 mb-3">{{ t('egress.topAsns.title') }}</h2>
      <div v-if="topAsns.length === 0" class="text-slate-400 text-sm py-6 text-center">{{ t('egress.topAsns.empty') }}</div>
      <div v-else class="grid grid-cols-1 sm:grid-cols-2 xl:grid-cols-3 gap-2.5">
        <StatBar v-for="r in topAsns.slice(0, 15)" :key="r.key" :label="r.key" mono
          :sub="r.as_org || undefined" :value="fmtBytes(r.bytes_total)"
          :pct="barPct(r.bytes_total, topAsns)" color="#6366f1" :title="r.as_org || r.key" />
      </div>
    </section>

    <!-- By instance -->
    <section class="rounded-xl bg-white dark:bg-slate-800 border border-slate-200 dark:border-slate-700 p-5">
      <h2 class="text-sm font-semibold text-slate-700 dark:text-slate-200 mb-3">{{ t('egress.byInstance.title') }}</h2>
      <div class="grid grid-cols-1 sm:grid-cols-2 xl:grid-cols-3 gap-2.5">
        <StatBar v-for="r in byInstance" :key="r.key || 'na'" :label="r.key || t('egress.byInstance.unknown')"
          :value="fmtBytes(r.bytes_total)" :pct="barPct(r.bytes_total, byInstance)"
          :dotColor="instColor(r.key)" :color="instColor(r.key)" />
      </div>
    </section>

    <!-- By user -->
    <section class="rounded-xl bg-white dark:bg-slate-800 border border-slate-200 dark:border-slate-700 p-5">
      <h2 class="text-sm font-semibold text-slate-700 dark:text-slate-200 mb-3">{{ t('egress.byUser.title') }}</h2>
      <div v-if="byUser.length === 0" class="text-slate-400 text-sm py-6 text-center">{{ t('egress.byUser.empty') }}</div>
      <div v-else class="grid grid-cols-1 sm:grid-cols-2 xl:grid-cols-3 gap-2.5">
        <StatBar v-for="r in byUser.slice(0, 15)" :key="r.key" :label="r.key" mono
          :value="fmtBytes(r.bytes_total)" :pct="barPct(r.bytes_total, byUser)" color="#f59e0b" />
      </div>
    </section>

    <!-- Detail table -->
    <section class="rounded-xl bg-white dark:bg-slate-800 border border-slate-200 dark:border-slate-700 overflow-hidden">
      <div class="px-4 py-3 border-b border-slate-100 dark:border-slate-700/40">
        <h2 class="text-sm font-semibold text-slate-800 dark:text-slate-100">{{ t('egress.table.title') }}</h2>
      </div>
      <div class="overflow-x-auto">
        <table class="w-full text-sm">
          <thead class="bg-slate-50 dark:bg-slate-900/40 text-slate-500 text-xs uppercase tracking-wide">
            <tr>
              <th class="text-left px-4 py-2.5">{{ t('egress.table.time') }}</th>
              <th class="text-left px-4 py-2.5">{{ t('egress.table.instance') }}</th>
              <th class="text-left px-4 py-2.5">{{ t('egress.table.user') }}</th>
              <th class="text-left px-4 py-2.5">{{ t('egress.table.dest') }}</th>
              <th class="text-left px-4 py-2.5">{{ t('egress.table.asn') }}</th>
              <th class="text-left px-4 py-2.5">{{ t('egress.table.direction') }}</th>
              <th class="text-right px-4 py-2.5">{{ t('egress.table.up') }}</th>
              <th class="text-right px-4 py-2.5">{{ t('egress.table.down') }}</th>
            </tr>
          </thead>
          <tbody>
            <tr v-if="rows.length === 0"><td colspan="8" class="text-center text-slate-400 py-8">{{ t('egress.table.empty') }}</td></tr>
            <tr v-for="(r, idx) in rows" :key="idx" class="border-t border-slate-100 dark:border-slate-700/50">
              <td class="px-4 py-2 text-xs text-slate-500 whitespace-nowrap">{{ fmtBucket(r.bucket) }}</td>
              <td class="px-4 py-2 text-xs">
                <span class="inline-flex items-center gap-1">
                  <span class="inline-block w-2 h-2 rounded-sm" :style="{ background: instColor(r.instance) }"></span>{{ r.instance }}
                </span>
              </td>
              <td class="px-4 py-2 text-xs text-slate-500 break-all max-w-[12rem]" :title="r.user_email">{{ r.user_email || '—' }}</td>
              <td class="px-4 py-2 text-xs font-mono text-slate-600 dark:text-slate-300 break-all max-w-[20rem]"
                :title="[r.dest_host || r.dest_rdns, r.dest_ip].filter(Boolean).join(' · ')">
                {{ r.dest_host || r.dest_rdns || r.dest_ip || '—' }}
                <span v-if="!r.dest_host && r.dest_rdns"
                  class="px-1 rounded text-[9px] bg-sky-100 text-sky-600 dark:bg-sky-900/40 dark:text-sky-300">rDNS</span>
                <span v-if="r.country" class="text-slate-400">· {{ r.country }}</span>
              </td>
              <td class="px-4 py-2 text-xs text-slate-500 break-all max-w-[12rem]" :title="r.as_org">{{ r.asn || '—' }}</td>
              <td class="px-4 py-2 text-xs">
                <span :class="r.direction === 'return' ? 'text-amber-600' : 'text-sky-600'">{{ r.direction === 'return' ? t('egress.directions.return') : t('egress.directions.egress') }}</span>
              </td>
              <td class="px-4 py-2 text-right text-xs tabular-nums text-emerald-600">{{ fmtBytes(r.bytes_up) }}</td>
              <td class="px-4 py-2 text-right text-xs tabular-nums text-sky-600">{{ fmtBytes(r.bytes_down) }}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </section>
  </div>
</template>
