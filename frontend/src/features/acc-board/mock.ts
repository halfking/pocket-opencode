/**
 * ACC 看板 Mock 引擎（融合方案 M1 任务9 + M2 任务13 · 评审演示路径）。
 *
 * 为什么单独一个模块而不是散在组件里：
 *   - 评审/截图时 ACC 不一定可用 ⇒ `?mock=1`（或设置面板开关）整面 UI 用
 *     内置数据渲染，全部操作（看列表 / 看详情 / 审批 / 重派 / 移交 / 日志流）
 *     都可交互；
 *   - mock 的审批会**真实推进状态机**（approve → 节点完成 → 后继节点运行；
 *     reject → run 失败；rollback → 目标节点起全部回 pending），评审能看到
 *     颜色编码与审批闭环，而不只是静态图；
 *   - M2 任务13 再补：主机池（host-A/B/C）、reassign/delegate（成功路径 +
 *     409 冲突路径）、编排事件日志（种子序列 + 实时追加 feed，供 LogStream
 *     无 ACC 演示）。
 *
 * 与 accApi.ts 的函数一一对应；组件层用 `useAccBoardSource()` 在真实现与
 * mock 之间选择，组件本身不感知 mock。
 *
 * 状态只存内存：刷新页面即重置为种子数据（演示可重复）。
 */

import {
  AccApiError,
  getAccApprover,
  normalizeRunStatus,
  type AccGateDecisionInput,
  type AccGateDecisionValue,
  type AccNodeStatus,
  type AccOrchestrationEvent,
  type AccRuntimeHost,
  type AccSopNode,
  type AccSopRun,
  type AccSopTemplate,
} from '../../api/accApi'

/** localStorage 键：设置面板里的 mock 开关（移动端评审改 URL 不方便）。 */
export const ACC_MOCK_FLAG_KEY = 'acc_board_mock'
/**
 * sessionStorage 键：会话级 mock 标记。
 *
 * 为什么需要它：`?mock=1` 挂在 board 路由的 query 上（hash 内），应用内
 * router.push 跳到详情页后 query 会丢 —— 不做会话级粘性，看板是 mock、
 * 详情页却去连真 ACC，演示路径就断了。评审期间整段会话保持 mock。
 */
export const ACC_MOCK_SESSION_KEY = 'acc_board_mock_session'

export function isMockFlagSet(): boolean {
  try {
    if (localStorage.getItem(ACC_MOCK_FLAG_KEY) === '1') return true
    if (sessionStorage.getItem(ACC_MOCK_SESSION_KEY) === '1') return true
    return false
  } catch {
    return false
  }
}

export function setMockFlag(on: boolean): void {
  try {
    if (on) {
      localStorage.setItem(ACC_MOCK_FLAG_KEY, '1')
      sessionStorage.setItem(ACC_MOCK_SESSION_KEY, '1')
    } else {
      localStorage.removeItem(ACC_MOCK_FLAG_KEY)
      sessionStorage.removeItem(ACC_MOCK_SESSION_KEY)
    }
  } catch {
    /* 忽略 */
  }
}

/** URL 是否要求 mock：支持 `?mock=1`（hash 前）与 `#/acc/board?mock=1`（hash 内）。 */
export function isMockQuery(): boolean {
  if (typeof window === 'undefined') return false
  if (/[?&]mock=1/.test(window.location.search)) return true
  if (/[?&]mock=1/.test(window.location.hash)) return true
  return false
}

/**
 * 若当前 URL 带 mock=1，把 mock 记到 sessionStorage（会话级粘性）。
 * 两个看板页面 onMounted 时各调一次 —— 深链直达详情页也生效。
 */
export function adoptMockQuery(): void {
  if (!isMockQuery()) return
  try {
    sessionStorage.setItem(ACC_MOCK_SESSION_KEY, '1')
  } catch {
    /* 忽略 */
  }
}

export function isMockActive(): boolean {
  return isMockQuery() || isMockFlagSet()
}

// ---------------------------------------------------------------------------
// 内存状态 + 种子数据
// ---------------------------------------------------------------------------

interface MockNodeSpec {
  nodeId: string
  name: string
  type: string
  status: AccNodeStatus
  dependencies: string[]
  allowFailure?: boolean
  /** 当前分配主机（M2 任务13：详情页展示 + 重派/移交改写）。 */
  host?: string
  /** 移交中节点携带的 envelope id。 */
  handoffEnvelopeId?: string
}

interface MockRun {
  runId: string
  status: string
  projectId: string
  createdAt: string
  nodes: MockNodeSpec[]
  /** 编排事件日志（seq 单调递增；LogStream mock 数据源）。 */
  events: AccOrchestrationEvent[]
  /** 实时 feed 定时器（LogStream 挂载期间追加演示事件）。 */
  feedTimer?: ReturnType<typeof setInterval>
  feedCount: number
  /** 移交自动接收定时器（演示 🔄 → ⏳ 闭环）。 */
  acceptTimer?: ReturnType<typeof setTimeout>
}

const state = {
  runs: [] as MockRun[],
  seq: 0,
  envSeq: 0,
}

function seed(): void {
  if (state.runs.length > 0) return
  const now = Date.now()
  state.runs = [
    {
      // 主演示 run：跑到 impl 节点撞上 HumanGate —— 颜色编码 + 审批闭环全在这条上。
      runId: 'run-mock-7c1a',
      status: 'waiting_approval',
      projectId: 'demo-api',
      createdAt: new Date(now - 26 * 60_000).toISOString(),
      nodes: [
        { nodeId: 'spec', name: '规格澄清', type: 'agent', status: 'completed', dependencies: [], host: 'host-A' },
        { nodeId: 'design', name: '方案设计', type: 'agent', status: 'completed', dependencies: ['spec'], host: 'host-A' },
        { nodeId: 'impl', name: '实现', type: 'agent', status: 'waiting_approval', dependencies: ['design'], host: 'host-B' },
        { nodeId: 'test', name: '测试', type: 'agent', status: 'pending', dependencies: ['impl'], allowFailure: true },
        { nodeId: 'review', name: '评审', type: 'agent', status: 'pending', dependencies: ['test'] },
      ],
      feedCount: 0,
      events: seedEvents('run-mock-7c1a', now - 26 * 60_000, [
        ev(1, 'run.created', 'run', 'pending', 'running', 'Run 已创建'),
        ev(2, 'plan.created', 'plan', '', 'planned', '执行计划已生成（2 个 agent 节点）'),
        ev(3, 'plan.published', 'plan', 'planned', 'published', '计划已发布'),
        ev(4, 'dispatch.created', 'dispatch', 'ready', 'queued', '节点 spec 已派发 → host-A', { node: 'spec', host: 'host-A' }),
        ev(5, 'task.succeeded', 'task', 'running', 'succeeded', '节点 spec 完成'),
        ev(6, 'dispatch.created', 'dispatch', 'ready', 'queued', '节点 design 已派发 → host-A', { node: 'design', host: 'host-A' }),
        ev(7, 'task.succeeded', 'task', 'running', 'succeeded', '节点 design 完成'),
        ev(8, 'dispatch.created', 'dispatch', 'ready', 'queued', '节点 impl 已派发 → host-B', { node: 'impl', host: 'host-B' }),
        ev(9, 'task.blocked', 'task', 'running', 'blocked', '节点 impl 触发 HumanGate，等待审批', { node: 'impl', gate: 'impl' }),
      ]),
    },
    {
      // 完成的 run：看板绿色态。
      runId: 'run-mock-b2e4',
      status: 'completed',
      projectId: 'demo-web',
      createdAt: new Date(now - 5 * 3600_000).toISOString(),
      nodes: [
        { nodeId: 'reproduce', name: '复现', type: 'agent', status: 'completed', dependencies: [], host: 'host-B' },
        { nodeId: 'fix', name: '修复', type: 'agent', status: 'completed', dependencies: ['reproduce'], host: 'host-B' },
        { nodeId: 'test', name: '测试', type: 'agent', status: 'completed', dependencies: ['fix'], host: 'host-C' },
        { nodeId: 'review', name: '评审', type: 'agent', status: 'completed', dependencies: ['test'], host: 'host-C' },
      ],
      feedCount: 0,
      events: seedEvents('run-mock-b2e4', now - 5 * 3600_000, [
        ev(1, 'run.created', 'run', 'pending', 'running', 'Run 已创建'),
        ev(2, 'dispatch.created', 'dispatch', 'ready', 'queued', '节点 reproduce 已派发 → host-B', { node: 'reproduce', host: 'host-B' }),
        ev(3, 'task.succeeded', 'task', 'running', 'succeeded', '节点 reproduce 完成'),
        ev(4, 'task.succeeded', 'task', 'running', 'succeeded', '节点 fix 完成'),
        ev(5, 'task.succeeded', 'task', 'running', 'succeeded', '节点 test 完成'),
        ev(6, 'task.succeeded', 'task', 'running', 'succeeded', '节点 review 完成'),
        ev(7, 'run.terminal', 'run', 'running', 'succeeded', 'Run 结束：succeeded'),
      ]),
    },
    {
      // 失败的 run：看板红色态（test 节点 failed，review 灰色待执行）。
      runId: 'run-mock-c9f0',
      status: 'failed',
      projectId: 'demo-api',
      createdAt: new Date(now - 27 * 3600_000).toISOString(),
      nodes: [
        { nodeId: 'spec', name: '规格澄清', type: 'agent', status: 'completed', dependencies: [], host: 'host-A' },
        { nodeId: 'impl', name: '实现', type: 'agent', status: 'completed', dependencies: ['spec'], host: 'host-A' },
        { nodeId: 'test', name: '测试', type: 'agent', status: 'failed', dependencies: ['impl'], host: 'host-C' },
        { nodeId: 'review', name: '评审', type: 'agent', status: 'pending', dependencies: ['test'] },
      ],
      feedCount: 0,
      events: seedEvents('run-mock-c9f0', now - 27 * 3600_000, [
        ev(1, 'run.created', 'run', 'pending', 'running', 'Run 已创建'),
        ev(2, 'task.succeeded', 'task', 'running', 'succeeded', '节点 spec 完成'),
        ev(3, 'task.succeeded', 'task', 'running', 'succeeded', '节点 impl 完成'),
        ev(4, 'dispatch.created', 'dispatch', 'ready', 'queued', '节点 test 已派发 → host-C', { node: 'test', host: 'host-C' }),
        ev(5, 'task.failed', 'task', 'running', 'failed', '节点 test 执行失败（exit code 1）', { node: 'test' }),
        ev(6, 'run.terminal', 'run', 'running', 'failed', 'Run 结束：failed'),
      ]),
    },
  ]
}

/** 种子事件构造辅助（occurredAt 由相对偏移生成）。 */
function seedEvents(runId: string, baseMs: number, list: AccOrchestrationEvent[]): AccOrchestrationEvent[] {
  return list.map((e, i) => ({
    ...e,
    runId,
    eventId: `evt-mock-${runId}-${e.seq}`,
    actorType: 'system',
    actorId: 'mock-orchestrator',
    source: 'mock',
    occurredAt: new Date(baseMs + i * 45_000).toISOString(),
  }))
}

function ev(
  seq: number,
  eventType: string,
  _subject: string,
  oldState: string,
  newState: string,
  text: string,
  payload?: Record<string, unknown>,
): AccOrchestrationEvent {
  return {
    seq,
    eventId: '',
    eventType,
    oldState: oldState || undefined,
    newState: newState || undefined,
    occurredAt: new Date().toISOString(),
    payload: { ...(payload ?? {}), text },
  }
}

function nodeToAcc(n: MockNodeSpec): AccSopNode {
  return {
    nodeId: n.nodeId,
    name: n.name,
    type: n.type,
    status: n.status,
    rawStatus: n.status,
    dependencies: n.dependencies,
    allowFailure: n.allowFailure === true,
    host: n.host,
    handoffEnvelopeId: n.handoffEnvelopeId,
  }
}

function runToAcc(r: MockRun): AccSopRun {
  return {
    runId: r.runId,
    status: normalizeRunStatus(r.status),
    rawStatus: r.status,
    projectId: r.projectId,
    createdAt: r.createdAt,
    nodes: r.nodes.map(nodeToAcc),
  }
}

const MOCK_TEMPLATES: AccSopTemplate[] = [
  { name: 'feature', nodes: ['spec', 'design', 'impl', 'test', 'review'] },
  { name: 'bugfix', nodes: ['reproduce', 'fix', 'test', 'review'] },
]

/** M2 任务13：mock 机器池（host 选择器数据源，与 ACC GET /api/v2/runtime/agents 对齐）。 */
const MOCK_HOSTS: AccRuntimeHost[] = [
  { hostId: 'host-A', name: 'host-A（开发机 win）', status: 'online', online: true, drivers: ['claude-code', 'opencode'], rawStatus: 'online' },
  { hostId: 'host-B', name: 'host-B（构建机 linux）', status: 'online', online: true, drivers: ['claude-code'], rawStatus: 'online' },
  { hostId: 'host-C', name: 'host-C（测试机 mac）', status: 'idle', online: true, drivers: ['opencode'], rawStatus: 'idle' },
]

/** 模拟网络延迟，让 loading 态可见、评审不至于看到瞬时假数据。 */
function delay<T>(value: T, ms = 150): Promise<T> {
  return new Promise((resolve) => setTimeout(() => resolve(value), ms))
}

/** 追加一条编排事件（seq 自增）。 */
function appendEvent(run: MockRun, eventType: string, oldState: string, newState: string, text: string, payload?: Record<string, unknown>): AccOrchestrationEvent {
  const seq = (run.events[run.events.length - 1]?.seq ?? 0) + 1
  const e: AccOrchestrationEvent = {
    seq,
    eventId: `evt-mock-${run.runId}-${seq}`,
    eventType,
    runId: run.runId,
    oldState: oldState || undefined,
    newState: newState || undefined,
    actorType: 'system',
    actorId: 'mock-orchestrator',
    source: 'mock',
    occurredAt: new Date().toISOString(),
    payload: { ...(payload ?? {}), text },
  }
  run.events.push(e)
  // 日志上限：mock 内存态，防长开 feed 涨爆（评审场景 200 条足够）。
  if (run.events.length > 200) run.events.splice(0, run.events.length - 200)
  return e
}

// ---------------------------------------------------------------------------
// mock 版 API（签名对齐 accApi.ts）
// ---------------------------------------------------------------------------

export function mockGetTemplates(): Promise<AccSopTemplate[]> {
  seed()
  return delay(MOCK_TEMPLATES.map((t) => ({ ...t, nodes: [...t.nodes] })))
}

export function mockCreateRun(input: { template?: string; projectId: string }): Promise<{ runId: string; status: string }> {
  seed()
  const tpl = MOCK_TEMPLATES.find((t) => t.name === input.template) ?? MOCK_TEMPLATES[0]
  state.seq += 1
  const runId = `run-mock-new-${state.seq}`
  const nameMap: Record<string, string> = { spec: '规格澄清', design: '方案设计', impl: '实现', test: '测试', review: '评审', reproduce: '复现', fix: '修复' }
  const run: MockRun = {
    runId,
    status: 'running',
    projectId: input.projectId || 'demo-api',
    createdAt: new Date().toISOString(),
    feedCount: 0,
    nodes: tpl.nodes.map((nodeId, idx) => ({
      nodeId,
      name: nameMap[nodeId] ?? nodeId,
      type: 'agent',
      status: idx === 0 ? 'running' : 'pending',
      dependencies: idx === 0 ? [] : [tpl.nodes[idx - 1]],
      host: idx === 0 ? 'host-A' : undefined,
    })),
    events: [],
  }
  appendEvent(run, 'run.created', 'pending', 'running', 'Run 已创建')
  const first = run.nodes[0]
  if (first) appendEvent(run, 'dispatch.created', 'ready', 'queued', `节点 ${first.nodeId} 已派发 → host-A`, { node: first.nodeId, host: 'host-A' })
  state.runs.unshift(run)
  return delay({ runId, status: 'running' })
}

export function mockGetRun(runId: string): Promise<AccSopRun> {
  seed()
  const run = state.runs.find((r) => r.runId === runId)
  if (!run) return Promise.reject(new Error(`mock: run ${runId} 不存在`))
  return delay(runToAcc(run))
}

export function mockListRuns(): Promise<{ source: 'mock'; items: { run: AccSopRun }[] }> {
  seed()
  const items = state.runs.map((r) => ({ run: runToAcc(r) }))
  return delay({ source: 'mock' as const, items })
}

/** M2 任务13：mock 机器池列表。 */
export function mockListHosts(): Promise<AccRuntimeHost[]> {
  seed()
  return delay(MOCK_HOSTS.map((h) => ({ ...h, drivers: [...h.drivers] })))
}

/** 409 冲突：run 里已有 handoff_pending 节点（= 存在 pending envelope）。 */
function findHandoffNode(run: MockRun): MockNodeSpec | undefined {
  return run.nodes.find((n) => n.status === 'handoff_pending')
}

/**
 * 选移交目标节点：优先 running，其次「依赖已满足的 pending」，再退而求
 * waiting_approval / 其余 pending —— 保证评审演示随时有可移交对象。
 */
function pickHandoffTarget(run: MockRun): MockNodeSpec {
  const running = run.nodes.find((n) => n.status === 'running')
  if (running) return running
  const dispatchable = run.nodes.find(
    (n) => n.status === 'pending' && n.dependencies.every((d) => run.nodes.find((x) => x.nodeId === d)?.status === 'completed'),
  )
  if (dispatchable) return dispatchable
  const waiting = run.nodes.find((n) => n.status === 'waiting_approval')
  if (waiting) return waiting
  const pending = run.nodes.find((n) => n.status === 'pending')
  if (pending) return pending
  throw new AccApiError(400, '任务没有可移交的节点（全部完成/失败/取消）')
}

/** 执行移交改写（reassign = 固定 target 的 delegate，共用这里）。 */
function applyHandoff(run: MockRun, targetHost: string | null, reason: string, kind: 'reassign' | 'delegate'): { envelopeId: string; targetNode: MockNodeSpec } {
  const targetNode = pickHandoffTarget(run)
  const prevState = targetNode.status
  state.envSeq += 1
  const envelopeId = `env-mock-${state.envSeq.toString(16).padStart(4, '0')}`
  const resolvedHost = targetHost || 'auto-balance'
  targetNode.status = 'handoff_pending'
  targetNode.host = resolvedHost
  targetNode.handoffEnvelopeId = envelopeId
  appendEvent(run, 'task.handoff.started', prevState, 'handoff_pending', `节点 ${targetNode.nodeId} 开始${kind === 'reassign' ? '重派' : '移交'} → ${targetHost || 'Auto balance'}`, {
    node: targetNode.nodeId,
    envelope_id: envelopeId,
    target_host: targetHost,
    reason: reason || undefined,
  })
  // 演示闭环：12s 后模拟目标主机（ac）接受 envelope → 节点回到 running。
  if (run.acceptTimer) clearTimeout(run.acceptTimer)
  run.acceptTimer = setTimeout(() => {
    if (targetNode.status === 'handoff_pending' && targetNode.handoffEnvelopeId === envelopeId) {
      targetNode.status = 'running'
      appendEvent(run, 'task.handoff.accepted', 'handoff_pending', 'running', `envelope ${envelopeId} 已被 ${resolvedHost} 接受，节点 ${targetNode.nodeId} 继续执行`, {
        envelope_id: envelopeId,
        target_host: resolvedHost,
      })
    }
  }, 12_000)
  return { envelopeId, targetNode }
}

/** M2 任务13：mock 重派（POST /api/v2/tasks/:task_id/reassign）。成功 + 409 两路。 */
export function mockReassignTask(taskId: string, targetHost: string): Promise<{ envelopeId: string; taskId: string; status: string; targetHost: string | null }> {
  seed()
  const run = state.runs.find((r) => r.runId === taskId)
  if (!run) return Promise.reject(new Error(`mock: run ${taskId} 不存在`))
  const existing = findHandoffNode(run)
  if (existing) {
    return Promise.reject(
      new AccApiError(409, `已有进行中的移交（pending envelope ${existing.handoffEnvelopeId ?? ''}，节点 ${existing.nodeId}）——等待其被接受后再试`),
    )
  }
  if (!targetHost) return Promise.reject(new AccApiError(400, '重派必须选择目标主机'))
  const { envelopeId } = applyHandoff(run, targetHost, '', 'reassign')
  return delay({ envelopeId, taskId, status: 'pending', targetHost })
}

/** M2 任务13：mock 移交（POST /api/v2/tasks/:task_id/delegate）。成功 + 409 两路。 */
export function mockDelegateTask(
  taskId: string,
  input: { targetHost?: string; reason?: string },
): Promise<{ envelopeId: string; taskId: string; status: string; targetHost: string | null }> {
  seed()
  const run = state.runs.find((r) => r.runId === taskId)
  if (!run) return Promise.reject(new Error(`mock: run ${taskId} 不存在`))
  const existing = findHandoffNode(run)
  if (existing) {
    return Promise.reject(
      new AccApiError(409, `已有进行中的移交（pending envelope ${existing.handoffEnvelopeId ?? ''}，节点 ${existing.nodeId}）——等待其被接受后再试`),
    )
  }
  const { envelopeId } = applyHandoff(run, input.targetHost || null, input.reason ?? '', 'delegate')
  return delay({ envelopeId, taskId, status: 'pending', targetHost: input.targetHost || null })
}

/**
 * M2 任务13：mock 编排事件轮询（GET /api/v2/orchestration/runs/:run_id/events?after=）。
 * 返回 seq > after 的增量；lastSeq 供游标推进。
 */
export function mockGetRunEvents(runId: string, after = 0): Promise<{ events: AccOrchestrationEvent[]; lastSeq: number }> {
  seed()
  const run = state.runs.find((r) => r.runId === runId)
  if (!run) return Promise.reject(new Error(`mock: run ${runId} 不存在`))
  const events = run.events.filter((e) => e.seq > after)
  const lastSeq = run.events.length > 0 ? run.events[run.events.length - 1]!.seq : 0
  return delay({ events, lastSeq }, 120)
}

/**
 * 启动 mock 实时事件 feed（LogStream mock 模式挂载时调用）：
 * 每 2.5s 给 run 追加一条演示事件（最多 60 条），让日志流「活着」。
 */
export function mockStartEventFeed(runId: string): void {
  seed()
  const run = state.runs.find((r) => r.runId === runId)
  if (!run || run.feedTimer) return
  run.feedTimer = setInterval(() => {
    if (run.feedCount >= 60) {
      stopFeed(run)
      return
    }
    run.feedCount += 1
    const active = run.nodes.find((n) => n.status === 'running' || n.status === 'handoff_pending') ?? run.nodes[0]
    const node = active?.nodeId ?? '-'
    const host = active?.host ?? 'host-A'
    if (run.feedCount % 4 === 0) {
      appendEvent(run, 'agent.progress', 'running', 'running', `节点 ${node} 执行进度 ${Math.min(95, run.feedCount * 5)}%（host ${host}）`, { node, host, percent: Math.min(95, run.feedCount * 5) })
    } else if (run.feedCount % 4 === 1) {
      appendEvent(run, 'agent.heartbeat', '', '', `执行主机 ${host} 心跳正常`, { host })
    } else if (run.feedCount % 4 === 2) {
      appendEvent(run, 'agent.log_line', '', '', `[${node}] info: step ${run.feedCount} finished`, { node, level: 'info' })
    } else {
      appendEvent(run, 'agent.log_line', '', '', `[${node}] warn: retrying flaky check (${run.feedCount}/3)`, { node, level: 'warn' })
    }
  }, 2_500)
}

/** 停止 mock 实时事件 feed（LogStream 卸载时调用）。 */
export function mockStopEventFeed(runId: string): void {
  const run = state.runs.find((r) => r.runId === runId)
  if (run) stopFeed(run)
}

function stopFeed(run: MockRun): void {
  if (run.feedTimer) {
    clearInterval(run.feedTimer)
    run.feedTimer = undefined
  }
}

export function mockApproveGate(
  runId: string,
  nodeId: string,
  input: AccGateDecisionInput,
): Promise<Record<string, unknown>> {
  seed()
  const run = state.runs.find((r) => r.runId === runId)
  if (!run) return Promise.reject(new Error(`mock: run ${runId} 不存在`))
  const node = run.nodes.find((n) => n.nodeId === nodeId)
  if (!node) return Promise.reject(new Error(`mock: 节点 ${nodeId} 不存在`))

  const decision: AccGateDecisionValue = input.decision
  if (decision === 'approve') {
    node.status = 'completed'
    run.status = 'running'
    appendEvent(run, 'task.review.approved', 'blocked', 'succeeded', `节点 ${nodeId} 审批通过（${input.approver || getAccApprover() || 'mobile-user'}）`, { node: nodeId })
    // 后继节点里依赖已满足的第一个 pending → running；若没有后继则整条 run 完成。
    const next = run.nodes.find(
      (n) =>
        n.status === 'pending' &&
        n.dependencies.every((d) => run.nodes.find((x) => x.nodeId === d)?.status === 'completed'),
    )
    if (next) {
      next.status = 'running'
      next.host = next.host ?? 'host-A'
      appendEvent(run, 'dispatch.created', 'ready', 'queued', `节点 ${next.nodeId} 已派发 → ${next.host}`, { node: next.nodeId, host: next.host })
    } else {
      run.status = 'completed'
      appendEvent(run, 'run.terminal', 'running', 'succeeded', 'Run 结束：succeeded')
    }
  } else if (decision === 'reject') {
    node.status = 'failed'
    run.status = 'failed'
    appendEvent(run, 'task.failed', 'blocked', 'failed', `节点 ${nodeId} 审批驳回，任务失败`, { node: nodeId })
    appendEvent(run, 'run.terminal', 'running', 'failed', 'Run 结束：failed')
  } else {
    // rollback：目标节点（默认第一个节点）起全部回 pending。
    const target = input.rollbackTo || run.nodes[0]?.nodeId
    const idx = run.nodes.findIndex((n) => n.nodeId === target)
    if (idx < 0) return Promise.reject(new Error(`mock: 回退目标 ${target} 不存在`))
    for (let i = idx; i < run.nodes.length; i++) {
      if (run.nodes[i].status !== 'failed') run.nodes[i].status = 'pending'
    }
    run.nodes[idx].status = 'running'
    run.status = 'running'
    appendEvent(run, 'task.rollback', 'blocked', 'pending', `审批退回：从节点 ${target} 起重跑`, { node: target })
  }
  return delay({ ok: true, decision, node_id: nodeId, approver: input.approver || getAccApprover() || 'mobile-user' })
}

export function mockCancelRun(runId: string): Promise<void> {
  seed()
  const run = state.runs.find((r) => r.runId === runId)
  if (!run) return Promise.reject(new Error(`mock: run ${runId} 不存在`))
  stopFeed(run)
  if (run.acceptTimer) {
    clearTimeout(run.acceptTimer)
    run.acceptTimer = undefined
  }
  run.status = 'cancelled'
  for (const n of run.nodes) {
    if (n.status === 'running' || n.status === 'waiting_approval' || n.status === 'pending' || n.status === 'handoff_pending') {
      n.status = 'cancelled'
    }
  }
  appendEvent(run, 'run.terminal', 'running', 'cancelled', 'Run 已取消（移动端请求）')
  return delay(undefined)
}
