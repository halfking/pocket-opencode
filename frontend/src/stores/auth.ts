/**
 * Auth store — replaces the hardcoded admin/admin login.
 *
 * Skeleton: token persistence via localStorage. The actual JWT acquisition
 * (POST /api/auth/login) is wired up in Phase 0; the LoginView currently
 * still writes a flag to localStorage for backward compatibility, so this
 * store falls back to that flag when no token is present.
 */
import { defineStore } from 'pinia'

const TOKEN_KEY = 'pocket_token'
const USER_KEY = 'pocket_user'

export const useAuthStore = defineStore('auth', {
  state: () => ({
    token: localStorage.getItem(TOKEN_KEY) || '',
    user: localStorage.getItem(USER_KEY) || '',
  }),
  getters: {
    isAuthenticated: (s) => Boolean(s.token) && Boolean(s.user),
  },
  actions: {
    /** Store token after a successful login API call. */
    setAuth(token: string, user: string) {
      this.token = token
      this.user = user
      localStorage.setItem(TOKEN_KEY, token)
      localStorage.setItem(USER_KEY, user)
    },
    /** Legacy compatibility: a session exists without a real token yet. */
    setLegacyUser(user: string) {
      this.user = user
      localStorage.setItem(USER_KEY, user)
    },
    logout() {
      this.token = ''
      this.user = ''
      localStorage.removeItem(TOKEN_KEY)
      localStorage.removeItem(USER_KEY)
    },
  },
})
