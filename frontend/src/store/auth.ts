import { defineStore } from 'pinia';
import type { ApiEnvelope, SessionUser } from '@/api/types';

type SessionState = 'unknown' | 'authenticated' | 'anonymous'

// Remove credentials written by older releases. The server now owns the JWT
// in an HttpOnly, SameSite cookie, so JavaScript never needs to read it.
localStorage.removeItem('token')

export const useAuthStore = defineStore('auth', {
  state: () => ({
		sessionState: 'unknown' as SessionState,
		user: null as SessionUser | null,
  }),
  getters: {
		isAuthenticated: (state) => state.sessionState === 'authenticated',
  },
  actions: {
		markAuthenticated(user?: SessionUser) {
			this.sessionState = 'authenticated'
			if (user) this.user = user
    },
		clearSession() {
			this.sessionState = 'anonymous'
      this.user = null;
    },
		async ensureSession() {
			if (this.sessionState !== 'unknown') return this.isAuthenticated
			try {
				const response = await fetch('/api/v1/auth/session', { credentials: 'same-origin' })
				if (!response.ok) {
					this.clearSession()
					return false
				}
				const envelope = await response.json() as ApiEnvelope<{ username: string }>
				this.markAuthenticated({ username: envelope.data?.username || '' })
				return true
			} catch {
				this.clearSession()
				return false
			}
		},
		async logout() {
			try {
				await fetch('/api/v1/auth/logout', { method: 'POST', credentials: 'same-origin' })
			} finally {
				this.clearSession()
			}
		},
  },
});
