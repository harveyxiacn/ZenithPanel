<script setup lang="ts">
import { ref } from 'vue'
import { PlusIcon, TrashIcon, ArrowPathIcon } from '@heroicons/vue/24/outline'

const activeTab = ref('inbounds')

const tabs = [
  { id: 'inbounds', name: 'Inbound Nodes' },
  { id: 'routing', name: 'Routing Engine' },
  { id: 'users', name: 'User & Subs' },
]

// Mock nodes for visual layout
const nodes = ref([
  { id: 1, tag: 'vless-xtls-reality', protocol: 'vless', port: 443, status: 'Active' },
  { id: 2, tag: 'vmess-ws-tls', protocol: 'vmess', port: 8443, status: 'Active' },
  { id: 3, tag: 'hysteria2-udp', protocol: 'hysteria2', port: 4430, status: 'Disabled' },
])

// Mock users for layout
const users = ref([
  { id: 1, email: 'admin@zenith.local', uuid: 'e010c2fc-...', traffic: '1.2 GB / 50 GB', status: 'Active' },
  { id: 2, email: 'guest1', uuid: 'f87a8b...', traffic: '500 MB / 10 GB', status: 'Active' },
])
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
      
      <!-- Inbounds List -->
      <div v-if="activeTab === 'inbounds'" class="flex flex-col h-full">
        <div class="p-6 border-b border-slate-100 flex justify-between items-center">
          <h3 class="text-lg font-medium text-slate-800">Inbound Listeners</h3>
          <button class="text-sm bg-slate-100 hover:bg-slate-200 text-slate-700 px-4 py-2 rounded-lg font-medium transition flex items-center">
            <PlusIcon class="h-4 w-4 mr-1" /> Add Node
          </button>
        </div>
        
        <table class="min-w-full divide-y divide-slate-200">
          <thead class="bg-slate-50">
            <tr>
              <th scope="col" class="px-6 py-3 text-left text-xs font-medium text-slate-500 uppercase tracking-wider">Tag</th>
              <th scope="col" class="px-6 py-3 text-left text-xs font-medium text-slate-500 uppercase tracking-wider">Protocol</th>
              <th scope="col" class="px-6 py-3 text-left text-xs font-medium text-slate-500 uppercase tracking-wider">Port</th>
              <th scope="col" class="px-6 py-3 text-left text-xs font-medium text-slate-500 uppercase tracking-wider">Status</th>
              <th scope="col" class="relative px-6 py-3"><span class="sr-only">Actions</span></th>
            </tr>
          </thead>
          <tbody class="bg-white divide-y divide-slate-200">
            <tr v-for="node in nodes" :key="node.id" class="hover:bg-slate-50 transition-colors">
              <td class="px-6 py-4 whitespace-nowrap text-sm font-medium text-slate-900">{{ node.tag }}</td>
              <td class="px-6 py-4 whitespace-nowrap text-sm text-slate-500">
                <span class="inline-flex items-center px-2.5 py-0.5 rounded-full text-xs font-medium bg-blue-100 text-blue-800">
                  {{ node.protocol.toUpperCase() }}
                </span>
              </td>
              <td class="px-6 py-4 whitespace-nowrap text-sm text-slate-500">{{ node.port }}</td>
              <td class="px-6 py-4 whitespace-nowrap">
                <span :class="[
                  node.status === 'Active' ? 'bg-emerald-100 text-emerald-800' : 'bg-slate-100 text-slate-800',
                  'px-2 inline-flex text-xs leading-5 font-semibold rounded-full'
                ]">{{ node.status }}</span>
              </td>
              <td class="px-6 py-4 whitespace-nowrap text-right text-sm font-medium">
                <button class="text-primary-600 hover:text-primary-900 mr-4">Edit</button>
                <button class="text-rose-600 hover:text-rose-900">
                  <TrashIcon class="h-4 w-4 inline" />
                </button>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
      
      <!-- Routing Placeholder -->
      <div v-else-if="activeTab === 'routing'" class="p-6">
        <h3 class="text-lg font-medium text-slate-800 mb-4">Routing Chains & Chains</h3>
        <p class="text-sm text-slate-500 mb-6">Design advanced routing policies to direct specific traffic to dedicated outbound nodes.</p>
        <div class="h-64 border-2 border-dashed border-slate-200 rounded-xl flex items-center justify-center bg-slate-50 text-slate-400">
           [ Visual Node Router Canvas ]
        </div>
      </div>
      
      <!-- Users & Subs Placeholder -->
      <div v-else-if="activeTab === 'users'" class="flex flex-col h-full">
         <div class="p-6 border-b border-slate-100 flex justify-between items-center">
          <h3 class="text-lg font-medium text-slate-800">Client Management</h3>
          <button class="text-sm bg-slate-100 hover:bg-slate-200 text-slate-700 px-4 py-2 rounded-lg font-medium transition flex items-center">
            <PlusIcon class="h-4 w-4 mr-1" /> Add Client
          </button>
        </div>
        <table class="min-w-full divide-y divide-slate-200">
          <thead class="bg-slate-50">
            <tr>
              <th scope="col" class="px-6 py-3 text-left text-xs font-medium text-slate-500 uppercase tracking-wider">Email</th>
              <th scope="col" class="px-6 py-3 text-left text-xs font-medium text-slate-500 uppercase tracking-wider">Traffic</th>
              <th scope="col" class="relative px-6 py-3"><span class="sr-only">Actions</span></th>
            </tr>
          </thead>
          <tbody class="bg-white divide-y divide-slate-200">
            <tr v-for="user in users" :key="user.id">
              <td class="px-6 py-4 whitespace-nowrap text-sm font-medium text-slate-900">{{ user.email }}</td>
              <td class="px-6 py-4 whitespace-nowrap text-sm text-slate-500">{{ user.traffic }}</td>
              <td class="px-6 py-4 whitespace-nowrap text-right text-sm font-medium">
                <button class="text-emerald-600 hover:text-emerald-900">Get Sub Link</button>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
      
    </div>
  </div>
</template>
