/**
 * connectivity store — 网络 / 同步 / 离线队列的全局状态（优化 v4 08 §4.2）。
 *
 * 状态模型：
 *   network: online | offline
 *   sync:    idle | syncing | failed（lastError 非空）
 *
 * 同时持有 MobileSyncRuntime 的唯一实例：online/offline/resume 事件由 runtime
 * 监听，触发结果通过事件回写本 store，全局状态条（GlobalStatusBar）只读这里。
 */
import { defineStore } from 'pinia'
import { watch } from 'vue'
import { useAuthStore } from './auth'
import { resolveApiBase } from '../config/api-base'
import { isLobsterReady, lobsterReady } from '../native/lobster-init'
import { localDB, localDbAsSql } from '../native/local-db'
import { MobileSyncRuntime, type RuntimeEvent } from '../native/mobileSyncRuntime'
import { startEmailFetchHost } from '../features/email/email-fetch-host'

export const useConnectivityStore = defineStore('connectivity', {
  state: () => ({
    online: typeof navigator !== 'undefined' ? navigator.onLine : true,
    syncing: false,
    pendingCount: 0,
    deadLetterCount: 0,
    lastSyncAt: 0,
    lastError: '',
    /** 同步 runtime 实例（init 创建；非序列化，仅 App 内使用）。 */
    runtime: null as MobileSyncRuntime | null,
  }),
  getters: {
    /** 是否有待发送的离线操作（全局状态条显示依据）。 */
    hasPendingQueue(state): boolean {
      return state.pendingCount > 0
    },
    statusLabel(state): string {
      if (!state.online) {
        return state.pendingCount > 0
          ? `离线中 · ${state.pendingCount} 条操作待联网发送`
          : '离线中 · 操作将保存到本地'
      }
      if (state.syncing) return '同步中…'
      if (state.pendingCount > 0) return `${state.pendingCount} 条操作待发送`
      return ''
    },
  },
  actions: {
    /** App 启动时调用一次：注册网络监听并启动同步 runtime。 */
    init() {
      if (this.runtime !== null) return
      window.addEventListener('online', () => {
        this.online = true
      })
      window.addEventListener('offline', () => {
        this.online = false
      })

      const auth = useAuthStore()
      this.runtime = new MobileSyncRuntime({
        isOnline: () => this.online,
        isReady: () => isLobsterReady(),
        auth: () => {
          if (!auth.isAuthenticated || auth.workspaceId === '' || auth.token === '') return null
          return { token: auth.token, workspaceId: auth.workspaceId }
        },
        db: () => (isLobsterReady() ? localDbAsSql(localDB) : null),
        fetchImpl: (...args: Parameters<typeof fetch>) => fetch(...args),
        apiBase: resolveApiBase(),
        onEvent: (event) => this.applyRuntimeEvent(event),
      })
      this.runtime.start()
      // 2026-10-06 加门：邮件拉取要读写本地库，而本函数可能在 initLobster 完成**之前**
      // 就被调用（`email-fetch-host.ts` 的注释自己说明了：冷启动时 visibilitychange /
      // appStateChange 都不触发，所以它专门加了一个启动即 kick）。
      // 真机实测那次冷启动就因此打出
      //   [email] sync from server: LocalDB 未初始化，请先调用 init(dbSecret)
      // ——而这条错误的上一任（4 条 duplicate column name）刚被我清掉。
      // 本文件上面 `isReady` / `db` 两处本来就用 isLobsterReady() 门控，只有这一行是裸的。
      //
      // ⚠️ 这里用 **watch(lobsterReady)** 而不是「不 ready 就 await 一次」。
      // 我第一版写的是：
      //   if (isLobsterReady()) startEmailFetchHost()
      //   else void whenLobsterReady().then(...).catch(() => {})
      // 那一版**表面修好了**（console 一行错都没有），但真机启动窗口取证显示
      // **一个 email 请求都没发** ⇒ 它把「响亮地失败」换成了「安静地不跑」：
      // 若此时既没 ready、也没有进行中的 init，whenLobsterReady() 会 reject，
      // 被 catch 吞掉 ⇒ 邮件拉取**再也不会启动**。
      // 照搬 `config-sync/runtime.ts` 的响应式写法：ready 一翻 true 就启动，不会自我放弃。
      watch(
        lobsterReady,
        (v) => {
          if (v) startEmailFetchHost()
        },
        { immediate: true },
      )
    },
    applyRuntimeEvent(event: RuntimeEvent) {
      switch (event.type) {
        case 'syncing':
          this.syncing = true
          break
        case 'done':
          this.syncing = false
          this.pendingCount = event.pendingCount
          this.deadLetterCount = event.deadLetterCount
          if (event.at > 0) this.lastSyncAt = event.at
          break
        case 'error':
          this.lastError = `${event.phase}: ${event.message}`
          break
        case 'drained':
          if (event.deadLettered > 0) {
            this.lastError = `${event.deadLettered} 条操作重试超限，已转入死信`
          } else {
            this.lastError = ''
          }
          break
        default:
          break
      }
    },
    /** 用户点击"立即同步/重试"。 */
    async syncNow() {
      await this.runtime?.trigger('manual')
    },
    /** 入队离线操作后刷新计数（全局状态条立即反映）。 */
    async refreshCounts() {
      if (!isLobsterReady()) return
      try {
        const { SqliteOutboxStore } = await import('../native/outboxStore.ts')
        const outbox = new SqliteOutboxStore(localDbAsSql(localDB))
        this.pendingCount = await outbox.countByState(['queued', 'inflight'])
        this.deadLetterCount = await outbox.countByState(['dead_letter'])
      } catch {
        // 本地库不可用时保持旧计数
      }
    },
    clearError() {
      this.lastError = ''
    },
  },
})
