/**
 * mobileSyncRuntime.ts — 离线同步的宿主接线（P1 遗留项：drain 循环接线）。
 *
 * 触发源（start() 注册）：
 *   - window online / offline（WebView 内 navigator 事件）
 *   - document visibilitychange（回前台）
 *   - Capacitor App appStateChange resume（原生切回前台）
 *   - 定时兜底（队列非空时每 30s 重试一轮，上限 60s）
 *
 * 每轮 trigger 的守卫与动作：
 *   守卫：在线 + 已登录（token + workspaceId）+ lobster 已解锁（拿到 SQLCipher 连接）
 *   动作：drainOutbox 重放离线队列 → 按本地已知 instance syncSessions
 *         （push dirty 再 pull 增量）→ pull 审批 pending 快照回填本地表。
 *
 * 依赖全部注入（RuntimeDeps），App 侧由 appSync.ts 提供 Pinia / LocalDB 实现，
 * Node 测试提供假实现——runtime 本身不 import 任何 Capacitor / Pinia 模块。
 */

import type { SqlDb } from './sqlDb.ts'
import type { OutboxStorage } from '../utils/outbox.ts'
import { drainOutbox, createMobileOutboxSenders, type AuthorizedFetch } from './outboxDrain.ts'
import {
  syncSessions,
  SqliteMobileSyncStore,
  type SyncTransport,
  type UpstreamSession,
} from './mobileSync.ts'
import { SqliteOutboxStore } from './outboxStore.ts'
import { SqliteApprovalStore, backfillApprovals, type ServerPendingApprovals } from './approvalStore.ts'

export interface RuntimeAuth {
  token: string
  workspaceId: string
}

export interface RuntimeDeps {
  isOnline(): boolean
  /** lobster（本地加密库）已解锁且可用。 */
  isReady(): boolean
  /** 未登录 / 无 workspace 时返回 null，本轮跳过。 */
  auth(): RuntimeAuth | null
  /** 返回底层 SQL 连接；不可用时 null。 */
  db(): SqlDb | null
  fetchImpl: typeof fetch
  apiBase?: string
  onEvent?(event: RuntimeEvent): void
}

export type RuntimeEvent =
  | { type: 'skipped'; reason: string }
  | { type: 'syncing' }
  | {
      type: 'drained'
      succeeded: number
      retried: number
      deadLettered: number
      droppedWorkspaceMismatch: number
    }
  | { type: 'sessions-synced'; instanceId: string; result: { pushedCreates: number; pushedDeletes: number; pulledUpserts: number } }
  | { type: 'approvals-backfilled'; instanceId: string; upserted: number; expired: number }
  | { type: 'error'; phase: string; message: string }
  | { type: 'done'; pendingCount: number; deadLetterCount: number; at: number }

export interface RuntimeStatus {
  syncing: boolean
  pendingCount: number
  deadLetterCount: number
  lastSyncAt: number
  lastError: string
}

const FALLBACK_INTERVAL_MS = 30_000

export class MobileSyncRuntime {
  private readonly deps: RuntimeDeps
  private syncing = false
  private status: RuntimeStatus = {
    syncing: false,
    pendingCount: 0,
    deadLetterCount: 0,
    lastSyncAt: 0,
    lastError: '',
  }
  private timer: ReturnType<typeof setInterval> | null = null
  private listeners: Array<() => void> = []

  constructor(deps: RuntimeDeps) {
    this.deps = deps
  }

  getStatus(): RuntimeStatus {
    return { ...this.status, syncing: this.syncing }
  }

  /** 注册宿主事件监听并启动定时兜底。幂等。 */
  start(): void {
    if (this.listeners.length > 0) return

    const onOnline = () => void this.trigger('network-online')
    const onOffline = () => {
      this.deps.onEvent?.({ type: 'skipped', reason: 'network-offline' })
    }
    const onVisible = () => {
      if (document.visibilityState === 'visible') void this.trigger('app-resume')
    }
    window.addEventListener('online', onOnline)
    window.addEventListener('offline', onOffline)
    document.addEventListener('visibilitychange', onVisible)
    this.listeners.push(
      () => window.removeEventListener('online', onOnline),
      () => window.removeEventListener('offline', onOffline),
      () => document.removeEventListener('visibilitychange', onVisible),
    )

    // 原生 resume：只在 Capacitor 环境加载成功时生效，Web 下静默跳过。
    void this.registerCapacitorResume()

    this.timer = setInterval(() => {
      if (this.status.pendingCount > 0 && this.deps.isOnline()) {
        void this.trigger('timer-fallback')
      }
    }, FALLBACK_INTERVAL_MS)
  }

  stop(): void {
    for (const off of this.listeners) off()
    this.listeners = []
    if (this.timer !== null) {
      clearInterval(this.timer)
      this.timer = null
    }
  }

  private async registerCapacitorResume(): Promise<void> {
    try {
      const { App } = await import('@capacitor/app')
      const sub = await App.addListener('appStateChange', (state: { isActive: boolean }) => {
        if (state.isActive) void this.trigger('app-resume')
      })
      this.listeners.push(() => {
        void sub.remove()
      })
    } catch {
      // Web / 测试环境没有原生插件：visibilitychange 已覆盖
    }
  }

  /** 一轮同步：守卫 → drain → 按实例 syncSessions → 审批回填。并发调用合并。 */
  async trigger(reason: string): Promise<void> {
    if (this.syncing) return
    if (!this.deps.isOnline()) {
      this.deps.onEvent?.({ type: 'skipped', reason: `${reason}:offline` })
      return
    }
    const auth = this.deps.auth()
    if (auth === null) {
      this.deps.onEvent?.({ type: 'skipped', reason: `${reason}:unauthenticated` })
      return
    }
    const db = this.deps.db()
    if (db === null) {
      this.deps.onEvent?.({ type: 'skipped', reason: `${reason}:db-locked` })
      return
    }

    this.syncing = true
    this.deps.onEvent?.({ type: 'syncing' })
    try {
      const outbox = new SqliteOutboxStore(db)
      const syncStore = new SqliteMobileSyncStore(db)
      const approvalStore = new SqliteApprovalStore(db)
      const doFetch = createRuntimeFetch(this.deps, auth)

      const drain = await drainOutbox(outbox, {
        workspaceId: auth.workspaceId,
        senders: createMobileOutboxSenders({ doFetch, syncStore, approvalStore }),
      })
      this.deps.onEvent?.({
        type: 'drained',
        succeeded: drain.succeeded,
        retried: drain.retried,
        deadLettered: drain.deadLettered,
        droppedWorkspaceMismatch: drain.droppedWorkspaceMismatch,
      })

      // 同步范围 = 本地已知实例（离线创建过会话的实例也会被覆盖）。
      const instanceIds = await listInstanceIds(db)
      const transport = createHttpSyncTransport(doFetch)
      for (const instanceId of instanceIds) {
        try {
          const result = await syncSessions(syncStore, transport, {
            workspaceId: auth.workspaceId,
            instanceId,
          })
          this.deps.onEvent?.({
            type: 'sessions-synced',
            instanceId,
            result: {
              pushedCreates: result.pushedCreates,
              pushedDeletes: result.pushedDeletes,
              pulledUpserts: result.pulledUpserts,
            },
          })
        } catch (err) {
          this.deps.onEvent?.({
            type: 'error',
            phase: `sync-sessions:${instanceId}`,
            message: err instanceof Error ? err.message : String(err),
          })
        }

        // 审批 pending 快照回填（失败不阻断其余实例）。
        try {
          const server = await fetchPendingApprovals(doFetch, instanceId)
          if (server !== null) {
            const backfill = await backfillApprovals(approvalStore, {
              workspaceId: auth.workspaceId,
              instanceId,
              server,
            })
            this.deps.onEvent?.({
              type: 'approvals-backfilled',
              instanceId,
              upserted: backfill.upserted,
              expired: backfill.expired,
            })
          }
        } catch (err) {
          this.deps.onEvent?.({
            type: 'error',
            phase: `backfill-approvals:${instanceId}`,
            message: err instanceof Error ? err.message : String(err),
          })
        }
      }

      this.status.lastSyncAt = Date.now()
      this.status.lastError = ''
    } catch (err) {
      this.status.lastError = err instanceof Error ? err.message : String(err)
      this.deps.onEvent?.({
        type: 'error',
        phase: 'drain',
        message: this.status.lastError,
      })
    } finally {
      this.syncing = false
      const dbAfter = this.deps.db()
      if (dbAfter !== null) {
        try {
          const outbox = new SqliteOutboxStore(dbAfter)
          this.status.pendingCount = await outbox.countByState(['queued', 'inflight'])
          this.status.deadLetterCount = await outbox.countByState(['dead_letter'])
        } catch {
          // 计数失败不影响本轮结果
        }
      }
      this.deps.onEvent?.({
        type: 'done',
        pendingCount: this.status.pendingCount,
        deadLetterCount: this.status.deadLetterCount,
        at: this.status.lastSyncAt,
      })
    }
  }
}

async function listInstanceIds(db: SqlDb): Promise<string[]> {
  const rows = await db.all(
    'SELECT DISTINCT instance_id FROM local_mobile_sessions ORDER BY instance_id LIMIT 50',
  )
  return rows.map((r) => String(r.instance_id)).filter((id) => id !== '')
}

/** 带 Bearer token 的 fetch，签名对齐 AuthorizedFetch。 */
export function createRuntimeFetch(deps: RuntimeDeps, auth: RuntimeAuth): AuthorizedFetch {
  const base = deps.apiBase ?? ''
  return async (input, init) => {
    const res = await deps.fetchImpl(`${base}${input}`, {
      method: init?.method ?? 'GET',
      headers: {
        ...(init?.headers ?? {}),
        Authorization: `Bearer ${auth.token}`,
      },
      body: init?.body,
    })
    return {
      status: res.status,
      json: () => res.json() as Promise<unknown>,
    }
  }
}

/** SyncTransport 的 HTTP 实现（GET/POST/DELETE /api/mobile/sessions）。 */
export function createHttpSyncTransport(doFetch: AuthorizedFetch): SyncTransport {
  return {
    listSessions: async (args) => {
      const res = await doFetch(
        `/api/mobile/sessions?instance_id=${encodeURIComponent(args.instanceId)}&since=${args.sinceMs}`,
      )
      if (res.status !== 200) throw new Error(`list sessions failed: http_${res.status}`)
      const parsed = (await res.json()) as { data?: Array<Record<string, unknown>> }
      const sessions: UpstreamSession[] = (parsed.data ?? []).map((row) => ({
        id: String(row.id ?? ''),
        title: typeof row.title === 'string' ? row.title : '',
        status: typeof row.status === 'string' ? row.status : '',
        timeUpdatedMs: Number(row.timeUpdatedMs ?? 0),
      }))
      sessions.sort((a, b) => a.timeUpdatedMs - b.timeUpdatedMs)
      return { sessions }
    },
    createSession: async (args) => {
      const res = await doFetch(
        `/api/mobile/sessions?instance_id=${encodeURIComponent(args.instanceId)}`,
        {
          method: 'POST',
          headers: { 'Idempotency-Key': args.idempotencyKey },
          body: JSON.stringify({}),
        },
      )
      if (res.status < 200 || res.status >= 300) {
        throw new Error(`create session failed: http_${res.status}`)
      }
      const parsed = (await res.json()) as Record<string, unknown>
      return {
        id: String(parsed.id ?? ''),
        title: typeof parsed.title === 'string' ? parsed.title : (args.title ?? ''),
        status: typeof parsed.status === 'string' ? parsed.status : 'idle',
        timeUpdatedMs: Number(parsed.timeUpdatedMs ?? parsed.time_updated ?? Date.now()),
      }
    },
    deleteSession: async (args) => {
      const res = await doFetch(
        `/api/mobile/sessions/${encodeURIComponent(args.serverId)}?instance_id=${encodeURIComponent(args.instanceId)}`,
        { method: 'DELETE' },
      )
      if (res.status < 200 || res.status >= 300) {
        throw new Error(`delete session failed: http_${res.status}`)
      }
    },
  }
}

async function fetchPendingApprovals(
  doFetch: AuthorizedFetch,
  instanceId: string,
): Promise<ServerPendingApprovals | null> {
  const res = await doFetch(
    `/api/mobile/approvals?instance_id=${encodeURIComponent(instanceId)}`,
  )
  if (res.status !== 200) return null
  const parsed = (await res.json()) as Partial<ServerPendingApprovals>
  return {
    permissions: Array.isArray(parsed.permissions) ? parsed.permissions : [],
    questions: Array.isArray(parsed.questions) ? parsed.questions : [],
  }
}
