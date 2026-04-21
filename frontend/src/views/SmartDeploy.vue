<script setup lang="ts">
import { computed, h, onMounted, ref } from 'vue'
import {
  deployApply,
  deployClients,
  deployPreview,
  deployProbe,
  deployRollback,
} from '@/api/deploy'
import type {
  Deployment,
  DeployPlan,
  PresetID,
  ProbeResult,
} from '@/types/deploy'
import { PRESETS } from '@/types/deploy'

type Step = 1 | 2 | 3 | 4 | 5

// Chip is a tiny inline component used only by the probe summary grid.
const chipToneClasses: Record<string, string> = {
  good: 'bg-emerald-50 dark:bg-emerald-900/20 text-emerald-700 dark:text-emerald-300 border-emerald-200 dark:border-emerald-800',
  warn: 'bg-amber-50 dark:bg-amber-900/20 text-amber-700 dark:text-amber-300 border-amber-200 dark:border-amber-800',
  bad: 'bg-rose-50 dark:bg-rose-900/20 text-rose-700 dark:text-rose-300 border-rose-200 dark:border-rose-800',
  neutral: 'bg-white dark:bg-slate-800 text-slate-700 dark:text-slate-300 border-slate-200 dark:border-slate-700',
}
function Chip(props: { label: string; value: string; tone?: string }) {
  const tone = props.tone ?? 'neutral'
  return h(
    'div',
    { class: `flex justify-between items-center px-3 py-2 rounded border text-sm ${chipToneClasses[tone] ?? chipToneClasses.neutral}` },
    [
      h('span', { class: 'text-xs uppercase tracking-wide text-slate-500 dark:text-slate-400' }, props.label),
      h('span', { class: 'font-mono text-xs' }, props.value),
    ],
  )
}

const step = ref<Step>(1)
const probe = ref<ProbeResult | null>(null)
const probeLoading = ref(false)
const probeError = ref('')

const selectedPreset = ref<PresetID>('stable_egress')
const domain = ref('')
const email = ref('')
const realityTarget = ref('')
const portOverride = ref<number | null>(null)

const plan = ref<DeployPlan | null>(null)
const previewLoading = ref(false)
const previewError = ref('')

const deployment = ref<Deployment | null>(null)
const applyLoading = ref(false)
const applyError = ref('')
const clientsInfo = ref<{ inbound_ids: string; note: string } | null>(null)

onMounted(() => {
  void runProbe()
})

async function runProbe() {
  probeLoading.value = true
  probeError.value = ''
  try {
    probe.value = await deployProbe()
  } catch (err: any) {
    probeError.value = err?.message ?? String(err)
  } finally {
    probeLoading.value = false
  }
}

function goto(s: Step) {
  step.value = s
}

async function generatePreview() {
  previewLoading.value = true
  previewError.value = ''
  plan.value = null
  try {
    const res = await deployPreview({
      preset_id: selectedPreset.value,
      domain: domain.value || undefined,
      email: email.value || undefined,
      reality_target: realityTarget.value || undefined,
      port_override: portOverride.value || undefined,
    })
    plan.value = res.plan
    probe.value = res.probe
    goto(4)
  } catch (err: any) {
    previewError.value = err?.response?.data?.msg ?? err?.message ?? String(err)
  } finally {
    previewLoading.value = false
  }
}

async function applyDeployment() {
  applyLoading.value = true
  applyError.value = ''
  try {
    deployment.value = await deployApply({
      preset_id: selectedPreset.value,
      domain: domain.value || undefined,
      email: email.value || undefined,
      reality_target: realityTarget.value || undefined,
      port_override: portOverride.value || undefined,
    })
    if (deployment.value.status === 'succeeded') {
      clientsInfo.value = await deployClients(deployment.value.id)
    }
    goto(5)
  } catch (err: any) {
    applyError.value = err?.response?.data?.msg ?? err?.message ?? String(err)
  } finally {
    applyLoading.value = false
  }
}

async function rollbackDeployment() {
  if (!deployment.value) return
  if (!confirm('Roll back this deployment? All tuning and inbounds from this run will be reverted.')) return
  const d = await deployRollback(deployment.value.id)
  deployment.value = d
}

function resetWizard() {
  step.value = 1
  plan.value = null
  deployment.value = null
  clientsInfo.value = null
  applyError.value = ''
  previewError.value = ''
  void runProbe()
}

const ramGB = computed(() =>
  probe.value ? (probe.value.hardware.ram_bytes / (1024 ** 3)).toFixed(1) : '?',
)

const blockers = computed(() => {
  if (!probe.value) return [] as string[]
  const out: string[] = []
  if (!probe.value.root_check.ok) out.push('面板不是 root 身份运行，无法应用系统调优。')
  if (!probe.value.systemd.present) out.push('未检测到 systemd，Phase 1 不支持 SysV init。')
  if (!probe.value.public_ip.v4) out.push('未能检测到公网 IPv4。')
  return out
})

const warnings = computed(() => {
  if (!probe.value) return [] as string[]
  const out: string[] = []
  if (!probe.value.time_sync.synced) out.push('系统时间未同步，TLS 握手可能失败。建议先启用 chronyd 或 systemd-timesyncd。')
  if (!probe.value.kernel.features.bbr) out.push('内核未启用 BBR 模块，调优效果会受限。')
  if (probe.value.port_avail.ports[443] === false) out.push('端口 443 已被占用，面板会自动选备用端口（8443 等）。')
  return out
})
</script>

<template>
  <div class="min-h-screen bg-slate-50 dark:bg-slate-900 p-6">
    <div class="max-w-4xl mx-auto">
      <header class="mb-8">
        <h1 class="text-2xl font-semibold text-slate-900 dark:text-slate-100">智能部署 Smart Deploy</h1>
        <p class="text-sm text-slate-600 dark:text-slate-400 mt-1">
          从零到可用出口隧道，一步完成：环境探测 → 预设选择 → 系统调优 → 协议部署 → 订阅生成。
        </p>
      </header>

      <!-- Step indicator -->
      <div class="flex items-center gap-2 mb-8">
        <template v-for="n in 5" :key="n">
          <div
            class="flex items-center gap-2"
            :class="n <= step ? 'text-emerald-600 dark:text-emerald-400' : 'text-slate-400 dark:text-slate-600'"
          >
            <div
              class="w-8 h-8 rounded-full flex items-center justify-center text-sm font-medium border"
              :class="n <= step
                ? 'border-emerald-500 bg-emerald-500 text-white'
                : 'border-slate-300 dark:border-slate-700'"
            >
              {{ n }}
            </div>
            <span class="text-sm hidden sm:inline">
              {{ ['探测', '预设', '选项', '预览', '完成'][n - 1] }}
            </span>
          </div>
          <div v-if="n < 5" class="flex-1 h-px bg-slate-200 dark:bg-slate-700"></div>
        </template>
      </div>

      <!-- Step 1: Probe -->
      <section v-if="step === 1" class="space-y-4">
        <div v-if="probeLoading" class="bg-white dark:bg-slate-800 rounded-lg p-6 text-center">
          <p class="text-slate-600 dark:text-slate-400">正在探测环境……</p>
        </div>
        <div v-else-if="probeError" class="bg-rose-50 dark:bg-rose-900/20 border border-rose-200 dark:border-rose-800 rounded-lg p-4">
          <p class="text-rose-700 dark:text-rose-300">探测失败：{{ probeError }}</p>
          <button class="mt-2 text-sm underline" @click="runProbe">重试</button>
        </div>
        <div v-else-if="probe">
          <div class="grid sm:grid-cols-2 lg:grid-cols-3 gap-3">
            <Chip tone="neutral" label="内核" :value="probe.kernel.version || '未知'" />
            <Chip :tone="probe.kernel.features.bbr ? 'good' : 'warn'"
                  label="BBR" :value="probe.kernel.features.bbr ? '可用' : '不可用'" />
            <Chip :tone="probe.root_check.ok ? 'good' : 'bad'"
                  label="权限" :value="probe.root_check.ok ? 'root' : `UID ${probe.root_check.uid}`" />
            <Chip tone="neutral" label="发行版"
                  :value="`${probe.distro.pretty_name || probe.distro.id}`" />
            <Chip tone="neutral" label="公网 IPv4" :value="probe.public_ip.v4 || '未检测到'" />
            <Chip tone="neutral" label="CPU / RAM"
                  :value="`${probe.hardware.cpu_cores} 核 / ${ramGB} GB`" />
            <Chip tone="neutral" label="主网卡"
                  :value="probe.nic.primary ? `${probe.nic.primary} · ${probe.nic.link_speed_mbps} Mbps` : '未识别'" />
            <Chip :tone="probe.time_sync.synced ? 'good' : 'warn'"
                  label="时间同步" :value="probe.time_sync.service || 'none'" />
            <Chip tone="neutral" label="防火墙" :value="probe.firewall.type" />
            <Chip :tone="probe.port_avail.ports[443] ? 'good' : 'warn'"
                  label="端口 443" :value="probe.port_avail.ports[443] ? '空闲' : '已占用'" />
            <Chip :tone="probe.port_avail.ports[8443] ? 'good' : 'warn'"
                  label="端口 8443" :value="probe.port_avail.ports[8443] ? '空闲' : '已占用'" />
            <Chip tone="neutral" label="Systemd"
                  :value="probe.systemd.present ? (probe.systemd.version || 'present') : '缺失'" />
          </div>

          <div v-if="blockers.length" class="mt-4 bg-rose-50 dark:bg-rose-900/20 border border-rose-200 dark:border-rose-800 rounded-lg p-4">
            <h3 class="font-medium text-rose-700 dark:text-rose-300">阻断</h3>
            <ul class="mt-2 text-sm text-rose-700 dark:text-rose-300 list-disc list-inside">
              <li v-for="(b, i) in blockers" :key="i">{{ b }}</li>
            </ul>
          </div>
          <div v-if="warnings.length" class="mt-4 bg-amber-50 dark:bg-amber-900/20 border border-amber-200 dark:border-amber-800 rounded-lg p-4">
            <h3 class="font-medium text-amber-700 dark:text-amber-300">警告</h3>
            <ul class="mt-2 text-sm text-amber-700 dark:text-amber-300 list-disc list-inside">
              <li v-for="(w, i) in warnings" :key="i">{{ w }}</li>
            </ul>
          </div>

          <div class="mt-6 flex justify-end gap-2">
            <button class="px-4 py-2 text-sm rounded border border-slate-300 dark:border-slate-700"
                    @click="runProbe">重新探测</button>
            <button class="px-4 py-2 text-sm rounded bg-emerald-600 text-white hover:bg-emerald-700 disabled:opacity-50"
                    :disabled="blockers.length > 0"
                    @click="goto(2)">下一步：选择预设</button>
          </div>
        </div>
      </section>

      <!-- Step 2: Preset selection -->
      <section v-else-if="step === 2" class="space-y-4">
        <div class="grid sm:grid-cols-2 gap-4">
          <button
            v-for="preset in PRESETS"
            :key="preset.id"
            class="text-left p-4 rounded-lg border-2 transition-colors"
            :class="selectedPreset === preset.id
              ? 'border-emerald-500 bg-emerald-50 dark:bg-emerald-900/20'
              : 'border-slate-200 dark:border-slate-700 hover:border-slate-400 bg-white dark:bg-slate-800'"
            @click="selectedPreset = preset.id"
          >
            <div class="flex items-start justify-between">
              <h3 class="font-semibold text-slate-900 dark:text-slate-100">{{ preset.displayName }}</h3>
              <span v-if="preset.recommended"
                    class="text-xs px-2 py-0.5 rounded bg-emerald-100 dark:bg-emerald-900/40 text-emerald-700 dark:text-emerald-300">
                推荐
              </span>
            </div>
            <p class="text-xs text-slate-500 dark:text-slate-400 mt-1 font-mono">{{ preset.description }}</p>
            <p class="text-sm text-slate-600 dark:text-slate-400 mt-2">{{ preset.useCase }}</p>
          </button>
        </div>
        <div class="flex justify-between pt-2">
          <button class="px-4 py-2 text-sm rounded border border-slate-300 dark:border-slate-700"
                  @click="goto(1)">上一步</button>
          <button class="px-4 py-2 text-sm rounded bg-emerald-600 text-white hover:bg-emerald-700"
                  @click="goto(3)">下一步：可选参数</button>
        </div>
      </section>

      <!-- Step 3: Options -->
      <section v-else-if="step === 3" class="space-y-4">
        <div class="bg-white dark:bg-slate-800 rounded-lg p-6 space-y-4">
          <div>
            <label class="block text-sm font-medium text-slate-700 dark:text-slate-300">
              域名 (可选)
              <span class="text-xs text-slate-500 dark:text-slate-400 font-normal">
                — 提供域名将使用 ACME 签发真实证书；留空使用自签
              </span>
            </label>
            <input v-model="domain" type="text" placeholder="proxy.example.com"
                   class="mt-1 w-full px-3 py-2 rounded border border-slate-300 dark:border-slate-700 bg-white dark:bg-slate-900" />
          </div>
          <div v-if="domain">
            <label class="block text-sm font-medium text-slate-700 dark:text-slate-300">
              邮箱 (ACME 注册用)
            </label>
            <input v-model="email" type="email" placeholder="you@example.com"
                   class="mt-1 w-full px-3 py-2 rounded border border-slate-300 dark:border-slate-700 bg-white dark:bg-slate-900" />
          </div>
          <div v-if="selectedPreset === 'stable_egress' || selectedPreset === 'combo'">
            <label class="block text-sm font-medium text-slate-700 dark:text-slate-300">
              Reality 目标 SNI (可选)
              <span class="text-xs text-slate-500 dark:text-slate-400 font-normal">
                — 默认 www.microsoft.com
              </span>
            </label>
            <input v-model="realityTarget" type="text" placeholder="www.microsoft.com"
                   class="mt-1 w-full px-3 py-2 rounded border border-slate-300 dark:border-slate-700 bg-white dark:bg-slate-900" />
          </div>
          <div>
            <label class="block text-sm font-medium text-slate-700 dark:text-slate-300">
              端口覆盖 (可选)
              <span class="text-xs text-slate-500 dark:text-slate-400 font-normal">
                — 留空使用 443 或自动回退
              </span>
            </label>
            <input v-model.number="portOverride" type="number" placeholder="443"
                   class="mt-1 w-full px-3 py-2 rounded border border-slate-300 dark:border-slate-700 bg-white dark:bg-slate-900" />
          </div>
        </div>
        <div class="flex justify-between pt-2">
          <button class="px-4 py-2 text-sm rounded border border-slate-300 dark:border-slate-700"
                  @click="goto(2)">上一步</button>
          <button class="px-4 py-2 text-sm rounded bg-emerald-600 text-white hover:bg-emerald-700 disabled:opacity-50"
                  :disabled="previewLoading"
                  @click="generatePreview">
            {{ previewLoading ? '生成预览中……' : '生成预览' }}
          </button>
        </div>
        <p v-if="previewError" class="text-sm text-rose-600 dark:text-rose-400">{{ previewError }}</p>
      </section>

      <!-- Step 4: Preview -->
      <section v-else-if="step === 4 && plan" class="space-y-4">
        <div class="bg-white dark:bg-slate-800 rounded-lg p-6">
          <h3 class="font-medium text-slate-900 dark:text-slate-100 mb-3">将要执行的操作</h3>

          <div class="text-sm space-y-3">
            <div>
              <span class="text-slate-500 dark:text-slate-400">预设：</span>
              <span class="font-medium">{{ plan.preset_id }}</span>
            </div>
            <div>
              <span class="text-slate-500 dark:text-slate-400">证书模式：</span>
              <span class="font-mono">{{ plan.cert_mode }}</span>
            </div>
            <div>
              <span class="text-slate-500 dark:text-slate-400">入站 ({{ plan.inbounds.length }})：</span>
              <ul class="mt-1 space-y-1 ml-4">
                <li v-for="ib in plan.inbounds" :key="ib.tag" class="font-mono text-xs">
                  {{ ib.engine }} / {{ ib.protocol }} · :{{ ib.port }}/{{ ib.network || 'tcp' }} · {{ ib.tag }}
                </li>
              </ul>
            </div>
            <div>
              <span class="text-slate-500 dark:text-slate-400">调优操作 ({{ plan.tuning.length }})：</span>
              <ul class="mt-1 space-y-1 ml-4">
                <li v-for="t in plan.tuning" :key="t.op_name" class="font-mono text-xs">
                  {{ t.op_name }}<span v-if="t.params"> {{ JSON.stringify(t.params) }}</span>
                </li>
              </ul>
            </div>
            <div v-if="plan.firewall_allow_ports?.length">
              <span class="text-slate-500 dark:text-slate-400">防火墙放行端口：</span>
              <span class="font-mono text-xs">{{ plan.firewall_allow_ports.join(', ') }}</span>
            </div>
          </div>

          <div v-if="plan.notes?.length" class="mt-4 bg-amber-50 dark:bg-amber-900/20 border border-amber-200 dark:border-amber-800 rounded p-3">
            <h4 class="font-medium text-amber-700 dark:text-amber-300 text-sm">提示</h4>
            <ul class="mt-1 text-sm text-amber-700 dark:text-amber-300 list-disc list-inside">
              <li v-for="(n, i) in plan.notes" :key="i">{{ n }}</li>
            </ul>
          </div>
        </div>

        <div class="flex justify-between pt-2">
          <button class="px-4 py-2 text-sm rounded border border-slate-300 dark:border-slate-700"
                  @click="goto(3)">修改选项</button>
          <button class="px-4 py-2 text-sm rounded bg-emerald-600 text-white hover:bg-emerald-700 disabled:opacity-50"
                  :disabled="applyLoading"
                  @click="applyDeployment">
            {{ applyLoading ? '部署中……' : '确认并部署' }}
          </button>
        </div>
        <p v-if="applyError" class="text-sm text-rose-600 dark:text-rose-400">{{ applyError }}</p>
      </section>

      <!-- Step 5: Result -->
      <section v-else-if="step === 5 && deployment" class="space-y-4">
        <div v-if="deployment.status === 'succeeded'"
             class="bg-emerald-50 dark:bg-emerald-900/20 border border-emerald-200 dark:border-emerald-800 rounded-lg p-6">
          <h3 class="font-medium text-emerald-700 dark:text-emerald-300 text-lg">✓ 部署成功</h3>
          <p class="text-sm text-emerald-700 dark:text-emerald-300 mt-1">
            部署 ID：{{ deployment.id }}
          </p>
          <p v-if="clientsInfo" class="text-sm text-emerald-700 dark:text-emerald-300 mt-2">
            {{ clientsInfo.note }}
          </p>
        </div>
        <div v-else
             class="bg-rose-50 dark:bg-rose-900/20 border border-rose-200 dark:border-rose-800 rounded-lg p-6">
          <h3 class="font-medium text-rose-700 dark:text-rose-300 text-lg">
            × 部署失败 · 状态：{{ deployment.status }}
          </h3>
          <p class="text-sm text-rose-700 dark:text-rose-300 mt-2 font-mono break-all">
            {{ deployment.error || '未知错误' }}
          </p>
          <p class="text-xs text-rose-600 dark:text-rose-400 mt-2">
            失败时已自动回滚已应用的操作。
          </p>
        </div>

        <div class="flex justify-end gap-2 pt-2">
          <button v-if="deployment.status === 'succeeded'"
                  class="px-4 py-2 text-sm rounded border border-rose-300 dark:border-rose-700 text-rose-600 dark:text-rose-400"
                  @click="rollbackDeployment">回滚</button>
          <button class="px-4 py-2 text-sm rounded bg-emerald-600 text-white hover:bg-emerald-700"
                  @click="resetWizard">新建部署</button>
        </div>
      </section>
    </div>
  </div>
</template>

