<script setup lang="ts">
import { ServerStackIcon, CpuChipIcon, ServerIcon, SignalIcon } from '@heroicons/vue/24/outline'

// Mock Data for now
const stats = [
  { name: 'CPU Usage', value: '12%', icon: CpuChipIcon, color: 'text-sky-500', bg: 'bg-sky-500/10' },
  { name: 'Memory', value: '450 MB \/ 1 GB', icon: ServerStackIcon, color: 'text-primary-500', bg: 'bg-primary-500/10' },
  { name: 'Disk I/O', value: '1.2 MB/s', icon: ServerIcon, color: 'text-indigo-500', bg: 'bg-indigo-500/10' },
  { name: 'Proxy Connections', value: '124', icon: SignalIcon, color: 'text-emerald-500', bg: 'bg-emerald-500/10' },
]
</script>

<template>
  <div class="py-2">
    <!-- Header -->
    <div class="mb-8 flex items-center justify-between">
      <div>
        <h1 class="text-3xl font-bold text-slate-800 tracking-tight">System Overview</h1>
        <p class="text-slate-500 mt-1">Real-time performance metrics and proxy status.</p>
      </div>
      <div class="bg-white rounded-full px-4 py-2 shadow-sm border border-slate-100 flex items-center space-x-2">
         <span class="h-2.5 w-2.5 rounded-full bg-emerald-500 animate-pulse"></span>
         <span class="text-sm font-medium text-slate-600">All Systems Operational</span>
      </div>
    </div>
    
    <!-- Stats Grid -->
    <div class="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-6 mb-8">
      <div 
        v-for="(stat, index) in stats" 
        :key="stat.name"
        class="glass-panel p-6 rounded-2xl bg-white hover:shadow-lg transition-all duration-300 transform hover:-translate-y-1"
        :style="{ animationDelay: `${index * 100}ms` }"
      >
        <div class="flex items-center">
          <div :class="[stat.bg, stat.color, 'p-3 rounded-xl mr-4']">
            <component :is="stat.icon" class="h-6 w-6" aria-hidden="true" />
          </div>
          <div>
            <p class="text-sm font-medium text-slate-500">{{ stat.name }}</p>
            <p class="text-2xl font-bold text-slate-800 mt-1">{{ stat.value }}</p>
          </div>
        </div>
      </div>
    </div>
    
    <!-- Large Content Area (e.g., Charts) -->
    <div class="grid grid-cols-1 lg:grid-cols-3 gap-6">
      <div class="lg:col-span-2 glass-panel p-6 rounded-2xl bg-white min-h-[400px]">
        <h3 class="text-lg font-semibold text-slate-800 mb-4 border-b border-slate-100 pb-3">Network Traffic</h3>
        <div class="flex items-center justify-center h-64 text-slate-400">
          [ Traffic Chart Visualization Area ]
        </div>
      </div>
      
      <div class="glass-panel p-6 rounded-2xl bg-white min-h-[400px]">
        <h3 class="text-lg font-semibold text-slate-800 mb-4 border-b border-slate-100 pb-3">Recent Logs</h3>
        <div class="space-y-4">
          <div class="text-sm flex">
            <span class="text-slate-400 w-16">10:42</span>
            <span class="text-slate-600">User 'admin' logged in</span>
          </div>
          <div class="text-sm flex">
            <span class="text-slate-400 w-16">10:40</span>
            <span class="text-emerald-500">Xray Core started successfully</span>
          </div>
          <div class="text-sm flex">
            <span class="text-slate-400 w-16">10:15</span>
            <span class="text-slate-600">Configuration updated</span>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>
