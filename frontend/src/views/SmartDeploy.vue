<script setup lang="ts">
import { computed, h, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
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

const { t } = useI18n()

const stepKeys = ['probe', 'preset', 'options', 'preview', 'done']

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
  if (!confirm(t('smartDeploy.rollbackConfirm'))) return
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
  if (!probe.value.root_check.ok) out.push(t('smartDeploy.blockers.notRoot'))
  if (!probe.value.systemd.present) out.push(t('smartDeploy.blockers.noSystemd'))
  if (!probe.value.public_ip.v4) out.push(t('smartDeploy.blockers.noPublicIp'))
  return out
})

const warnings = computed(() => {
  if (!probe.value) return [] as string[]
  const out: string[] = []
  if (!probe.value.time_sync.synced) out.push(t('smartDeploy.warnings.timeNotSynced'))
  if (!probe.value.kernel.features.bbr) out.push(t('smartDeploy.warnings.noBbr'))
  if (probe.value.port_avail.ports[443] === false) out.push(t('smartDeploy.warnings.port443Occupied'))
  return out
})
</script>

<template>
  <div class="min-h-screen bg-slate-50 dark:bg-slate-900 p-6">
    <div class="max-w-4xl mx-auto">
      <header class="mb-8">
        <h1 class="text-2xl font-semibold text-slate-900 dark:text-slate-100">{{ t('smartDeploy.title') }}</h1>
        <p class="text-sm text-slate-600 dark:text-slate-400 mt-1">
          {{ t('smartDeploy.subtitle') }}
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
              {{ t('smartDeploy.steps.' + stepKeys[n - 1]) }}
            </span>
          </div>
          <div v-if="n < 5" class="flex-1 h-px bg-slate-200 dark:bg-slate-700"></div>
        </template>
      </div>

      <!-- Step 1: Probe -->
      <section v-if="step === 1" class="space-y-4">
        <div v-if="probeLoading" class="bg-white dark:bg-slate-800 rounded-lg p-6 text-center">
          <p class="text-slate-600 dark:text-slate-400">{{ t('smartDeploy.probing') }}</p>
        </div>
        <div v-else-if="probeError" class="bg-rose-50 dark:bg-rose-900/20 border border-rose-200 dark:border-rose-800 rounded-lg p-4">
          <p class="text-rose-700 dark:text-rose-300">{{ t('smartDeploy.probeFailed', { err: probeError }) }}</p>
          <button class="mt-2 text-sm underline" @click="runProbe">{{ t('smartDeploy.retry') }}</button>
        </div>
        <div v-else-if="probe">
          <div class="grid sm:grid-cols-2 lg:grid-cols-3 gap-3">
            <Chip tone="neutral" :label="t('smartDeploy.chips.kernel')" :value="probe.kernel.version || t('smartDeploy.values.unknown')" />
            <Chip :tone="probe.kernel.features.bbr ? 'good' : 'warn'"
                  :label="t('smartDeploy.chips.bbr')" :value="probe.kernel.features.bbr ? t('smartDeploy.values.available') : t('smartDeploy.values.unavailable')" />
            <Chip :tone="probe.root_check.ok ? 'good' : 'bad'"
                  :label="t('smartDeploy.chips.perms')" :value="probe.root_check.ok ? 'root' : `UID ${probe.root_check.uid}`" />
            <Chip tone="neutral" :label="t('smartDeploy.chips.distro')"
                  :value="`${probe.distro.pretty_name || probe.distro.id}`" />
            <Chip tone="neutral" :label="t('smartDeploy.chips.publicIp')" :value="probe.public_ip.v4 || t('smartDeploy.values.notDetected')" />
            <Chip tone="neutral" :label="t('smartDeploy.chips.cpuRam')"
                  :value="probe.hardware.cpu_cores + ' ' + t('smartDeploy.values.cores') + ' / ' + ramGB + ' GB'" />
            <Chip tone="neutral" :label="t('smartDeploy.chips.primaryNic')"
                  :value="probe.nic.primary ? `${probe.nic.primary} · ${probe.nic.link_speed_mbps} Mbps` : t('smartDeploy.values.unrecognized')" />
            <Chip :tone="probe.time_sync.synced ? 'good' : 'warn'"
                  :label="t('smartDeploy.chips.timeSync')" :value="probe.time_sync.service || 'none'" />
            <Chip tone="neutral" :label="t('smartDeploy.chips.firewall')" :value="probe.firewall.type" />
            <Chip :tone="probe.port_avail.ports[443] ? 'good' : 'warn'"
                  :label="t('smartDeploy.chips.port443')" :value="probe.port_avail.ports[443] ? t('smartDeploy.values.free') : t('smartDeploy.values.occupied')" />
            <Chip :tone="probe.port_avail.ports[8443] ? 'good' : 'warn'"
                  :label="t('smartDeploy.chips.port8443')" :value="probe.port_avail.ports[8443] ? t('smartDeploy.values.free') : t('smartDeploy.values.occupied')" />
            <Chip tone="neutral" :label="t('smartDeploy.chips.systemd')"
                  :value="probe.systemd.present ? (probe.systemd.version || 'present') : t('smartDeploy.values.missing')" />
          </div>

          <div v-if="blockers.length" class="mt-4 bg-rose-50 dark:bg-rose-900/20 border border-rose-200 dark:border-rose-800 rounded-lg p-4">
            <h3 class="font-medium text-rose-700 dark:text-rose-300">{{ t('smartDeploy.blockers.title') }}</h3>
            <ul class="mt-2 text-sm text-rose-700 dark:text-rose-300 list-disc list-inside">
              <li v-for="(b, i) in blockers" :key="i">{{ b }}</li>
            </ul>
          </div>
          <div v-if="warnings.length" class="mt-4 bg-amber-50 dark:bg-amber-900/20 border border-amber-200 dark:border-amber-800 rounded-lg p-4">
            <h3 class="font-medium text-amber-700 dark:text-amber-300">{{ t('smartDeploy.warnings.title') }}</h3>
            <ul class="mt-2 text-sm text-amber-700 dark:text-amber-300 list-disc list-inside">
              <li v-for="(w, i) in warnings" :key="i">{{ w }}</li>
            </ul>
          </div>

          <div class="mt-6 flex justify-end gap-2">
            <button class="px-4 py-2 text-sm rounded border border-slate-300 dark:border-slate-700"
                    @click="runProbe">{{ t('smartDeploy.reprobe') }}</button>
            <button class="px-4 py-2 text-sm rounded bg-emerald-600 text-white hover:bg-emerald-700 disabled:opacity-50"
                    :disabled="blockers.length > 0"
                    @click="goto(2)">{{ t('smartDeploy.nextPreset') }}</button>
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
                {{ t('smartDeploy.recommended') }}
              </span>
            </div>
            <p class="text-xs text-slate-500 dark:text-slate-400 mt-1 font-mono">{{ preset.description }}</p>
            <p class="text-sm text-slate-600 dark:text-slate-400 mt-2">{{ preset.useCase }}</p>
          </button>
        </div>
        <div class="flex justify-between pt-2">
          <button class="px-4 py-2 text-sm rounded border border-slate-300 dark:border-slate-700"
                  @click="goto(1)">{{ t('smartDeploy.prev') }}</button>
          <button class="px-4 py-2 text-sm rounded bg-emerald-600 text-white hover:bg-emerald-700"
                  @click="goto(3)">{{ t('smartDeploy.nextOptions') }}</button>
        </div>
      </section>

      <!-- Step 3: Options -->
      <section v-else-if="step === 3" class="space-y-4">
        <div class="bg-white dark:bg-slate-800 rounded-lg p-6 space-y-4">
          <div>
            <label class="block text-sm font-medium text-slate-700 dark:text-slate-300">
              {{ t('smartDeploy.options.domain') }}
              <span class="text-xs text-slate-500 dark:text-slate-400 font-normal">
                {{ t('smartDeploy.options.domainHint') }}
              </span>
            </label>
            <input v-model="domain" type="text" placeholder="proxy.example.com"
                   class="mt-1 w-full px-3 py-2 rounded border border-slate-300 dark:border-slate-700 bg-white dark:bg-slate-900" />
          </div>
          <div v-if="domain">
            <label class="block text-sm font-medium text-slate-700 dark:text-slate-300">
              {{ t('smartDeploy.options.email') }}
            </label>
            <input v-model="email" type="email" placeholder="you@example.com"
                   class="mt-1 w-full px-3 py-2 rounded border border-slate-300 dark:border-slate-700 bg-white dark:bg-slate-900" />
          </div>
          <div v-if="selectedPreset === 'stable_egress' || selectedPreset === 'combo'">
            <label class="block text-sm font-medium text-slate-700 dark:text-slate-300">
              {{ t('smartDeploy.options.realitySni') }}
              <span class="text-xs text-slate-500 dark:text-slate-400 font-normal">
                {{ t('smartDeploy.options.realitySniHint') }}
              </span>
            </label>
            <input v-model="realityTarget" type="text" placeholder="www.microsoft.com"
                   class="mt-1 w-full px-3 py-2 rounded border border-slate-300 dark:border-slate-700 bg-white dark:bg-slate-900" />
          </div>
          <div>
            <label class="block text-sm font-medium text-slate-700 dark:text-slate-300">
              {{ t('smartDeploy.options.portOverride') }}
              <span class="text-xs text-slate-500 dark:text-slate-400 font-normal">
                {{ t('smartDeploy.options.portOverrideHint') }}
              </span>
            </label>
            <input v-model.number="portOverride" type="number" placeholder="443"
                   class="mt-1 w-full px-3 py-2 rounded border border-slate-300 dark:border-slate-700 bg-white dark:bg-slate-900" />
          </div>
        </div>
        <div class="flex justify-between pt-2">
          <button class="px-4 py-2 text-sm rounded border border-slate-300 dark:border-slate-700"
                  @click="goto(2)">{{ t('smartDeploy.prev') }}</button>
          <button class="px-4 py-2 text-sm rounded bg-emerald-600 text-white hover:bg-emerald-700 disabled:opacity-50"
                  :disabled="previewLoading"
                  @click="generatePreview">
            {{ previewLoading ? t('smartDeploy.generating') : t('smartDeploy.generatePreview') }}
          </button>
        </div>
        <p v-if="previewError" class="text-sm text-rose-600 dark:text-rose-400">{{ previewError }}</p>
      </section>

      <!-- Step 4: Preview -->
      <section v-else-if="step === 4 && plan" class="space-y-4">
        <div class="bg-white dark:bg-slate-800 rounded-lg p-6">
          <h3 class="font-medium text-slate-900 dark:text-slate-100 mb-3">{{ t('smartDeploy.preview.title') }}</h3>

          <div class="text-sm space-y-3">
            <div>
              <span class="text-slate-500 dark:text-slate-400">{{ t('smartDeploy.preview.preset') }}</span>
              <span class="font-medium">{{ plan.preset_id }}</span>
            </div>
            <div>
              <span class="text-slate-500 dark:text-slate-400">{{ t('smartDeploy.preview.certMode') }}</span>
              <span class="font-mono">{{ plan.cert_mode }}</span>
            </div>
            <div>
              <span class="text-slate-500 dark:text-slate-400">{{ t('smartDeploy.preview.inbounds', { n: plan.inbounds.length }) }}</span>
              <ul class="mt-1 space-y-1 ml-4">
                <li v-for="ib in plan.inbounds" :key="ib.tag" class="font-mono text-xs">
                  {{ ib.engine }} / {{ ib.protocol }} · :{{ ib.port }}/{{ ib.network || 'tcp' }} · {{ ib.tag }}
                </li>
              </ul>
            </div>
            <div>
              <span class="text-slate-500 dark:text-slate-400">{{ t('smartDeploy.preview.tuning', { n: plan.tuning.length }) }}</span>
              <ul class="mt-1 space-y-1 ml-4">
                <li v-for="t in plan.tuning" :key="t.op_name" class="font-mono text-xs">
                  {{ t.op_name }}<span v-if="t.params"> {{ JSON.stringify(t.params) }}</span>
                </li>
              </ul>
            </div>
            <div v-if="plan.firewall_allow_ports?.length">
              <span class="text-slate-500 dark:text-slate-400">{{ t('smartDeploy.preview.firewallPorts') }}</span>
              <span class="font-mono text-xs">{{ plan.firewall_allow_ports.join(', ') }}</span>
            </div>
          </div>

          <div v-if="plan.notes?.length" class="mt-4 bg-amber-50 dark:bg-amber-900/20 border border-amber-200 dark:border-amber-800 rounded p-3">
            <h4 class="font-medium text-amber-700 dark:text-amber-300 text-sm">{{ t('smartDeploy.preview.notes') }}</h4>
            <ul class="mt-1 text-sm text-amber-700 dark:text-amber-300 list-disc list-inside">
              <li v-for="(n, i) in plan.notes" :key="i">{{ n }}</li>
            </ul>
          </div>
        </div>

        <div class="flex justify-between pt-2">
          <button class="px-4 py-2 text-sm rounded border border-slate-300 dark:border-slate-700"
                  @click="goto(3)">{{ t('smartDeploy.preview.modifyOptions') }}</button>
          <button class="px-4 py-2 text-sm rounded bg-emerald-600 text-white hover:bg-emerald-700 disabled:opacity-50"
                  :disabled="applyLoading"
                  @click="applyDeployment">
            {{ applyLoading ? t('smartDeploy.deploying') : t('smartDeploy.confirmDeploy') }}
          </button>
        </div>
        <p v-if="applyError" class="text-sm text-rose-600 dark:text-rose-400">{{ applyError }}</p>
      </section>

      <!-- Step 5: Result -->
      <section v-else-if="step === 5 && deployment" class="space-y-4">
        <div v-if="deployment.status === 'succeeded'"
             class="bg-emerald-50 dark:bg-emerald-900/20 border border-emerald-200 dark:border-emerald-800 rounded-lg p-6">
          <h3 class="font-medium text-emerald-700 dark:text-emerald-300 text-lg">{{ t('smartDeploy.result.success') }}</h3>
          <p class="text-sm text-emerald-700 dark:text-emerald-300 mt-1">
            {{ t('smartDeploy.result.deployId', { id: deployment.id }) }}
          </p>
          <p v-if="clientsInfo" class="text-sm text-emerald-700 dark:text-emerald-300 mt-2">
            {{ clientsInfo.note }}
          </p>
        </div>
        <div v-else
             class="bg-rose-50 dark:bg-rose-900/20 border border-rose-200 dark:border-rose-800 rounded-lg p-6">
          <h3 class="font-medium text-rose-700 dark:text-rose-300 text-lg">
            {{ t('smartDeploy.result.failPrefix', { status: deployment.status }) }}
          </h3>
          <p class="text-sm text-rose-700 dark:text-rose-300 mt-2 font-mono break-all">
            {{ deployment.error || t('smartDeploy.result.unknownError') }}
          </p>
          <p class="text-xs text-rose-600 dark:text-rose-400 mt-2">
            {{ t('smartDeploy.result.autoRolledBack') }}
          </p>
        </div>

        <div class="flex justify-end gap-2 pt-2">
          <button v-if="deployment.status === 'succeeded'"
                  class="px-4 py-2 text-sm rounded border border-rose-300 dark:border-rose-700 text-rose-600 dark:text-rose-400"
                  @click="rollbackDeployment">{{ t('smartDeploy.rollback') }}</button>
          <button class="px-4 py-2 text-sm rounded bg-emerald-600 text-white hover:bg-emerald-700"
                  @click="resetWizard">{{ t('smartDeploy.newDeploy') }}</button>
        </div>
      </section>
    </div>
  </div>
</template>

