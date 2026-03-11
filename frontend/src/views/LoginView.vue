<script setup lang="ts">
import { useRouter } from 'vue-router'

const router = useRouter()

const handleLogin = () => {
  // Demo redirect
  router.push('/dashboard')
}
</script>

<template>
  <div class="min-h-screen flex items-center justify-center bg-slate-50 p-4">
    <div class="glass-panel w-full max-w-md p-8 rounded-2xl shadow-xl bg-white">
      <div class="text-center mb-8">
        <h1 class="text-2xl font-bold text-slate-800 tracking-tight">Login to Zenith</h1>
      </div>
      
      <div class="space-y-4">
        <div>
          <label class="block text-sm font-medium text-slate-600 mb-1">Username</label>
          <input type="text" class="input-field" placeholder="admin" />
        </div>
        <div>
          <label class="block text-sm font-medium text-slate-600 mb-1">Password</label>
          <input type="password" class="input-field" placeholder="••••••••" />
        </div>
        <button @click="handleLogin" class="btn-primary w-full mt-2">Login</button>
      </div>
    </div>
  </div>
</template>
