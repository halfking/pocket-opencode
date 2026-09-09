/**
 * aiStreamRuntime.ts — AI 流的进程级 singleton 运行时。
 *
 * 设计动机（2026-09-09）：AI 流的"创建+销毁"原本被绑死在 Vue 组件生命周期上，
 * 用户一切走（路由切换 / 切标签 / App 进后台）就被 abort。本模块把流所有权上移到
 * 进程级 singleton，组件只"订阅/退订"——离场 ≠ 取消。
 *
 * 契约要点（详见 docs/design/2026-09-09-ai-async-background-survival.md §D1）：
 *   1. spawnXxx 幂等：同 id 重复调用返回首次的 handle，流不在跑才重启。
 *   2. 流取消三态：用户主动 / 服务端不可恢复 / 显式 dispose。**组件 unmount 不是取消**。
 *   3. 订阅者离场后再 subscribe 拿到 buf replay（≤ MAX_REPLAY_FRAMES 帧防爆）。
 *   4. 120s 看门狗：隐藏态暂停计时；切回前台后用剩余预算继续，不报"超时"而报"网络中断"。
 *   5. 流式 / abort / 重启由本模块负责；流层（llm-bff）只做"发起 fetch + 喂字节"。
 *
 * M1 落地：仅 chat 流（POST /api/llm/stream）。Session/Gateway 流在 M2/M4。
 */

import { appLifecycleHub, type LifecycleEvent } from './appLifecycleHub.ts'

export type StreamStatus =
  | 'idle'      // 已注册但未起跑
  | 'running'   // fetch 进行中
  | 'done'      // 正常结束
  | 'error'     // 错误结束（含 watchdog / 网络中断 / 服务端 error frame）
  | 'aborted'   // 用户主动 abort

export type StreamAbortReason =
  | 'user'           // 用户点"停止"
  | 'watchdog'       // 120s 无字节
  | 'network'        // fetch 失败 / 网络中断
  | 'server-error'   // 服务端下发 error frame
  | 'empty'          // 正常关闭但一帧都没有
  | 'replaced'       // 同 id 被新 spawn 替换（M1 未启用，留位）

export interface ChatStreamInput {
  messages: Array<{
    role: 'system' | 'user' | 'assistant'
    content: string
    images?: string[]
  }>
  model?: string
  temperature?: number
  max_tokens?: number
  kind?: string
}

export interface ChatStreamDelta {
  content?: string
  done: boolean
  model?: string
  usage?: { prompt_tokens: number; completion_tokens: number; total_tokens: number }
  error?: string
  retry?: string
}

export interface ChatStreamHandlers {
  onDelta?: (delta: ChatStreamDelta) => void
  onDone?: (finalUsage?: ChatStreamDelta['usage']) => void
  /** error 带 reason：UI 层据此决定文案（超时 / 网络中断 / 用户停止）。 */
  onError?: (err: Error, reason: StreamAbortReason) => void
  onRetry?: (model: string) => void
}

export interface ChatStreamHandle {
  readonly id: string
  /** 用户主动停止。返回 false 表示流已经处于终态。 */
  abort(): boolean
  status(): StreamStatus
}

interface StreamEntry {
  id: string
  kind: 'chat'
  ctrl: AbortController
  status: StreamStatus
  abortReason: StreamAbortReason | null
  subs: Set<Subscriber>
  /** 同 id spawnChat 必须返回同一 handle 引用（幂等性）。 */
  handle: ChatStreamHandle
  /** 看门狗剩余预算（活跃时间计）；只在 pause 时按"已活跃时长"扣减。 */
  watchdogRemainingMs: number
  watchdogTimer: ReturnType<typeof setTimeout> | null
  /** 最近一次 arm 时间；pause 时据其计算"已活跃时长"并扣减 remainingMs。 */
  watchdogArmedAt: number | null
  watchdogPausedAt: number | null
  spawnAt: number
  /** replay buffer：subscribe 时回放给新订阅方（上限 MAX_REPLAY_FRAMES）。 */
  replay: ChatStreamDelta[]
}

interface Subscriber {
  handlers: ChatStreamHandlers
}

const MAX_REPLAY_FRAMES = 64
/** 默认 watchdog：120s 累计活跃时间（不含后台暂停时间）。 */
const DEFAULT_WATCHDOG_MS = 120_000

/**
 * 流式 fetch + SSE 解析：抽离成纯函数供 runtime 注入，便于测试假实现替换。
 * 默认实现走真实 fetch；测试场景用 setFetchImpl 替换。
 */
export type SpawnFetcher = (
  input: ChatStreamInput,
  signal: AbortSignal,
  baseUrl: string,
  token: string | null,
) => Promise<{
  status: number
  statusText: string
  contentType: string
  body: ReadableStream<Uint8Array> | null
}>

let _fetcher: SpawnFetcher | null = null
let _resolveBase: () => string = () => ''
let _resolveToken: () => string | null = () => null

export function setStreamDeps(deps: {
  fetcher: SpawnFetcher
  resolveBase: () => string
  resolveToken: () => string | null
}): void {
  _fetcher = deps.fetcher
  _resolveBase = deps.resolveBase
  _resolveToken = deps.resolveToken
}

/**
 * 进程级 singleton。
 *
 * 注意：所有方法都是同步的"轻量操作"；实际 fetch / SSE 解析在 runtime
 * 内部异步跑（spawn 后立刻返回 handle，订阅方通过 subscribe 拿 delta）。
 */
class AiStreamRuntime {
  private streams = new Map<string, StreamEntry>()
  private started = false
  private unsubscribeLifecycle: (() => void) | null = null
  /** 流活跃计数：runtime 知道是否有流在跑（给 native 层 / debug 用）。 */
  private activeCount = 0

  start(): void {
    if (this.started) return
    this.started = true
    this.unsubscribeLifecycle = appLifecycleHub.on((evt) => this.onLifecycle(evt))
  }

  stop(): void {
    if (!this.started) return
    this.unsubscribeLifecycle?.()
    this.unsubscribeLifecycle = null
    // 不 abort 现有流——交给进程退出；浏览器关闭即全部丢，符合用户预期。
    this.started = false
  }

  /** 给消费方看的瞬时指标。 */
  getStats(): { activeCount: number; totalRegistered: number } {
    return {
      activeCount: this.activeCount,
      totalRegistered: this.streams.size,
    }
  }

  /**
   * 启动一个 chat 流。同 id 重复调用：流在跑则返回旧 handle（同一引用）；
   * 流已结束则用新 handlers 起新流（典型场景：用户重发问题）。
   */
  spawnChat(id: string, input: ChatStreamInput, handlers: ChatStreamHandlers): ChatStreamHandle {
    this.start()

    const existing = this.streams.get(id)
    if (existing && (existing.status === 'running' || existing.status === 'idle')) {
      // 幂等：流在跑，新增订阅方；不动现有 fetch，返回原 handle 引用。
      existing.subs.add({ handlers })
      return existing.handle
    }

    // 流已结束 / 第一次起：建新条目
    const ctrl = new AbortController()
    const entry: StreamEntry = {
      id,
      kind: 'chat',
      ctrl,
      status: 'running',
      abortReason: null,
      subs: new Set([{ handlers }]),
      // handle 字段后填（避免循环引用）
      handle: undefined as unknown as ChatStreamHandle,
      watchdogRemainingMs: DEFAULT_WATCHDOG_MS,
      watchdogTimer: null,
      watchdogArmedAt: null,
      watchdogPausedAt: null,
      spawnAt: Date.now(),
      replay: [],
    }
    const handle: ChatStreamHandle = {
      id,
      abort: () => this.abort(id),
      status: () => entry.status,
    }
    entry.handle = handle
    this.streams.set(id, entry)
    this.activeCount++

    // 启动 watchdog（隐藏态时跑空跑时长会被 pause 回收）。
    this.armWatchdog(entry)

    // 异步跑 fetch + SSE 解析
    void this.runChat(entry, input)

    return handle
  }

  /** 订阅已有流（同 id）；不存在则返回 no-op 退订函数。 */
  subscribe(id: string, handlers: ChatStreamHandlers): () => void {
    const entry = this.streams.get(id)
    if (!entry) return () => {}
    const sub: Subscriber = { handlers }
    entry.subs.add(sub)
    // replay 缓冲回放给新订阅方（最终态也回放一次，让晚到的 UI 拿到完整内容）
    for (const delta of entry.replay) {
      try {
        sub.handlers.onDelta?.(delta)
      } catch (err) {
        // eslint-disable-next-line no-console
        console.error('[aiStreamRuntime] replay onDelta error', err)
      }
    }
    // 已结束的流：补发一次终态（onDone 或 onError）
    if (entry.status === 'done') {
      try {
        const last = entry.replay[entry.replay.length - 1]
        sub.handlers.onDone?.(last?.usage)
      } catch (err) {
        // eslint-disable-next-line no-console
        console.error('[aiStreamRuntime] replay onDone error', err)
      }
    } else if (entry.status === 'error' || entry.status === 'aborted') {
      try {
        sub.handlers.onError?.(
          errorForReason(entry.abortReason ?? 'network'),
          entry.abortReason ?? 'network',
        )
      } catch (err) {
        // eslint-disable-next-line no-console
        console.error('[aiStreamRuntime] replay onError error', err)
      }
    }
    return () => {
      entry.subs.delete(sub)
    }
  }

  /** 列出当前所有流 id（debug / 测试用）。 */
  listStreamIds(): string[] {
    return [...this.streams.keys()]
  }

  /**
   * 用户主动取消。只有用户级语义才走这里；服务端错误 / 网络中断由 runChat 自己处理。
   */
  abort(id: string): boolean {
    const entry = this.streams.get(id)
    if (!entry) return false
    if (entry.status !== 'running' && entry.status !== 'idle') return false
    entry.status = 'aborted'
    entry.abortReason = 'user'
    this.clearWatchdog(entry)
    this.activeCount = Math.max(0, this.activeCount - 1)
    entry.ctrl.abort()
    const err = errorForReason('user')
    for (const sub of entry.subs) {
      try {
        sub.handlers.onError?.(err, 'user')
      } catch (e) {
        // eslint-disable-next-line no-console
        console.error('[aiStreamRuntime] onError fanout error', e)
      }
    }
    // 终态：保留 entry 一段时间供迟到订阅方 replay，TTL 由后台 GC 兜底（M2）。
    return true
  }

  // ---- 内部 ----

  private onLifecycle(evt: LifecycleEvent): void {
    if (evt === 'hidden' || evt === 'frozen') {
      for (const entry of this.streams.values()) {
        if (entry.status !== 'running') continue
        if (entry.watchdogPausedAt !== null) continue
        this.pauseWatchdog(entry)
      }
    } else if (evt === 'visible' || evt === 'resumed') {
      for (const entry of this.streams.values()) {
        if (entry.status !== 'running') continue
        if (entry.watchdogPausedAt === null) continue
        this.resumeWatchdog(entry)
      }
    }
  }

  private armWatchdog(entry: StreamEntry): void {
    if (appLifecycleHub.isHidden()) {
      // 启动时已经处于后台：直接进入暂停态，等 visible 再 arm
      entry.watchdogPausedAt = Date.now()
      return
    }
    const ms = entry.watchdogRemainingMs
    if (ms <= 0) {
      this.triggerWatchdog(entry)
      return
    }
    entry.watchdogTimer = setTimeout(() => this.triggerWatchdog(entry), ms)
    entry.watchdogArmedAt = Date.now()
  }

  private pauseWatchdog(entry: StreamEntry): void {
    if (entry.watchdogTimer !== null) {
      clearTimeout(entry.watchdogTimer)
      entry.watchdogTimer = null
      // 把"已活跃时长"从剩余预算中扣掉；暂停时长不计入。
      if (entry.watchdogArmedAt !== null) {
        const elapsed = Date.now() - entry.watchdogArmedAt
        entry.watchdogRemainingMs = Math.max(0, entry.watchdogRemainingMs - elapsed)
        entry.watchdogArmedAt = null
      }
    }
    if (entry.watchdogPausedAt !== null) return
    entry.watchdogPausedAt = Date.now()
  }

  private resumeWatchdog(entry: StreamEntry): void {
    if (entry.watchdogPausedAt === null) return
    entry.watchdogPausedAt = null
    if (appLifecycleHub.isHidden()) {
      // 极短闪烁：visible 后立即 hidden（极端）；不再 arm
      return
    }
    this.armWatchdog(entry)
  }

  private clearWatchdog(entry: StreamEntry): void {
    if (entry.watchdogTimer !== null) {
      clearTimeout(entry.watchdogTimer)
      entry.watchdogTimer = null
    }
    entry.watchdogArmedAt = null
    entry.watchdogPausedAt = null
  }

  private triggerWatchdog(entry: StreamEntry): void {
    if (entry.status !== 'running') return
    // 切回前台时 watchdog 命中：依据最近一次 hidden 时间窗判断 reason。
    // 简化策略：watchdog 触发统一记 'network'；UI 层看到 reason='network' 时
    // 如果上一次 hidden 距今 < 60s 则渲染为"网络中断，可重试"非"超时"。
    entry.status = 'error'
    entry.abortReason = 'watchdog'
    this.clearWatchdog(entry)
    this.activeCount = Math.max(0, this.activeCount - 1)
    entry.ctrl.abort()
    const err = errorForReason('watchdog')
    for (const sub of entry.subs) {
      try {
        sub.handlers.onError?.(err, 'watchdog')
      } catch (e) {
        // eslint-disable-next-line no-console
        console.error('[aiStreamRuntime] onError watchdog fanout', e)
      }
    }
  }

  private async runChat(entry: StreamEntry, input: ChatStreamInput): Promise<void> {
    const fetcher = _fetcher
    const base = _resolveBase()
    const token = _resolveToken()
    if (!fetcher) {
      this.failEntry(entry, new Error('aiStreamRuntime 未注入 fetcher；请在 main.ts 调用 setStreamDeps'), 'network')
      return
    }
    try {
      // 401 单飞续期一次：runtime 不直接依赖 auth store，由 fetcher 内部处理（M1 暂沿用 llm-bff 同款）。
      const res = await fetcher(input, entry.ctrl.signal, base, token)
      if (!res || res.status >= 400 || !res.body) {
        // 把 fetch-level HTTP 错误映射为 error frame，走 'server-error' 路径
        const err = new Error(`stream failed: ${res?.status ?? 0} ${res?.statusText ?? ''}`)
        this.failEntry(entry, err, res && res.status >= 500 ? 'network' : 'server-error')
        return
      }
      const ct = (res.contentType || '').toLowerCase()
      if (ct.includes('text/html')) {
        this.failEntry(
          entry,
          new Error('对话流返回了 HTML 页面而非 SSE 流：多为移动端打包漏注入 VITE_API_BASE，请检查打包配置'),
          'network',
        )
        return
      }
      const reader = res.body.getReader()
      const decoder = new TextDecoder()
      let buf = ''
      let sawDelta = false
      let finalUsage: ChatStreamDelta['usage'] | undefined

      while (true) {
        const { done, value } = await reader.read()
        if (done) break
        buf += decoder.decode(value, { stream: true })
        let nl: number
        while ((nl = buf.indexOf('\n\n')) >= 0) {
          const chunk = buf.slice(0, nl)
          buf = buf.slice(nl + 2)
          if (!chunk.startsWith('data: ')) continue
          const data = chunk.slice(6)
          if (data === '[DONE]') {
            this.completeEntry(entry, finalUsage, sawDelta)
            return
          }
          let delta: ChatStreamDelta
          try {
            delta = JSON.parse(data) as ChatStreamDelta
          } catch {
            // eslint-disable-next-line no-console
            console.warn('[aiStreamRuntime] bad SSE frame:', data)
            continue
          }
          if (delta.error) {
            this.failEntry(entry, new Error(delta.error), 'server-error')
            return
          }
          if (delta.retry && !delta.content && !delta.done) {
            // 进度帧：单独通知 onRetry，但不计入 sawDelta / 不计入 replay（仅终态帧值得 replay）
            for (const sub of entry.subs) {
              try {
                sub.handlers.onRetry?.(delta.retry!)
              } catch (e) {
                // eslint-disable-next-line no-console
                console.error('[aiStreamRuntime] onRetry fanout', e)
              }
            }
            continue
          }
          if (delta.usage) finalUsage = delta.usage
          sawDelta = true
          // 维护 replay 缓冲（终态帧也保留，迟到订阅方能拿到 usage）
          entry.replay.push(delta)
          if (entry.replay.length > MAX_REPLAY_FRAMES) {
            entry.replay.splice(0, entry.replay.length - MAX_REPLAY_FRAMES)
          }
          for (const sub of entry.subs) {
            try {
              sub.handlers.onDelta?.(delta)
            } catch (e) {
              // eslint-disable-next-line no-console
              console.error('[aiStreamRuntime] onDelta fanout', e)
            }
          }
        }
      }
      // 流正常关闭
      this.completeEntry(entry, finalUsage, sawDelta)
    } catch (err) {
      if ((err as Error).name === 'AbortError') {
        // 用户主动 abort 已由 abort() 处理；这里到的是 watchdog/network 触发的 abort
        // 若 entry.status 仍为 running，说明 abort 来源不在 runtime 自身——按 network 处理
        if (entry.status === 'running') {
          this.failEntry(entry, new Error('流被中止'), 'network')
        }
        return
      }
      this.failEntry(entry, err instanceof Error ? err : new Error(String(err)), 'network')
    }
  }

  private completeEntry(entry: StreamEntry, finalUsage: ChatStreamDelta['usage'] | undefined, sawDelta: boolean): void {
    if (entry.status !== 'running') return // 已被 abort / error 接管
    if (!sawDelta && !finalUsage) {
      // 流正常关闭但一帧都没有：视为错误而非静默成功（空气泡陷阱）
      this.failEntry(entry, new Error('模型未返回内容（空流）'), 'empty')
      return
    }
    entry.status = 'done'
    this.clearWatchdog(entry)
    this.activeCount = Math.max(0, this.activeCount - 1)
    for (const sub of entry.subs) {
      try {
        sub.handlers.onDone?.(finalUsage)
      } catch (e) {
        // eslint-disable-next-line no-console
        console.error('[aiStreamRuntime] onDone fanout', e)
      }
    }
  }

  private failEntry(entry: StreamEntry, err: Error, reason: StreamAbortReason): void {
    if (entry.status !== 'running') return
    entry.status = reason === 'user' ? 'aborted' : 'error'
    entry.abortReason = reason
    this.clearWatchdog(entry)
    this.activeCount = Math.max(0, this.activeCount - 1)
    for (const sub of entry.subs) {
      try {
        sub.handlers.onError?.(err, reason)
      } catch (e) {
        // eslint-disable-next-line no-console
        console.error('[aiStreamRuntime] onError failEntry fanout', e)
      }
    }
  }
}

function errorForReason(reason: StreamAbortReason): Error {
  switch (reason) {
    case 'user':
      return new Error('已停止')
    case 'watchdog':
      return new Error('网络中断，可重试')
    case 'network':
      return new Error('网络异常，请检查连接')
    case 'server-error':
      return new Error('服务端错误')
    case 'empty':
      return new Error('模型未返回内容（空流）')
    case 'replaced':
      return new Error('流已被替换')
  }
}

/** 进程级单例；HMR 兼容。 */
const RUNTIME_KEY = '__openpocket_aiStreamRuntime__'
type GlobalWithRuntime = typeof globalThis & { [RUNTIME_KEY]?: AiStreamRuntime }
const g = globalThis as GlobalWithRuntime
export const aiStreamRuntime: AiStreamRuntime = g[RUNTIME_KEY] ?? (g[RUNTIME_KEY] = new AiStreamRuntime())
