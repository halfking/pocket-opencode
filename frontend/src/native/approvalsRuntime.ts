/**
 * approvalsRuntime.ts — 进程级审批轮询 + WS 订阅运行时（M2，2026-09-09）。
 *
 * 设计动机：原 usePendingApprovals 在 SessionConversationView 组件级创建，
 * 路由一切走就 stopPolling() → 切回又 startPolling()。结果：
 *   - 用户切到其他页再回来，发现审批轮询窗口空了一段（可能漏掉弹窗）
 *   - WS 重连期间断线兜底轮询也跟着停
 *   - 多组件订阅同一会话时 timer 重复建立（虽然 idempotentWsBus 是累加的，
 *     但 setInterval 各管各的）
 *
 * 本模块把"轮询 + WS 订阅 + 当前 instance+session 跟踪"全部上移到进程级
 * singleton。组件级 usePendingApprovals 改为薄包装，订阅特定 instance+session
 * 的 pendingPermissions 列表。切走/切回页面都不影响运行时继续轮询。
 *
 * 依赖注入（RuntimeDeps）参照 mobileSyncRuntime.ts：所有副作用（fetch / store /
 * WS bus）由调用方在 main.ts 注入，Node 测试场景用假实现替换，runtime 本身
 * 不 import 任何 Pinia / Capacitor 模块。
 *
 * 不在范围（M3+）：
 *   - 真后台（iOS BackgroundModes / Android FOREGROUND_SERVICE_DATA_SYNC）
 *     本模块在前台范围内保活；后台冻结时 JS 计时器一并冻结。
 *   - 通知推送（local notification）。当前 M2 仅做前台语义。
 */

import type { PermissionRequest } from '../api/approvals.ts'

export interface RuntimeDeps {
  /** 当前是否在线（offline 跳过 fetch）。 */
  isOnline(): boolean
  /** 当前 WS 是否已连；true 时跳过轮询（事件驱动）。 */
  isWsConnected(): boolean
  /** 拉取待审批列表（受 instanceID + sessionID 过滤）。 */
  fetchPending(args: { instanceId: string; sessionId: string }): Promise<{ permissions: PermissionRequest[] }>
  /** 注册 WS 事件订阅；返回反订阅函数。 */
  subscribeApprovalEvents(handler: (event: { type: string; payload: unknown }) => void): () => void
}

interface Subscription {
  instanceId: string
  sessionId: string
  onChange: (list: PermissionRequest[], err: string) => void
}

const DEFAULT_POLL_MS = 10_000
const REFRESH_DEBOUNCE_MS = 250

export class ApprovalsRuntime {
  private readonly deps: RuntimeDeps
  private readonly opts: { unrefTimer?: boolean }
  private subs: Set<Subscription> = new Set()
  private wsUnsubscribe: (() => void) | null = null
  private timer: ReturnType<typeof setInterval> | null = null
  private refreshDebounce: ReturnType<typeof setTimeout> | null = null
  private wasWsConnected = false
  private refreshing = false
  private started = false

  constructor(deps: RuntimeDeps, opts: { unrefTimer?: boolean } = {}) {
    this.deps = deps
    this.opts = opts
  }

  private makeInterval(cb: () => void, ms: number): ReturnType<typeof setInterval> {
    const t = setInterval(cb, ms)
    // 测试场景：unref 让 timer 不阻塞 Node 进程退出；浏览器无 unref
    if (this.opts.unrefTimer) {
      const maybe = t as unknown as { unref?: () => void }
      if (typeof maybe.unref === 'function') maybe.unref()
    }
    return t
  }

  start(): void {
    if (this.started) return
    this.started = true
    this.wasWsConnected = this.deps.isWsConnected()
    if (!this.wsUnsubscribe) {
      this.wsUnsubscribe = this.deps.subscribeApprovalEvents((evt) => this.onApprovalEvent(evt))
    }
    // 进程级定时器：只要有订阅方就一直轮询
    this.timer = this.makeInterval(() => {
      if (this.subs.size === 0) return
      if (!this.deps.isOnline()) return
      const connected = this.deps.isWsConnected()
      if (connected && this.wasWsConnected) return // WS 在线：事件驱动，跳过轮询
      this.wasWsConnected = connected
      void this.refreshAll()
    }, DEFAULT_POLL_MS)
  }

  stop(): void {
    if (!this.started) return
    if (this.timer !== null) {
      clearInterval(this.timer)
      this.timer = null
    }
    if (this.refreshDebounce !== null) {
      clearTimeout(this.refreshDebounce)
      this.refreshDebounce = null
    }
    if (this.wsUnsubscribe) {
      this.wsUnsubscribe()
      this.wsUnsubscribe = null
    }
    this.subs.clear()
    this.started = false
  }

  /**
   * 订阅特定 (instanceId, sessionId) 的 pending list。
   * onChange 在 list 变化或拉取失败时被调用。
   * 返回反订阅函数。同 (iid, sid, onChange) 幂等。
   *
   * 注意：本函数仅注册订阅，**不**触发立即拉取（避免 fetch 不可用场景报错）。
   * 调用方按需调 refresh 触发一次立即拉；runtime 自身 10s 定时器兜底。
   */
  subscribe(
    instanceId: string,
    sessionId: string,
    onChange: (list: PermissionRequest[], err: string) => void,
  ): () => void {
    if (!instanceId || !sessionId) return () => {}
    this.start()
    const sub: Subscription = { instanceId, sessionId, onChange }
    for (const s of this.subs) {
      if (s.instanceId === instanceId && s.sessionId === sessionId && s.onChange === onChange) {
        return () => {}
      }
    }
    this.subs.add(sub)
    return () => {
      this.subs.delete(sub)
    }
  }

  /** 仅测试 / 显式触发：拉一次所有订阅方的当前 list。 */
  async refresh(): Promise<void> {
    await this.refreshAll()
  }

  /** 仅测试用：列出当前订阅方数。 */
  size(): number {
    return this.subs.size
  }

  // ---- 内部 ----

  private scheduleRefresh(): void {
    if (this.refreshDebounce !== null) return
    this.refreshDebounce = setTimeout(() => {
      this.refreshDebounce = null
      void this.refreshAll()
    }, REFRESH_DEBOUNCE_MS)
  }

  private async refreshOne(sub: Subscription): Promise<void> {
    if (!this.deps.isOnline()) return
    try {
      const result = await this.deps.fetchPending({
        instanceId: sub.instanceId,
        sessionId: sub.sessionId,
      })
      const list = (result.permissions ?? []).filter(
        (p) => typeof p?.id === 'string' && p.id !== '' && p.sessionID === sub.sessionId,
      )
      if (this.subs.has(sub)) {
        sub.onChange(list, '')
      }
    } catch (err) {
      if (this.subs.has(sub)) {
        sub.onChange([], err instanceof Error ? err.message : '审批状态拉取失败')
      }
    }
  }

  private async refreshAll(): Promise<void> {
    if (this.refreshing) return
    this.refreshing = true
    try {
      const subs = [...this.subs]
      await Promise.all(subs.map((s) => this.refreshOne(s)))
    } finally {
      this.refreshing = false
    }
  }

  private onApprovalEvent(evt: { type: string; payload: unknown }): void {
    const payload = evt.payload as Record<string, unknown> | null
    if (!payload) return
    // 后端 envelope: data: { instance_id, session_id, ... }；approve API 与 WS 一致
    const data = (payload.data ?? payload) as Record<string, unknown> | null
    if (!data) return
    const instanceId = typeof data.instance_id === 'string' ? data.instance_id : null
    const sessionId = typeof data.session_id === 'string' ? data.session_id : null
    if (!instanceId || !sessionId) return
    const matched = [...this.subs].filter(
      (s) => s.instanceId === instanceId && s.sessionId === sessionId,
    )
    if (matched.length === 0) return
    // 简化策略：所有匹配订阅方统一触发对齐刷新（runtime 内部去抖 250ms）。
    this.scheduleRefresh()
  }
}

/** 进程级单例；HMR 兼容。 */
const RUNTIME_KEY = '__openpocket_approvalsRuntime__'
type GlobalWithRuntime = typeof globalThis & { [RUNTIME_KEY]?: ApprovalsRuntime }
const g = globalThis as GlobalWithRuntime
export function getApprovalsRuntime(): ApprovalsRuntime | undefined {
  return g[RUNTIME_KEY]
}
export function setApprovalsRuntime(r: ApprovalsRuntime): void {
  g[RUNTIME_KEY] = r
}
