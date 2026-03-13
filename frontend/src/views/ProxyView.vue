<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { PlusIcon, TrashIcon, ArrowPathIcon, XMarkIcon, ClipboardDocumentIcon } from '@heroicons/vue/24/outline'
import { listInbounds, createInbound, updateInbound, deleteInbound, listClients, createClient, deleteClient, listRoutingRules, createRoutingRule, deleteRoutingRule } from '@/api/proxy'

const props = defineProps<{ defaultTab?: string }>()
const activeTab = ref(props.defaultTab || 'inbounds')
const tabs = [
  { id: 'inbounds', name: 'Inbound Nodes' },
  { id: 'routing', name: 'Routing Rules' },
  { id: 'users', name: 'User & Subs' },
]

// ---- Inbounds ----
const inbounds = ref<any[]>([])
const inboundsLoading = ref(false)
const showInboundForm = ref(false)
const editingInbound = ref<any>(null)
const inboundForm = ref({ tag: '', protocol: 'vless', listen: '0.0.0.0', port: 443, settings: '{}' })

async function fetchInbounds() {
  inboundsLoading.value = true
  try {
    const res = await listInbounds() as any
    if (res.code === 200) inbounds.value = res.data || []
  } catch { /* ignore */ }
  inboundsLoading.value = false
}

function openInboundForm(inbound?: any) {
  if (inbound) {
    editingInbound.value = inbound
    inboundForm.value = {
      tag: inbound.tag || '',
      protocol: inbound.protocol || 'vless',
      listen: inbound.listen || '0.0.0.0',
      port: inbound.port || 443,
      settings: typeof inbound.settings === 'string' ? inbound.settings : JSON.stringify(inbound.settings || {}, null, 2),
    }
  } else {
    editingInbound.value = null
    inboundForm.value = { tag: '', protocol: 'vless', listen: '0.0.0.0', port: 443, settings: '{}' }
  }
  showInboundForm.value = true
}

async function saveInbound() {
  try {
    const data = { ...inboundForm.value }
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
  if (!confirm('Delete this inbound?')) return
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
  if (!confirm('Delete this client?')) return
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
  if (!confirm('Delete this routing rule?')) return
  try {
    await deleteRoutingRule(id)
    await fetchRoutingRules()
  } catch { /* ignore */ }
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
        <h1 class="text-3xl font-bold text-slate-800 tracking-tight">Proxy Services</h1>
        <p class="text-slate-500 mt-1">Configure Xray/Sing-box engine, routing rules, and user subscriptions.</p>
      </div>
      <div>
        <button class="bg-primary-600 hover:bg-primary-700 text-white px-5 py-2.5 rounded-xl text-sm font-medium transition-colors shadow-sm flex items-center">
          <ArrowPathIcon class="h-5 w-5 mr-2" />
          Apply Configuration
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
          <h3 class="text-lg font-medium text-slate-800">Inbound Listeners</h3>
          <button @click="openInboundForm()" class="text-sm bg-slate-100 hover:bg-slate-200 text-slate-700 px-4 py-2 rounded-lg font-medium transition flex items-center">
            <PlusIcon class="h-4 w-4 mr-1" /> Add Node
          </button>
        </div>

        <!-- Inbound Form Modal -->
        <div v-if="showInboundForm" class="p-6 border-b border-slate-100 bg-slate-50">
          <div class="flex items-center justify-between mb-4">
            <h4 class="font-medium text-slate-700">{{ editingInbound ? 'Edit' : 'Add' }} Inbound</h4>
            <button @click="showInboundForm = false"><XMarkIcon class="h-5 w-5 text-slate-400" /></button>
          </div>
          <div class="grid grid-cols-2 md:grid-cols-4 gap-3 mb-3">
            <input v-model="inboundForm.tag" placeholder="Tag" class="input-field text-sm" />
            <select v-model="inboundForm.protocol" class="input-field text-sm">
              <option value="vless">VLESS</option>
              <option value="vmess">VMess</option>
              <option value="trojan">Trojan</option>
              <option value="hysteria2">Hysteria2</option>
              <option value="shadowsocks">Shadowsocks</option>
            </select>
            <input v-model="inboundForm.listen" placeholder="Listen" class="input-field text-sm" />
            <input v-model.number="inboundForm.port" type="number" placeholder="Port" class="input-field text-sm" />
          </div>
          <textarea v-model="inboundForm.settings" placeholder="Settings (JSON)" rows="3" class="input-field text-sm w-full font-mono mb-3"></textarea>
          <button @click="saveInbound" class="bg-primary-600 text-white px-4 py-2 rounded-lg text-sm hover:bg-primary-700">Save</button>
        </div>

        <div v-if="inboundsLoading" class="text-sm text-slate-400 text-center py-12">Loading...</div>

        <table v-else class="min-w-full divide-y divide-slate-200">
          <thead class="bg-slate-50">
            <tr>
              <th class="px-6 py-3 text-left text-xs font-medium text-slate-500 uppercase">Tag</th>
              <th class="px-6 py-3 text-left text-xs font-medium text-slate-500 uppercase">Protocol</th>
              <th class="px-6 py-3 text-left text-xs font-medium text-slate-500 uppercase">Port</th>
              <th class="px-6 py-3 text-left text-xs font-medium text-slate-500 uppercase">Listen</th>
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
              <td class="px-6 py-4 text-sm text-slate-500">{{ node.listen || '0.0.0.0' }}</td>
              <td class="px-6 py-4 text-right text-sm font-medium">
                <button @click="openInboundForm(node)" class="text-primary-600 hover:text-primary-900 mr-4">Edit</button>
                <button @click="removeInbound(node.id)" class="text-rose-600 hover:text-rose-900">
                  <TrashIcon class="h-4 w-4 inline" />
                </button>
              </td>
            </tr>
            <tr v-if="inbounds.length === 0">
              <td colspan="5" class="py-8 text-center text-sm text-slate-400">No inbounds configured</td>
            </tr>
          </tbody>
        </table>
      </div>

      <!-- Routing Rules -->
      <div v-else-if="activeTab === 'routing'" class="flex flex-col h-full">
        <div class="p-6 border-b border-slate-100 flex justify-between items-center">
          <div>
            <h3 class="text-lg font-medium text-slate-800">Routing Rules</h3>
            <p class="text-sm text-slate-500 mt-1">Direct traffic to dedicated outbound nodes.</p>
          </div>
          <button @click="showRoutingForm = !showRoutingForm" class="text-sm bg-slate-100 hover:bg-slate-200 text-slate-700 px-4 py-2 rounded-lg font-medium flex items-center">
            <PlusIcon class="h-4 w-4 mr-1" /> Add Rule
          </button>
        </div>

        <!-- Add Routing Rule Form -->
        <div v-if="showRoutingForm" class="p-6 border-b border-slate-100 bg-slate-50">
          <div class="grid grid-cols-2 md:grid-cols-4 gap-3 mb-3">
            <input v-model="routingForm.name" placeholder="Rule Name" class="input-field text-sm" />
            <input v-model="routingForm.domain_keyword" placeholder="Domain Keyword" class="input-field text-sm" />
            <input v-model="routingForm.domain_suffix" placeholder="Domain Suffix" class="input-field text-sm" />
            <input v-model="routingForm.outbound_tag" placeholder="Outbound Tag" class="input-field text-sm" />
          </div>
          <div class="grid grid-cols-2 md:grid-cols-4 gap-3 mb-3">
            <input v-model="routingForm.geosite" placeholder="Geosite" class="input-field text-sm" />
            <input v-model="routingForm.geoip" placeholder="GeoIP" class="input-field text-sm" />
            <input v-model.number="routingForm.priority" type="number" placeholder="Priority" class="input-field text-sm" />
            <button @click="saveRoutingRule" class="bg-primary-600 text-white rounded-lg text-sm hover:bg-primary-700">Add</button>
          </div>
        </div>

        <div v-if="routingLoading" class="text-sm text-slate-400 text-center py-12">Loading...</div>

        <table v-else class="min-w-full divide-y divide-slate-200">
          <thead class="bg-slate-50">
            <tr>
              <th class="px-6 py-3 text-left text-xs font-medium text-slate-500 uppercase">Name</th>
              <th class="px-6 py-3 text-left text-xs font-medium text-slate-500 uppercase">Domain</th>
              <th class="px-6 py-3 text-left text-xs font-medium text-slate-500 uppercase">Geo</th>
              <th class="px-6 py-3 text-left text-xs font-medium text-slate-500 uppercase">Outbound</th>
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
              <td colspan="5" class="py-8 text-center text-sm text-slate-400">No routing rules configured</td>
            </tr>
          </tbody>
        </table>
      </div>

      <!-- Users & Subs -->
      <div v-else-if="activeTab === 'users'" class="flex flex-col h-full">
        <div class="p-6 border-b border-slate-100 flex justify-between items-center">
          <h3 class="text-lg font-medium text-slate-800">Client Management</h3>
          <button @click="showClientForm = !showClientForm" class="text-sm bg-slate-100 hover:bg-slate-200 text-slate-700 px-4 py-2 rounded-lg font-medium transition flex items-center">
            <PlusIcon class="h-4 w-4 mr-1" /> Add Client
          </button>
        </div>

        <!-- Add Client Form -->
        <div v-if="showClientForm" class="p-6 border-b border-slate-100 bg-slate-50">
          <div class="grid grid-cols-2 md:grid-cols-4 gap-3 mb-3">
            <input v-model="clientForm.email" placeholder="Email" class="input-field text-sm" />
            <select v-model.number="clientForm.inbound_id" class="input-field text-sm">
              <option :value="0" disabled>Select Inbound</option>
              <option v-for="ib in inbounds" :key="ib.id" :value="ib.id">{{ ib.tag }}</option>
            </select>
            <input v-model.number="clientForm.traffic_limit" type="number" placeholder="Traffic Limit (bytes)" class="input-field text-sm" />
            <button @click="saveClient" class="bg-primary-600 text-white rounded-lg text-sm hover:bg-primary-700">Add</button>
          </div>
        </div>

        <div v-if="clientsLoading" class="text-sm text-slate-400 text-center py-12">Loading...</div>

        <table v-else class="min-w-full divide-y divide-slate-200">
          <thead class="bg-slate-50">
            <tr>
              <th class="px-6 py-3 text-left text-xs font-medium text-slate-500 uppercase">Email</th>
              <th class="px-6 py-3 text-left text-xs font-medium text-slate-500 uppercase">UUID</th>
              <th class="px-6 py-3 text-left text-xs font-medium text-slate-500 uppercase">Traffic</th>
              <th class="px-6 py-3 text-left text-xs font-medium text-slate-500 uppercase">Status</th>
              <th class="relative px-6 py-3"><span class="sr-only">Actions</span></th>
            </tr>
          </thead>
          <tbody class="bg-white divide-y divide-slate-200">
            <tr v-for="user in clients" :key="user.id" class="hover:bg-slate-50">
              <td class="px-6 py-4 text-sm font-medium text-slate-900">{{ user.email }}</td>
              <td class="px-6 py-4 text-sm text-slate-500 font-mono">{{ (user.uuid || '').slice(0, 8) }}...</td>
              <td class="px-6 py-4 text-sm text-slate-500">{{ user.traffic_used || 0 }} / {{ user.traffic_limit || 'Unlimited' }}</td>
              <td class="px-6 py-4">
                <span :class="[user.enable ? 'bg-emerald-100 text-emerald-800' : 'bg-slate-100 text-slate-800', 'px-2 inline-flex text-xs leading-5 font-semibold rounded-full']">
                  {{ user.enable ? 'Active' : 'Disabled' }}
                </span>
              </td>
              <td class="px-6 py-4 text-right text-sm font-medium space-x-2">
                <button @click="copySubLink(user.uuid)" class="text-emerald-600 hover:text-emerald-900 inline-flex items-center">
                  <ClipboardDocumentIcon class="h-4 w-4 mr-1" />
                  {{ copiedUuid === user.uuid ? 'Copied!' : 'Sub Link' }}
                </button>
                <button @click="removeClient(user.id)" class="text-rose-600 hover:text-rose-900">
                  <TrashIcon class="h-4 w-4 inline" />
                </button>
              </td>
            </tr>
            <tr v-if="clients.length === 0">
              <td colspan="5" class="py-8 text-center text-sm text-slate-400">No clients configured</td>
            </tr>
          </tbody>
        </table>
      </div>

    </div>
  </div>
</template>
