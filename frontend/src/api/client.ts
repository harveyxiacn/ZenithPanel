import axios from 'axios';
import type { AxiosError, AxiosRequestConfig } from 'axios';
import { useAuthStore } from '@/store/auth';
import { shouldLogoutOnUnauthorized } from './session-recovery';
import type { ApiEnvelope } from './types';

const api = axios.create({
  baseURL: '/api',
  timeout: 10000,
  withCredentials: true,
});

api.interceptors.response.use(
  (response) => response.data,
  async (error: AxiosError) => {
    const status = error?.response?.status;
    const requestUrl = String(error?.config?.url || '');

    if (shouldLogoutOnUnauthorized(status, requestUrl)) {
      const authStore = useAuthStore();
		authStore.clearSession();
      if (window.location.pathname !== '/login') {
        window.location.href = '/login';
      }
    }

    return Promise.reject(error);
  }
);

// Axios does not propagate a response interceptor's transformed result type.
// Every request below passes through the interceptor above, so it resolves to
// response.data at runtime rather than AxiosResponse<T>.
function unwrapResponseData<T>(request: Promise<unknown>): Promise<T> {
	return request as Promise<T>
}

const apiClient = {
	get<T = ApiEnvelope>(url: string, config?: AxiosRequestConfig): Promise<T> {
		return unwrapResponseData<T>(api.get<T>(url, config))
	},
	post<T = ApiEnvelope, D = unknown>(url: string, data?: D, config?: AxiosRequestConfig<D>): Promise<T> {
		return unwrapResponseData<T>(api.post<T, unknown, D>(url, data, config))
	},
	put<T = ApiEnvelope, D = unknown>(url: string, data?: D, config?: AxiosRequestConfig<D>): Promise<T> {
		return unwrapResponseData<T>(api.put<T, unknown, D>(url, data, config))
	},
	delete<T = ApiEnvelope>(url: string, config?: AxiosRequestConfig): Promise<T> {
		return unwrapResponseData<T>(api.delete<T>(url, config))
	},
}

export default apiClient;
