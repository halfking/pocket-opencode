/**
 * localagent/runtime.ts — 本地智能体进程级 singleton。
 *
 * 职责对齐 aiStreamRuntime 的设计契约(离场 ≠ 取消、replay、用户级 abort),
 * 在其上多管一层「多回合循环」:
 *   - 会话(时间线 + 状态)持有与 localStorage 持久化;
 *   - send:为会话装配 expert/skills/tools/system prompt 并启动 agent-loop;
 *   - 审批闸门:medium/high 工具暂停循环,pending 审批可 respond 放行/拒绝;
 *   - 事件分发:订阅者(Pinia store / 测试)收 AgentEvent。
 *
 * 平台注入:localStorage 缺失时(测试)自动降级为内存实现。
 *
 * 注:session.history 是「跨 send 的对话近似」——只保留用户 prompt 与助手
 * 最终/中间说明文本,单次任务内部的工具往返不重放(由时间线留存)。这让
 * 下一次 send 的上下文保持精简,MVP 取舍。
 */

import { runAgentLoop } from './agent-loop.ts'
import { builtinExperts, getExpert } from './experts.ts'
import { createLlmStreamFn } from './llm-stream.ts'
import { builtinSkills, SkillRegistry } from './skills.ts'
import { buildSystemPrompt } from './system-prompt.ts'
import { createBuiltinTools, resetPlanState } from './tools/index.ts'
import type { AgentChatMessage, AgentEvent, Expert, RunStatus, ToolRisk } from './types.ts'

// ---------------------------------------------------------------------------
// 时间线(持久化数据结构;UI 展示以时间线为准)
// ---------------------------------------------------------------------------

export interface PlanItemView {
  title: string
  notes?: string
  status: 'todo' | 'in_progress' | 'done'
}

export interface TimelineItem {
  kind: 'user' | 'assistant' | 'tool' | 'plan' | 'system'
  /** user/assistant:文本;tool:工具名。 */
  text?: string
  /** assistant 流式尾标(未定型);定型后置 false。 */
  interim?: boolean
  id?: string
  name?: string
  args?: Record<string, unknown>
  state?: 'running' | 'completed' | 'error' | 'denied'
  result?: string
  error?: string
  data?: unknown
  durationMs?: number
  risk?: ToolRisk
  items?: PlanItemView[]
  at: number
}

export interface PendingApproval {
  toolCallId: string
  tool: string
  label: string
  args: Record<string, unknown>
  risk: ToolRisk
}

export interface AgentSession {
  id: string
  title: string
  expert: string
  createdAt: number
  updatedAt: number
  status: RunStatus
  timeline: TimelineItem[]
  usage: { promptTokens: number; completionTokens: number }
  /** 跨 send 的对话近似(见头注释)。 */
  history: AgentChatMessage[]
}

export interface RunHandle {
  readonly sessionId: string
  abort(): void
}

export type RuntimeEventListener = (sessionId: string, evt: AgentEvent) => void

// ---------------------------------------------------------------------------
// 持久化(截断 + 上限;失败静默)
// ---------------------------------------------------------------------------

const LS_KEY = 'pocket:localagent:sessions'
const MAX_SESSIONS = 20
const MAX_ITEMS = 200
const MAX_RESULT_LEN = 4096

interface StoreLike {
  getItem(k: string): string | null
  setItem(k: string, v: string): void
  removeItem(k: string): void
}

function resolveStore(): StoreLike | null {
  if (typeof localStorage !== 'undefined') return localStorage
  return null
}

function sanitizeForPersist(s: AgentSession): AgentSession {
  const timeline = s.timeline.slice(-MAX_ITEMS).map((it) =>
    it.kind === 'tool' && (it.result?.length ?? 0) > MAX_RESULT_LEN
      ? { ...it, result: `${(it.result as string).slice(0, MAX_RESULT_LEN)}…(已截断)` }
      : it,
  )
  return { ...s, timeline }
}

function loadSessions(store: StoreLike | null): AgentSession[] {
  if (!store) return []
  try {
    const raw = store.getItem(LS_KEY)
    if (!raw) return []
    const parsed = JSON.parse(raw) as unknown
    if (!Array.isArray(parsed)) return []
    // 运行中状态归位 error(进程重启循环必然已断);字段做防御兜底,
    // 避免 localStorage 半旧/损坏数据把 UI 打崩。
    const fix = (st: unknown): RunStatus =>
      st === 'thinking' || st === 'tool_running' || st === 'waiting_approval'
        ? 'error'
        : st === 'aborted' || st === 'error' || st === 'done' || st === 'idle'
          ? st
          : 'error'
    return parsed
      .filter((s): s is Record<string, unknown> => Boolean(s) && typeof s === 'object')
      .map((s) => {
        const timeline = Array.isArray(s['timeline']) ? (s['timeline'] as TimelineItem[]) : []
        const history = Array.isArray(s['history']) ? (s['history'] as AgentChatMessage[]) : []
        const usage = (s['usage'] ?? {}) as Partial<AgentSession['usage']>
        return {
          ...(s as unknown as AgentSession),
          status: fix(s['status']),
          timeline: timeline.filter((it) => it && typeof it === 'object' && typeof it.kind === 'string'),
          history: history.filter((m) => m && (m.role === 'user' || m.role === 'assistant' || m.role === 'system') && typeof m.content === 'string'),
          usage: {
            promptTokens: Number(usage.promptTokens) || 0,
            completionTokens: Number(usage.completionTokens) || 0,
          },
        }
      })
  } catch {
    return []
  }
}

function persistSessions(store: StoreLike | null, sessions: AgentSession[]): void {
  if (!store) return
  try {
    store.setItem(LS_KEY, JSON.stringify(sessions.slice(0, MAX_SESSIONS).map(sanitizeForPersist)))
  } catch {
    // 配额满/序列化失败:静默(对齐 usage best-effort 风格)。
  }
}

// ---------------------------------------------------------------------------
// Runtime
// ---------------------------------------------------------------------------

export interface SendOptions {
  expert?: string
  /** 附加技能名(正文注入 prompt)。 */
  skills?: string[]
  model?: string
  /** 测试注入。 */
  streamFnOverride?: ReturnType<typeof createLlmStreamFn>
}

interface ApprovalEntry {
  sessionId: string
  approval: PendingApproval
  resolve: (allow: boolean) => void
}

class LocalAgentRuntime {
  private sessions = new Map<string, AgentSession>()
  private order: string[] = []
  private listeners = new Set<RuntimeEventListener>()
  private activeRuns = new Map<string, () => void>()
  private approvals = new Map<string, ApprovalEntry>()
  private skillRegistry = new SkillRegistry(builtinSkills)
  private store: StoreLike | null

  constructor(store: StoreLike | null = resolveStore()) {
    this.store = store
    for (const s of loadSessions(store)) {
      this.sessions.set(s.id, s)
      this.order.push(s.id)
    }
  }

  // ---- 查询 ----

  listSessions(): AgentSession[] {
    return this.order.map((id) => this.sessions.get(id)).filter((s): s is AgentSession => Boolean(s))
  }

  getSession(id: string): AgentSession | undefined {
    return this.sessions.get(id)
  }

  listExperts(): Expert[] {
    return builtinExperts
  }

  listSkills() {
    return this.skillRegistry.list()
  }

  getPendingApproval(sessionId: string): PendingApproval | undefined {
    for (const entry of this.approvals.values()) {
      if (entry.sessionId === sessionId) return entry.approval
    }
    return undefined
  }

  // ---- 订阅 ----

  subscribe(listener: RuntimeEventListener): () => void {
    this.listeners.add(listener)
    return () => this.listeners.delete(listener)
  }

  // ---- 会话管理 ----

  createSession(expert = 'general', title?: string): AgentSession {
    const id = `la-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 8)}`
    const session: AgentSession = {
      id,
      title: title ?? '新任务',
      expert,
      createdAt: Date.now(),
      updatedAt: Date.now(),
      status: 'idle',
      timeline: [],
      usage: { promptTokens: 0, completionTokens: 0 },
      history: [],
    }
    this.sessions.set(id, session)
    this.order.unshift(id)
    this.trimSessions()
    this.persist()
    return session
  }

  deleteSession(id: string): void {
    this.abort(id)
    this.sessions.delete(id)
    this.order = this.order.filter((x) => x !== id)
    this.persist()
  }

  renameSession(id: string, title: string): void {
    const s = this.sessions.get(id)
    if (!s) return
    s.title = title
    this.persist()
  }

  // ---- 运行 ----

  isRunning(sessionId: string): boolean {
    return this.activeRuns.has(sessionId)
  }

  /**
   * 发起一轮任务。同一会话串行:运行中调用返回 null(UI 禁用发送兜底)。
   */
  send(sessionId: string, prompt: string, opts: SendOptions = {}): RunHandle | null {
    const session = this.sessions.get(sessionId)
    if (!session) return null
    if (this.activeRuns.has(sessionId)) return null

    const expert = getExpert(opts.expert ?? session.expert)
    session.expert = expert.name
    const attachedSkills = (opts.skills ?? [])
      .map((n) => this.skillRegistry.get(n))
      .filter((s): s is NonNullable<ReturnType<SkillRegistry['get']>> => Boolean(s))

    // 附加技能正文内联注入 prompt(pi /skill: 命令同语义)。
    const promptText = attachedSkills.length
      ? `${prompt}\n\n${attachedSkills.map((s) => `<skill name="${s.name}">\n${s.body}\n</skill>`).join('\n\n')}`
      : prompt
    this.pushItem(session, { kind: 'user', text: prompt, at: Date.now() })
    session.history.push({ role: 'user', content: promptText })
    if (session.title === '新任务' && prompt) session.title = prompt.slice(0, 24)

    const tools = this.assembleTools(expert)
    const systemPrompt = buildSystemPrompt({
      expert,
      skills: this.skillRegistry.list(),
      tools,
      platform: describePlatform(),
    })
    resetPlanState()

    const ctrl = new AbortController()
    const streamFn = opts.streamFnOverride ?? createLlmStreamFn({ sessionId, model: opts.model })

    const emitLocal = (evt: AgentEvent) => {
      this.applyEvent(session, evt)
      for (const l of this.listeners) {
        try {
          l(sessionId, evt)
        } catch {
          // 订阅者异常不拖垮循环。
        }
      }
    }

    const run = runAgentLoop({
      systemPrompt,
      history: [...session.history.slice(0, -1)], // 循环自拼本次 prompt;历史不含它
      prompt: promptText,
      tools,
      streamFn,
      approvalGate: (req) =>
        new Promise<boolean>((resolve) => {
          const label = tools.find((t) => t.name === req.tool)?.label ?? req.tool
          this.approvals.set(req.toolCallId, {
            sessionId,
            approval: { toolCallId: req.toolCallId, tool: req.tool, label, args: req.args, risk: req.risk },
            resolve,
          })
          // 注册后再发事件:订阅方 sync 时 getPendingApproval 一定能看到。
          emitLocal({
            type: 'approval_required',
            toolCallId: req.toolCallId,
            tool: req.tool,
            args: req.args,
            risk: req.risk,
          })
        }),
      onEvent: emitLocal,
      signal: ctrl.signal,
    })
      .catch(() => {
        // runAgentLoop 内部已把错误转为 run_error 事件;此处兜底防未处理拒绝。
        if (session.status === 'thinking' || session.status === 'tool_running' || session.status === 'waiting_approval') {
          session.status = 'error'
        }
      })
      .finally(() => {
        this.activeRuns.delete(sessionId)
        // 清掉该会话残留审批(abort 时 gate 永不 resolve 也会泄漏)。
        for (const [cid, entry] of this.approvals) {
          if (entry.sessionId !== sessionId) continue
          this.approvals.delete(cid)
          entry.resolve(false)
        }
        this.persist()
      })
    void run

    this.activeRuns.set(sessionId, () => ctrl.abort())
    this.persist()
    return { sessionId, abort: () => ctrl.abort() }
  }

  abort(sessionId: string): boolean {
    const abortFn = this.activeRuns.get(sessionId)
    if (!abortFn) return false
    abortFn()
    return true
  }

  /** 审批响应:allow=true 放行;false 拒绝(结果回灌模型改道)。 */
  respondApproval(sessionId: string, toolCallId: string, allow: boolean): boolean {
    const entry = this.approvals.get(toolCallId)
    if (!entry || entry.sessionId !== sessionId) return false
    this.approvals.delete(toolCallId)
    entry.resolve(allow)
    return true
  }

  // ---- 内部 ----

  private assembleTools(expert: Expert) {
    const all = createBuiltinTools({ skills: this.skillRegistry })
    if (!expert.allowedTools) return all
    const allow = new Set(expert.allowedTools)
    return all.filter((t) => allow.has(t.name))
  }

  private applyEvent(session: AgentSession, evt: AgentEvent): void {
    session.updatedAt = Date.now()
    switch (evt.type) {
      case 'run_started':
        session.status = 'thinking'
        break
      case 'status':
        session.status = evt.status
        break
      case 'text_delta': {
        const last = session.timeline[session.timeline.length - 1]
        if (last && last.kind === 'assistant' && last.interim) {
          last.text = (last.text ?? '') + evt.text
        } else {
          this.pushItem(session, { kind: 'assistant', text: evt.text, interim: true, at: Date.now() })
        }
        break
      }
      case 'assistant': {
        const last = session.timeline[session.timeline.length - 1]
        if (last && last.kind === 'assistant' && last.interim) {
          last.interim = false
          last.text = evt.text
        } else {
          this.pushItem(session, { kind: 'assistant', text: evt.text, interim: evt.interim, at: Date.now() })
        }
        if (!evt.interim && evt.text) {
          session.history.push({ role: 'assistant', content: evt.text })
        }
        break
      }
      case 'tool_call': {
        if (evt.state === 'running') {
          this.pushItem(session, {
            kind: 'tool',
            id: evt.id,
            name: evt.name,
            args: evt.args,
            state: 'running',
            risk: evt.risk,
            at: Date.now(),
          })
        } else {
          const existing = [...session.timeline].reverse().find((it) => it.kind === 'tool' && it.id === evt.id)
          if (existing) {
            existing.state = evt.state
            existing.result = evt.result
            existing.error = evt.error
            existing.data = evt.data
            existing.durationMs = evt.durationMs
          } else {
            this.pushItem(session, {
              kind: 'tool',
              id: evt.id,
              name: evt.name,
              args: evt.args,
              state: evt.state,
              result: evt.result,
              error: evt.error,
              data: evt.data,
              durationMs: evt.durationMs,
              risk: evt.risk,
              at: Date.now(),
            })
          }
        }
        break
      }
      case 'approval_required':
        session.status = 'waiting_approval'
        break
      case 'plan': {
        const existing = session.timeline.find((it) => it.kind === 'plan')
        if (existing) {
          existing.items = evt.items
          existing.at = Date.now()
        } else {
          this.pushItem(session, { kind: 'plan', items: evt.items, at: Date.now() })
        }
        break
      }
      case 'usage':
        session.usage.promptTokens += evt.promptTokens
        session.usage.completionTokens += evt.completionTokens
        break
      case 'run_done':
        session.status = evt.reason === 'aborted' ? 'aborted' : evt.reason === 'completed' ? 'idle' : 'error'
        break
      case 'run_error':
        // 错误必须可见(空气泡/静默失败陷阱):落一条系统条目。
        this.pushItem(session, { kind: 'system', text: `执行中断:${evt.error}`, at: Date.now() })
        session.status = 'error'
        break
    }
  }

  private pushItem(session: AgentSession, item: TimelineItem): void {
    session.timeline.push(item)
    if (session.timeline.length > MAX_ITEMS) {
      session.timeline.splice(0, session.timeline.length - MAX_ITEMS)
    }
  }

  private trimSessions(): void {
    while (this.order.length > MAX_SESSIONS) {
      const oldest = this.order.pop()
      if (oldest) this.sessions.delete(oldest)
    }
  }

  private persist(): void {
    persistSessions(this.store, this.listSessions())
  }
}

function describePlatform(): string {
  try {
    const ua = globalThis.navigator?.userAgent ?? ''
    if (/Android/i.test(ua)) return 'Android WebView'
    if (/iPhone|iPad/i.test(ua)) return 'iOS WebView'
    if (ua) return 'Web'
  } catch {
    // ignore
  }
  return 'unknown'
}

/** 进程级单例;HMR 兼容(对齐 aiStreamRuntime 的全局挂载模式)。 */
const RUNTIME_KEY = '__openpocket_localAgentRuntime__'
type GlobalWithRuntime = typeof globalThis & { [RUNTIME_KEY]?: LocalAgentRuntime }
const g = globalThis as GlobalWithRuntime
export const localAgentRuntime: LocalAgentRuntime = g[RUNTIME_KEY] ?? (g[RUNTIME_KEY] = new LocalAgentRuntime())

/** 测试用:构造独立实例(不触碰全局单例)。 */
export function createLocalAgentRuntimeForTest(store: StoreLike | null = null): LocalAgentRuntime {
  return new LocalAgentRuntime(store)
}

export type { LocalAgentRuntime }
