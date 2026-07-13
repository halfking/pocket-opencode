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
const WS_KEY = 'pocket_workspace_id'

export const useAuthStore = defineStore('auth', {
  state: () => ({
    token: localStorage.getItem(TOKEN_KEY) || '',
    user: localStorage.getItem(USER_KEY) || '',
    /** S0-A: 当前 workspace_id（登录后由后端 EnsureDefaultWorkspace 返回）。 */
    workspaceId: localStorage.getItem(WS_KEY) || '',
    /** S0-A: 服务端 user_id（JWT claim）。 */
    userId: '',
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
    /**
     * S0-A 扩展：登录后同时记录 workspace_id + user_id。
     * 后端 /api/auth/login 现在返回 { token, user, user_id, workspace_id }。
     */
    setAuthWithWorkspace(token: string, user: string, userId: string, workspaceId: string) {
      this.setAuth(token, user)
      this.userId = userId
      this.workspaceId = workspaceId
      localStorage.setItem(WS_KEY, workspaceId)
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
      this.workspaceId = ''
      this.userId = ''
      localStorage.removeItem(TOKEN_KEY)
      localStorage.removeItem(USER_KEY)
      localStorage.removeItem(WS_KEY)
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
