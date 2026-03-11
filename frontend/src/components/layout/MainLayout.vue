<script setup lang="ts">
import { useRouter } from 'vue-router'
import { useAuthStore } from '@/store/auth'
import { 
  HomeIcon, 
  ServerIcon, 
  ShieldCheckIcon, 
  GlobeAltIcon, 
  UsersIcon, 
  ArrowLeftOnRectangleIcon 
} from '@heroicons/vue/24/outline'

const router = useRouter()
const authStore = useAuthStore()

const navigation = [
  { name: 'Dashboard', href: '/dashboard', icon: HomeIcon },
  { name: 'Servers & Files', href: '/servers', icon: ServerIcon },
  { name: 'Proxy Nodes', href: '/nodes', icon: GlobeAltIcon },
  { name: 'Users & Subs', href: '/users', icon: UsersIcon },
  { name: 'Security', href: '/security', icon: ShieldCheckIcon },
]

const handleLogout = () => {
  authStore.logout()
  router.push('/login')
}
</script>

<template>
  <div class="min-h-screen bg-slate-50 flex">
    <!-- Sidebar -->
    <div class="w-64 bg-slate-900 text-white flex flex-col">
      <div class="h-16 flex items-center px-6 border-b border-slate-800">
        <ShieldCheckIcon class="h-8 w-8 text-primary-500 mr-2" />
        <span class="text-xl font-bold tracking-wider text-white">ZENITH<span class="text-primary-500">PANEL</span></span>
      </div>
      
      <div class="flex-1 py-6 px-4 space-y-1 overflow-y-auto">
        <router-link
          v-for="item in navigation"
          :key="item.name"
          :to="item.href"
          class="flex items-center px-4 py-3 text-sm font-medium rounded-xl transition-all duration-300 group"
          :class="[$route.path.startsWith(item.href) ? 'bg-primary-600/10 text-primary-400' : 'text-slate-400 hover:bg-slate-800 hover:text-white']"
        >
          <component 
            :is="item.icon" 
            class="mr-3 h-5 w-5 transition-transform duration-300 group-hover:scale-110" 
            :class="[$route.path.startsWith(item.href) ? 'text-primary-400' : 'text-slate-500 group-hover:text-white']" 
            aria-hidden="true" 
          />
          {{ item.name }}
        </router-link>
      </div>
      
      <div class="p-4 border-t border-slate-800">
        <button 
          @click="handleLogout"
          class="flex w-full items-center px-4 py-3 text-sm font-medium text-slate-400 rounded-xl hover:bg-slate-800 hover:text-white transition-all duration-300"
        >
          <ArrowLeftOnRectangleIcon class="mr-3 h-5 w-5" />
          Logout
        </button>
      </div>
    </div>

    <!-- Main Content -->
    <div class="flex-1 flex flex-col overflow-hidden">
      <!-- Top header could go here -->
      <main class="flex-1 overflow-x-hidden overflow-y-auto bg-slate-50 p-8">
        <div class="max-w-7xl mx-auto">
          <router-view v-slot="{ Component }">
            <transition name="fade" mode="out-in">
              <component :is="Component" />
            </transition>
          </router-view>
        </div>
      </main>
    </div>
  </div>
</template>

<style scoped>
.fade-enter-active,
.fade-leave-active {
  transition: opacity 0.3s ease, transform 0.3s ease;
}

.fade-enter-from {
  opacity: 0;
  transform: translateY(10px);
}

.fade-leave-to {
  opacity: 0;
  transform: translateY(-10px);
}
</style>
