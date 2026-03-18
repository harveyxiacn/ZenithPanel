<script setup lang="ts">
import { ref, computed } from 'vue'
import { useRouter } from 'vue-router'
import { useAuthStore } from '@/store/auth'
import { useI18n } from 'vue-i18n'
import { setLocale, availableLocales } from '@/i18n'
import {
  HomeIcon,
  ServerIcon,
  ShieldCheckIcon,
  GlobeAltIcon,
  UsersIcon,
  ArrowLeftOnRectangleIcon,
  Bars3Icon,
  XMarkIcon,
  LanguageIcon,
  SunIcon,
  MoonIcon
} from '@heroicons/vue/24/outline'
import { useDarkMode } from '@/composables/useDarkMode'

const { isDark, toggle: toggleDark } = useDarkMode()

const router = useRouter()
const authStore = useAuthStore()
const { t, locale } = useI18n()
const sidebarOpen = ref(false)
const showLangMenu = ref(false)

const navigation = computed(() => [
  { name: t('nav.dashboard'), href: '/dashboard', icon: HomeIcon },
  { name: t('nav.servers'), href: '/servers', icon: ServerIcon },
  { name: t('nav.proxyNodes'), href: '/nodes', icon: GlobeAltIcon },
  { name: t('nav.usersSubs'), href: '/users', icon: UsersIcon },
  { name: t('nav.security'), href: '/security', icon: ShieldCheckIcon },
])

const currentLocaleName = computed(() => {
  return availableLocales.find(l => l.code === locale.value)?.name || 'English'
})

function switchLang(code: string) {
  setLocale(code)
  showLangMenu.value = false
}

const handleLogout = () => {
  authStore.logout()
  router.push('/login')
}

const handleNavClick = () => {
  sidebarOpen.value = false
}
</script>

<template>
  <div class="min-h-screen bg-slate-50 dark:bg-slate-900 flex">
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
          :key="item.href"
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

      <div class="p-4 border-t border-slate-800 space-y-1">
        <!-- Dark Mode Toggle -->
        <button
          @click="toggleDark()"
          class="flex w-full items-center px-4 py-2.5 text-sm font-medium text-slate-400 rounded-xl hover:bg-slate-800 hover:text-white transition-all duration-300"
        >
          <SunIcon v-if="isDark" class="mr-3 h-5 w-5" />
          <MoonIcon v-else class="mr-3 h-5 w-5" />
          {{ isDark ? 'Light Mode' : 'Dark Mode' }}
        </button>

        <!-- Language Switcher -->
        <div class="relative">
          <button
            @click="showLangMenu = !showLangMenu"
            class="flex w-full items-center px-4 py-2.5 text-sm font-medium text-slate-400 rounded-xl hover:bg-slate-800 hover:text-white transition-all duration-300"
          >
            <LanguageIcon class="mr-3 h-5 w-5" />
            {{ currentLocaleName }}
          </button>
          <div
            v-if="showLangMenu"
            class="absolute bottom-full left-0 mb-1 w-full bg-slate-800 rounded-xl border border-slate-700 shadow-lg overflow-hidden"
          >
            <button
              v-for="lang in availableLocales"
              :key="lang.code"
              @click="switchLang(lang.code)"
              :class="[
                'w-full px-4 py-2.5 text-sm text-left transition-colors',
                locale === lang.code ? 'bg-primary-600/20 text-primary-400' : 'text-slate-300 hover:bg-slate-700'
              ]"
            >
              {{ lang.name }}
            </button>
          </div>
        </div>

        <button
          @click="handleLogout"
          class="flex w-full items-center px-4 py-3 text-sm font-medium text-slate-400 rounded-xl hover:bg-slate-800 hover:text-white transition-all duration-300"
        >
          <ArrowLeftOnRectangleIcon class="mr-3 h-5 w-5" />
          {{ $t('nav.logout') }}
        </button>
      </div>
    </div>

    <!-- Main Content -->
    <div class="flex-1 flex flex-col overflow-hidden">
      <!-- Mobile top bar -->
      <div class="md:hidden h-16 bg-white dark:bg-slate-800 border-b border-slate-200 dark:border-slate-700 flex items-center px-4">
        <button @click="sidebarOpen = true" class="text-slate-600 dark:text-slate-300 hover:text-slate-900 dark:hover:text-white">
          <Bars3Icon class="h-6 w-6" />
        </button>
        <span class="ml-4 text-lg font-bold text-slate-800 dark:text-white">ZENITH<span class="text-primary-500">PANEL</span></span>
      </div>

      <main class="flex-1 overflow-x-hidden overflow-y-auto bg-slate-50 dark:bg-slate-900 p-4 md:p-8">
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
