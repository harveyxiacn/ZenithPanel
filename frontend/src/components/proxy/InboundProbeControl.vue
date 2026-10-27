<script setup lang="ts">
import type { InboundProbeResult } from '@/api/proxy'

defineProps<{
  result?: InboundProbeResult | 'pending'
}>()

defineEmits<{
  probe: []
}>()
</script>

<template>
  <span
    v-if="result === 'pending'"
    class="inline-flex items-center px-2 py-0.5 rounded-full text-xs font-medium bg-slate-100 text-slate-600"
  >…</span>
  <button
    v-else-if="result?.ok"
    @click="$emit('probe')"
    class="inline-flex items-center px-2 py-0.5 rounded-full text-xs font-medium transition bg-emerald-100 text-emerald-700 hover:bg-emerald-200"
    :title="`OK · ${result.elapsed_ms}ms · ${$t('proxy.inbounds.probeRecheck')}`"
  >✓ {{ result.elapsed_ms }}ms</button>
  <button
    v-else-if="result"
    @click="$emit('probe')"
    class="inline-flex items-center px-2 py-0.5 rounded-full text-xs font-medium transition bg-rose-100 text-rose-700 hover:bg-rose-200"
    :title="`${result.stage}: ${result.err || ''}`"
  >✗ {{ result.stage }}</button>
  <button
    v-else
    @click="$emit('probe')"
    class="text-slate-500 hover:text-slate-700 inline-flex items-center"
    :title="$t('proxy.inbounds.probeTooltip')"
  >{{ $t('proxy.inbounds.probe') }}</button>
</template>
