import axios from 'axios';
import { useAuthStore } from '@/store/auth';
import { shouldLogoutOnUnauthorized } from './session-recovery';

const api = axios.create({
  baseURL: '/api',
  timeout: 10000,
});

api.interceptors.request.use(
  (config) => {
    const authStore = useAuthStore();
    if (authStore.token) {
      config.headers['Authorization'] = `Bearer ${authStore.token}`;
    }
    return config;
  },
  (error) => Promise.reject(error)
);

api.interceptors.response.use(
  (response: any) => response.data,
  async (error: any) => {
    const status = error?.response?.status;
    const requestUrl = String(error?.config?.url || '');

    if (shouldLogoutOnUnauthorized(status, requestUrl)) {
      const authStore = useAuthStore();
      authStore.logout();
      if (window.location.pathname !== '/login') {
        window.location.href = '/login';
      }
    }

    return Promise.reject(error);
  }
);

export default api;
