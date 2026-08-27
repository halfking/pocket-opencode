/**
 * useSessionEvents — P1 会话工作台的实时事件状态（设计方案 v2 §4.3-1/2）。
 *
 * 数据流（契约见 docs/2026-08-27-p1-contracts-frozen.md §2/§3）：
 *   1. initIdempotentWsBus + subscribe('session.activity' / 'round.completed')，
 *      用 services/sessionEvents.ts（已冻结）的 parse 函数双层解包；
 *   2. 挂载时拉 GET /api/mobile/events/snapshot 做重连追赶对齐（快照而非回放）；
 *   3. 业务幂等：session.activity 按 last_event_at（次选 round_index）最新胜出；
 *      round.completed 按 session_id+round_index 去重覆盖；
 *   4. 断线降级：事件/快照均不可用时 eventsAvailable=false，调用方回落现有
 *      session store 消息流推导（deriveFallbackPhase / statsFromMessages），不白屏。
 *
 * 订阅写法参照 useInstanceApprovals.ts（initIdempotentWsBus + subscribe + cleanup）。
 *
 * 本文件顶层的 import 仅限 node --test 可直接加载的纯模块（vue / sessionEvents /
 * diffParse）；idempotentWsBus 与 api/http 会连带 import.meta.env / localStorage
 * 副作用，因此全部走函数内动态 import。
 */
import { computed, ref, type ComputedRef, type Ref } from 'vue'
import {
  parseRoundCompleted,
  parseSessionActivity,
  type EventsSnapshot,
  type EventsSnapshotSession,
  type RoundCompletedData,
  type SessionPhase,
} from '../../services/sessionEvents.ts'
import { extractDiffText, parseUnifiedDiff } from '../../utils/diffParse.ts'

export type { RoundCompletedData, SessionPhase } from '../../services/sessionEvents.ts'

/** 时间线消息的最小结构（与 stores/session.ts 的 Message 结构兼容，避免拉入 pinia）。 */
export interface TimelineMessageLike {
  id: string
  role: 'user' | 'assistant' | 'system'
  text: string
  content?: Array<{
    type: string
    state?: string
    name?: string
    input?: unknown
    output?: unknown
    error?: string
    durationMs?: number
  }> | null
  time: number
  streaming?: boolean
}

/** session.activity 归约后的当前会话活动态。 */
export interface SessionActivityState {
  phase: SessionPhase
  lastEventAt: number
  roundIndex: number
}

/** 状态条 phase 文案映射（契约 §4.3 冻结）。 */
export const PHASE_LABELS: Record<SessionPhase, string> = {
  thinking: '思考中',
  tool: '执行工具',
  file_write: '改文件中',
  pty: '跑命令',
  idle: '空闲',
}

// ---------------------------------------------------------------------------
// 纯函数（node --test 直接覆盖）
// ---------------------------------------------------------------------------

/**
 * 业务幂等规则 A：session.activity 最新胜出。
 * last_event_at 大者胜；相等时 round_index 大者胜（允许同时刻的 phase 切换覆盖）。
 */
export function newerActivityWins(
  current: SessionActivityState | null,
  incoming: { last_event_at: number; round_index: number },
): boolean {
  if (!current) return true
  if (incoming.last_event_at !== current.lastEventAt) {
    return incoming.last_event_at > current.lastEventAt
  }
  return incoming.round_index >= current.roundIndex
}

/** 快照行 → 活动态（phase 为 null 表示该会话无活跃事件，不覆盖已有状态）。 */
export function activityFromSnapshot(snap: EventsSnapshotSession): SessionActivityState | null {
  if (snap.phase === null) return null
  return {
    phase: snap.phase,
    lastEventAt: snap.last_event_at ?? 0,
    roundIndex: snap.round_index,
  }
}

/** 业务幂等规则 B 的键：round.completed 按 session_id+round_index 去重覆盖。 */
export function roundKey(sessionId: string, roundIndex: number): string {
  return `${sessionId}:${roundIndex}`
}

/**
 * 轮次分组：用户 prompt 开新轮；编号 = 1-based 用户消息序数（与契约 round_index
 * 同规则）。首条用户消息之前的杂散 assistant/system 消息并入第 1 轮，不产生额外轮。
 */
export interface RoundGroup {
  index: number
  messages: TimelineMessageLike[]
}

export function groupMessagesIntoRounds(messages: TimelineMessageLike[]): RoundGroup[] {
  const groups: RoundGroup[] = []
  let roundCount = 0
  let lead: TimelineMessageLike[] = []
  for (const m of messages) {
    if (m.role === 'user') {
      roundCount++
      groups.push({
        index: roundCount,
        messages: lead.length ? [...lead, m] : [m],
      })
      lead = []
    } else if (groups.length === 0) {
      lead.push(m)
    } else {
      groups[groups.length - 1].messages.push(m)
    }
  }
  if (lead.length) groups.push({ index: 1, messages: lead })
  return groups
}

/** 一轮内的"过程事件"数：assistant 文本块 + 结构化 content 项（用户消息不计）。 */
export function countRoundEvents(group: RoundGroup): number {
  let n = 0
  for (const m of group.messages) {
    if (m.role !== 'assistant') continue
    if (m.text && m.text.trim()) n++
    n += m.content?.length ?? 0
  }
  return n
}

/** 无 round.completed 事件时的轮摘要降级：取该轮最后一条 assistant 文本首行截断。 */
export function roundSummaryFallback(group: RoundGroup): string {
  for (let i = group.messages.length - 1; i >= 0; i--) {
    const m = group.messages[i]
    if (m.role === 'assistant' && m.text && m.text.trim()) {
      const firstLine = m.text.trim().split('\n')[0]
      return firstLine.length > 60 ? `${firstLine.slice(0, 60)}…` : firstLine
    }
  }
  const user = group.messages.find((m) => m.role === 'user')
  return user ? truncate(user.text, 60) : ''
}

export function truncate(text: string, max: number): string {
  const t = (text ?? '').trim()
  if (t.length <= max) return t
  return `${t.slice(0, max)}…`
}

/** 工具名 → phase 近似（事件不可用时的降级推导用）。 */
const FILE_WRITE_TOOL_RE = /edit|write|patch|apply/i
const PTY_TOOL_RE = /bash|shell|terminal|exec|command|run/i
export function phaseFromToolName(name: string | undefined): SessionPhase {
  if (!name) return 'tool'
  if (FILE_WRITE_TOOL_RE.test(name)) return 'file_write'
  if (PTY_TOOL_RE.test(name)) return 'pty'
  return 'tool'
}

/**
 * 断线降级：从现有 session store 消息流推导当前 phase。
 * 非流式 → idle；流式且最后 assistant 块中有 running/pending 工具 → 工具 phase
 * （按工具名细分 file_write/pty/tool），否则 → thinking。
 */
export function deriveFallbackPhase(args: {
  streaming: boolean
  messages: TimelineMessageLike[]
}): SessionPhase | null {
  if (!args.streaming) return 'idle'
  for (let i = args.messages.length - 1; i >= 0; i--) {
    const m = args.messages[i]
    if (m.role !== 'assistant') break
    const contents = m.content ?? []
    for (let j = contents.length - 1; j >= 0; j--) {
      const c = contents[j]
      if (c.type === 'tool' && (c.state === 'running' || c.state === 'pending')) {
        return phaseFromToolName(c.name)
      }
    }
  }
  return 'thinking'
}

/** 详情抽屉/导出用的会话统计（与旧 SessionDetailView 的能力对齐）。 */
export interface SessionStats {
  added: number
  removed: number
  files: number
  messageCount: number
}

/** 事件可用路径：round.completed 的变更统计按轮累计（轮覆盖去重后求和）。 */
export function statsFromRounds(
  rounds: RoundCompletedData[],
  messageCount: number,
): SessionStats {
  let added = 0
  let removed = 0
  let files = 0
  for (const r of rounds) {
    added += r.changes.added
    removed += r.changes.removed
    files += r.changes.files
  }
  return { added, removed, files, messageCount }
}

/** 断线降级路径：从消息流里的工具输出 diff 解析累计 +/- 与文件数。 */
export function statsFromMessages(messages: TimelineMessageLike[]): SessionStats {
  let added = 0
  let removed = 0
  let files = 0
  let seenDiff = false
  for (const m of messages) {
    for (const c of m.content ?? []) {
      if (c.type !== 'tool') continue
      const diffText = extractDiffText(c.output)
      if (!diffText) continue
      const parsed = parseUnifiedDiff(diffText)
      if (!parsed) continue
      seenDiff = true
      added += parsed.adds
      removed += parsed.dels
      const fileHeads = diffText.match(/^diff --git /gm)
      files += fileHeads ? fileHeads.length : 1
    }
  }
  if (!seenDiff) files = 0
  return { added, removed, files, messageCount: messages.length }
}

/** 导出 markdown（迁移旧 SessionDetailView.exportSummary，数据源换成轮摘要）。 */
export function buildSessionMarkdown(args: {
  title: string
  sessionId: string
  stats: SessionStats
  rounds: Array<{ index: number; data: RoundCompletedData | null; fallbackSummary: string }>
}): string {
  const lines: string[] = []
  lines.push(`# ${args.title || args.sessionId}`)
  lines.push('')
  lines.push('## 基本信息')
  lines.push(`- 会话ID: ${args.sessionId}`)
  lines.push(`- 导出时间: ${new Date().toISOString()}`)
  lines.push('')
  lines.push('## 代码变更')
  lines.push(`- 新增: +${args.stats.added} 行`)
  lines.push(`- 删除: -${args.stats.removed} 行`)
  lines.push(`- 文件: ${args.stats.files} 个`)
  lines.push(`- 消息: ${args.stats.messageCount} 条`)
  lines.push('')
  lines.push('## 轮次摘要')
  if (args.rounds.length === 0) {
    lines.push('暂无记录')
  } else {
    for (const r of args.rounds) {
      if (r.data) {
        const status = r.data.status
        lines.push(
          `- 轮 ${r.index} [${status}] +${r.data.changes.added}/-${r.data.changes.removed} · ${r.data.changes.files} 文件：${r.data.summary}`,
        )
      } else {
        lines.push(`- 轮 ${r.index}：${r.fallbackSummary}`)
      }
    }
  }
  return lines.join('\n')
}

// ---------------------------------------------------------------------------
// 事件 → 状态的纯 reducer（把"过滤 + 幂等归约"从订阅回调里抽出来，node --test
// 直接喂 env 即可覆盖整条链路，无需拉起 WS）
// ---------------------------------------------------------------------------

/** session.activity 事件归约：不匹配/过期返回原引用，否则返回新状态。 */
export function applySessionActivityEvent(
  current: SessionActivityState | null,
  env: unknown,
  scope: { sessionId: string; instanceId: string },
): SessionActivityState | null {
  const evt = parseSessionActivity(env)
  if (!evt) return current
  if (evt.data.session_id !== scope.sessionId) return current
  if (evt.data.instance_id !== scope.instanceId) return current
  if (!newerActivityWins(current, evt.data)) return current
  return {
    phase: evt.data.phase,
    lastEventAt: evt.data.last_event_at,
    roundIndex: evt.data.round_index,
  }
}

/** round.completed 事件归约：session_id+round_index 去重覆盖；无变化返回原引用。 */
export function applyRoundCompletedEvent(
  rounds: Map<string, RoundCompletedData>,
  env: unknown,
  scope: { sessionId: string; instanceId: string },
): Map<string, RoundCompletedData> {
  const evt = parseRoundCompleted(env)
  if (!evt) return rounds
  if (evt.data.session_id !== scope.sessionId) return rounds
  if (evt.data.instance_id !== scope.instanceId) return rounds
  const key = roundKey(evt.data.session_id, evt.data.round_index)
  const prev = rounds.get(key)
  if (prev === evt.data) return rounds
  const next = new Map(rounds)
  next.set(key, evt.data)
  return next
}

/** 快照行归约（§3 追赶对齐）：最新 phase 覆盖 + 最近一轮缓存写入。 */
export function applySnapshotRow(
  current: SessionActivityState | null,
  rounds: Map<string, RoundCompletedData>,
  row: EventsSnapshotSession,
): { activity: SessionActivityState | null; rounds: Map<string, RoundCompletedData> } {
  const snapActivity = activityFromSnapshot(row)
  const nextActivity =
    snapActivity &&
    newerActivityWins(current, {
      last_event_at: snapActivity.lastEventAt,
      round_index: snapActivity.roundIndex,
    })
      ? snapActivity
      : current
  let nextRounds = rounds
  if (row.latest_round) {
    const key = roundKey(row.latest_round.session_id, row.latest_round.round_index)
    const merged = new Map(rounds)
    merged.set(key, row.latest_round)
    nextRounds = merged
  }
  return { activity: nextActivity, rounds: nextRounds }
}

// ---------------------------------------------------------------------------
// Composable（薄封装：订阅 + 快照 + 响应式状态）
// ---------------------------------------------------------------------------

export interface UseSessionEventsReturn {
  /** 快照或事件任一成功后为 true；false 时调用方走消息流降级推导。 */
  eventsAvailable: Ref<boolean>
  /** 事件驱动的活动态；null = 尚未收到任何有效事件。 */
  activity: Ref<SessionActivityState | null>
  /** 当前会话的轮摘要（round_index → data），按 index 升序的 Map。 */
  roundsByIndex: ComputedRef<Map<number, RoundCompletedData>>
  startLive: () => void
  stopLive: () => void
}

export function useSessionEvents(args: {
  sessionId: () => string
  instanceId: () => string
}): UseSessionEventsReturn {
  const activity = ref<SessionActivityState | null>(null)
  const roundsByKey = ref<Map<string, RoundCompletedData>>(new Map())
  const eventsAvailable = ref(false)

  let handles: Array<{ unsubscribe(): void }> = []
  let starting = false

  function onSessionActivity(env: unknown): void {
    const next = applySessionActivityEvent(activity.value, env, {
      sessionId: args.sessionId(),
      instanceId: args.instanceId(),
    })
    if (next !== activity.value) {
      activity.value = next
      eventsAvailable.value = true
    }
  }

  function onRoundCompleted(env: unknown): void {
    const next = applyRoundCompletedEvent(roundsByKey.value, env, {
      sessionId: args.sessionId(),
      instanceId: args.instanceId(),
    })
    if (next !== roundsByKey.value) {
      roundsByKey.value = next
      eventsAvailable.value = true
    }
  }

  /** 挂载时拉一次快照做追赶对齐；失败静默（保持降级，不白屏）。 */
  async function loadSnapshot(): Promise<void> {
    const sid = args.sessionId()
    if (!sid) return
    try {
      const { http } = await import('../../api/http.ts')
      const snap = await http<EventsSnapshot>('/api/mobile/events/snapshot')
      const row = snap.sessions.find((s) => s.session_id === sid)
      if (row) {
        const merged = applySnapshotRow(activity.value, roundsByKey.value, row)
        activity.value = merged.activity
        roundsByKey.value = merged.rounds
      }
      eventsAvailable.value = true
    } catch {
      // 快照端点未上线/断线：保留既有事件态；从未可用则维持降级。
    }
  }

  function startLive(): void {
    if (starting) return
    starting = true
    void (async () => {
      const { initIdempotentWsBus, subscribe } = await import('../../services/idempotentWsBus.ts')
      initIdempotentWsBus()
      if (handles.length === 0) {
        handles = [
          subscribe('session.activity', onSessionActivity),
          subscribe('round.completed', onRoundCompleted),
        ]
      }
      await loadSnapshot()
    })()
  }

  function stopLive(): void {
    for (const h of handles) h.unsubscribe()
    handles = []
    starting = false
  }

  const roundsByIndex = computed(() => {
    const sid = args.sessionId()
    const out = new Map<number, RoundCompletedData>()
    for (const data of roundsByKey.value.values()) {
      if (data.session_id !== sid) continue
      out.set(data.round_index, data)
    }
    return new Map([...out.entries()].sort((a, b) => a[0] - b[0]))
  })

  return { eventsAvailable, activity, roundsByIndex, startLive, stopLive }
}
