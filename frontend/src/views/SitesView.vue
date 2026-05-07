<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { PlusIcon, TrashIcon, GlobeAltIcon, ArrowTopRightOnSquareIcon } from '@heroicons/vue/24/outline'
import { listSites, createSite, deleteSite, toggleSite, issueSiteCert, type Site } from '@/api/sites'
import { useToast } from '@/composables/useToast'
import { useConfirm } from '@/composables/useConfirm'

const { t } = useI18n()
const toast = useToast()
const { confirm: confirmDialog } = useConfirm()

const sites = ref<any[]>([])
const loading = ref(false)
const showForm = ref(false)
const certIssuing = ref<number | null>(null)

const defaultForm = (): Site => ({
  name: '', domain: '', type: 'reverse_proxy',
  upstream_url: '', root_path: '', redirect_url: '',
  tls_mode: 'none', cert_path: '', key_path: '', tls_email: '',
  enable: true,
})
const form = ref<Site>(defaultForm())

async function fetchSites() {
  loading.value = true
  try {
    const res = await listSites() as any
    if (res.code === 200) sites.value = res.data || []
  } catch { toast.error(t('common.errorOccurred')) }
  loading.value = false
}

async function saveSite() {
  try {
    const res = await createSite(form.value) as any
    if (res.code === 200) {
      toast.success(t('common.created'))
      showForm.value = false
      form.value = defaultForm()
      await fetchSites()
    } else toast.error(res.msg)
  } catch (e: any) { toast.error(e?.response?.data?.msg || t('common.errorOccurred')) }
}

async function removeSite(id: number, name: string) {
  const ok = await confirmDialog({
    title: t('common.confirm'),
    message: `Delete site "${name}"?`,
    confirmText: t('common.delete'),
    variant: 'danger',
  })
  if (!ok) return
  try {
    await deleteSite(id)
    await fetchSites()
    toast.success(t('common.deleted'))
  } catch { toast.error(t('common.errorOccurred')) }
}

async function doToggle(id: number) {
  try {
    await toggleSite(id)
    await fetchSites()
  } catch { toast.error(t('common.errorOccurred')) }
}

async function doIssueCert(id: number) {
  certIssuing.value = id
  try {
    const res = await issueSiteCert(id) as any
    if (res.code === 200) {
      toast.success('Certificate issued successfully')
      await fetchSites()
    } else toast.error(res.msg)
  } catch (e: any) { toast.error(e?.response?.data?.msg || 'Certificate issuance failed') }
  certIssuing.value = null
}

function tlsBadge(site: any) {
  if (site.tls_mode === 'none' || !site.tls_mode) return { text: 'HTTP', cls: 'bg-slate-100 text-slate-500' }
  if (site.cert_path) return { text: 'TLS', cls: 'bg-emerald-100 text-emerald-700' }
  return { text: 'TLS pending', cls: 'bg-amber-100 text-amber-700' }
}

function typeLabel(t: string) {
  return { reverse_proxy: 'Reverse Proxy', static: 'Static Files', redirect: 'Redirect' }[t] || t
}

onMounted(fetchSites)
</script>

<template>
  <div class="py-2">
    <div class="mb-6 flex items-start justify-between">
      <div>
        <h1 class="text-3xl font-bold text-slate-800 dark:text-white tracking-tight">Sites</h1>
        <p class="text-slate-500 dark:text-slate-400 mt-1 text-sm">Manage virtual hosts — reverse proxy, static files, and redirects on ports 80/443</p>
      </div>
      <button @click="showForm = !showForm; form = defaultForm()"
        class="bg-primary-600 hover:bg-primary-700 text-white px-4 py-2 rounded-xl text-sm font-medium flex items-center gap-2">
        <PlusIcon class="h-4 w-4" /> Add Site
      </button>
    </div>

    <!-- Create Form -->
    <div v-if="showForm" class="bg-white dark:bg-slate-800 rounded-2xl shadow-sm border border-slate-100 dark:border-slate-700 p-6 mb-6">
      <h3 class="text-base font-semibold text-slate-800 dark:text-slate-100 mb-4">New Site</h3>
      <div class="grid grid-cols-2 gap-4 mb-4">
        <div>
          <label class="text-xs font-medium text-slate-600 dark:text-slate-400">Name *</label>
          <input v-model="form.name" placeholder="my-site" class="input-field text-sm mt-1 w-full" />
        </div>
        <div>
          <label class="text-xs font-medium text-slate-600 dark:text-slate-400">Domain *</label>
          <input v-model="form.domain" placeholder="example.com" class="input-field text-sm mt-1 w-full" />
        </div>
        <div>
          <label class="text-xs font-medium text-slate-600 dark:text-slate-400">Type *</label>
          <select v-model="form.type" class="input-field text-sm mt-1 w-full">
            <option value="reverse_proxy">Reverse Proxy</option>
            <option value="static">Static Files</option>
            <option value="redirect">Redirect</option>
          </select>
        </div>
        <div v-if="form.type === 'reverse_proxy'">
          <label class="text-xs font-medium text-slate-600 dark:text-slate-400">Upstream URL *</label>
          <input v-model="form.upstream_url" placeholder="http://127.0.0.1:3000" class="input-field text-sm mt-1 w-full" />
        </div>
        <div v-if="form.type === 'static'">
          <label class="text-xs font-medium text-slate-600 dark:text-slate-400">Root Path *</label>
          <input v-model="form.root_path" placeholder="/var/www/mysite" class="input-field text-sm mt-1 w-full" />
        </div>
        <div v-if="form.type === 'redirect'">
          <label class="text-xs font-medium text-slate-600 dark:text-slate-400">Redirect URL *</label>
          <input v-model="form.redirect_url" placeholder="https://new.example.com" class="input-field text-sm mt-1 w-full" />
        </div>
      </div>

      <!-- TLS Section -->
      <div class="border border-slate-200 dark:border-slate-700 rounded-xl p-4 mb-4">
        <p class="text-xs font-medium text-slate-600 dark:text-slate-400 mb-3">TLS Configuration</p>
        <div class="flex gap-4 mb-3">
          <label v-for="opt in [{ v: 'none', l: 'None (HTTP)' }, { v: 'acme', l: 'ACME / Let\'s Encrypt' }, { v: 'custom', l: 'Custom Certificate' }]"
            :key="opt.v" class="flex items-center gap-2 text-sm cursor-pointer">
            <input type="radio" v-model="form.tls_mode" :value="opt.v" class="accent-primary-600" />
            {{ opt.l }}
          </label>
        </div>
        <template v-if="form.tls_mode === 'acme'">
          <input v-model="form.tls_email" placeholder="Email for ACME registration" class="input-field text-sm w-full" />
        </template>
        <template v-else-if="form.tls_mode === 'custom'">
          <div class="grid grid-cols-2 gap-3">
            <input v-model="form.cert_path" placeholder="/path/to/cert.pem" class="input-field text-sm" />
            <input v-model="form.key_path" placeholder="/path/to/key.pem" class="input-field text-sm" />
          </div>
        </template>
      </div>

      <div class="flex gap-3">
        <button @click="saveSite" class="bg-primary-600 text-white text-sm px-5 py-2 rounded-xl hover:bg-primary-700">Create</button>
        <button @click="showForm = false" class="text-sm text-slate-600 hover:text-slate-800 px-4 py-2 border border-slate-200 rounded-xl">Cancel</button>
      </div>
    </div>

    <!-- Sites List -->
    <div v-if="loading" class="text-sm text-slate-400 text-center py-16">Loading…</div>

    <div v-else-if="sites.length === 0" class="bg-white dark:bg-slate-800 rounded-2xl border border-slate-100 dark:border-slate-700 p-12 text-center">
      <GlobeAltIcon class="h-12 w-12 text-slate-300 mx-auto mb-3" />
      <p class="text-slate-500 text-sm">No sites configured</p>
      <p class="text-slate-400 text-xs mt-1">Add a site to start serving custom domains on ports 80/443</p>
    </div>

    <div v-else class="grid gap-4">
      <div v-for="site in sites" :key="site.id"
        :class="!site.enable && 'opacity-60'"
        class="bg-white dark:bg-slate-800 rounded-2xl shadow-sm border border-slate-100 dark:border-slate-700 p-5 flex items-start justify-between">
        <div class="flex items-start gap-4">
          <div class="w-10 h-10 rounded-xl bg-sky-500/10 flex items-center justify-center flex-shrink-0">
            <GlobeAltIcon class="h-5 w-5 text-sky-500" />
          </div>
          <div>
            <div class="flex items-center gap-2 flex-wrap">
              <span class="font-semibold text-slate-800 dark:text-slate-100">{{ site.name }}</span>
              <span class="text-sm text-slate-500">{{ site.domain }}</span>
              <span class="text-xs px-2 py-0.5 rounded-full bg-slate-100 dark:bg-slate-700 text-slate-600 dark:text-slate-300">{{ typeLabel(site.type) }}</span>
              <span :class="[tlsBadge(site).cls, 'text-xs px-2 py-0.5 rounded-full font-medium']">{{ tlsBadge(site).text }}</span>
              <span v-if="!site.enable" class="text-xs px-2 py-0.5 rounded-full bg-slate-100 text-slate-400">Disabled</span>
            </div>
            <div class="text-xs text-slate-400 mt-1.5 space-x-3">
              <span v-if="site.upstream_url">→ {{ site.upstream_url }}</span>
              <span v-if="site.root_path">→ {{ site.root_path }}</span>
              <span v-if="site.redirect_url">→ {{ site.redirect_url }}</span>
            </div>
          </div>
        </div>
        <div class="flex items-center gap-2 flex-shrink-0">
          <a v-if="site.enable" :href="`http://${site.domain}`" target="_blank" rel="noopener"
            class="text-slate-400 hover:text-sky-500 transition">
            <ArrowTopRightOnSquareIcon class="h-4 w-4" />
          </a>
          <template v-if="site.tls_mode === 'acme' && !site.cert_path">
            <button @click="doIssueCert(site.id)" :disabled="certIssuing === site.id"
              class="text-xs bg-emerald-50 text-emerald-700 hover:bg-emerald-100 px-3 py-1.5 rounded-lg disabled:opacity-50 transition">
              {{ certIssuing === site.id ? 'Issuing…' : 'Issue Cert' }}
            </button>
          </template>
          <button @click="doToggle(site.id)"
            :class="site.enable ? 'text-amber-600 hover:text-amber-800' : 'text-emerald-600 hover:text-emerald-800'"
            class="text-xs border px-3 py-1.5 rounded-lg transition">
            {{ site.enable ? 'Disable' : 'Enable' }}
          </button>
          <button @click="removeSite(site.id, site.name)" class="text-rose-500 hover:text-rose-700 transition">
            <TrashIcon class="h-4 w-4" />
          </button>
        </div>
      </div>
    </div>
  </div>
</template>
