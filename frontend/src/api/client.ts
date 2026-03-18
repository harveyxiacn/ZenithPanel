import axios from 'axios';
import { useAuthStore } from '@/store/auth';

const api = axios.create({
  baseURL: '/api',
  timeout: 10000,
});

// Request interceptor for API calls
api.interceptors.request.use(
  (config) => {
    const authStore = useAuthStore();
    if (authStore.token) {
      config.headers['Authorization'] = `Bearer ${authStore.token}`;
    }
    return config;
  },
  (error) => {
    return Promise.reject(error);
  }
);

// Response interceptor for API calls
api.interceptors.response.use(
  (response: any) => response.data,
  async (error: any) => {
    const original = error.config;
    if (error.response?.status === 401 && !original._retry) {
      original._retry = true;
      try {
        const res: any = await api.post('/v1/auth/refresh');
        if (res.token) {
          const authStore = useAuthStore();
          authStore.setToken(res.token);
          original.headers['Authorization'] = `Bearer ${res.token}`;
          return api(original);
        }
      } catch {
        // refresh also failed, logout
      }
      const authStore = useAuthStore();
      authStore.logout();
      window.location.href = '/login';
    }
    return Promise.reject(error);
  }
);

export default api;
