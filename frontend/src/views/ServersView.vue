<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted, watch, nextTick } from 'vue'
import { useI18n } from 'vue-i18n'
import { CommandLineIcon, FolderIcon, WrenchIcon, ShieldCheckIcon, ChevronRightIcon, DocumentIcon, ArrowUpIcon, PlusIcon, TrashIcon } from '@heroicons/vue/24/outline'
import { useAuthStore } from '@/store/auth'
import { listContainers, startContainer, stopContainer, restartContainer, removeContainer } from '@/api/docker'
import { listDirectory, readFile, writeFile } from '@/api/fs'
import { listFirewallRules, addFirewallRule, deleteFirewallRule } from '@/api/firewall'
import { useToast } from '../composables/useToast'
import { useConfirm } from '@/composables/useConfirm'

const { t } = useI18n()
const toast = useToast()
const { confirm: confirmDialog } = useConfirm()

const activeTab = ref('terminal')
const tabs = computed(() => [
  { id: 'terminal', name: t('servers.tabs.terminal'), icon: CommandLineIcon },
  { id: 'files', name: t('servers.tabs.files'), icon: FolderIcon },
  { id: 'docker', name: t('servers.tabs.docker'), icon: WrenchIcon },
  { id: 'firewall', name: t('servers.tabs.firewall'), icon: ShieldCheckIcon },
])

// ---- Terminal ----
const terminalEl = ref<HTMLElement | null>(null)
const termInitialized = ref(false)
const termConnecting = ref(false)
const hostname = window.location.hostname
let termWs: WebSocket | null = null

async function connectTerminal() {
  if (termInitialized.value || !terminalEl.value) return
  termConnecting.value = true
  try {
    const { Terminal } = await import('xterm')
    await import('xterm/css/xterm.css')
    const { FitAddon } = await import('xterm-addon-fit')
    const term = new Terminal({ cursorBlink: true, fontSize: 14, theme: { background: '#1a1b26' } })
    const fitAddon = new FitAddon()
    term.loadAddon(fitAddon)
    term.open(terminalEl.value)
    fitAddon.fit()

    const proto = location.protocol === 'https:' ? 'wss' : 'ws'
    const token = useAuthStore().token
    const ws = new WebSocket(`${proto}://${location.host}/api/v1/terminal?token=${token}`)
    ws.binaryType = 'arraybuffer'

    ws.onopen = () => {
      termInitialized.value = true
      termConnecting.value = false
      // Re-fit now that overlay is gone, then send correct size to PTY
      nextTick(() => {
        fitAddon.fit()
        ws.send(JSON.stringify({ type: 'resize', cols: term.cols, rows: term.rows }))
      })
    }
    ws.onmessage = (e) => {
      if (e.data instanceof ArrayBuffer) {
        term.write(new Uint8Array(e.data))
      } else {
        term.write(e.data)
      }
    }
    term.onData((data: string) => { if (ws.readyState === WebSocket.OPEN) ws.send(data) })
    term.onResize(({ cols, rows }) => {
      if (ws.readyState === WebSocket.OPEN) {
        ws.send(JSON.stringify({ type: 'resize', cols, rows }))
      }
    })
    ws.onclose = () => term.write('\r\n\x1b[31m[Connection closed]\x1b[0m\r\n')
    ws.onerror = () => {
      term.write('\r\n\x1b[31m[Connection error]\x1b[0m\r\n')
      termConnecting.value = false
    }
    termWs = ws

    window.addEventListener('resize', () => fitAddon.fit())
  } catch {
    toast.error(t('common.errorOccurred'))
    termConnecting.value = false
  }
}

// ---- File Explorer ----
const currentPath = ref('/')
const files = ref<any[]>([])
const filesLoading = ref(false)
const editingFile = ref<string | null>(null)
const fileContent = ref('')
const fileSaving = ref(false)

async function fetchFiles(path: string) {
  filesLoading.value = true
  try {
    const res = await listDirectory(path) as any
    if (res.code === 200) {
      files.value = res.data || []
      currentPath.value = path
    }
  } catch { toast.error(t('common.errorOccurred')) }
  filesLoading.value = false
}

async function openItem(item: any) {
  if (item.is_dir) {
    const newPath = currentPath.value === '/' ? '/' + item.name : currentPath.value + '/' + item.name
    await fetchFiles(newPath)
  } else {
    const filePath = currentPath.value === '/' ? '/' + item.name : currentPath.value + '/' + item.name
    try {
      const res = await readFile(filePath) as any
      if (res.code === 200) {
        editingFile.value = filePath
        fileContent.value = res.data || ''
      }
    } catch { toast.error(t('common.errorOccurred')) }
  }
}

function goUpDir() {
  const parts = currentPath.value.split('/').filter(Boolean)
  parts.pop()
  fetchFiles('/' + parts.join('/') || '/')
}

function navigateToBreadcrumb(index: number) {
  const parts = currentPath.value.split('/').filter(Boolean)
  fetchFiles('/' + parts.slice(0, index + 1).join('/'))
}

async function saveFile() {
  if (!editingFile.value) return
  fileSaving.value = true
  try {
    await writeFile(editingFile.value, fileContent.value)
    toast.success(t('common.saved'))
  } catch { toast.error(t('common.errorOccurred')) }
  fileSaving.value = false
}

// ---- Docker ----
const containers = ref<any[]>([])
const dockerLoading = ref(false)
let dockerPollTimer: ReturnType<typeof setInterval> | null = null

async function fetchContainers() {
  try {
    const res = await listContainers() as any
    if (res.code === 200) containers.value = res.data || []
  } catch { toast.error(t('common.errorOccurred')) }
  dockerLoading.value = false
}

async function dockerAction(action: string, id: string) {
  try {
    if (action === 'start') await startContainer(id)
    else if (action === 'stop') await stopContainer(id)
    else if (action === 'restart') await restartContainer(id)
    else if (action === 'remove') {
      const ok = await confirmDialog({
        title: t('common.confirm'),
        message: t('servers.docker.confirmRemove'),
        confirmText: t('common.delete'),
        variant: 'danger',
      })
      if (ok) await removeContainer(id)
    }
    await fetchContainers()
  } catch { toast.error(t('common.errorOccurred')) }
}

function containerStatus(state: string) {
  if (state === 'running') return { text: t('servers.docker.running'), class: 'bg-emerald-100 text-emerald-800' }
  if (state === 'exited') return { text: t('servers.docker.exited'), class: 'bg-rose-100 text-rose-800' }
  return { text: state, class: 'bg-amber-100 text-amber-800' }
}

// ---- Firewall ----
const fwRules = ref<any[]>([])
const fwLoading = ref(false)
const showFwForm = ref(false)
const fwForm = ref({ protocol: 'tcp', port: '', action: 'ACCEPT', source: '', comment: '' })

async function fetchFwRules() {
  fwLoading.value = true
  try {
    const res = await listFirewallRules() as any
    if (res.code === 200) fwRules.value = res.data || []
  } catch { toast.error(t('common.errorOccurred')) }
  fwLoading.value = false
}

async function addFwRule() {
  try {
    await addFirewallRule(fwForm.value)
    showFwForm.value = false
    fwForm.value = { protocol: 'tcp', port: '', action: 'ACCEPT', source: '', comment: '' }
    await fetchFwRules()
    toast.success(t('common.created'))
  } catch { toast.error(t('common.errorOccurred')) }
}

async function deleteFwRule(num: string) {
  const ok = await confirmDialog({
    title: t('common.confirm'),
    message: t('servers.firewall.confirmDelete'),
    confirmText: t('common.delete'),
    variant: 'danger',
  })
  if (!ok) return
  try {
    await deleteFirewallRule(num)
    await fetchFwRules()
    toast.success(t('common.deleted'))
  } catch { toast.error(t('common.errorOccurred')) }
}

// ---- Lifecycle ----
onMounted(() => {
  if (activeTab.value === 'terminal') nextTick(() => connectTerminal())
  dockerLoading.value = true
  fetchContainers()
  dockerPollTimer = setInterval(fetchContainers, 10000)
})

onUnmounted(() => {
  if (termWs) termWs.close()
  if (dockerPollTimer) clearInterval(dockerPollTimer)
})

// Load data when switching tabs
watch(activeTab, (tab) => {
  if (tab === 'terminal' && !termInitialized.value) nextTick(() => connectTerminal())
  if (tab === 'files' && files.value.length === 0) fetchFiles(currentPath.value)
  if (tab === 'firewall' && fwRules.value.length === 0) fetchFwRules()
})
</script>

<template>
  <div class="py-2">
    <!-- Header -->
    <div class="mb-8 flex items-center justify-between">
      <div>
        <h1 class="text-3xl font-bold text-slate-800 tracking-tight">{{ $t('servers.title') }}</h1>
        <p class="text-slate-500 mt-1">{{ $t('servers.subtitle') }}</p>
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
            'group inline-flex items-center border-b-2 py-4 px-1 text-sm font-medium transition-colors duration-200'
          ]"
        >
          <component
            :is="tab.icon"
            :class="[
              activeTab === tab.id ? 'text-primary-500' : 'text-slate-400 group-hover:text-slate-500',
              '-ml-0.5 mr-2 h-5 w-5 transition-colors duration-200'
            ]"
          />
          {{ tab.name }}
        </button>
      </nav>
    </div>

    <!-- Tab Contents -->
    <div class="bg-white rounded-2xl shadow-sm border border-slate-100 min-h-[600px] overflow-hidden">

      <!-- Terminal -->
      <div v-if="activeTab === 'terminal'" class="h-[600px] bg-[#1a1b26] flex flex-col">
        <div class="bg-[#2d2d2d] px-4 py-2 border-b border-[#3d3d3d] flex items-center justify-between">
          <div class="flex items-center space-x-2">
            <div class="h-3 w-3 rounded-full bg-rose-500"></div>
            <div class="h-3 w-3 rounded-full bg-amber-500"></div>
            <div class="h-3 w-3 rounded-full bg-emerald-500"></div>
          </div>
          <span class="text-xs text-slate-400 font-mono">{{ termInitialized ? 'shell@' + hostname : $t('servers.terminal.connecting') }}</span>
          <div class="w-16"></div>
        </div>

        <!-- Terminal Canvas (must not be hidden when xterm opens, or cols/rows = 0) -->
        <div class="flex-1 relative">
          <div v-if="termConnecting && !termInitialized" class="absolute inset-0 flex items-center justify-center bg-[#1a1b26] z-10">
            <p class="text-slate-400 text-sm">{{ $t('servers.terminal.connectingToShell') }}</p>
          </div>
          <div ref="terminalEl" class="h-full p-1"></div>
        </div>
      </div>

      <!-- File Explorer -->
      <div v-else-if="activeTab === 'files'" class="p-6">
        <!-- Breadcrumb -->
        <div class="flex items-center space-x-1 text-sm mb-4">
          <button @click="fetchFiles('/')" class="text-primary-600 hover:underline">/</button>
          <template v-for="(part, i) in currentPath.split('/').filter(Boolean)" :key="i">
            <ChevronRightIcon class="h-4 w-4 text-slate-400" />
            <button @click="navigateToBreadcrumb(i)" class="text-primary-600 hover:underline">{{ part }}</button>
          </template>
        </div>

        <!-- File Editor -->
        <div v-if="editingFile" class="mb-4">
          <div class="flex items-center justify-between mb-2">
            <span class="text-sm font-medium text-slate-700">{{ editingFile }}</span>
            <div class="space-x-2">
              <button @click="editingFile = null" class="text-sm text-slate-500 hover:text-slate-700 px-3 py-1 border rounded-lg">{{ $t('common.close') }}</button>
              <button @click="saveFile" :disabled="fileSaving" class="text-sm bg-primary-600 text-white px-3 py-1 rounded-lg hover:bg-primary-700 disabled:opacity-50">
                {{ fileSaving ? $t('servers.files.saving') : $t('common.save') }}
              </button>
            </div>
          </div>
          <textarea v-model="fileContent" class="w-full h-80 font-mono text-sm border border-slate-200 rounded-lg p-3 focus:outline-none focus:ring-2 focus:ring-primary-500"></textarea>
        </div>

        <!-- Directory List -->
        <div v-else>
          <div v-if="currentPath !== '/'" class="flex items-center px-3 py-2 hover:bg-slate-50 rounded-lg cursor-pointer" @click="goUpDir">
            <ArrowUpIcon class="h-5 w-5 text-slate-400 mr-3" />
            <span class="text-sm text-slate-600">..</span>
          </div>

          <div v-if="filesLoading" class="text-sm text-slate-400 text-center py-12">{{ $t('servers.files.loadingFiles') }}</div>

          <div v-else-if="files.length === 0" class="text-sm text-slate-400 text-center py-12">{{ $t('servers.files.emptyDir') }}</div>

          <div v-for="file in files" :key="file.name"
            class="flex items-center px-3 py-2 hover:bg-slate-50 rounded-lg cursor-pointer transition"
            @click="openItem(file)"
          >
            <FolderIcon v-if="file.is_dir" class="h-5 w-5 text-amber-500 mr-3" />
            <DocumentIcon v-else class="h-5 w-5 text-slate-400 mr-3" />
            <span class="text-sm text-slate-800 flex-1">{{ file.name }}</span>
            <span class="text-xs text-slate-400">{{ file.is_dir ? '' : formatSize(file.size) }}</span>
          </div>
        </div>
      </div>

      <!-- Docker Containers -->
      <div v-else-if="activeTab === 'docker'" class="p-6">
        <div class="flex justify-between items-center mb-6">
          <h3 class="text-lg font-medium text-slate-800">{{ $t('servers.docker.title') }}</h3>
          <button @click="fetchContainers" class="text-sm text-slate-500 hover:text-slate-700 px-3 py-1 border rounded-lg">{{ $t('common.refresh') }}</button>
        </div>

        <div v-if="dockerLoading" class="text-sm text-slate-400 text-center py-12">{{ $t('servers.docker.loadingContainers') }}</div>

        <table v-else class="min-w-full divide-y divide-slate-200">
          <thead>
            <tr>
              <th class="py-3 text-left text-xs font-medium text-slate-500 uppercase tracking-wider">{{ $t('servers.docker.container') }}</th>
              <th class="py-3 text-left text-xs font-medium text-slate-500 uppercase tracking-wider">{{ $t('servers.docker.image') }}</th>
              <th class="py-3 text-left text-xs font-medium text-slate-500 uppercase tracking-wider">{{ $t('servers.docker.status') }}</th>
              <th class="py-3 text-right text-xs font-medium text-slate-500 uppercase tracking-wider">{{ $t('common.actions') }}</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-slate-200">
            <tr v-for="c in containers" :key="c.Id" class="hover:bg-slate-50">
              <td class="py-4 whitespace-nowrap text-sm font-medium text-slate-900">{{ (c.Names || [])[0]?.replace(/^\//, '') || c.Id?.slice(0, 12) }}</td>
              <td class="py-4 whitespace-nowrap text-sm text-slate-500">{{ c.Image }}</td>
              <td class="py-4 whitespace-nowrap">
                <span :class="[containerStatus(c.State).class, 'px-2 inline-flex text-xs leading-5 font-semibold rounded-full']">
                  {{ containerStatus(c.State).text }}
                </span>
              </td>
              <td class="py-4 whitespace-nowrap text-right text-sm space-x-2">
                <button v-if="c.State !== 'running'" @click="dockerAction('start', c.Id)" class="text-emerald-600 hover:text-emerald-800">{{ $t('servers.docker.start') }}</button>
                <button v-if="c.State === 'running'" @click="dockerAction('stop', c.Id)" class="text-amber-600 hover:text-amber-800">{{ $t('servers.docker.stop') }}</button>
                <button v-if="c.State === 'running'" @click="dockerAction('restart', c.Id)" class="text-sky-600 hover:text-sky-800">{{ $t('servers.docker.restart') }}</button>
                <button @click="dockerAction('remove', c.Id)" class="text-rose-600 hover:text-rose-800">{{ $t('servers.docker.remove') }}</button>
              </td>
            </tr>
            <tr v-if="containers.length === 0">
              <td colspan="4" class="py-8 text-center text-sm text-slate-400">{{ $t('servers.docker.noContainers') }}</td>
            </tr>
          </tbody>
        </table>
      </div>

      <!-- Firewall -->
      <div v-else-if="activeTab === 'firewall'" class="p-6">
        <div class="flex justify-between items-center mb-6">
          <div>
            <h3 class="text-lg font-medium text-slate-800">{{ $t('servers.firewall.title') }}</h3>
            <p class="text-sm text-slate-500 mt-1">{{ $t('servers.firewall.subtitle') }}</p>
          </div>
          <button @click="showFwForm = !showFwForm" class="text-sm bg-slate-100 hover:bg-slate-200 text-slate-700 px-4 py-2 rounded-lg font-medium flex items-center">
            <PlusIcon class="h-4 w-4 mr-1" /> {{ $t('servers.firewall.addRule') }}
          </button>
        </div>

        <!-- Add Rule Form -->
        <div v-if="showFwForm" class="bg-slate-50 border border-slate-200 rounded-xl p-4 mb-6 grid grid-cols-2 md:grid-cols-5 gap-3">
          <select v-model="fwForm.protocol" class="input-field text-sm">
            <option value="tcp">TCP</option>
            <option value="udp">UDP</option>
            <option value="all">ALL</option>
          </select>
          <input v-model="fwForm.port" :placeholder="$t('servers.firewall.port')" class="input-field text-sm" />
          <select v-model="fwForm.action" class="input-field text-sm">
            <option value="ACCEPT">ACCEPT</option>
            <option value="DROP">DROP</option>
            <option value="REJECT">REJECT</option>
          </select>
          <input v-model="fwForm.source" :placeholder="$t('servers.firewall.sourceIp')" class="input-field text-sm" />
          <button @click="addFwRule" class="bg-primary-600 text-white rounded-lg text-sm hover:bg-primary-700">{{ $t('common.add') }}</button>
        </div>

        <div v-if="fwLoading" class="text-sm text-slate-400 text-center py-12">{{ $t('servers.firewall.loadingRules') }}</div>

        <table v-else class="min-w-full divide-y divide-slate-200">
          <thead>
            <tr>
              <th class="py-3 text-left text-xs font-medium text-slate-500 uppercase">#</th>
              <th class="py-3 text-left text-xs font-medium text-slate-500 uppercase">{{ $t('servers.firewall.target') }}</th>
              <th class="py-3 text-left text-xs font-medium text-slate-500 uppercase">{{ $t('servers.firewall.protocol') }}</th>
              <th class="py-3 text-left text-xs font-medium text-slate-500 uppercase">{{ $t('servers.firewall.source') }}</th>
              <th class="py-3 text-left text-xs font-medium text-slate-500 uppercase">{{ $t('servers.firewall.port') }}</th>
              <th class="py-3 text-right text-xs font-medium text-slate-500 uppercase">{{ $t('common.actions') }}</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-slate-200">
            <tr v-for="rule in fwRules" :key="rule.num" class="hover:bg-slate-50">
              <td class="py-3 text-sm text-slate-500">{{ rule.num }}</td>
              <td class="py-3 text-sm font-medium" :class="rule.target === 'ACCEPT' ? 'text-emerald-600' : rule.target === 'DROP' ? 'text-rose-600' : 'text-amber-600'">{{ rule.target }}</td>
              <td class="py-3 text-sm text-slate-500">{{ rule.protocol }}</td>
              <td class="py-3 text-sm text-slate-500">{{ rule.source }}</td>
              <td class="py-3 text-sm text-slate-500">{{ rule.port || '-' }}</td>
              <td class="py-3 text-right">
                <button @click="deleteFwRule(rule.num)" class="text-rose-600 hover:text-rose-800">
                  <TrashIcon class="h-4 w-4 inline" />
                </button>
              </td>
            </tr>
            <tr v-if="fwRules.length === 0">
              <td colspan="6" class="py-8 text-center text-sm text-slate-400">{{ $t('servers.firewall.noRules') }}</td>
            </tr>
          </tbody>
        </table>
      </div>

    </div>
  </div>
</template>

<script lang="ts">
function formatSize(bytes: number): string {
  if (!bytes) return ''
  const k = 1024
  const sizes = ['B', 'KB', 'MB', 'GB']
  const i = Math.floor(Math.log(bytes) / Math.log(k))
  return parseFloat((bytes / Math.pow(k, i)).toFixed(1)) + ' ' + sizes[i]
}
</script>
