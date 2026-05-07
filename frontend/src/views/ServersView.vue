<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted, watch, nextTick } from 'vue'
import { useI18n } from 'vue-i18n'
import { CommandLineIcon, FolderIcon, WrenchIcon, ShieldCheckIcon, ChevronRightIcon, DocumentIcon, ArrowUpIcon, PlusIcon, TrashIcon } from '@heroicons/vue/24/outline'
import { useAuthStore } from '@/store/auth'
import { listContainers, startContainer, stopContainer, restartContainer, removeContainer, listImages, pullImage, removeImage, getContainerLogs, getContainerStats, runContainer, type RunContainerRequest } from '@/api/docker'
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
const dockerSubTab = ref<'containers' | 'images'>('containers')
const containers = ref<any[]>([])
const images = ref<any[]>([])
const dockerLoading = ref(false)
let dockerPollTimer: ReturnType<typeof setInterval> | null = null

// Logs modal
const logsModalOpen = ref(false)
const logsContainerName = ref('')
const logsContent = ref('')
const logsLoading = ref(false)

// Stats inline
const containerStats = ref<Record<string, any>>({})

// Run container modal
const runModalOpen = ref(false)
const runLoading = ref(false)
const runForm = ref<RunContainerRequest & { portsRaw: string; volumesRaw: string; envRaw: string }>({
  image: '', name: '', restart_policy: 'unless-stopped', network_mode: 'bridge',
  portsRaw: '', volumesRaw: '', envRaw: '', cmd: []
})

// Pull image
const pullImageRef = ref('')
const pullLoading = ref(false)

async function fetchContainers() {
  try {
    const res = await listContainers() as any
    if (res.code === 200) containers.value = res.data || []
  } catch { toast.error(t('common.errorOccurred')) }
  dockerLoading.value = false
}

async function fetchImages() {
  try {
    const res = await listImages() as any
    if (res.code === 200) images.value = res.data || []
  } catch { toast.error(t('common.errorOccurred')) }
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

async function openLogs(id: string, name: string) {
  logsContainerName.value = name
  logsModalOpen.value = true
  logsLoading.value = true
  logsContent.value = ''
  try {
    const res = await getContainerLogs(id, 200) as any
    logsContent.value = res.data || ''
  } catch { logsContent.value = 'Failed to load logs.' }
  logsLoading.value = false
}

async function toggleStats(id: string) {
  if (containerStats.value[id]) {
    delete containerStats.value[id]
    return
  }
  try {
    const res = await getContainerStats(id) as any
    if (res.code === 200) containerStats.value = { ...containerStats.value, [id]: res.data }
  } catch { toast.error(t('common.errorOccurred')) }
}

async function doRunContainer() {
  runLoading.value = true
  try {
    const req: RunContainerRequest = {
      image: runForm.value.image,
      name: runForm.value.name || undefined,
      restart_policy: runForm.value.restart_policy,
      network_mode: runForm.value.network_mode,
      ports: runForm.value.portsRaw ? runForm.value.portsRaw.split('\n').map(s => s.trim()).filter(Boolean) : undefined,
      volumes: runForm.value.volumesRaw ? runForm.value.volumesRaw.split('\n').map(s => s.trim()).filter(Boolean) : undefined,
      env: runForm.value.envRaw ? runForm.value.envRaw.split('\n').map(s => s.trim()).filter(Boolean) : undefined,
    }
    await runContainer(req)
    toast.success('Container started')
    runModalOpen.value = false
    await fetchContainers()
  } catch (e: any) {
    toast.error(e?.response?.data?.msg || t('common.errorOccurred'))
  }
  runLoading.value = false
}

async function doPullImage() {
  if (!pullImageRef.value.trim()) return
  pullLoading.value = true
  try {
    await pullImage(pullImageRef.value.trim())
    toast.success('Image pulled')
    pullImageRef.value = ''
    await fetchImages()
  } catch { toast.error(t('common.errorOccurred')) }
  pullLoading.value = false
}

async function doRemoveImage(id: string) {
  const ok = await confirmDialog({
    title: t('common.confirm'),
    message: 'Remove this image?',
    confirmText: t('common.delete'),
    variant: 'danger',
  })
  if (!ok) return
  try {
    await removeImage(id)
    await fetchImages()
    toast.success(t('common.deleted'))
  } catch { toast.error(t('common.errorOccurred')) }
}

function containerStatus(state: string) {
  if (state === 'running') return { text: t('servers.docker.running'), class: 'bg-emerald-100 text-emerald-800 dark:bg-emerald-900 dark:text-emerald-200' }
  if (state === 'exited') return { text: t('servers.docker.exited'), class: 'bg-rose-100 text-rose-800 dark:bg-rose-900 dark:text-rose-200' }
  return { text: state, class: 'bg-amber-100 text-amber-800 dark:bg-amber-900 dark:text-amber-200' }
}

function formatImageSize(size: number) {
  if (!size) return '—'
  const mb = size / 1024 / 1024
  return mb > 1000 ? (mb / 1024).toFixed(1) + ' GB' : mb.toFixed(0) + ' MB'
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
  fetchImages()
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

      <!-- Docker Containers & Images -->
      <div v-else-if="activeTab === 'docker'" class="p-6">
        <!-- Sub-tabs -->
        <div class="flex items-center justify-between mb-4">
          <div class="flex gap-2">
            <button @click="dockerSubTab = 'containers'"
              :class="dockerSubTab === 'containers' ? 'bg-primary-600 text-white' : 'bg-slate-100 text-slate-600 hover:bg-slate-200'"
              class="px-4 py-1.5 rounded-lg text-sm font-medium transition">
              Containers
            </button>
            <button @click="dockerSubTab = 'images'; fetchImages()"
              :class="dockerSubTab === 'images' ? 'bg-primary-600 text-white' : 'bg-slate-100 text-slate-600 hover:bg-slate-200'"
              class="px-4 py-1.5 rounded-lg text-sm font-medium transition">
              Images
            </button>
          </div>
          <div class="flex gap-2">
            <button v-if="dockerSubTab === 'containers'" @click="runModalOpen = true"
              class="text-sm bg-primary-600 text-white px-3 py-1.5 rounded-lg hover:bg-primary-700 flex items-center gap-1">
              <PlusIcon class="h-4 w-4" /> Run
            </button>
            <button @click="fetchContainers(); fetchImages()"
              class="text-sm text-slate-500 hover:text-slate-700 px-3 py-1 border rounded-lg">
              {{ $t('common.refresh') }}
            </button>
          </div>
        </div>

        <!-- Containers sub-tab -->
        <template v-if="dockerSubTab === 'containers'">
          <div v-if="dockerLoading" class="text-sm text-slate-400 text-center py-12">{{ $t('servers.docker.loadingContainers') }}</div>
          <table v-else class="min-w-full divide-y divide-slate-200 dark:divide-slate-700">
            <thead>
              <tr>
                <th class="py-3 text-left text-xs font-medium text-slate-500 uppercase tracking-wider">{{ $t('servers.docker.container') }}</th>
                <th class="py-3 text-left text-xs font-medium text-slate-500 uppercase tracking-wider">{{ $t('servers.docker.image') }}</th>
                <th class="py-3 text-left text-xs font-medium text-slate-500 uppercase tracking-wider">{{ $t('servers.docker.status') }}</th>
                <th class="py-3 text-right text-xs font-medium text-slate-500 uppercase tracking-wider">{{ $t('common.actions') }}</th>
              </tr>
            </thead>
            <tbody class="divide-y divide-slate-200 dark:divide-slate-700">
              <template v-for="c in containers" :key="c.Id">
                <tr class="hover:bg-slate-50 dark:hover:bg-slate-800/50">
                  <td class="py-3 text-sm font-medium text-slate-900 dark:text-slate-100">{{ (c.Names || [])[0]?.replace(/^\//, '') || c.Id?.slice(0, 12) }}</td>
                  <td class="py-3 text-sm text-slate-500 dark:text-slate-400 max-w-[200px] truncate">{{ c.Image }}</td>
                  <td class="py-3">
                    <span :class="[containerStatus(c.State).class, 'px-2 inline-flex text-xs leading-5 font-semibold rounded-full']">
                      {{ containerStatus(c.State).text }}
                    </span>
                  </td>
                  <td class="py-3 text-right text-sm space-x-1">
                    <button @click="toggleStats(c.Id)" class="text-slate-500 hover:text-slate-700 text-xs border px-2 py-0.5 rounded">
                      {{ containerStats[c.Id] ? '▲' : 'Stats' }}
                    </button>
                    <button @click="openLogs(c.Id, (c.Names || [])[0]?.replace(/^\//, '') || c.Id?.slice(0, 12))" class="text-sky-600 hover:text-sky-800 text-xs">Logs</button>
                    <button v-if="c.State !== 'running'" @click="dockerAction('start', c.Id)" class="text-emerald-600 hover:text-emerald-800 text-xs">{{ $t('servers.docker.start') }}</button>
                    <button v-if="c.State === 'running'" @click="dockerAction('stop', c.Id)" class="text-amber-600 hover:text-amber-800 text-xs">{{ $t('servers.docker.stop') }}</button>
                    <button v-if="c.State === 'running'" @click="dockerAction('restart', c.Id)" class="text-indigo-600 hover:text-indigo-800 text-xs">{{ $t('servers.docker.restart') }}</button>
                    <button @click="dockerAction('remove', c.Id)" class="text-rose-600 hover:text-rose-800 text-xs">{{ $t('servers.docker.remove') }}</button>
                  </td>
                </tr>
                <!-- Stats inline row -->
                <tr v-if="containerStats[c.Id]" :key="c.Id + '-stats'" class="bg-slate-50 dark:bg-slate-800/30">
                  <td colspan="4" class="px-4 py-2 text-xs text-slate-500 flex gap-6">
                    <span>CPU: <strong>{{ containerStats[c.Id].cpu_percent?.toFixed(1) }}%</strong></span>
                    <span>Memory: <strong>{{ containerStats[c.Id].memory_usage_mb?.toFixed(0) }} MB</strong> / {{ containerStats[c.Id].memory_limit_mb?.toFixed(0) }} MB</span>
                  </td>
                </tr>
              </template>
              <tr v-if="containers.length === 0">
                <td colspan="4" class="py-8 text-center text-sm text-slate-400">{{ $t('servers.docker.noContainers') }}</td>
              </tr>
            </tbody>
          </table>
        </template>

        <!-- Images sub-tab -->
        <template v-else-if="dockerSubTab === 'images'">
          <div class="flex gap-2 mb-4">
            <input v-model="pullImageRef" placeholder="nginx:latest" class="input-field text-sm flex-1" @keyup.enter="doPullImage" />
            <button @click="doPullImage" :disabled="pullLoading" class="bg-primary-600 text-white px-4 py-2 rounded-lg text-sm hover:bg-primary-700 disabled:opacity-50">
              {{ pullLoading ? 'Pulling…' : 'Pull' }}
            </button>
          </div>
          <table class="min-w-full divide-y divide-slate-200 dark:divide-slate-700">
            <thead>
              <tr>
                <th class="py-3 text-left text-xs font-medium text-slate-500 uppercase">Repository</th>
                <th class="py-3 text-left text-xs font-medium text-slate-500 uppercase">Tag</th>
                <th class="py-3 text-left text-xs font-medium text-slate-500 uppercase">Size</th>
                <th class="py-3 text-right text-xs font-medium text-slate-500 uppercase">{{ $t('common.actions') }}</th>
              </tr>
            </thead>
            <tbody class="divide-y divide-slate-200 dark:divide-slate-700">
              <tr v-for="img in images" :key="img.Id" class="hover:bg-slate-50 dark:hover:bg-slate-800/50">
                <td class="py-3 text-sm text-slate-800 dark:text-slate-200 max-w-[240px] truncate">
                  {{ (img.RepoTags || ['&lt;none&gt;'])[0]?.split(':')[0] }}
                </td>
                <td class="py-3 text-sm text-slate-500 dark:text-slate-400">{{ (img.RepoTags || ['&lt;none&gt;:none'])[0]?.split(':')[1] }}</td>
                <td class="py-3 text-sm text-slate-500 dark:text-slate-400">{{ formatImageSize(img.Size) }}</td>
                <td class="py-3 text-right">
                  <button @click="doRemoveImage(img.Id)" class="text-rose-600 hover:text-rose-800 text-xs">{{ $t('servers.docker.remove') }}</button>
                </td>
              </tr>
              <tr v-if="images.length === 0">
                <td colspan="4" class="py-8 text-center text-sm text-slate-400">No images</td>
              </tr>
            </tbody>
          </table>
        </template>
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

  <!-- Logs modal -->
  <Teleport to="body">
    <div v-if="logsModalOpen" class="fixed inset-0 bg-black/60 z-50 flex items-center justify-center p-4">
      <div class="bg-white dark:bg-slate-900 rounded-xl shadow-2xl w-full max-w-3xl max-h-[80vh] flex flex-col">
        <div class="flex items-center justify-between px-5 py-3 border-b border-slate-200 dark:border-slate-700">
          <span class="font-medium text-slate-800 dark:text-slate-100 text-sm">Logs: {{ logsContainerName }}</span>
          <button @click="logsModalOpen = false" class="text-slate-400 hover:text-slate-600 text-lg">✕</button>
        </div>
        <div class="flex-1 overflow-auto p-4 bg-slate-950 rounded-b-xl">
          <div v-if="logsLoading" class="text-slate-400 text-sm text-center py-8">Loading…</div>
          <pre v-else class="text-xs text-green-300 font-mono whitespace-pre-wrap break-all">{{ logsContent }}</pre>
        </div>
      </div>
    </div>
  </Teleport>

  <!-- Run container modal -->
  <Teleport to="body">
    <div v-if="runModalOpen" class="fixed inset-0 bg-black/60 z-50 flex items-center justify-center p-4">
      <div class="bg-white dark:bg-slate-900 rounded-xl shadow-2xl w-full max-w-lg">
        <div class="flex items-center justify-between px-5 py-4 border-b border-slate-200 dark:border-slate-700">
          <span class="font-semibold text-slate-800 dark:text-slate-100">Run Container</span>
          <button @click="runModalOpen = false" class="text-slate-400 hover:text-slate-600 text-lg">✕</button>
        </div>
        <div class="p-5 space-y-3">
          <div>
            <label class="text-xs font-medium text-slate-600 dark:text-slate-400">Image *</label>
            <input v-model="runForm.image" placeholder="nginx:latest" class="input-field text-sm mt-1 w-full" />
          </div>
          <div>
            <label class="text-xs font-medium text-slate-600 dark:text-slate-400">Container Name</label>
            <input v-model="runForm.name" placeholder="my-nginx" class="input-field text-sm mt-1 w-full" />
          </div>
          <div class="grid grid-cols-2 gap-3">
            <div>
              <label class="text-xs font-medium text-slate-600 dark:text-slate-400">Restart Policy</label>
              <select v-model="runForm.restart_policy" class="input-field text-sm mt-1 w-full">
                <option value="no">no</option>
                <option value="always">always</option>
                <option value="unless-stopped">unless-stopped</option>
                <option value="on-failure">on-failure</option>
              </select>
            </div>
            <div>
              <label class="text-xs font-medium text-slate-600 dark:text-slate-400">Network</label>
              <select v-model="runForm.network_mode" class="input-field text-sm mt-1 w-full">
                <option value="bridge">bridge</option>
                <option value="host">host</option>
              </select>
            </div>
          </div>
          <div>
            <label class="text-xs font-medium text-slate-600 dark:text-slate-400">Port Mappings (one per line: 8080:80/tcp)</label>
            <textarea v-model="runForm.portsRaw" rows="2" class="input-field text-sm mt-1 w-full font-mono" />
          </div>
          <div>
            <label class="text-xs font-medium text-slate-600 dark:text-slate-400">Volumes (one per line: /host:/container)</label>
            <textarea v-model="runForm.volumesRaw" rows="2" class="input-field text-sm mt-1 w-full font-mono" />
          </div>
          <div>
            <label class="text-xs font-medium text-slate-600 dark:text-slate-400">Environment Variables (one per line: KEY=VALUE)</label>
            <textarea v-model="runForm.envRaw" rows="2" class="input-field text-sm mt-1 w-full font-mono" />
          </div>
        </div>
        <div class="px-5 py-4 border-t border-slate-200 dark:border-slate-700 flex justify-end gap-3">
          <button @click="runModalOpen = false" class="text-sm text-slate-600 hover:text-slate-800 px-4 py-2 border rounded-lg">Cancel</button>
          <button @click="doRunContainer" :disabled="runLoading || !runForm.image"
            class="text-sm bg-primary-600 text-white px-4 py-2 rounded-lg hover:bg-primary-700 disabled:opacity-50">
            {{ runLoading ? 'Starting…' : 'Run' }}
          </button>
        </div>
      </div>
    </div>
  </Teleport>
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
