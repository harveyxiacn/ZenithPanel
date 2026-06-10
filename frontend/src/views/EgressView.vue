<script setup lang="ts">
import { ref, computed, onMounted, onBeforeUnmount } from 'vue'
import {
  ArrowTrendingUpIcon, ArrowUpIcon, ArrowDownIcon, GlobeAltIcon,
  Cog6ToothIcon, ArrowPathIcon, ServerStackIcon,
} from '@heroicons/vue/24/outline'
import { useToast } from '@/composables/useToast'
import {
  getEgressSeries, getEgressSummary, getEgressCoverage, getEgressList,
  getEgressConfig, updateEgressConfig,
  type EgressSeriesPoint, type EgressSummaryRow, type EgressCoverage,
  type EgressRow, type EgressConfig,
} from '@/api/traffic'

const toast = useToast()

// ---- filters ----
type RangeKey = '1h' | '6h' | '24h' | '7d' | '30d'
const rangePresets: { key: RangeKey; label: string; secs: number }[] = [
  { key: '1h', label: '1 小时', secs: 3600 },
  { key: '6h', label: '6 小时', secs: 6 * 3600 },
  { key: '24h', label: '24 小时', secs: 24 * 3600 },
  { key: '7d', label: '7 天', secs: 7 * 86400 },
  { key: '30d', label: '30 天', secs: 30 * 86400 },
]
const range = ref<RangeKey>('6h')
const instance = ref<string>('')
const direction = ref<string>('egress')
const userSearch = ref<string>('')
const directionOptions: { val: string; label: string }[] = [
  { val: 'egress', label: '出口' },
  { val: 'return', label: '回程' },
  { val: '', label: '全部' },
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
    toast.error(e?.response?.data?.msg || '加载出口流量失败')
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
      toast.success('已保存')
    }
  } catch (e: any) {
    toast.error(e?.response?.data?.msg || '保存失败')
  } finally {
    cfgSaving.value = false
  }
}
function cfgBool(key: string): boolean {
  const v = cfg.value[key]
  return v === '' || v === undefined ? true : (v === 'true' || v === '1' || v === 'on')
}
function setCfgBool(key: string, val: boolean) { cfg.value[key] = val ? 'true' : 'false' }

function destLabel(r: EgressSummaryRow): string { return r.key || '(未解析)' }
</script>

<template>
  <div class="space-y-6">
    <header class="flex items-center justify-between flex-wrap gap-3">
      <div>
        <h1 class="text-2xl font-bold text-slate-800 dark:text-white flex items-center gap-2">
          <ArrowTrendingUpIcon class="h-6 w-6 text-primary-500" />
          出口流量
        </h1>
        <p class="text-sm text-slate-500 dark:text-slate-400 mt-1">
          按代理实例 / 用户 / 目的地（域名·IP·ASN）统计的出口与回程流量历史
        </p>
      </div>
      <div class="flex items-center gap-3">
        <span v-if="lastUpdated" class="text-xs text-slate-400">更新于 {{ lastUpdated.toLocaleTimeString() }}</span>
        <button @click="refresh" :disabled="loading"
          class="inline-flex items-center gap-1.5 rounded-lg border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-800 px-3 py-1.5 text-sm text-slate-600 dark:text-slate-300 hover:bg-slate-50 disabled:opacity-50">
          <ArrowPathIcon class="h-4 w-4" :class="loading ? 'animate-spin' : ''" />刷新
        </button>
        <button @click="showConfig = !showConfig"
          class="inline-flex items-center gap-1.5 rounded-lg border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-800 px-3 py-1.5 text-sm text-slate-600 dark:text-slate-300 hover:bg-slate-50">
          <Cog6ToothIcon class="h-4 w-4" />设置
        </button>
      </div>
    </header>

    <!-- Filters -->
    <div class="flex flex-wrap items-center gap-3 text-sm">
      <div class="inline-flex rounded-lg bg-slate-100 dark:bg-slate-800 p-1">
        <button v-for="r in rangePresets" :key="r.key" @click="range = r.key; refresh()"
          :class="['px-3 py-1.5 rounded-md transition text-sm', range === r.key ? 'bg-white dark:bg-slate-900 text-primary-600 shadow-sm' : 'text-slate-500 hover:text-slate-700']">
          {{ r.label }}
        </button>
      </div>
      <div class="inline-flex rounded-lg bg-slate-100 dark:bg-slate-800 p-1">
        <button v-for="d in directionOptions" :key="d.val" @click="direction = d.val; refresh()"
          :class="['px-3 py-1.5 rounded-md transition text-sm', direction === d.val ? 'bg-white dark:bg-slate-900 text-primary-600 shadow-sm' : 'text-slate-500 hover:text-slate-700']">
          {{ d.label }}
        </button>
      </div>
      <select v-model="instance" @change="refresh"
        class="rounded-md border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-800 px-2 py-1.5 text-sm">
        <option value="">全部实例</option>
        <option v-for="i in instanceOptions" :key="i" :value="i">{{ i }}</option>
      </select>
      <input v-model="userSearch" @keyup.enter="refresh" placeholder="按用户邮箱筛选…"
        class="rounded-md border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-800 px-2 py-1.5 text-sm w-40" />
    </div>

    <!-- Config panel -->
    <section v-if="showConfig" class="rounded-xl bg-white dark:bg-slate-800 border border-slate-200 dark:border-slate-700 p-5 space-y-4">
      <h2 class="text-sm font-semibold text-slate-700 dark:text-slate-200 flex items-center gap-2">
        <Cog6ToothIcon class="h-4 w-4" />采集设置
      </h2>
      <div class="grid grid-cols-1 md:grid-cols-2 gap-4 text-sm">
        <label class="flex items-center justify-between gap-3">
          <span>启用出口流量采集</span>
          <input type="checkbox" :checked="cfgBool('traffic_egress_enabled')" @change="setCfgBool('traffic_egress_enabled', ($event.target as HTMLInputElement).checked)" />
        </label>
        <label class="flex items-center justify-between gap-3">
          <span>ss 套接字采样器（全服务）</span>
          <input type="checkbox" :checked="cfgBool('traffic_egress_socket_sampler')" @change="setCfgBool('traffic_egress_socket_sampler', ($event.target as HTMLInputElement).checked)" />
        </label>
        <label class="flex items-center justify-between gap-3">
          <span>ASN/国家 DNS 解析</span>
          <input type="checkbox" :checked="cfgBool('traffic_egress_asn_enabled')" @change="setCfgBool('traffic_egress_asn_enabled', ($event.target as HTMLInputElement).checked)" />
        </label>
        <label class="flex items-center justify-between gap-3">
          <span>反向 DNS 域名补全（IP-only 目的地）</span>
          <input type="checkbox" :checked="cfgBool('traffic_egress_rdns_enabled')" @change="setCfgBool('traffic_egress_rdns_enabled', ($event.target as HTMLInputElement).checked)" />
        </label>
        <label class="flex items-center justify-between gap-3">
          <span>明细保留天数</span>
          <input v-model="cfg['traffic_egress_retention_days']" placeholder="7" class="w-20 rounded border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-900 px-2 py-1 text-right" />
        </label>
        <label class="flex items-center justify-between gap-3">
          <span>小时汇总保留天数</span>
          <input v-model="cfg['traffic_egress_hourly_retention_days']" placeholder="90" class="w-20 rounded border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-900 px-2 py-1 text-right" />
        </label>
        <label class="flex items-center justify-between gap-3">
          <span>清理时刻（0-23）</span>
          <input v-model="cfg['traffic_egress_prune_hour']" placeholder="5" class="w-20 rounded border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-900 px-2 py-1 text-right" />
        </label>
        <label class="flex items-center justify-between gap-3 md:col-span-2">
          <span class="whitespace-nowrap">zenith-xray access.log 路径（留空=不启用域名采集）</span>
          <input v-model="cfg['traffic_egress_xray_access_path']" placeholder="/var/log/xray/access.log" class="flex-1 rounded border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-900 px-2 py-1 font-mono text-xs" />
        </label>
      </div>
      <div class="flex justify-end">
        <button @click="saveConfig" :disabled="cfgSaving"
          class="rounded-lg bg-primary-600 text-white px-4 py-1.5 text-sm hover:bg-primary-700 disabled:opacity-50">保存</button>
      </div>
    </section>

    <!-- Summary cards -->
    <div class="grid grid-cols-2 md:grid-cols-4 gap-4">
      <div class="rounded-xl bg-white dark:bg-slate-800 border border-slate-200 dark:border-slate-700 p-4">
        <div class="text-xs text-slate-500 uppercase tracking-wide">区间总流量</div>
        <div class="text-2xl font-bold text-slate-800 dark:text-white mt-1">{{ fmtBytes(totalBytes) }}</div>
      </div>
      <div class="rounded-xl bg-white dark:bg-slate-800 border border-slate-200 dark:border-slate-700 p-4">
        <div class="text-xs text-slate-500 uppercase tracking-wide">上行 / 下行</div>
        <div class="text-base font-semibold text-slate-800 dark:text-white mt-1 flex items-center gap-2">
          <ArrowUpIcon class="h-4 w-4 text-emerald-500" />{{ fmtBytes(totalUp) }}
          <ArrowDownIcon class="h-4 w-4 text-sky-500 ml-1" />{{ fmtBytes(totalDown) }}
        </div>
      </div>
      <div class="rounded-xl bg-white dark:bg-slate-800 border border-slate-200 dark:border-slate-700 p-4">
        <div class="text-xs text-slate-500 uppercase tracking-wide">目的地数</div>
        <div class="text-2xl font-bold text-slate-800 dark:text-white mt-1">{{ distinctDests }}</div>
      </div>
      <div class="rounded-xl bg-white dark:bg-slate-800 border border-slate-200 dark:border-slate-700 p-4">
        <div class="text-xs text-slate-500 uppercase tracking-wide">活跃实例</div>
        <div class="text-2xl font-bold text-slate-800 dark:text-white mt-1">{{ byInstance.length }}</div>
      </div>
    </div>

    <!-- Time series (stacked by instance) -->
    <section class="rounded-xl bg-white dark:bg-slate-800 border border-slate-200 dark:border-slate-700 p-5">
      <div class="flex items-center justify-between mb-3">
        <h2 class="text-sm font-semibold text-slate-700 dark:text-slate-200">流量时间序列（按实例堆叠）</h2>
        <div class="flex flex-wrap gap-3 text-xs">
          <span v-for="i in seriesInstances" :key="i" class="inline-flex items-center gap-1 text-slate-500">
            <span class="inline-block w-2.5 h-2.5 rounded-sm" :style="{ background: instColor(i) }"></span>{{ i }}
          </span>
        </div>
      </div>
      <div v-if="columns.length === 0" class="text-center text-slate-400 py-12 text-sm">该区间暂无数据（采集每 30 秒写入一次，新启用需稍等）</div>
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
        <ServerStackIcon class="h-4 w-4" />各实例数据精度（诚实标注）
      </h2>
      <div class="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-3">
        <div v-for="c in coverage" :key="c.instance" class="rounded-lg border border-slate-100 dark:border-slate-700/60 p-3">
          <div class="flex items-center gap-2">
            <span class="inline-block w-2.5 h-2.5 rounded-sm" :style="{ background: instColor(c.instance) }"></span>
            <span class="font-medium text-slate-700 dark:text-slate-200 text-sm">{{ c.instance }}</span>
          </div>
          <div class="flex gap-1.5 mt-2">
            <span :class="['px-1.5 py-0.5 rounded text-[10px]', c.domain ? 'bg-emerald-100 text-emerald-700 dark:bg-emerald-900/40 dark:text-emerald-300' : 'bg-slate-100 text-slate-400 dark:bg-slate-700']">域名</span>
            <span :class="['px-1.5 py-0.5 rounded text-[10px]', c.per_user ? 'bg-emerald-100 text-emerald-700 dark:bg-emerald-900/40 dark:text-emerald-300' : 'bg-slate-100 text-slate-400 dark:bg-slate-700']">用户</span>
            <span :class="['px-1.5 py-0.5 rounded text-[10px]', c.bytes ? 'bg-emerald-100 text-emerald-700 dark:bg-emerald-900/40 dark:text-emerald-300' : 'bg-slate-100 text-slate-400 dark:bg-slate-700']">字节</span>
          </div>
          <p class="text-[11px] text-slate-400 mt-2 leading-snug">{{ c.note }}</p>
        </div>
      </div>
    </section>

    <!-- Top destinations + ASNs -->
    <div class="grid grid-cols-1 lg:grid-cols-2 gap-4">
      <section class="rounded-xl bg-white dark:bg-slate-800 border border-slate-200 dark:border-slate-700 p-5">
        <h2 class="text-sm font-semibold text-slate-700 dark:text-slate-200 mb-3 flex items-center gap-2">
          <GlobeAltIcon class="h-4 w-4" />热门目的地（域名 / IP）
        </h2>
        <div v-if="topDests.length === 0" class="text-slate-400 text-sm py-6 text-center">暂无数据</div>
        <div v-for="r in topDests.slice(0, 15)" :key="r.key || 'na'" class="flex items-center gap-3 mb-1.5">
          <div class="w-48 flex items-center gap-1 min-w-0" :title="r.key + (r.kind === 'rdns' ? '（反向 DNS 解析，仅供参考）' : '')">
            <span class="truncate text-xs font-mono text-slate-600 dark:text-slate-300">{{ destLabel(r) }}</span>
            <span v-if="r.kind === 'rdns'"
              class="shrink-0 px-1 rounded text-[9px] leading-4 bg-sky-100 text-sky-600 dark:bg-sky-900/40 dark:text-sky-300">rDNS</span>
          </div>
          <div class="flex-1 bg-slate-100 dark:bg-slate-700/50 rounded h-4 overflow-hidden">
            <div class="h-full bg-primary-500" :style="{ width: barPct(r.bytes_total, topDests) + '%' }"></div>
          </div>
          <div class="w-20 text-right text-xs tabular-nums text-slate-500">{{ fmtBytes(r.bytes_total) }}</div>
        </div>
      </section>

      <section class="rounded-xl bg-white dark:bg-slate-800 border border-slate-200 dark:border-slate-700 p-5">
        <h2 class="text-sm font-semibold text-slate-700 dark:text-slate-200 mb-3">热门 ASN / 机构</h2>
        <div v-if="topAsns.length === 0" class="text-slate-400 text-sm py-6 text-center">暂无数据（ASN 异步解析，稍后出现）</div>
        <div v-for="r in topAsns.slice(0, 15)" :key="r.key" class="flex items-center gap-3 mb-1.5">
          <div class="w-48 truncate text-xs text-slate-600 dark:text-slate-300" :title="r.as_org || r.key">
            <span class="font-mono">{{ r.key }}</span>
            <span v-if="r.as_org" class="text-slate-400"> · {{ r.as_org }}</span>
          </div>
          <div class="flex-1 bg-slate-100 dark:bg-slate-700/50 rounded h-4 overflow-hidden">
            <div class="h-full bg-indigo-500" :style="{ width: barPct(r.bytes_total, topAsns) + '%' }"></div>
          </div>
          <div class="w-20 text-right text-xs tabular-nums text-slate-500">{{ fmtBytes(r.bytes_total) }}</div>
        </div>
      </section>
    </div>

    <!-- By instance + by user -->
    <div class="grid grid-cols-1 lg:grid-cols-2 gap-4">
      <section class="rounded-xl bg-white dark:bg-slate-800 border border-slate-200 dark:border-slate-700 p-5">
        <h2 class="text-sm font-semibold text-slate-700 dark:text-slate-200 mb-3">按实例</h2>
        <div v-for="r in byInstance" :key="r.key || 'na'" class="flex items-center gap-3 mb-1.5">
          <div class="w-40 flex items-center gap-1.5 text-xs text-slate-600 dark:text-slate-300">
            <span class="inline-block w-2.5 h-2.5 rounded-sm" :style="{ background: instColor(r.key) }"></span>{{ r.key || '(未知)' }}
          </div>
          <div class="flex-1 bg-slate-100 dark:bg-slate-700/50 rounded h-4 overflow-hidden">
            <div class="h-full" :style="{ width: barPct(r.bytes_total, byInstance) + '%', background: instColor(r.key) }"></div>
          </div>
          <div class="w-20 text-right text-xs tabular-nums text-slate-500">{{ fmtBytes(r.bytes_total) }}</div>
        </div>
      </section>

      <section class="rounded-xl bg-white dark:bg-slate-800 border border-slate-200 dark:border-slate-700 p-5">
        <h2 class="text-sm font-semibold text-slate-700 dark:text-slate-200 mb-3">按用户</h2>
        <div v-if="byUser.length === 0" class="text-slate-400 text-sm py-6 text-center">无 per-user 数据（IP-only 实例不含用户）</div>
        <div v-for="r in byUser.slice(0, 15)" :key="r.key" class="flex items-center gap-3 mb-1.5">
          <div class="w-40 truncate text-xs text-slate-600 dark:text-slate-300" :title="r.key">{{ r.key }}</div>
          <div class="flex-1 bg-slate-100 dark:bg-slate-700/50 rounded h-4 overflow-hidden">
            <div class="h-full bg-amber-500" :style="{ width: barPct(r.bytes_total, byUser) + '%' }"></div>
          </div>
          <div class="w-20 text-right text-xs tabular-nums text-slate-500">{{ fmtBytes(r.bytes_total) }}</div>
        </div>
      </section>
    </div>

    <!-- Detail table -->
    <section class="rounded-xl bg-white dark:bg-slate-800 border border-slate-200 dark:border-slate-700 overflow-hidden">
      <div class="px-4 py-3 border-b border-slate-100 dark:border-slate-700/40">
        <h2 class="text-sm font-semibold text-slate-800 dark:text-slate-100">明细（最近 200 条桶记录）</h2>
      </div>
      <div class="overflow-x-auto">
        <table class="w-full text-sm">
          <thead class="bg-slate-50 dark:bg-slate-900/40 text-slate-500 text-xs uppercase tracking-wide">
            <tr>
              <th class="text-left px-4 py-2.5">时间</th>
              <th class="text-left px-4 py-2.5">实例</th>
              <th class="text-left px-4 py-2.5">用户</th>
              <th class="text-left px-4 py-2.5">目的地</th>
              <th class="text-left px-4 py-2.5">ASN</th>
              <th class="text-left px-4 py-2.5">方向</th>
              <th class="text-right px-4 py-2.5">上行</th>
              <th class="text-right px-4 py-2.5">下行</th>
            </tr>
          </thead>
          <tbody>
            <tr v-if="rows.length === 0"><td colspan="8" class="text-center text-slate-400 py-8">暂无记录</td></tr>
            <tr v-for="(r, idx) in rows" :key="idx" class="border-t border-slate-100 dark:border-slate-700/50">
              <td class="px-4 py-2 text-xs text-slate-500 whitespace-nowrap">{{ fmtBucket(r.bucket) }}</td>
              <td class="px-4 py-2 text-xs">
                <span class="inline-flex items-center gap-1">
                  <span class="inline-block w-2 h-2 rounded-sm" :style="{ background: instColor(r.instance) }"></span>{{ r.instance }}
                </span>
              </td>
              <td class="px-4 py-2 text-xs text-slate-500 truncate max-w-[10rem]" :title="r.user_email">{{ r.user_email || '—' }}</td>
              <td class="px-4 py-2 text-xs font-mono text-slate-600 dark:text-slate-300 truncate max-w-[16rem]"
                :title="[r.dest_host || r.dest_rdns, r.dest_ip].filter(Boolean).join(' · ')">
                {{ r.dest_host || r.dest_rdns || r.dest_ip || '—' }}
                <span v-if="!r.dest_host && r.dest_rdns"
                  class="px-1 rounded text-[9px] bg-sky-100 text-sky-600 dark:bg-sky-900/40 dark:text-sky-300">rDNS</span>
                <span v-if="r.country" class="text-slate-400">· {{ r.country }}</span>
              </td>
              <td class="px-4 py-2 text-xs text-slate-500 truncate max-w-[10rem]" :title="r.as_org">{{ r.asn || '—' }}</td>
              <td class="px-4 py-2 text-xs">
                <span :class="r.direction === 'return' ? 'text-amber-600' : 'text-sky-600'">{{ r.direction === 'return' ? '回程' : '出口' }}</span>
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
