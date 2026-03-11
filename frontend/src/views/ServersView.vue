<script setup lang="ts">
import { ref } from 'vue'
import { CommandLineIcon, FolderIcon, WrenchIcon, ShieldCheckIcon } from '@heroicons/vue/24/outline'

const activeTab = ref('terminal')

const tabs = [
  { id: 'terminal', name: 'Web Terminal', icon: CommandLineIcon },
  { id: 'files', name: 'File Explorer', icon: FolderIcon },
  { id: 'docker', name: 'Containers', icon: WrenchIcon },
  { id: 'firewall', name: 'Firewall', icon: ShieldCheckIcon },
]
</script>

<template>
  <div class="py-2">
    <!-- Header -->
    <div class="mb-8 flex items-center justify-between">
      <div>
        <h1 class="text-3xl font-bold text-slate-800 tracking-tight">Servers & Files</h1>
        <p class="text-slate-500 mt-1">Manage underlying VPS, file system, and Docker engine.</p>
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
            aria-hidden="true"
          />
          {{ tab.name }}
        </button>
      </nav>
    </div>
    
    <!-- Tab Contents -->
    <div class="bg-white rounded-2xl shadow-sm border border-slate-100 min-h-[600px] overflow-hidden">
      <!-- Terminal -->
      <div v-if="activeTab === 'terminal'" class="h-full bg-[#1e1e1e] flex flex-col">
        <div class="bg-[#2d2d2d] px-4 py-2 border-b border-[#3d3d3d] flex items-center justify-between">
          <div class="flex items-center space-x-2">
             <div class="h-3 w-3 rounded-full bg-rose-500"></div>
             <div class="h-3 w-3 rounded-full bg-amber-500"></div>
             <div class="h-3 w-3 rounded-full bg-emerald-500"></div>
          </div>
          <span class="text-xs text-slate-400 font-mono">root@zenith:~</span>
          <div class="w-16"></div> <!-- Spacer for balance -->
        </div>
        <div class="flex-1 p-4 font-mono text-sm text-green-400 overflow-y-auto">
          <p>ZenithPanel Web Terminal</p>
          <p>Connecting to ssh://127.0.0.1:22...</p>
          <p class="text-slate-400 animate-pulse mt-2">_</p>
          <!-- Future implementation: import 'xterm' and mount here -->
        </div>
      </div>
      
      <!-- File Explorer Placeholder -->
      <div v-else-if="activeTab === 'files'" class="p-6">
        <h3 class="text-lg font-medium text-slate-800">File Explorer</h3>
        <p class="text-sm text-slate-500 mt-2">Root directory access will be rendered here.</p>
        <div class="mt-6 border-2 border-dashed border-slate-200 rounded-xl h-64 flex flex-col items-center justify-center text-slate-400">
           <FolderIcon class="h-12 w-12 mb-3 text-slate-300" />
           <span>Directory list loading...</span>
        </div>
      </div>
      
      <!-- Docker Containers Placeholder -->
      <div v-else-if="activeTab === 'docker'" class="p-6">
        <div class="flex justify-between items-center mb-6">
          <h3 class="text-lg font-medium text-slate-800">Docker Engine</h3>
          <button class="bg-primary-600 text-white px-4 py-2 rounded-lg text-sm hover:bg-primary-700 transition">Deploy Context</button>
        </div>
        <table class="min-w-full divide-y divide-slate-200">
          <thead>
            <tr>
              <th scope="col" class="py-3 text-left text-xs font-medium text-slate-500 uppercase tracking-wider">Container</th>
              <th scope="col" class="py-3 text-left text-xs font-medium text-slate-500 uppercase tracking-wider">Image</th>
              <th scope="col" class="py-3 text-left text-xs font-medium text-slate-500 uppercase tracking-wider">Status</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-slate-200">
            <tr>
              <td class="py-4 whitespace-nowrap text-sm font-medium text-slate-900">watchtower</td>
              <td class="py-4 whitespace-nowrap text-sm text-slate-500">containrrr/watchtower</td>
              <td class="py-4 whitespace-nowrap">
                <span class="px-2 inline-flex text-xs leading-5 font-semibold rounded-full bg-emerald-100 text-emerald-800">Running</span>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
      
      <!-- Firewall Placeholder -->
      <div v-else-if="activeTab === 'firewall'" class="p-6">
        <h3 class="text-lg font-medium text-slate-800">Firewall Rules</h3>
        <p class="text-sm text-slate-500 mt-2">Manage open ports and Iptables.</p>
      </div>
      
    </div>
  </div>
</template>
