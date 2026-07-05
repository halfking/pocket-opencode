/**
 * Auth store — replaces the hardcoded admin/admin login.
 *
 * Phase 7 enhancement: Added syncFromStorage() to support runtime localStorage updates
 * and automatic synchronization when storage changes in other tabs/windows.
 *
 * Features:
 * - JWT token persistence via localStorage
 * - Automatic sync when localStorage changes
 * - Support for manual sync (useful for testing/automation)
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
    /** 
     * Phase 7: Sync state from localStorage.
     * Useful for:
     * - Recovering state after page reload
     * - Syncing when localStorage is modified externally (testing/automation)
     * - Cross-tab synchronization
     */
    syncFromStorage() {
      const storedToken = localStorage.getItem(TOKEN_KEY) || ''
      const storedUser = localStorage.getItem(USER_KEY) || ''
      
      // Only update if values actually changed
      if (this.token !== storedToken) {
        this.token = storedToken
      }
      if (this.user !== storedUser) {
        this.user = storedUser
      }
    },
    logout() {
      this.token = ''
      this.user = ''
      localStorage.removeItem(TOKEN_KEY)
      localStorage.removeItem(USER_KEY)
    },
  },
})

/**
 * Phase 7: Setup storage event listener for cross-tab sync
 * This allows the auth state to stay in sync across multiple tabs/windows
 */
if (typeof window !== 'undefined') {
  window.addEventListener('storage', (event) => {
    if (event.key === TOKEN_KEY || event.key === USER_KEY) {
      // Only sync if the store is already initialized
      try {
        const authStore = useAuthStore()
        authStore.syncFromStorage()
      } catch (e) {
        // Store not initialized yet, will sync on first access
        console.debug('Auth store not initialized, skipping storage sync')
      }
    }
  })
}
