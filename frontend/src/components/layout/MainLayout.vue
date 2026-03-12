<script setup lang="ts">
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import { useAuthStore } from '@/store/auth'
import {
  HomeIcon,
  ServerIcon,
  ShieldCheckIcon,
  GlobeAltIcon,
  UsersIcon,
  ArrowLeftOnRectangleIcon,
  Bars3Icon,
  XMarkIcon
} from '@heroicons/vue/24/outline'

const router = useRouter()
const authStore = useAuthStore()
const sidebarOpen = ref(false)

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

const handleNavClick = () => {
  sidebarOpen.value = false
}
</script>

<template>
  <div class="min-h-screen bg-slate-50 flex">
    <!-- Mobile overlay -->
    <div
      v-if="sidebarOpen"
      class="fixed inset-0 bg-black/50 z-30 md:hidden"
      @click="sidebarOpen = false"
    ></div>

    <!-- Sidebar -->
    <div
      :class="[
        'fixed inset-y-0 left-0 z-40 w-64 bg-slate-900 text-white flex flex-col transform transition-transform duration-300 md:relative md:translate-x-0',
        sidebarOpen ? 'translate-x-0' : '-translate-x-full'
      ]"
    >
      <div class="h-16 flex items-center px-6 border-b border-slate-800 justify-between">
        <div class="flex items-center">
          <ShieldCheckIcon class="h-8 w-8 text-primary-500 mr-2" />
          <span class="text-xl font-bold tracking-wider text-white">ZENITH<span class="text-primary-500">PANEL</span></span>
        </div>
        <button class="md:hidden text-slate-400 hover:text-white" @click="sidebarOpen = false">
          <XMarkIcon class="h-6 w-6" />
        </button>
      </div>

      <div class="flex-1 py-6 px-4 space-y-1 overflow-y-auto">
        <router-link
          v-for="item in navigation"
          :key="item.name"
          :to="item.href"
          @click="handleNavClick"
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
      <!-- Mobile top bar -->
      <div class="md:hidden h-16 bg-white border-b border-slate-200 flex items-center px-4">
        <button @click="sidebarOpen = true" class="text-slate-600 hover:text-slate-900">
          <Bars3Icon class="h-6 w-6" />
        </button>
        <span class="ml-4 text-lg font-bold text-slate-800">ZENITH<span class="text-primary-500">PANEL</span></span>
      </div>

      <main class="flex-1 overflow-x-hidden overflow-y-auto bg-slate-50 p-4 md:p-8">
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
