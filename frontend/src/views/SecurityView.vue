<script setup lang="ts">
import { ref } from 'vue'
import { KeyIcon, LockClosedIcon, FingerPrintIcon, ShieldCheckIcon, GlobeAltIcon } from '@heroicons/vue/24/outline'

const securityLogs = ref([
  { id: 1, action: 'Failed Login Attempt', ip: '114.114.114.114', time: '10 mins ago', status: 'Blocked' },
  { id: 2, action: 'Panel Password Changed', ip: '192.168.1.100', time: '2 hours ago', status: 'Success' },
  { id: 3, action: 'API Key Generated', ip: '127.0.0.1', time: '1 day ago', status: 'Success' },
])
</script>

<template>
  <div class="py-2">
    <!-- Header -->
    <div class="mb-8">
      <h1 class="text-3xl font-bold text-slate-800 tracking-tight">Security & Settings</h1>
      <p class="text-slate-500 mt-1">Configure panel security, customize paths, and view audit logs.</p>
    </div>
    
    <div class="grid grid-cols-1 lg:grid-cols-3 gap-8">
      <!-- Left Column: Settings -->
      <div class="lg:col-span-2 space-y-6">
        
        <!-- Panel Settings -->
        <div class="bg-white rounded-2xl shadow-sm border border-slate-100 overflow-hidden">
          <div class="p-6 border-b border-slate-100 flex items-center">
            <div class="bg-indigo-500/10 text-indigo-500 p-2 rounded-lg mr-4">
              <GlobeAltIcon class="h-6 w-6" />
            </div>
            <div>
              <h3 class="text-lg font-medium text-slate-800">Access Configuration</h3>
              <p class="text-sm text-slate-500">Customize how you connect to ZenithPanel</p>
            </div>
          </div>
          <div class="p-6 space-y-4">
            <div>
              <label class="block text-sm font-medium text-slate-700">Security Path Suffix</label>
              <div class="mt-1 flex rounded-md shadow-sm">
                <span class="inline-flex items-center rounded-l-md border border-r-0 border-slate-300 bg-slate-50 px-3 text-slate-500 sm:text-sm">
                  https://ip:port/
                </span>
                <input type="text" value="zenith-secret-path" class="block w-full min-w-0 flex-1 rounded-none rounded-r-md border-slate-300 px-3 py-2 text-slate-900 focus:border-primary-500 focus:ring-primary-500 sm:text-sm" />
              </div>
            </div>
            
            <div class="flex items-center justify-between pt-4 border-t border-slate-100">
              <div>
                <h4 class="text-sm font-medium text-slate-900">API White-list</h4>
                <p class="text-xs text-slate-500">Restrict backend API access to specific IPs.</p>
              </div>
              <button class="bg-slate-100 hover:bg-slate-200 text-slate-700 px-4 py-2 rounded-lg text-sm font-medium transition">Configure</button>
            </div>
          </div>
        </div>
        
        <!-- Authentication Settings -->
        <div class="bg-white rounded-2xl shadow-sm border border-slate-100 overflow-hidden">
          <div class="p-6 border-b border-slate-100 flex items-center">
            <div class="bg-rose-500/10 text-rose-500 p-2 rounded-lg mr-4">
              <KeyIcon class="h-6 w-6" />
            </div>
            <div>
              <h3 class="text-lg font-medium text-slate-800">Authentication</h3>
              <p class="text-sm text-slate-500">Manage your passwords and two-factor auth</p>
            </div>
          </div>
          <div class="p-6 space-y-4">
            <div class="flex items-center justify-between">
              <div class="flex items-center">
                <LockClosedIcon class="h-5 w-5 text-slate-400 mr-3" />
                <div>
                  <h4 class="text-sm font-medium text-slate-900">Panel Password</h4>
                  <p class="text-xs text-slate-500">Last changed 2 hours ago</p>
                </div>
              </div>
              <button class="bg-slate-100 hover:bg-slate-200 text-slate-700 px-4 py-2 rounded-lg text-sm font-medium transition">Change</button>
            </div>
            
            <div class="flex items-center justify-between pt-4 border-t border-slate-100">
              <div class="flex items-center">
                <FingerPrintIcon class="h-5 w-5 text-slate-400 mr-3" />
                <div>
                  <h4 class="text-sm font-medium text-slate-900">Two-Factor Authentication (2FA)</h4>
                  <p class="text-xs text-rose-500 font-medium mt-0.5">Not configured</p>
                </div>
              </div>
              <button class="bg-primary-600 hover:bg-primary-700 text-white px-4 py-2 rounded-lg text-sm font-medium transition">Enable Auth</button>
            </div>
          </div>
        </div>
        
      </div>
      
      <!-- Right Column: Audit Logs -->
      <div class="bg-white rounded-2xl shadow-sm border border-slate-100 overflow-hidden h-full">
        <div class="p-6 border-b border-slate-100 flex justify-between items-center">
          <div class="flex items-center">
            <ShieldCheckIcon class="h-6 w-6 text-emerald-500 mr-2" />
            <h3 class="text-lg font-medium text-slate-800">Security Audit</h3>
          </div>
        </div>
        <div class="p-0">
          <ul class="divide-y divide-slate-100">
            <li v-for="log in securityLogs" :key="log.id" class="p-4 hover:bg-slate-50 transition-colors">
              <div class="flex justify-between items-start">
                <div>
                  <p class="text-sm font-medium text-slate-900">{{ log.action }}</p>
                  <p class="text-xs text-slate-500 mt-1">IP: {{ log.ip }}</p>
                </div>
                <div class="text-right">
                  <span :class="[
                    log.status === 'Success' ? 'text-emerald-600 bg-emerald-50' : 'text-rose-600 bg-rose-50',
                    'px-2 py-1 rounded text-[10px] font-bold uppercase tracking-wider'
                  ]">{{ log.status }}</span>
                  <p class="text-[11px] text-slate-400 mt-2">{{ log.time }}</p>
                </div>
              </div>
            </li>
          </ul>
        </div>
      </div>
      
    </div>
  </div>
</template>
