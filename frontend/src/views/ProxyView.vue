<script setup lang="ts">
import { ref, computed, onMounted, watch } from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { PlusIcon, TrashIcon, ArrowPathIcon, XMarkIcon, ClipboardDocumentIcon, SparklesIcon, CheckCircleIcon, ChevronDownIcon, ChevronRightIcon, QrCodeIcon, KeyIcon, CodeBracketIcon, AdjustmentsHorizontalIcon } from '@heroicons/vue/24/outline'
import { listInbounds, createInbound, updateInbound, deleteInbound, listClients, createClient, deleteClient, listRoutingRules, createRoutingRule, deleteRoutingRule, generateRealityKeys } from '@/api/proxy'
import apiClient from '@/api/client'
import QRCode from 'qrcode'

const { t } = useI18n()
const route = useRoute()
const tabFromRoute = route.name === 'Users' ? 'users' : 'inbounds'
const activeTab = ref(tabFromRoute)

watch(() => route.name, (name) => {
  if (name === 'Users') activeTab.value = 'users'
  else if (name === 'ProxyNodes') activeTab.value = 'inbounds'
})
const tabs = computed(() => [
  { id: 'inbounds', name: t('proxy.tabs.inbounds') },
  { id: 'routing', name: t('proxy.tabs.routing') },
  { id: 'users', name: t('proxy.tabs.users') },
])

// ---- Inbounds ----
const inbounds = ref<any[]>([])
const inboundsLoading = ref(false)
const showInboundForm = ref(false)
const editingInbound = ref<any>(null)
const inboundForm = ref({ tag: '', protocol: 'vless', listen: '0.0.0.0', port: 443, settings: '{}', stream: '{}' })
const editMode = ref<'visual' | 'json'>('visual')

// Visual form fields extracted from settings/stream JSON
const vf = ref({
  // Settings
  flow: 'xtls-rprx-vision',
  decryption: 'none',
  ssMethod: '2022-blake3-aes-128-gcm',
  ssPassword: '',
  // Stream
  network: 'tcp',
  security: 'none',
  // TLS
  sni: '',
  fingerprint: 'chrome',
  alpn: 'h2,http/1.1',
  certFile: '/opt/zenithpanel/data/certs/fullchain.pem',
  keyFile: '/opt/zenithpanel/data/certs/privkey.pem',
  // Reality
  realityDest: 'www.microsoft.com:443',
  realityServerNames: 'www.microsoft.com',
  realityPrivateKey: '',
  realityPublicKey: '',
  realityShortId: '',
  // WebSocket
  wsPath: '',
  wsHost: '',
  // gRPC
  grpcServiceName: '',
})

function resetVisualForm() {
  vf.value = {
    flow: 'xtls-rprx-vision', decryption: 'none',
    ssMethod: '2022-blake3-aes-128-gcm', ssPassword: '',
    network: 'tcp', security: 'none',
    sni: '', fingerprint: 'chrome', alpn: 'h2,http/1.1',
    certFile: '/opt/zenithpanel/data/certs/fullchain.pem',
    keyFile: '/opt/zenithpanel/data/certs/privkey.pem',
    realityDest: 'www.microsoft.com:443', realityServerNames: 'www.microsoft.com',
    realityPrivateKey: '', realityPublicKey: '', realityShortId: '',
    wsPath: '', wsHost: '', grpcServiceName: '',
  }
}

function parseJsonToVisual(settingsStr: string, streamStr: string) {
  resetVisualForm()
  try {
    const s = JSON.parse(settingsStr || '{}')
    if (s.flow) vf.value.flow = s.flow
    if (s.decryption) vf.value.decryption = s.decryption
    if (s.method) vf.value.ssMethod = s.method
    if (s.password) vf.value.ssPassword = s.password
  } catch { /* keep defaults */ }
  try {
    const st = JSON.parse(streamStr || '{}')
    if (st.network) vf.value.network = st.network
    if (st.security) vf.value.security = st.security
    if (st.tlsSettings) {
      const tls = st.tlsSettings
      if (tls.serverName) vf.value.sni = tls.serverName
      if (tls.fingerprint) vf.value.fingerprint = tls.fingerprint
      if (tls.alpn) vf.value.alpn = Array.isArray(tls.alpn) ? tls.alpn.join(',') : tls.alpn
      if (tls.certificates?.[0]) {
        if (tls.certificates[0].certificateFile) vf.value.certFile = tls.certificates[0].certificateFile
        if (tls.certificates[0].keyFile) vf.value.keyFile = tls.certificates[0].keyFile
      }
    }
    if (st.realitySettings) {
      const r = st.realitySettings
      if (r.dest) vf.value.realityDest = r.dest
      if (r.serverNames) vf.value.realityServerNames = Array.isArray(r.serverNames) ? r.serverNames.join(',') : r.serverNames
      if (r.privateKey) vf.value.realityPrivateKey = r.privateKey
      if (r.publicKey) vf.value.realityPublicKey = r.publicKey
      if (r.shortIds?.[0]) vf.value.realityShortId = r.shortIds[0]
      if (r.fingerprint) vf.value.fingerprint = r.fingerprint
    }
    if (st.wsSettings) {
      if (st.wsSettings.path) vf.value.wsPath = st.wsSettings.path
      if (st.wsSettings.headers?.Host) vf.value.wsHost = st.wsSettings.headers.Host
    }
    if (st.grpcSettings) {
      if (st.grpcSettings.serviceName) vf.value.grpcServiceName = st.grpcSettings.serviceName
    }
  } catch { /* keep defaults */ }
}

function buildVisualToJson(protocol: string): { settings: string, stream: string } {
  const v = vf.value
  let settings: any = {}
  let stream: any = { network: v.network, security: v.security }

  // Build settings
  if (protocol === 'vless') {
    settings = { decryption: v.decryption || 'none' }
    if (v.flow && v.security === 'reality') settings.flow = v.flow
  } else if (protocol === 'vmess') {
    settings = {}
  } else if (protocol === 'trojan') {
    settings = {}
  } else if (protocol === 'shadowsocks') {
    settings = { method: v.ssMethod, password: v.ssPassword }
  } else if (protocol === 'hysteria2') {
    settings = {}
  }

  // Build stream TLS settings
  if (v.security === 'tls') {
    stream.tlsSettings = {
      serverName: v.sni,
      certificates: [{ certificateFile: v.certFile, keyFile: v.keyFile }],
    }
    if (v.alpn) stream.tlsSettings.alpn = v.alpn.split(',').map((a: string) => a.trim()).filter(Boolean)
    if (v.fingerprint) stream.tlsSettings.fingerprint = v.fingerprint
  } else if (v.security === 'reality') {
    stream.realitySettings = {
      dest: v.realityDest,
      serverNames: v.realityServerNames.split(',').map((s: string) => s.trim()).filter(Boolean),
      privateKey: v.realityPrivateKey,
      publicKey: v.realityPublicKey,
      shortIds: [v.realityShortId],
      fingerprint: v.fingerprint || 'chrome',
    }
  }

  // Build transport
  if (v.network === 'ws') {
    stream.wsSettings = { path: v.wsPath || '/' }
    if (v.wsHost) stream.wsSettings.headers = { Host: v.wsHost }
  } else if (v.network === 'grpc') {
    stream.grpcSettings = { serviceName: v.grpcServiceName }
  }

  return { settings: JSON.stringify(settings), stream: JSON.stringify(stream) }
}

const regenLoading = ref(false)
async function regenRealityKeys() {
  regenLoading.value = true
  try {
    const res = await generateRealityKeys() as any
    if (res.code === 200 && res.data) {
      vf.value.realityPrivateKey = res.data.private_key
      vf.value.realityPublicKey = res.data.public_key
      vf.value.realityShortId = res.data.short_id
    }
  } catch { /* ignore */ }
  regenLoading.value = false
}

async function fetchInbounds() {
  inboundsLoading.value = true
  try {
    const res = await listInbounds() as any
    if (res.code === 200) inbounds.value = res.data || []
  } catch { /* ignore */ }
  inboundsLoading.value = false
}

function openInboundForm(inbound?: any) {
  editMode.value = 'visual'
  if (inbound) {
    editingInbound.value = inbound
    const settingsStr = typeof inbound.settings === 'string' ? inbound.settings : JSON.stringify(inbound.settings || {})
    const streamStr = typeof inbound.stream === 'string' ? inbound.stream : JSON.stringify(inbound.stream || {})
    inboundForm.value = {
      tag: inbound.tag || '',
      protocol: inbound.protocol || 'vless',
      listen: inbound.listen || '0.0.0.0',
      port: inbound.port || 443,
      settings: JSON.stringify(JSON.parse(settingsStr || '{}'), null, 2),
      stream: JSON.stringify(JSON.parse(streamStr || '{}'), null, 2),
    }
    parseJsonToVisual(settingsStr, streamStr)
  } else {
    editingInbound.value = null
    inboundForm.value = { tag: '', protocol: 'vless', listen: '0.0.0.0', port: 443, settings: '{}', stream: '{}' }
    resetVisualForm()
  }
  showInboundForm.value = true
}

function syncVisualToJson() {
  const { settings, stream } = buildVisualToJson(inboundForm.value.protocol)
  inboundForm.value.settings = JSON.stringify(JSON.parse(settings), null, 2)
  inboundForm.value.stream = JSON.stringify(JSON.parse(stream), null, 2)
}

function syncJsonToVisual() {
  parseJsonToVisual(inboundForm.value.settings, inboundForm.value.stream)
}

async function saveInbound() {
  try {
    let data = { ...inboundForm.value }
    if (editMode.value === 'visual') {
      const built = buildVisualToJson(data.protocol)
      data.settings = built.settings
      data.stream = built.stream
    }
    if (editingInbound.value) {
      await updateInbound(editingInbound.value.id, data)
    } else {
      await createInbound(data)
    }
    showInboundForm.value = false
    await fetchInbounds()
  } catch { /* ignore */ }
}

async function removeInbound(id: number) {
  if (!confirm(t('proxy.inbounds.confirmDelete'))) return
  try {
    await deleteInbound(id)
    await fetchInbounds()
  } catch { /* ignore */ }
}

// ---- Clients ----
const clients = ref<any[]>([])
const clientsLoading = ref(false)
const showClientForm = ref(false)
const clientForm = ref({ email: '', inbound_id: 0, traffic_limit: 0, enable: true })
const copiedUuid = ref('')

async function fetchClients() {
  clientsLoading.value = true
  try {
    const res = await listClients() as any
    if (res.code === 200) clients.value = res.data || []
  } catch { /* ignore */ }
  clientsLoading.value = false
}

async function saveClient() {
  try {
    await createClient(clientForm.value)
    showClientForm.value = false
    clientForm.value = { email: '', inbound_id: 0, traffic_limit: 0, enable: true }
    await fetchClients()
  } catch { /* ignore */ }
}

async function removeClient(id: number) {
  if (!confirm(t('proxy.clients.confirmDelete'))) return
  try {
    await deleteClient(id)
    await fetchClients()
  } catch { /* ignore */ }
}

function copySubLink(uuid: string) {
  const link = `${location.origin}/api/v1/sub/${uuid}`
  navigator.clipboard.writeText(link)
  copiedUuid.value = uuid
  setTimeout(() => { copiedUuid.value = '' }, 2000)
}

// ---- QR Code ----
const showQrModal = ref(false)
const qrUuid = ref('')
const qrEmail = ref('')
const qrDataUrl = ref('')
const qrFormat = ref<'v2ray' | 'clash'>('v2ray')

async function openQrCode(uuid: string, email: string) {
  qrUuid.value = uuid
  qrEmail.value = email
  qrFormat.value = 'v2ray'
  showQrModal.value = true
  await generateQr()
}

async function generateQr() {
  try {
    if (qrFormat.value === 'v2ray') {
      // Fetch actual proxy links so V2RayNG can scan directly (no subscription fetch needed)
      const res: any = await apiClient.get(`/v1/sub/${qrUuid.value}`, { params: { format: 'base64' } })
      const raw = typeof res === 'string' ? res : String(res)
      const decoded = atob(raw.trim())
      const firstLink = decoded.split('\n').filter((l: string) => l.trim())[0] || ''
      qrDataUrl.value = await QRCode.toDataURL(firstLink, { width: 280, margin: 2, color: { dark: '#1e293b', light: '#ffffff' } })
    } else {
      // Clash: encode subscription URL (Clash clients fetch it natively)
      const link = `${location.origin}/api/v1/sub/${qrUuid.value}?format=clash`
      qrDataUrl.value = await QRCode.toDataURL(link, { width: 280, margin: 2, color: { dark: '#1e293b', light: '#ffffff' } })
    }
  } catch {
    qrDataUrl.value = ''
  }
}

function downloadQr() {
  if (!qrDataUrl.value) return
  const a = document.createElement('a')
  a.href = qrDataUrl.value
  a.download = `${qrEmail.value || 'sub'}-${qrFormat.value}.png`
  a.click()
}

// ---- Routing Rules ----
const routingRules = ref<any[]>([])
const routingLoading = ref(false)
const showRoutingForm = ref(false)
const routingForm = ref({ name: '', domain_keyword: '', domain_suffix: '', geosite: '', geoip: '', outbound_tag: '', priority: 0 })

async function fetchRoutingRules() {
  routingLoading.value = true
  try {
    const res = await listRoutingRules() as any
    if (res.code === 200) routingRules.value = res.data || []
  } catch { /* ignore */ }
  routingLoading.value = false
}

async function saveRoutingRule() {
  try {
    await createRoutingRule(routingForm.value)
    showRoutingForm.value = false
    routingForm.value = { name: '', domain_keyword: '', domain_suffix: '', geosite: '', geoip: '', outbound_tag: '', priority: 0 }
    await fetchRoutingRules()
  } catch { /* ignore */ }
}

async function removeRoutingRule(id: number) {
  if (!confirm(t('proxy.routing.confirmDelete'))) return
  try {
    await deleteRoutingRule(id)
    await fetchRoutingRules()
  } catch { /* ignore */ }
}

// ---- Routing Presets ----
const routingPresets = [
  { id: 'block-ads', rule_tag: 'Block Ads', domain: 'geosite:category-ads-all', ip: '', outbound_tag: 'block' },
  { id: 'block-private', rule_tag: 'Block Private IP', domain: '', ip: 'geoip:private', outbound_tag: 'block' },
  { id: 'cn-direct', rule_tag: 'China Direct', domain: 'geosite:cn', ip: 'geoip:cn', outbound_tag: 'direct' },
  { id: 'block-quic', rule_tag: 'Block QUIC', domain: '', ip: '', port: '443', outbound_tag: 'block' },
  { id: 'ir-direct', rule_tag: 'Iran Direct', domain: 'geosite:category-ir', ip: 'geoip:ir', outbound_tag: 'direct' },
  { id: 'ru-direct', rule_tag: 'Russia Direct', domain: 'geosite:category-ru', ip: 'geoip:ru', outbound_tag: 'direct' },
]

const addingPresets = ref(false)
async function addRoutingPreset(preset: typeof routingPresets[0]) {
  addingPresets.value = true
  try {
    await createRoutingRule({ rule_tag: preset.rule_tag, domain: preset.domain, ip: preset.ip, port: preset.port || '', outbound_tag: preset.outbound_tag, enable: true })
    await fetchRoutingRules()
  } catch { /* ignore */ }
  addingPresets.value = false
}

async function addRecommendedRules() {
  addingPresets.value = true
  const recommended = routingPresets.filter(p => ['block-ads', 'block-private', 'cn-direct'].includes(p.id))
  for (const preset of recommended) {
    try {
      await createRoutingRule({ rule_tag: preset.rule_tag, domain: preset.domain, ip: preset.ip, outbound_tag: preset.outbound_tag, enable: true })
    } catch { /* ignore */ }
  }
  await fetchRoutingRules()
  addingPresets.value = false
}

// ---- Helpers ----
function parseTransport(stream: string): string {
  if (!stream || stream === '{}') return 'tcp'
  try {
    const s = JSON.parse(stream)
    const net = s.network || 'tcp'
    const sec = s.security || 'none'
    return sec !== 'none' ? `${net}+${sec}` : net
  } catch { return 'tcp' }
}

// ---- Quick Setup Wizard ----
const showQuickSetup = ref(false)
const quickSetupStep = ref(1)
const selectedPresetIds = ref<string[]>([])
const presetConfigs = ref<Record<string, any>>({})
const quickSetupCreating = ref(false)
const quickSetupResults = ref<{tag: string, success: boolean, error?: string}[]>([])
const addDefaultRouting = ref(true)
const addDefaultClient = ref(true)
const defaultClientEmail = ref('user1')
const expandedPreset = ref<string | null>(null)

const presets = [
  {
    id: 'vless-reality',
    protocol: 'vless',
    badgeKey: 'recommended',
    badgeColor: 'bg-emerald-100 text-emerald-700',
    defaultPort: 443,
    needsRealityKeys: true,
    needsDomain: false,
    needsCert: false,
  },
  {
    id: 'vless-ws-tls',
    protocol: 'vless',
    badgeKey: 'cdnFriendly',
    badgeColor: 'bg-blue-100 text-blue-700',
    defaultPort: 2083,
    needsRealityKeys: false,
    needsDomain: true,
    needsCert: true,
  },
  {
    id: 'vmess-ws-tls',
    protocol: 'vmess',
    badgeKey: 'wideSupport',
    badgeColor: 'bg-violet-100 text-violet-700',
    defaultPort: 2087,
    needsRealityKeys: false,
    needsDomain: true,
    needsCert: true,
  },
  {
    id: 'trojan-tls',
    protocol: 'trojan',
    badgeKey: 'simpleFast',
    badgeColor: 'bg-amber-100 text-amber-700',
    defaultPort: 2096,
    needsRealityKeys: false,
    needsDomain: true,
    needsCert: true,
  },
  {
    id: 'hysteria2',
    protocol: 'hysteria2',
    badgeKey: 'ultraFast',
    badgeColor: 'bg-rose-100 text-rose-700',
    defaultPort: 8443,
    needsRealityKeys: false,
    needsDomain: true,
    needsCert: true,
  },
  {
    id: 'shadowsocks',
    protocol: 'shadowsocks',
    badgeKey: 'lightweight',
    badgeColor: 'bg-slate-200 text-slate-700',
    defaultPort: 8388,
    needsRealityKeys: false,
    needsDomain: false,
    needsCert: false,
  },
]

function randomHex(bytes: number): string {
  const arr = new Uint8Array(bytes)
  crypto.getRandomValues(arr)
  return Array.from(arr).map(b => b.toString(16).padStart(2, '0')).join('')
}

function randomBase64(bytes: number): string {
  const arr = new Uint8Array(bytes)
  crypto.getRandomValues(arr)
  return btoa(String.fromCharCode(...arr))
}

function openQuickSetup() {
  showQuickSetup.value = true
  quickSetupStep.value = 1
  selectedPresetIds.value = []
  presetConfigs.value = {}
  quickSetupResults.value = []
  quickSetupCreating.value = false
}

function togglePreset(id: string) {
  const idx = selectedPresetIds.value.indexOf(id)
  if (idx >= 0) selectedPresetIds.value.splice(idx, 1)
  else selectedPresetIds.value.push(id)
}

async function useRecommended() {
  selectedPresetIds.value = ['vless-reality']
  await proceedToReview()
}

async function proceedToReview() {
  if (selectedPresetIds.value.length === 0) return
  for (const id of selectedPresetIds.value) {
    const preset = presets.find(p => p.id === id)!
    const cfg: any = { tag: id, port: preset.defaultPort }

    if (preset.needsRealityKeys) {
      try {
        const res = await generateRealityKeys() as any
        if (res.code === 200) {
          cfg.privateKey = res.data.private_key
          cfg.publicKey = res.data.public_key
          cfg.shortId = res.data.short_id
        }
      } catch {
        cfg.privateKey = ''
        cfg.publicKey = ''
        cfg.shortId = randomHex(4)
      }
      cfg.dest = 'www.microsoft.com:443'
      cfg.serverNames = 'www.microsoft.com'
      cfg.flow = 'xtls-rprx-vision'
      cfg.fingerprint = 'chrome'
    }
    if (preset.needsDomain) {
      cfg.domain = ''
    }
    if (preset.needsCert) {
      cfg.certFile = '/opt/zenithpanel/certs/fullchain.pem'
      cfg.keyFile = '/opt/zenithpanel/certs/privkey.pem'
    }
    if (id === 'vless-ws-tls' || id === 'vmess-ws-tls') {
      cfg.wsPath = '/' + randomHex(6)
    }
    if (id === 'shadowsocks') {
      cfg.method = '2022-blake3-aes-128-gcm'
      cfg.password = randomBase64(16)
    }
    presetConfigs.value[id] = cfg
  }
  expandedPreset.value = selectedPresetIds.value[0] ?? null
  quickSetupStep.value = 2
}

function buildPayload(presetId: string) {
  const preset = presets.find(p => p.id === presetId)!
  const cfg = presetConfigs.value[presetId]
  let settings: any = {}
  let stream: any = {}

  switch (presetId) {
    case 'vless-reality':
      settings = { decryption: 'none', flow: cfg.flow }
      stream = {
        network: 'tcp', security: 'reality',
        realitySettings: {
          dest: cfg.dest,
          serverNames: cfg.serverNames.split(',').map((s: string) => s.trim()),
          privateKey: cfg.privateKey,
          publicKey: cfg.publicKey,
          shortIds: [cfg.shortId],
          fingerprint: cfg.fingerprint || 'chrome',
        },
      }
      break
    case 'vless-ws-tls':
      settings = { decryption: 'none' }
      stream = {
        network: 'ws', security: 'tls',
        wsSettings: { path: cfg.wsPath },
        tlsSettings: { serverName: cfg.domain, certificates: [{ certificateFile: cfg.certFile, keyFile: cfg.keyFile }] },
      }
      break
    case 'vmess-ws-tls':
      settings = {}
      stream = {
        network: 'ws', security: 'tls',
        wsSettings: { path: cfg.wsPath },
        tlsSettings: { serverName: cfg.domain, certificates: [{ certificateFile: cfg.certFile, keyFile: cfg.keyFile }] },
      }
      break
    case 'trojan-tls':
      settings = {}
      stream = {
        network: 'tcp', security: 'tls',
        tlsSettings: { serverName: cfg.domain, certificates: [{ certificateFile: cfg.certFile, keyFile: cfg.keyFile }] },
      }
      break
    case 'hysteria2':
      settings = {}
      stream = {
        network: 'tcp', security: 'tls',
        tlsSettings: { serverName: cfg.domain, certificates: [{ certificateFile: cfg.certFile, keyFile: cfg.keyFile }] },
      }
      break
    case 'shadowsocks':
      settings = { method: cfg.method, password: cfg.password }
      stream = { network: 'tcp', security: 'none' }
      break
  }

  return {
    tag: cfg.tag,
    protocol: preset.protocol,
    port: cfg.port,
    listen: '0.0.0.0',
    settings: JSON.stringify(settings),
    stream: JSON.stringify(stream),
    enable: true,
  }
}

async function executeQuickSetup() {
  quickSetupCreating.value = true
  quickSetupResults.value = []

  for (const id of selectedPresetIds.value) {
    const cfg = presetConfigs.value[id]
    try {
      const payload = buildPayload(id)
      await createInbound(payload)
      quickSetupResults.value.push({ tag: cfg.tag, success: true })
    } catch (e: any) {
      quickSetupResults.value.push({ tag: cfg.tag, success: false, error: e?.message || t('proxy.quickSetup.failed') })
    }
  }

  if (addDefaultRouting.value) {
    try { await createRoutingRule({ rule_tag: 'Block Ads', domain: 'geosite:category-ads-all', outbound_tag: 'block', enable: true }) } catch {}
    try { await createRoutingRule({ rule_tag: 'Block Private IP', ip: 'geoip:private', outbound_tag: 'block', enable: true }) } catch {}
  }

  if (addDefaultClient.value && selectedPresetIds.value.length > 0) {
    await fetchInbounds()
    if (inbounds.value.length > 0) {
      try { await createClient({ email: defaultClientEmail.value, inbound_id: inbounds.value[0].id, enable: true }) } catch {}
    }
  }

  quickSetupCreating.value = false
  quickSetupStep.value = 3
  fetchInbounds()
  fetchClients()
  fetchRoutingRules()
}

// ---- Lifecycle ----
onMounted(() => {
  fetchInbounds()
  fetchClients()
  fetchRoutingRules()
})
</script>

<template>
  <div class="py-2">
    <!-- Header -->
    <div class="mb-8 flex items-center justify-between">
      <div>
        <h1 class="text-3xl font-bold text-slate-800 tracking-tight">{{ $t('proxy.title') }}</h1>
        <p class="text-slate-500 mt-1">{{ $t('proxy.subtitle') }}</p>
      </div>
      <div>
        <button class="bg-primary-600 hover:bg-primary-700 text-white px-5 py-2.5 rounded-xl text-sm font-medium transition-colors shadow-sm flex items-center">
          <ArrowPathIcon class="h-5 w-5 mr-2" />
          {{ $t('proxy.applyConfig') }}
        </button>
      </div>
    </div>

    <!-- Tab Navigation -->
    <div class="border-b border-slate-200 mb-6">
      <nav class="-mb-px flex space-x-8">
        <button
          v-for="tab in tabs"
          :key="tab.id"
          @click="activeTab = tab.id"
          :class="[
            activeTab === tab.id
              ? 'border-primary-500 text-primary-600'
              : 'border-transparent text-slate-500 hover:border-slate-300 hover:text-slate-700',
            'whitespace-nowrap pb-4 px-1 border-b-2 font-medium text-sm transition-colors duration-200'
          ]"
        >
          {{ tab.name }}
        </button>
      </nav>
    </div>

    <!-- Tab Contents -->
    <div class="bg-white rounded-2xl shadow-sm border border-slate-100 min-h-[500px] overflow-hidden">

      <!-- Inbounds -->
      <div v-if="activeTab === 'inbounds'" class="flex flex-col h-full">
        <div class="p-6 border-b border-slate-100 flex justify-between items-center">
          <h3 class="text-lg font-medium text-slate-800">{{ $t('proxy.inbounds.title') }}</h3>
          <div class="flex gap-2">
            <button @click="openQuickSetup()" class="text-sm bg-primary-600 hover:bg-primary-700 text-white px-4 py-2 rounded-lg font-medium transition flex items-center">
              <SparklesIcon class="h-4 w-4 mr-1" /> {{ $t('proxy.inbounds.quickSetup') }}
            </button>
            <button @click="openInboundForm()" class="text-sm bg-slate-100 hover:bg-slate-200 text-slate-700 px-4 py-2 rounded-lg font-medium transition flex items-center">
              <PlusIcon class="h-4 w-4 mr-1" /> {{ $t('proxy.inbounds.addNode') }}
            </button>
          </div>
        </div>

        <!-- Inbound Form Modal -->
        <div v-if="showInboundForm" class="p-6 border-b border-slate-100 bg-slate-50">
          <div class="flex items-center justify-between mb-4">
            <h4 class="font-medium text-slate-700">{{ editingInbound ? $t('proxy.inbounds.editInbound') : $t('proxy.inbounds.addInbound') }}</h4>
            <div class="flex items-center gap-2">
              <!-- Visual / JSON toggle -->
              <div class="flex rounded-lg bg-slate-200 p-0.5">
                <button @click="editMode = 'visual'; syncJsonToVisual()" :class="['px-2.5 py-1 text-xs font-medium rounded-md transition', editMode === 'visual' ? 'bg-white text-slate-800 shadow-sm' : 'text-slate-500']">
                  <AdjustmentsHorizontalIcon class="h-3.5 w-3.5 inline mr-1" />{{ $t('proxy.inbounds.visual') }}
                </button>
                <button @click="editMode = 'json'; syncVisualToJson()" :class="['px-2.5 py-1 text-xs font-medium rounded-md transition', editMode === 'json' ? 'bg-white text-slate-800 shadow-sm' : 'text-slate-500']">
                  <CodeBracketIcon class="h-3.5 w-3.5 inline mr-1" />JSON
                </button>
              </div>
              <button @click="showInboundForm = false"><XMarkIcon class="h-5 w-5 text-slate-400" /></button>
            </div>
          </div>

          <!-- Basic fields (always visible) -->
          <div class="grid grid-cols-2 md:grid-cols-4 gap-3 mb-4">
            <input v-model="inboundForm.tag" :placeholder="$t('proxy.inbounds.tag')" class="input-field text-sm" />
            <select v-model="inboundForm.protocol" class="input-field text-sm">
              <option value="vless">VLESS</option>
              <option value="vmess">VMess</option>
              <option value="trojan">Trojan</option>
              <option value="hysteria2">Hysteria2</option>
              <option value="shadowsocks">Shadowsocks</option>
            </select>
            <input v-model="inboundForm.listen" :placeholder="$t('proxy.inbounds.listen')" class="input-field text-sm" />
            <input v-model.number="inboundForm.port" type="number" :placeholder="$t('proxy.inbounds.port')" class="input-field text-sm" />
          </div>

          <!-- Visual Mode -->
          <div v-if="editMode === 'visual'" class="space-y-4">
            <!-- Network & Security -->
            <div class="grid grid-cols-2 md:grid-cols-4 gap-3">
              <div>
                <label class="block text-xs font-medium text-slate-500 mb-1">{{ $t('proxy.inbounds.network') }}</label>
                <select v-model="vf.network" class="input-field text-sm w-full">
                  <option value="tcp">TCP</option>
                  <option value="ws">WebSocket</option>
                  <option value="grpc">gRPC</option>
                  <option value="h2">HTTP/2</option>
                </select>
              </div>
              <div>
                <label class="block text-xs font-medium text-slate-500 mb-1">{{ $t('proxy.inbounds.security') }}</label>
                <select v-model="vf.security" class="input-field text-sm w-full">
                  <option value="none">None</option>
                  <option value="tls">TLS</option>
                  <option value="reality">Reality</option>
                </select>
              </div>
              <div v-if="inboundForm.protocol === 'vless'">
                <label class="block text-xs font-medium text-slate-500 mb-1">Flow</label>
                <select v-model="vf.flow" class="input-field text-sm w-full">
                  <option value="">None</option>
                  <option value="xtls-rprx-vision">xtls-rprx-vision</option>
                </select>
              </div>
              <div>
                <label class="block text-xs font-medium text-slate-500 mb-1">{{ $t('proxy.inbounds.fingerprint') }}</label>
                <select v-model="vf.fingerprint" class="input-field text-sm w-full">
                  <option value="chrome">Chrome</option>
                  <option value="firefox">Firefox</option>
                  <option value="safari">Safari</option>
                  <option value="edge">Edge</option>
                  <option value="random">Random</option>
                </select>
              </div>
            </div>

            <!-- TLS settings -->
            <template v-if="vf.security === 'tls'">
              <div class="grid grid-cols-2 md:grid-cols-3 gap-3">
                <div>
                  <label class="block text-xs font-medium text-slate-500 mb-1">SNI</label>
                  <input v-model="vf.sni" class="input-field text-sm w-full" placeholder="example.com" />
                </div>
                <div>
                  <label class="block text-xs font-medium text-slate-500 mb-1">ALPN</label>
                  <input v-model="vf.alpn" class="input-field text-sm w-full" placeholder="h2,http/1.1" />
                </div>
              </div>
              <div class="grid grid-cols-2 gap-3">
                <div>
                  <label class="block text-xs font-medium text-slate-500 mb-1">{{ $t('proxy.inbounds.certFile') }}</label>
                  <input v-model="vf.certFile" class="input-field text-sm w-full font-mono text-xs" />
                </div>
                <div>
                  <label class="block text-xs font-medium text-slate-500 mb-1">{{ $t('proxy.inbounds.keyFile') }}</label>
                  <input v-model="vf.keyFile" class="input-field text-sm w-full font-mono text-xs" />
                </div>
              </div>
            </template>

            <!-- Reality settings -->
            <template v-if="vf.security === 'reality'">
              <div class="grid grid-cols-2 gap-3">
                <div>
                  <label class="block text-xs font-medium text-slate-500 mb-1">{{ $t('proxy.inbounds.destSni') }}</label>
                  <input v-model="vf.realityDest" class="input-field text-sm w-full" placeholder="www.microsoft.com:443" />
                </div>
                <div>
                  <label class="block text-xs font-medium text-slate-500 mb-1">{{ $t('proxy.inbounds.serverNames') }}</label>
                  <input v-model="vf.realityServerNames" class="input-field text-sm w-full" placeholder="www.microsoft.com" />
                </div>
              </div>
              <div class="grid grid-cols-1 md:grid-cols-2 gap-3">
                <div>
                  <label class="block text-xs font-medium text-slate-500 mb-1">{{ $t('proxy.inbounds.privateKey') }}</label>
                  <input v-model="vf.realityPrivateKey" class="input-field text-sm w-full font-mono text-xs" />
                </div>
                <div>
                  <label class="block text-xs font-medium text-slate-500 mb-1">{{ $t('proxy.inbounds.publicKey') }}</label>
                  <input v-model="vf.realityPublicKey" class="input-field text-sm w-full font-mono text-xs" />
                </div>
              </div>
              <div class="grid grid-cols-2 md:grid-cols-3 gap-3 items-end">
                <div>
                  <label class="block text-xs font-medium text-slate-500 mb-1">Short ID</label>
                  <input v-model="vf.realityShortId" class="input-field text-sm w-full font-mono" />
                </div>
                <div>
                  <button
                    @click="regenRealityKeys"
                    :disabled="regenLoading"
                    class="inline-flex items-center px-3 py-2 text-xs font-medium bg-amber-50 text-amber-700 border border-amber-200 rounded-lg hover:bg-amber-100 transition disabled:opacity-50"
                  >
                    <KeyIcon class="h-3.5 w-3.5 mr-1.5" :class="{ 'animate-spin': regenLoading }" />
                    {{ $t('proxy.inbounds.regenKeys') }}
                  </button>
                </div>
              </div>
            </template>

            <!-- WebSocket settings -->
            <template v-if="vf.network === 'ws'">
              <div class="grid grid-cols-2 gap-3">
                <div>
                  <label class="block text-xs font-medium text-slate-500 mb-1">{{ $t('proxy.inbounds.wsPath') }}</label>
                  <input v-model="vf.wsPath" class="input-field text-sm w-full font-mono" placeholder="/path" />
                </div>
                <div>
                  <label class="block text-xs font-medium text-slate-500 mb-1">{{ $t('proxy.inbounds.wsHost') }}</label>
                  <input v-model="vf.wsHost" class="input-field text-sm w-full" placeholder="example.com" />
                </div>
              </div>
            </template>

            <!-- gRPC settings -->
            <template v-if="vf.network === 'grpc'">
              <div>
                <label class="block text-xs font-medium text-slate-500 mb-1">{{ $t('proxy.inbounds.grpcService') }}</label>
                <input v-model="vf.grpcServiceName" class="input-field text-sm w-full font-mono" placeholder="grpc-service" />
              </div>
            </template>

            <!-- Shadowsocks settings -->
            <template v-if="inboundForm.protocol === 'shadowsocks'">
              <div class="grid grid-cols-2 gap-3">
                <div>
                  <label class="block text-xs font-medium text-slate-500 mb-1">{{ $t('proxy.inbounds.ssMethod') }}</label>
                  <select v-model="vf.ssMethod" class="input-field text-sm w-full">
                    <option value="2022-blake3-aes-128-gcm">2022-blake3-aes-128-gcm</option>
                    <option value="2022-blake3-aes-256-gcm">2022-blake3-aes-256-gcm</option>
                    <option value="aes-256-gcm">aes-256-gcm</option>
                    <option value="chacha20-ietf-poly1305">chacha20-ietf-poly1305</option>
                  </select>
                </div>
                <div>
                  <label class="block text-xs font-medium text-slate-500 mb-1">{{ $t('proxy.inbounds.ssPassword') }}</label>
                  <input v-model="vf.ssPassword" class="input-field text-sm w-full font-mono text-xs" />
                </div>
              </div>
            </template>
          </div>

          <!-- JSON Mode -->
          <div v-else class="grid grid-cols-1 md:grid-cols-2 gap-3 mb-3">
            <div>
              <label class="block text-xs font-medium text-slate-500 mb-1">{{ $t('proxy.inbounds.settingsJson') }}</label>
              <textarea v-model="inboundForm.settings" rows="6" class="input-field text-sm w-full font-mono"></textarea>
            </div>
            <div>
              <label class="block text-xs font-medium text-slate-500 mb-1">{{ $t('proxy.inbounds.streamJson') }}</label>
              <textarea v-model="inboundForm.stream" rows="6" class="input-field text-sm w-full font-mono"></textarea>
            </div>
          </div>

          <div class="mt-4">
            <button @click="saveInbound" class="bg-primary-600 text-white px-4 py-2 rounded-lg text-sm hover:bg-primary-700">{{ $t('common.save') }}</button>
          </div>
        </div>

        <div v-if="inboundsLoading" class="text-sm text-slate-400 text-center py-12">{{ $t('common.loading') }}</div>

        <table v-else class="min-w-full divide-y divide-slate-200">
          <thead class="bg-slate-50">
            <tr>
              <th class="px-6 py-3 text-left text-xs font-medium text-slate-500 uppercase">{{ $t('proxy.inbounds.tag') }}</th>
              <th class="px-6 py-3 text-left text-xs font-medium text-slate-500 uppercase">{{ $t('proxy.inbounds.protocol') }}</th>
              <th class="px-6 py-3 text-left text-xs font-medium text-slate-500 uppercase">{{ $t('proxy.inbounds.port') }}</th>
              <th class="px-6 py-3 text-left text-xs font-medium text-slate-500 uppercase">{{ $t('proxy.inbounds.transport') }}</th>
              <th class="relative px-6 py-3"><span class="sr-only">Actions</span></th>
            </tr>
          </thead>
          <tbody class="bg-white divide-y divide-slate-200">
            <tr v-for="node in inbounds" :key="node.id" class="hover:bg-slate-50 transition-colors">
              <td class="px-6 py-4 text-sm font-medium text-slate-900">{{ node.tag }}</td>
              <td class="px-6 py-4 text-sm">
                <span class="inline-flex items-center px-2.5 py-0.5 rounded-full text-xs font-medium bg-blue-100 text-blue-800">
                  {{ (node.protocol || '').toUpperCase() }}
                </span>
              </td>
              <td class="px-6 py-4 text-sm text-slate-500">{{ node.port }}</td>
              <td class="px-6 py-4 text-sm text-slate-500">{{ parseTransport(node.stream) }}</td>
              <td class="px-6 py-4 text-right text-sm font-medium">
                <button @click="openInboundForm(node)" class="text-primary-600 hover:text-primary-900 mr-4">{{ $t('common.edit') }}</button>
                <button @click="removeInbound(node.id)" class="text-rose-600 hover:text-rose-900">
                  <TrashIcon class="h-4 w-4 inline" />
                </button>
              </td>
            </tr>
            <tr v-if="inbounds.length === 0">
              <td colspan="5" class="py-12 text-center">
                <SparklesIcon class="h-10 w-10 mx-auto mb-3 text-slate-300" />
                <p class="text-sm text-slate-400 mb-4">{{ $t('proxy.inbounds.noInbounds') }}</p>
                <button @click="openQuickSetup()" class="bg-primary-600 text-white px-5 py-2.5 rounded-lg text-sm font-medium hover:bg-primary-700 transition inline-flex items-center">
                  <SparklesIcon class="h-4 w-4 mr-1.5" /> {{ $t('proxy.inbounds.quickSetup') }}
                </button>
                <p class="text-xs text-slate-400 mt-2">{{ $t('proxy.inbounds.oneClickConfig') }}</p>
              </td>
            </tr>
          </tbody>
        </table>
      </div>

      <!-- Routing Rules -->
      <div v-else-if="activeTab === 'routing'" class="flex flex-col h-full">
        <div class="p-6 border-b border-slate-100 flex justify-between items-center">
          <div>
            <h3 class="text-lg font-medium text-slate-800">{{ $t('proxy.routing.title') }}</h3>
            <p class="text-sm text-slate-500 mt-1">{{ $t('proxy.routing.subtitle') }}</p>
          </div>
          <div class="flex gap-2">
            <button @click="addRecommendedRules" :disabled="addingPresets" class="text-sm bg-primary-600 hover:bg-primary-700 text-white px-4 py-2 rounded-lg font-medium transition flex items-center disabled:opacity-50">
              <SparklesIcon class="h-4 w-4 mr-1" /> {{ $t('proxy.routing.addRecommended') }}
            </button>
            <button @click="showRoutingForm = !showRoutingForm" class="text-sm bg-slate-100 hover:bg-slate-200 text-slate-700 px-4 py-2 rounded-lg font-medium flex items-center">
              <PlusIcon class="h-4 w-4 mr-1" /> {{ $t('proxy.routing.addRule') }}
            </button>
          </div>
        </div>

        <!-- Routing Presets -->
        <div class="px-6 py-3 border-b border-slate-100 bg-slate-50/50">
          <p class="text-xs font-medium text-slate-500 mb-2">{{ $t('proxy.routing.presets') }}</p>
          <div class="flex flex-wrap gap-2">
            <button
              v-for="preset in routingPresets"
              :key="preset.id"
              @click="addRoutingPreset(preset)"
              :disabled="addingPresets"
              class="inline-flex items-center px-3 py-1.5 text-xs font-medium rounded-full border transition disabled:opacity-50"
              :class="preset.outbound_tag === 'block'
                ? 'bg-rose-50 text-rose-700 border-rose-200 hover:bg-rose-100'
                : 'bg-emerald-50 text-emerald-700 border-emerald-200 hover:bg-emerald-100'"
            >
              <PlusIcon class="h-3 w-3 mr-1" />
              {{ preset.rule_tag }}
              <span class="ml-1.5 text-[10px] opacity-60">→ {{ preset.outbound_tag }}</span>
            </button>
          </div>
        </div>

        <!-- Add Routing Rule Form -->
        <div v-if="showRoutingForm" class="p-6 border-b border-slate-100 bg-slate-50">
          <div class="grid grid-cols-2 md:grid-cols-4 gap-3 mb-3">
            <input v-model="routingForm.name" :placeholder="$t('proxy.routing.ruleName')" class="input-field text-sm" />
            <input v-model="routingForm.domain_keyword" :placeholder="$t('proxy.routing.domainKeyword')" class="input-field text-sm" />
            <input v-model="routingForm.domain_suffix" :placeholder="$t('proxy.routing.domainSuffix')" class="input-field text-sm" />
            <input v-model="routingForm.outbound_tag" :placeholder="$t('proxy.routing.outboundTag')" class="input-field text-sm" />
          </div>
          <div class="grid grid-cols-2 md:grid-cols-4 gap-3 mb-3">
            <input v-model="routingForm.geosite" :placeholder="$t('proxy.routing.geosite')" class="input-field text-sm" />
            <input v-model="routingForm.geoip" :placeholder="$t('proxy.routing.geoip')" class="input-field text-sm" />
            <input v-model.number="routingForm.priority" type="number" :placeholder="$t('proxy.routing.priority')" class="input-field text-sm" />
            <button @click="saveRoutingRule" class="bg-primary-600 text-white rounded-lg text-sm hover:bg-primary-700">{{ $t('common.add') }}</button>
          </div>
        </div>

        <div v-if="routingLoading" class="text-sm text-slate-400 text-center py-12">{{ $t('common.loading') }}</div>

        <table v-else class="min-w-full divide-y divide-slate-200">
          <thead class="bg-slate-50">
            <tr>
              <th class="px-6 py-3 text-left text-xs font-medium text-slate-500 uppercase">{{ $t('proxy.routing.name') }}</th>
              <th class="px-6 py-3 text-left text-xs font-medium text-slate-500 uppercase">{{ $t('proxy.routing.domain') }}</th>
              <th class="px-6 py-3 text-left text-xs font-medium text-slate-500 uppercase">{{ $t('proxy.routing.geo') }}</th>
              <th class="px-6 py-3 text-left text-xs font-medium text-slate-500 uppercase">{{ $t('proxy.routing.outbound') }}</th>
              <th class="relative px-6 py-3"><span class="sr-only">Actions</span></th>
            </tr>
          </thead>
          <tbody class="bg-white divide-y divide-slate-200">
            <tr v-for="rule in routingRules" :key="rule.id" class="hover:bg-slate-50">
              <td class="px-6 py-4 text-sm font-medium text-slate-900">{{ rule.name }}</td>
              <td class="px-6 py-4 text-sm text-slate-500">{{ rule.domain_keyword || rule.domain_suffix || '-' }}</td>
              <td class="px-6 py-4 text-sm text-slate-500">{{ [rule.geosite, rule.geoip].filter(Boolean).join(', ') || '-' }}</td>
              <td class="px-6 py-4 text-sm text-slate-500">{{ rule.outbound_tag }}</td>
              <td class="px-6 py-4 text-right">
                <button @click="removeRoutingRule(rule.id)" class="text-rose-600 hover:text-rose-900">
                  <TrashIcon class="h-4 w-4 inline" />
                </button>
              </td>
            </tr>
            <tr v-if="routingRules.length === 0">
              <td colspan="5" class="py-8 text-center text-sm text-slate-400">{{ $t('proxy.routing.noRules') }}</td>
            </tr>
          </tbody>
        </table>
      </div>

      <!-- Users & Subs -->
      <div v-else-if="activeTab === 'users'" class="flex flex-col h-full">
        <div class="p-6 border-b border-slate-100 flex justify-between items-center">
          <h3 class="text-lg font-medium text-slate-800">{{ $t('proxy.clients.title') }}</h3>
          <button @click="showClientForm = !showClientForm" class="text-sm bg-slate-100 hover:bg-slate-200 text-slate-700 px-4 py-2 rounded-lg font-medium transition flex items-center">
            <PlusIcon class="h-4 w-4 mr-1" /> {{ $t('proxy.clients.addClient') }}
          </button>
        </div>

        <!-- Add Client Form -->
        <div v-if="showClientForm" class="p-6 border-b border-slate-100 bg-slate-50">
          <div class="grid grid-cols-2 md:grid-cols-4 gap-3 mb-3">
            <input v-model="clientForm.email" :placeholder="$t('proxy.clients.email')" class="input-field text-sm" />
            <select v-model.number="clientForm.inbound_id" class="input-field text-sm">
              <option :value="0" disabled>{{ $t('proxy.clients.selectInbound') }}</option>
              <option v-for="ib in inbounds" :key="ib.id" :value="ib.id">{{ ib.tag }}</option>
            </select>
            <input v-model.number="clientForm.traffic_limit" type="number" :placeholder="$t('proxy.clients.trafficLimit')" class="input-field text-sm" />
            <button @click="saveClient" class="bg-primary-600 text-white rounded-lg text-sm hover:bg-primary-700">{{ $t('common.add') }}</button>
          </div>
        </div>

        <div v-if="clientsLoading" class="text-sm text-slate-400 text-center py-12">{{ $t('common.loading') }}</div>

        <table v-else class="min-w-full divide-y divide-slate-200">
          <thead class="bg-slate-50">
            <tr>
              <th class="px-6 py-3 text-left text-xs font-medium text-slate-500 uppercase">{{ $t('proxy.clients.email') }}</th>
              <th class="px-6 py-3 text-left text-xs font-medium text-slate-500 uppercase">{{ $t('proxy.clients.uuid') }}</th>
              <th class="px-6 py-3 text-left text-xs font-medium text-slate-500 uppercase">{{ $t('proxy.clients.traffic') }}</th>
              <th class="px-6 py-3 text-left text-xs font-medium text-slate-500 uppercase">{{ $t('proxy.clients.status') }}</th>
              <th class="relative px-6 py-3"><span class="sr-only">Actions</span></th>
            </tr>
          </thead>
          <tbody class="bg-white divide-y divide-slate-200">
            <tr v-for="user in clients" :key="user.id" class="hover:bg-slate-50">
              <td class="px-6 py-4 text-sm font-medium text-slate-900">{{ user.email }}</td>
              <td class="px-6 py-4 text-sm text-slate-500 font-mono">{{ (user.uuid || '').slice(0, 8) }}...</td>
              <td class="px-6 py-4 text-sm text-slate-500">{{ user.traffic_used || 0 }} / {{ user.traffic_limit || $t('proxy.clients.unlimited') }}</td>
              <td class="px-6 py-4">
                <span :class="[user.enable ? 'bg-emerald-100 text-emerald-800' : 'bg-slate-100 text-slate-800', 'px-2 inline-flex text-xs leading-5 font-semibold rounded-full']">
                  {{ user.enable ? $t('common.active') : $t('common.disabled') }}
                </span>
              </td>
              <td class="px-6 py-4 text-right text-sm font-medium space-x-2">
                <button @click="copySubLink(user.uuid)" class="text-emerald-600 hover:text-emerald-900 inline-flex items-center">
                  <ClipboardDocumentIcon class="h-4 w-4 mr-1" />
                  {{ copiedUuid === user.uuid ? $t('proxy.clients.copied') : $t('proxy.clients.subLink') }}
                </button>
                <button @click="openQrCode(user.uuid, user.email)" class="text-blue-600 hover:text-blue-900 inline-flex items-center">
                  <QrCodeIcon class="h-4 w-4 mr-1" />
                  {{ $t('proxy.clients.qrCode') }}
                </button>
                <button @click="removeClient(user.id)" class="text-rose-600 hover:text-rose-900">
                  <TrashIcon class="h-4 w-4 inline" />
                </button>
              </td>
            </tr>
            <tr v-if="clients.length === 0">
              <td colspan="5" class="py-8 text-center text-sm text-slate-400">{{ $t('proxy.clients.noClients') }}</td>
            </tr>
          </tbody>
        </table>
      </div>

    </div>

    <!-- Quick Setup Wizard Modal -->
    <div v-if="showQuickSetup" class="fixed inset-0 z-50 flex items-center justify-center bg-black/40 backdrop-blur-sm p-4">
      <div class="bg-white rounded-2xl shadow-2xl w-full max-w-3xl max-h-[90vh] overflow-hidden flex flex-col">

        <!-- Modal Header -->
        <div class="px-6 py-4 border-b border-slate-100 flex items-center justify-between flex-shrink-0">
          <div>
            <h2 class="text-lg font-bold text-slate-800">{{ $t('proxy.quickSetup.title') }}</h2>
            <div class="flex items-center gap-3 mt-1">
              <div v-for="s in 3" :key="s" class="flex items-center gap-1.5">
                <div :class="[
                  'w-6 h-6 rounded-full flex items-center justify-center text-xs font-bold transition-colors',
                  quickSetupStep >= s ? 'bg-primary-600 text-white' : 'bg-slate-200 text-slate-500'
                ]">{{ s }}</div>
                <span class="text-xs text-slate-500 hidden sm:inline">{{ [$t('proxy.quickSetup.steps.select'), $t('proxy.quickSetup.steps.review'), $t('proxy.quickSetup.steps.done')][s - 1] }}</span>
                <div v-if="s < 3" class="w-6 h-px bg-slate-200"></div>
              </div>
            </div>
          </div>
          <button @click="showQuickSetup = false" class="text-slate-400 hover:text-slate-600 transition">
            <XMarkIcon class="h-5 w-5" />
          </button>
        </div>

        <!-- Modal Content -->
        <div class="flex-1 overflow-y-auto p-6">

          <!-- Step 1: Select Presets -->
          <div v-if="quickSetupStep === 1">
            <p class="text-sm text-slate-600 mb-5" v-html="$t('proxy.quickSetup.selectDesc', { recommended: '<strong>' + $t('proxy.quickSetup.recommended') + '</strong>' })"></p>
            <div class="grid grid-cols-1 sm:grid-cols-2 gap-3">
              <div
                v-for="preset in presets"
                :key="preset.id"
                @click="togglePreset(preset.id)"
                :class="[
                  'relative cursor-pointer rounded-xl border-2 p-4 transition-all',
                  selectedPresetIds.includes(preset.id)
                    ? 'border-primary-500 bg-primary-50 shadow-sm'
                    : 'border-slate-200 hover:border-slate-300 bg-white'
                ]"
              >
                <div class="flex items-start justify-between">
                  <div class="flex-1 min-w-0">
                    <div class="flex items-center gap-2 flex-wrap">
                      <h4 class="font-semibold text-slate-800 text-sm">{{ $t(`proxy.quickSetup.presets.${preset.id}.name`) }}</h4>
                      <span :class="[preset.badgeColor, 'text-xs font-medium px-2 py-0.5 rounded-full whitespace-nowrap']">
                        {{ $t(`proxy.quickSetup.badges.${preset.badgeKey}`) }}
                      </span>
                    </div>
                    <p class="text-xs text-slate-500 mt-1 leading-relaxed">{{ $t(`proxy.quickSetup.presets.${preset.id}.desc`) }}</p>
                    <p class="text-xs text-slate-400 mt-1.5">{{ $t('proxy.quickSetup.fields.defaultPort') }}: {{ preset.defaultPort }}</p>
                  </div>
                  <div :class="[
                    'w-5 h-5 rounded-full border-2 flex items-center justify-center flex-shrink-0 ml-3 mt-0.5 transition-colors',
                    selectedPresetIds.includes(preset.id)
                      ? 'border-primary-500 bg-primary-500'
                      : 'border-slate-300'
                  ]">
                    <svg v-if="selectedPresetIds.includes(preset.id)" class="w-3 h-3 text-white" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="3">
                      <path stroke-linecap="round" stroke-linejoin="round" d="M5 13l4 4L19 7" />
                    </svg>
                  </div>
                </div>
              </div>
            </div>
          </div>

          <!-- Step 2: Review & Customize -->
          <div v-else-if="quickSetupStep === 2">
            <p class="text-sm text-slate-600 mb-5">{{ $t('proxy.quickSetup.reviewDesc') }}</p>

            <div class="space-y-3">
              <div v-for="id in selectedPresetIds" :key="id" class="border border-slate-200 rounded-xl overflow-hidden">
                <button
                  @click="expandedPreset = expandedPreset === id ? null : id"
                  class="w-full px-4 py-3 flex items-center justify-between bg-slate-50 hover:bg-slate-100 transition text-left"
                >
                  <div class="flex items-center gap-2">
                    <span class="font-medium text-slate-800 text-sm">{{ $t(`proxy.quickSetup.presets.${id}.name`) }}</span>
                    <span :class="[presets.find(p => p.id === id)?.badgeColor, 'text-xs font-medium px-1.5 py-0.5 rounded-full']">
                      {{ $t(`proxy.quickSetup.badges.${presets.find(p => p.id === id)?.badgeKey}`) }}
                    </span>
                    <span class="text-xs text-slate-400">{{ $t('proxy.inbounds.port') }} {{ presetConfigs[id]?.port }}</span>
                  </div>
                  <component :is="expandedPreset === id ? ChevronDownIcon : ChevronRightIcon" class="h-4 w-4 text-slate-400 flex-shrink-0" />
                </button>

                <div v-if="expandedPreset === id" class="p-4 space-y-3 border-t border-slate-100">
                  <!-- Common fields -->
                  <div class="grid grid-cols-2 gap-3">
                    <div>
                      <label class="block text-xs font-medium text-slate-500 mb-1">{{ $t('proxy.inbounds.tag') }}</label>
                      <input v-model="presetConfigs[id].tag" class="input-field text-sm w-full" />
                    </div>
                    <div>
                      <label class="block text-xs font-medium text-slate-500 mb-1">{{ $t('proxy.inbounds.port') }}</label>
                      <input v-model.number="presetConfigs[id].port" type="number" class="input-field text-sm w-full" />
                    </div>
                  </div>

                  <!-- Reality fields -->
                  <template v-if="presets.find(p => p.id === id)?.needsRealityKeys">
                    <div class="grid grid-cols-2 gap-3">
                      <div>
                        <label class="block text-xs font-medium text-slate-500 mb-1">{{ $t('proxy.quickSetup.fields.destSni') }}</label>
                        <input v-model="presetConfigs[id].dest" class="input-field text-sm w-full" placeholder="www.microsoft.com:443" />
                      </div>
                      <div>
                        <label class="block text-xs font-medium text-slate-500 mb-1">{{ $t('proxy.quickSetup.fields.serverNames') }}</label>
                        <input v-model="presetConfigs[id].serverNames" class="input-field text-sm w-full" placeholder="www.microsoft.com" />
                      </div>
                    </div>
                    <div class="grid grid-cols-2 gap-3">
                      <div>
                        <label class="block text-xs font-medium text-slate-500 mb-1">{{ $t('proxy.quickSetup.fields.privateKey') }}</label>
                        <input v-model="presetConfigs[id].privateKey" class="input-field text-sm w-full font-mono text-xs" />
                      </div>
                      <div>
                        <label class="block text-xs font-medium text-slate-500 mb-1">{{ $t('proxy.quickSetup.fields.publicKey') }}</label>
                        <input v-model="presetConfigs[id].publicKey" class="input-field text-sm w-full font-mono text-xs" />
                      </div>
                    </div>
                    <div class="grid grid-cols-2 gap-3">
                      <div>
                        <label class="block text-xs font-medium text-slate-500 mb-1">{{ $t('proxy.quickSetup.fields.shortId') }}</label>
                        <input v-model="presetConfigs[id].shortId" class="input-field text-sm w-full font-mono" />
                      </div>
                      <div>
                        <label class="block text-xs font-medium text-slate-500 mb-1">{{ $t('proxy.quickSetup.fields.fingerprint') }}</label>
                        <select v-model="presetConfigs[id].fingerprint" class="input-field text-sm w-full">
                          <option value="chrome">Chrome</option>
                          <option value="firefox">Firefox</option>
                          <option value="safari">Safari</option>
                          <option value="edge">Edge</option>
                          <option value="random">Random</option>
                        </select>
                      </div>
                    </div>
                  </template>

                  <!-- Domain field (for TLS-based presets) -->
                  <div v-if="presets.find(p => p.id === id)?.needsDomain">
                    <label class="block text-xs font-medium text-slate-500 mb-1">{{ $t('proxy.quickSetup.fields.domain') }} <span class="text-rose-400">*</span></label>
                    <input v-model="presetConfigs[id].domain" class="input-field text-sm w-full" placeholder="example.com" />
                    <p class="text-xs text-slate-400 mt-1">{{ $t('proxy.quickSetup.fields.domainRequired') }}</p>
                  </div>

                  <!-- Certificate fields -->
                  <template v-if="presets.find(p => p.id === id)?.needsCert">
                    <div class="grid grid-cols-2 gap-3">
                      <div>
                        <label class="block text-xs font-medium text-slate-500 mb-1">{{ $t('proxy.quickSetup.fields.certFile') }}</label>
                        <input v-model="presetConfigs[id].certFile" class="input-field text-sm w-full font-mono text-xs" />
                      </div>
                      <div>
                        <label class="block text-xs font-medium text-slate-500 mb-1">{{ $t('proxy.quickSetup.fields.keyFile') }}</label>
                        <input v-model="presetConfigs[id].keyFile" class="input-field text-sm w-full font-mono text-xs" />
                      </div>
                    </div>
                  </template>

                  <!-- WebSocket path -->
                  <div v-if="id === 'vless-ws-tls' || id === 'vmess-ws-tls'">
                    <label class="block text-xs font-medium text-slate-500 mb-1">{{ $t('proxy.quickSetup.fields.wsPath') }}</label>
                    <input v-model="presetConfigs[id].wsPath" class="input-field text-sm w-full font-mono" />
                  </div>

                  <!-- Shadowsocks fields -->
                  <template v-if="id === 'shadowsocks'">
                    <div class="grid grid-cols-2 gap-3">
                      <div>
                        <label class="block text-xs font-medium text-slate-500 mb-1">{{ $t('proxy.quickSetup.fields.encryptionMethod') }}</label>
                        <select v-model="presetConfigs[id].method" class="input-field text-sm w-full">
                          <option value="2022-blake3-aes-128-gcm">2022-blake3-aes-128-gcm</option>
                          <option value="2022-blake3-aes-256-gcm">2022-blake3-aes-256-gcm</option>
                          <option value="aes-256-gcm">aes-256-gcm</option>
                          <option value="chacha20-ietf-poly1305">chacha20-ietf-poly1305</option>
                        </select>
                      </div>
                      <div>
                        <label class="block text-xs font-medium text-slate-500 mb-1">{{ $t('proxy.quickSetup.fields.password') }}</label>
                        <input v-model="presetConfigs[id].password" class="input-field text-sm w-full font-mono text-xs" />
                      </div>
                    </div>
                  </template>
                </div>
              </div>
            </div>

            <!-- Additional options -->
            <div class="mt-6 pt-4 border-t border-slate-200 space-y-3">
              <h4 class="text-sm font-medium text-slate-700">{{ $t('proxy.quickSetup.additionalSetup') }}</h4>
              <label class="flex items-center gap-2 cursor-pointer">
                <input type="checkbox" v-model="addDefaultRouting" class="rounded border-slate-300 text-primary-600 focus:ring-primary-500" />
                <span class="text-sm text-slate-600">{{ $t('proxy.quickSetup.addRoutingRules') }}</span>
              </label>
              <div class="flex items-center gap-2 flex-wrap">
                <label class="flex items-center gap-2 cursor-pointer">
                  <input type="checkbox" v-model="addDefaultClient" class="rounded border-slate-300 text-primary-600 focus:ring-primary-500" />
                  <span class="text-sm text-slate-600">{{ $t('proxy.quickSetup.createFirstClient') }}</span>
                </label>
                <input v-if="addDefaultClient" v-model="defaultClientEmail" class="input-field text-sm w-40" :placeholder="$t('proxy.clients.email')" />
              </div>
            </div>
          </div>

          <!-- Step 3: Complete -->
          <div v-else-if="quickSetupStep === 3" class="text-center py-8">
            <div class="w-16 h-16 bg-emerald-100 rounded-full flex items-center justify-center mx-auto mb-4">
              <CheckCircleIcon class="h-8 w-8 text-emerald-600" />
            </div>
            <h3 class="text-xl font-bold text-slate-800 mb-2">{{ $t('proxy.quickSetup.completeTitle') }}</h3>
            <p class="text-sm text-slate-500 mb-6">{{ $t('proxy.quickSetup.completeDesc') }}</p>

            <div class="max-w-sm mx-auto space-y-2 text-left">
              <div
                v-for="result in quickSetupResults"
                :key="result.tag"
                :class="[
                  'flex items-center justify-between px-4 py-2.5 rounded-lg',
                  result.success ? 'bg-emerald-50' : 'bg-rose-50'
                ]"
              >
                <span class="text-sm font-medium" :class="result.success ? 'text-emerald-700' : 'text-rose-700'">{{ result.tag }}</span>
                <span class="text-xs font-medium" :class="result.success ? 'text-emerald-500' : 'text-rose-500'">{{ result.success ? $t('proxy.quickSetup.created') : result.error }}</span>
              </div>
            </div>

            <p class="text-sm text-slate-500 mt-6" v-html="$t('proxy.quickSetup.applyHint', { apply: '<strong>' + $t('proxy.applyConfig') + '</strong>' })"></p>
          </div>
        </div>

        <!-- Modal Footer -->
        <div class="px-6 py-4 border-t border-slate-100 flex justify-between flex-shrink-0">
          <div>
            <button
              v-if="quickSetupStep === 2"
              @click="quickSetupStep = 1"
              class="text-sm text-slate-600 hover:text-slate-800 font-medium transition"
            >{{ $t('common.back') }}</button>
          </div>
          <div class="flex gap-2">
            <template v-if="quickSetupStep === 1">
              <button
                @click="useRecommended"
                class="bg-primary-600 text-white px-5 py-2 rounded-lg text-sm font-medium hover:bg-primary-700 transition"
              >{{ $t('proxy.quickSetup.useRecommended') }}</button>
              <button
                v-if="selectedPresetIds.length > 0"
                @click="proceedToReview"
                class="bg-slate-800 text-white px-5 py-2 rounded-lg text-sm font-medium hover:bg-slate-900 transition"
              >{{ $t('proxy.quickSetup.continueN', { n: selectedPresetIds.length }) }}</button>
            </template>
            <template v-else-if="quickSetupStep === 2">
              <button
                @click="executeQuickSetup"
                :disabled="quickSetupCreating"
                class="bg-primary-600 text-white px-5 py-2 rounded-lg text-sm font-medium hover:bg-primary-700 transition disabled:opacity-50 disabled:cursor-not-allowed"
              >{{ quickSetupCreating ? $t('proxy.quickSetup.creating') : $t('proxy.quickSetup.createAll') }}</button>
            </template>
            <template v-else>
              <button
                @click="showQuickSetup = false"
                class="bg-primary-600 text-white px-5 py-2 rounded-lg text-sm font-medium hover:bg-primary-700 transition"
              >{{ $t('common.done') }}</button>
            </template>
          </div>
        </div>
      </div>
    </div>

    <!-- QR Code Modal -->
    <div v-if="showQrModal" class="fixed inset-0 z-50 flex items-center justify-center bg-black/40 backdrop-blur-sm p-4">
      <div class="bg-white rounded-2xl shadow-2xl w-full max-w-sm overflow-hidden">
        <div class="px-6 py-4 border-b border-slate-100 flex items-center justify-between">
          <div>
            <h2 class="text-lg font-bold text-slate-800">{{ $t('proxy.qr.title') }}</h2>
            <p class="text-xs text-slate-500 mt-0.5">{{ qrEmail }}</p>
          </div>
          <button @click="showQrModal = false" class="text-slate-400 hover:text-slate-600 transition">
            <XMarkIcon class="h-5 w-5" />
          </button>
        </div>

        <div class="p-6">
          <!-- Format Toggle -->
          <div class="flex rounded-lg bg-slate-100 p-1 mb-5">
            <button
              @click="qrFormat = 'v2ray'; generateQr()"
              :class="[
                'flex-1 px-3 py-1.5 text-sm font-medium rounded-md transition-colors',
                qrFormat === 'v2ray' ? 'bg-white text-slate-800 shadow-sm' : 'text-slate-500 hover:text-slate-700'
              ]"
            >{{ $t('proxy.qr.v2ray') }}</button>
            <button
              @click="qrFormat = 'clash'; generateQr()"
              :class="[
                'flex-1 px-3 py-1.5 text-sm font-medium rounded-md transition-colors',
                qrFormat === 'clash' ? 'bg-white text-slate-800 shadow-sm' : 'text-slate-500 hover:text-slate-700'
              ]"
            >{{ $t('proxy.qr.clash') }}</button>
          </div>

          <!-- QR Code Image -->
          <div class="flex justify-center mb-4">
            <div v-if="qrDataUrl" class="p-3 bg-white border border-slate-200 rounded-xl">
              <img :src="qrDataUrl" alt="QR Code" class="w-[280px] h-[280px]" />
            </div>
            <div v-else class="w-[280px] h-[280px] bg-slate-100 rounded-xl flex items-center justify-center">
              <span class="text-sm text-slate-400">{{ $t('proxy.qr.generating') }}</span>
            </div>
          </div>

          <p class="text-xs text-slate-400 text-center mb-4">
            {{ qrFormat === 'v2ray' ? $t('proxy.qr.scanWith', { clients: $t('proxy.qr.v2rayClients') }) : $t('proxy.qr.scanWith', { clients: $t('proxy.qr.clashClients') }) }}
          </p>

          <!-- Actions -->
          <div class="flex gap-2">
            <button
              @click="downloadQr"
              class="flex-1 bg-slate-100 hover:bg-slate-200 text-slate-700 px-4 py-2 rounded-lg text-sm font-medium transition"
            >{{ $t('proxy.qr.downloadPng') }}</button>
            <button
              @click="copySubLink(qrUuid)"
              class="flex-1 bg-primary-600 hover:bg-primary-700 text-white px-4 py-2 rounded-lg text-sm font-medium transition"
            >{{ copiedUuid === qrUuid ? $t('proxy.clients.copied') : $t('proxy.qr.copyLink') }}</button>
          </div>
        </div>
      </div>
    </div>

  </div>
</template>
