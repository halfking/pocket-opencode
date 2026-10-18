/**
 * Auth store — replaces the hardcoded admin/admin login.
 *
 * Phase 7 enhancement: Added syncFromStorage() to support runtime localStorage updates
 * and automatic synchronization when storage changes in other tabs/windows.
 *
 * Phase 1 RedClaw switch (2026-09):
 *  - Token is now issued by RedClaw Admin (passed through openpocket backend)
 *  - logout() calls backend /api/auth/logout to revoke RedClaw session,
 *    then clears localStorage (idempotent if token already invalid)
 *  - authMethod records the login path (password / sso / dev-bypass / legacy)
 */
import { defineStore } from 'pinia'
import { logoutRemote, refreshAuth } from '../api/auth.ts'

const TOKEN_KEY = 'pocket_token'
const USER_KEY = 'pocket_user'
const WS_KEY = 'pocket_workspace_id'
const METHOD_KEY = 'pocket_auth_method'

/** token 剩余寿命低于该阈值（ms）时主动续期。 */
const REFRESH_AHEAD_MS = 5 * 60 * 1000

/**
 * JWT 滑动续期的单飞（single-flight）句柄：并发 401/临期只触发一次
 * POST /api/auth/refresh，其余调用共享同一 Promise。放在模块级避免进
 * Pinia state（不可序列化）。
 */
let refreshInFlight: Promise<boolean> | null = null

/** 解析 JWT payload 的 exp（秒→ms）；非 JWT / 无 exp 返回 null。 */
export function tokenExpiresAtMs(token: string): number | null {
  try {
    const payload = token.split('.')[1]
    if (!payload) return null
    const json = atob(payload.replace(/-/g, '+').replace(/_/g, '/'))
    const exp = JSON.parse(json)?.exp
    return typeof exp === 'number' ? exp * 1000 : null
  } catch {
    return null
  }
}

export const useAuthStore = defineStore('auth', {
  state: () => ({
    token: localStorage.getItem(TOKEN_KEY) || '',
    user: localStorage.getItem(USER_KEY) || '',
    /** S0-A: 当前 workspace_id（登录后由后端 EnsureDefaultWorkspace 返回）。 */
    workspaceId: localStorage.getItem(WS_KEY) || '',
    /** S0-A: 服务端 user_id（JWT claim）。 */
    userId: '',
    /** Phase 1: 登录方式（password / sso / dev-bypass / legacy），仅用于埋点。 */
    authMethod: localStorage.getItem(METHOD_KEY) || '',
    /**
     * ⚠§867/D33：会话**没能**写入本地存储。
     * 为 true 时，内存里是登录态而存储里没有 ⇒ 刷新/重启后会被静默登出。
     * 只置位 + warn，界面出口属产品口径（简报第 86 条）。
     */
    sessionPersistFailed: false,
    /**
     * ⚠§870/D39：**删除**失败与写入失败同样要可见。
     * 为 true 时，「退出登录」没有真的把会话从本地存储里删掉。
     */
    sessionClearFailed: false,
    /** 未删掉的键名（可诊断用；界面出口属产品口径，简报第 89 条）。 */
    sessionClearMissing: [] as string[],
  }),
  getters: {
    isAuthenticated: (s) => Boolean(s.token) && Boolean(s.user),
  },
  actions: {
    /** Store token after a successful login API call. */
    setAuth(token: string, user: string, method = 'password') {
      this.token = token
      this.user = user
      this.authMethod = method
      this.writeSession([
        [TOKEN_KEY, token],
        [USER_KEY, user],
        [METHOD_KEY, method],
      ])
    },
    /**
     * ⚠⚠§867/D33：会话落盘原来是一条条裸 `setItem`。配额写不进去时
     *   **内存已经改完、存储一个字没进**，而 `isAuthenticated` 只看内存
     *   ⇒ 用户以为自己登录成功，**刷新/重启后被静默登出**（syncFromStorage
     *   从存储读回空 token）。界面全程零提示。
     *
     * 姿态按 §866 的族规律选：丢的是「用户正在持有的会话」⇒ **必须说话**，
     * 所以除 warn 外还置 `sessionPersistFailed`，让宿主有机会说实话
     * （界面要不要挂横幅属产品口径，登记在简报第 86 条）。
     *
     * ⚠ 撕裂写：第 1 条成功、第 2 条抛错时，存储里会留下「有 token 没 user」。
     *   这在 `isAuthenticated = Boolean(token) && Boolean(user)` 下**失败关闭**
     *   （读回来判为未登录），不会造成越权，故不额外回滚——回滚反而可能
     *   误删上一段本来完好的会话。
     */
    writeSession(entries: [string, string][]) {
      try {
        for (const [k, v] of entries) localStorage.setItem(k, v)
        this.sessionPersistFailed = false
      } catch (err) {
        this.sessionPersistFailed = true
        console.warn('[auth] 会话未能写入本地存储，本次登录在刷新后会失效：', err)
      }
    },
    /**
     * S0-A 扩展：登录后同时记录 workspace_id + user_id。
     * 后端 /api/auth/login 现在返回 { token, user, user_id, workspace_id }。
     */
    setAuthWithWorkspace(
      token: string,
      user: string,
      userId: string,
      workspaceId: string,
      method = 'password',
    ) {
      this.setAuth(token, user, method)
      this.userId = userId
      this.workspaceId = workspaceId
      this.writeSession([[WS_KEY, workspaceId]])
    },
    /** Legacy compatibility: a session exists without a real token yet. */
    setLegacyUser(user: string) {
      this.user = user
      this.writeSession([[USER_KEY, user]])
    },
    /**
     * Phase 7: Sync state from localStorage.
     * Useful for:
     * - Recovering state after page reload
     * - Syncing when localStorage is modified externally (testing/automation)
     * - Cross-tab synchronization
     *
     * ⚠⚠⚠§871/D41：原来这里只读 token + user 两个键。
     * 而它**不是**只在 storage 事件里被调用 —— `app/routeGuards.ts:55-56`
     * 在**每次导航**都会调它。也就是说，「从存储刷新会话」这条指定路径
     * 系统性地漏掉了 `workspaceId` / `authMethod`。
     *
     * 有消费者的后果（不是推测）：
     *   `composables/usePendingApprovals.ts:80` → `workspaceId: auth.workspaceId || 'default'`
     *   `app/routeGuards.ts:87` → 缺 workspaceId 时要求补齐
     * ⇒ 跨标签登录到**另一个** workspace 后，本标签仍按**旧 workspace** 取待审批。
     *
     * ⚠ `userId` 刻意**不在**这里：它从来就不落盘（`state` 里写死 `''`，
     *   全仓没有 `pocket_user_id` 这个键）—— 见 §871.3 的登记。
     * ⚠ 那个 `storage` 监听**也已同步补齐**（见文件末尾）：
     *   它原先只认两个键，只修本函数在跨标签场景下**不会生效**；
     *   同时补上了 `event.key === null`（规范里 `localStorage.clear()`
     *   触发的事件 key 是 null）—— 全仓 `clear()` 目前 0 处，但既然条件表达式
     *   本来就要改，把这个分支一并处理掉比留一个已知缺口更省事，
     *   且语义正确（`null` 时重新读一遍全部键，正是 clear() 想要的）。
     */
    syncFromStorage() {
      const storedToken = localStorage.getItem(TOKEN_KEY) || ''
      const storedUser = localStorage.getItem(USER_KEY) || ''
      const storedWorkspace = localStorage.getItem(WS_KEY) || ''
      const storedMethod = localStorage.getItem(METHOD_KEY) || ''

      // Only update if values actually changed
      if (this.token !== storedToken) {
        this.token = storedToken
      }
      if (this.user !== storedUser) {
        this.user = storedUser
      }
      if (this.workspaceId !== storedWorkspace) {
        this.workspaceId = storedWorkspace
      }
      if (this.authMethod !== storedMethod) {
        this.authMethod = storedMethod
      }
    },
    /**
     * JWT 滑动续期（runbook §15.2）：单飞调 /api/auth/refresh 换新 token。
     * 成功返回 true 并更新 store + localStorage；失败返回 false（由调用方
     * 决定是否登出），不抛异常。
     */
    async refreshSession(): Promise<boolean> {
      if (!this.token) return false
      if (refreshInFlight) return refreshInFlight
      refreshInFlight = (async () => {
        try {
          const res = await refreshAuth()
          if (!res?.token) return false
          // 显示名保持不变：refresh 端点 legacy 路径只能从 claims 反推
          // user 字段（如 user-admin），覆盖会漂移 App 内的登录显示名。
          const displayUser = this.user || res.user || ''
          this.setAuth(res.token, displayUser, this.authMethod)
          if (res.workspace_id) {
            this.workspaceId = res.workspace_id
            localStorage.setItem(WS_KEY, res.workspace_id)
          }
          if (res.user_id) this.userId = res.user_id
          return true
        } catch (e) {
          console.debug('auth refresh failed:', e)
          return false
        } finally {
          refreshInFlight = null
        }
      })()
      return refreshInFlight
    },
    /**
     * 临期主动续期：token 剩余寿命 <5min 时触发 refreshSession。
     * 不临期 / 无法解析 exp 时立即返回，绝不抛异常——可放在每个请求前调用。
     */
    async maybeRefresh(): Promise<void> {
      if (!this.token) return
      const exp = tokenExpiresAtMs(this.token)
      if (exp === null) return
      if (exp - Date.now() > REFRESH_AHEAD_MS) return
      await this.refreshSession()
    },
    /**
     * 只清本地登录态，不打后端。
     *
     * BUG-I (2026-09-30)：供 api/http.ts 在「401 且 refresh 失败」时调用。
     * 这种场景下后端必然拒绝 /api/auth/logout（token 已不可用），
     * 调它只会白等一轮网络超时，把用户卡在死胡同里。
     */
    clearLocal() {
      this.token = ''
      this.user = ''
      this.workspaceId = ''
      this.userId = ''
      this.authMethod = ''
      // ⚠⚠⚠§870/D39：原来这里是四条裸 `removeItem`。失败链与 §867/D33
      // **恰好相反但后果更重**：写失败 = 用户的会话被弄丢（只是丢东西），
      // 而**删失败 = 会话被留在盘上** —— 用户明确点了「退出」，
      // 而系统其实还在记着他。
      //   内存态已经在上面清掉了 ⇒ 本次会话看起来登出成功；
      //   但 `state: () => ({ token: localStorage.getItem(TOKEN_KEY) … })`
      //   在下次启动会把**旧 token 读回来** ⇒ 共享设备上「退出登录」等于没退。
      // 而且抛错会冒泡出本函数 —— `logout()` 没有包 try/catch。
      //   ⚠ 对照：`api/http.ts` 的 401 路径**是**包了的（那里是正确的样板）。
      const failed: string[] = []
      for (const k of [TOKEN_KEY, USER_KEY, WS_KEY, METHOD_KEY]) {
        try {
          localStorage.removeItem(k)
        } catch (err) {
          failed.push(k)
          console.warn(`[auth] 本地会话键 ${k} 未能删除（下次启动可能仍被视为已登录）：`, err)
        }
      }
      this.sessionClearFailed = failed.length > 0
      this.sessionClearMissing = failed
    },
    /**
     * Phase 1: 先调后端 /api/auth/logout 撤销 RedClaw session，再清本地。
     * 失败（401/网络）视作幂等成功，因为本地状态反正要被清。
     */
    async logout() {
      if (this.token) {
        try {
          await logoutRemote()
        } catch (e) {
          // 静默吞掉：登出路径不允许阻塞
          console.debug('logoutRemote failed (ignored):', e)
        }
      }
      this.clearLocal()
    },
  },
})

/**
 * Phase 7: Setup storage event listener for cross-tab sync
 * This allows the auth state to stay in sync across multiple tabs/windows
 */
if (typeof window !== 'undefined') {
  window.addEventListener('storage', (event) => {
    // ⚠⚠⚠§871/D41：原来这里只认 TOKEN_KEY / USER_KEY 两个键，
    // 而 syncFromStorage 现在会刷新四个键 —— 只修后者是**不完整**的：
    // 别的标签改了 `pocket_workspace_id`，本标签连事件都不收，
    // 同步函数压根不会被调用 ⇒ §871 的修复在跨标签场景下不会生效。
    // ⇒ 触发条件必须与「被刷新的键集合」保持一致。
    // `event.key === null` 对应 `localStorage.clear()`（规范：
    //   clear() 触发的事件 key 为 null）：此时无法知道改了哪些键，
    //   重新读一遍全部键正是它想要的行为。
    if (event.key === null || event.key === TOKEN_KEY || event.key === USER_KEY
      || event.key === WS_KEY || event.key === METHOD_KEY) {
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
