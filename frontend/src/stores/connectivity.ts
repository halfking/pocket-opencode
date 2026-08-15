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
import { useAuthStore } from './auth'
import { apiBase } from '../api/http'
import { isLobsterReady } from '../native/lobster-init'
import { localDB, localDbAsSql } from '../native/local-db'
import { MobileSyncRuntime, type RuntimeEvent } from '../native/mobileSyncRuntime'

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
        apiBase,
        onEvent: (event) => this.applyRuntimeEvent(event),
      })
      this.runtime.start()
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
