import { createRouter, createWebHistory } from 'vue-router'
import type { RouteRecordRaw } from 'vue-router'
import SetupWizard from '../views/SetupWizard.vue'
import LoginView from '../views/LoginView.vue'
import DashboardView from '@/views/DashboardView.vue'
import MainLayout from '@/components/layout/MainLayout.vue'
import { useAuthStore } from '@/store/auth'
import { useUsageProfile } from '@/composables/useUsageProfile'

const routes: RouteRecordRaw[] = [
  {
    path: '/zenith-setup-:suffix',
    name: 'Setup',
    component: SetupWizard,
    meta: { requiresGuest: true }
  },
  {
    path: '/login',
    name: 'Login',
    component: LoginView,
    meta: { requiresGuest: true }
  },
  {
    path: '/',
    component: MainLayout,
    meta: { requiresAuth: true },
    children: [
      {
        path: 'dashboard',
        name: 'Dashboard',
        component: DashboardView
      },
      // Stubs for future pages
      {
        path: 'servers',
        name: 'Servers',
        component: () => import('@/views/ServersView.vue')
      },
      {
        path: 'nodes',
        name: 'ProxyNodes',
        component: () => import('@/views/ProxyView.vue')
      },
      {
        path: 'users',
        name: 'Users',
        component: () => import('@/views/ProxyView.vue')
      },
      {
        path: 'security',
        name: 'Security',
        component: () => import('@/views/SecurityView.vue')
      }
    ]
  }
]

const router = createRouter({
  history: createWebHistory(),
  routes
})

router.beforeEach(async (to) => {
  const authStore = useAuthStore()
  const { homeRoute, loadUsageProfile, usageProfileLoaded } = useUsageProfile()

  const isAuthenticated = authStore.isAuthenticated

  if (to.meta.requiresAuth && !isAuthenticated) {
    return '/login'
  }

  if (isAuthenticated && (to.meta.requiresAuth || to.meta.requiresGuest || to.path === '/') && !usageProfileLoaded.value) {
    await loadUsageProfile()
  }

  if (isAuthenticated && (to.meta.requiresGuest || to.path === '/')) {
    return homeRoute.value
  }

  return true
})

export default router
