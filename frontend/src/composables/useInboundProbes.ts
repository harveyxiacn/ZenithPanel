import { onBeforeUnmount, ref, type Ref } from 'vue'
import { probeInbound, type InboundProbeResult } from '@/api/proxy'
import type { Inbound } from '@/types/proxy'

const requestFailure = (id: number, message: string): InboundProbeResult => ({
  inbound_id: id,
  tag: '',
  protocol: '',
  transport: '',
  port: 0,
  ok: false,
  stage: 'request',
  elapsed_ms: 0,
  err: message,
})

export function useInboundProbes(inbounds: Ref<Inbound[]>, activeTab: Ref<string>) {
  const probeResults = ref<Record<number, InboundProbeResult | 'pending'>>({})
  const probeAllBusy = ref(false)
  const probeAutoEnabled = ref(false)
  let probeAutoTimer: ReturnType<typeof setInterval> | null = null

  async function onProbeInbound(id: number) {
    probeResults.value = { ...probeResults.value, [id]: 'pending' }
    try {
      const response = await probeInbound(id)
      probeResults.value = {
        ...probeResults.value,
        [id]: response.code === 200 && response.data
          ? response.data
          : requestFailure(id, response.msg || 'unexpected response'),
      }
    } catch (error: unknown) {
      const message = error instanceof Error ? error.message : 'network'
      probeResults.value = { ...probeResults.value, [id]: requestFailure(id, message) }
    }
  }

  async function onProbeAllInbounds() {
    if (probeAllBusy.value || inbounds.value.length === 0) return
    probeAllBusy.value = true
    probeResults.value = {
      ...probeResults.value,
      ...Object.fromEntries(inbounds.value.map((inbound) => [inbound.id, 'pending' as const])),
    }

    const queue = [...inbounds.value]
    const worker = async () => {
      for (;;) {
        const inbound = queue.shift()
        if (!inbound) return
        await onProbeInbound(inbound.id)
      }
    }

    try {
      await Promise.all(Array.from({ length: Math.min(4, queue.length) }, worker))
    } finally {
      probeAllBusy.value = false
    }
  }

  function setAutoProbe(enabled: boolean) {
    probeAutoEnabled.value = enabled
    if (probeAutoTimer) clearInterval(probeAutoTimer)
    probeAutoTimer = null
    if (!enabled) return

    void onProbeAllInbounds()
    probeAutoTimer = setInterval(() => {
      if (activeTab.value === 'inbounds') void onProbeAllInbounds()
    }, 30_000)
  }

  function clearProbeResults() {
    probeResults.value = {}
  }

  onBeforeUnmount(() => {
    if (probeAutoTimer) clearInterval(probeAutoTimer)
  })

  return {
    probeResults,
    probeAllBusy,
    probeAutoEnabled,
    onProbeInbound,
    onProbeAllInbounds,
    setAutoProbe,
    clearProbeResults,
  }
}

