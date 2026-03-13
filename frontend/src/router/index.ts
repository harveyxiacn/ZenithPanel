import { createRouter, createWebHistory } from 'vue-router'
import type { RouteRecordRaw } from 'vue-router'
import SetupWizard from '../views/SetupWizard.vue'
import LoginView from '../views/LoginView.vue'
import DashboardView from '@/views/DashboardView.vue'
import MainLayout from '@/components/layout/MainLayout.vue'
import { useAuthStore } from '@/store/auth'

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
        path: '',
        redirect: '/dashboard'
      },
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
        component: () => import('@/views/ProxyView.vue'),
        props: { defaultTab: 'users' }
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

router.beforeEach((to, _from, next) => {
  const authStore = useAuthStore()
  
  // Minimal auth guard logic
  const isAuthenticated = authStore.isAuthenticated
  
  if (to.meta.requiresAuth && !isAuthenticated) {
    next('/login')
  } else if (to.meta.requiresGuest && isAuthenticated) {
    next('/dashboard')
  } else {
    next()
  }
})

export default router
