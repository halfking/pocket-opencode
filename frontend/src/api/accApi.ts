/**
 * ACC 直连 API 客户端（融合方案 M1 任务9：移动端任务看板 MVP）。
 *
 * 与 `accTasks.ts` / `accApprovals.ts` 的区别（为什么不复用 http.ts）：
 *   - 那两个模块走 **pocketd 代理**（/api/acc/...），鉴权是 pocketd 的 JWT；
 *   - 本模块**直连 ACC**（默认 http://127.0.0.1:4101），鉴权用 ACC 自己的
 *     Bearer JWT（ACC RequireAuth）。MVP 让用户在设置面板手工粘贴 token，
 *     存 localStorage —— 所以**不能**走 http.ts（它绑定 authStore 的续期/策略链）。
 *
 * 契约来源：docs/api/M0_API_REFERENCE.md（§3 SOP 流水线 / §4 Runtime 注册）+
 * 融合方案 M1 任务6 的 gate 审批契约（实现中，字段按任务书）：
 *   POST /api/v2/sop/runs                          → 202 {run_id, status, nodes}
 *   GET  /api/v2/sop/runs/:run_id                  → {run_id, status, nodes:[...]}
 *   GET  /api/v2/sop/templates                     → [{name, nodes}]
 *   POST /api/v2/sop/runs/:run_id/gates/:node_id/approve
 *         {decision: approve|reject|rollback, rollback_to, comment, approver}
 *   POST /api/v2/orchestration/runs/:run_id/cancel  ← **预留**：ACC 目前只有
 *         sessions/commands/executions 的 cancel（orchestrator_native.go 等），
 *         SOP run 级终止端点尚未实现；UI 预留按钮，调用失败时降级提示。
 *
 * 超时：全部走 `fetchWithTimeout`（§951 前端卡口：每个出站 fetch 必须有上界）。
 * SSE 与降级轮询由 acc-log-runtime.ts 进程持有；fetch 可携带 Authorization，
 * LogStream.vue 只订阅/退订，切页不取消。下方轮询函数保留给普通调用方。
 *
 * M2 任务13 新增契约（docs/api/M0_API_REFERENCE.md §1/§4/§6）：
 *   GET  /api/v2/runtime/agents          → 机器池列表（主机选择器数据源）
 *   POST /api/v2/tasks/:task_id/reassign → 202 {envelope_id,...}（手动重派；
 *                                          409 = 已有 pending envelope）
 *   POST /api/v2/tasks/:task_id/delegate → 200 {envelope_id, task_id, status,
 *                                          target_host}（移交；target_host
 *                                          缺省 = Auto balance）
 *   GET  /api/v2/orchestration/runs/:run_id/events?after=<seq>
 *                                        → {ok, events:[...]}（SSE 的轮询替身）
 *
 * 注：M2 移动端把 SOP run 当作任务主体 —— reassign/delegate 的 task_id 参数
 * 传 run_id（ACC 侧 SOP run 与 canonical task 1:1 对应）。
 */

import { fetchWithTimeout, TimeoutError } from './fetch-timeout.ts'

/** ACC 地址 / token / 其它本地配置的 localStorage 键。 */
export const ACC_BASE_KEY = 'acc_board_base_url'
export const ACC_TOKEN_KEY = 'acc_board_token'
export const ACC_APPROVER_KEY = 'acc_board_approver'
/** 已知 SOP run 的本地登记表（ACC 暂无 run 列表端点，MVP 用登记表兜底）。 */
export const ACC_RUN_REGISTRY_KEY = 'acc_board_run_registry'

export const ACC_DEFAULT_BASE = 'http://127.0.0.1:4101'

/** ACC 请求超时预算：ACC 为本地服务，15s 足够覆盖 run 创建这类聚合操作。 */
export const ACC_TIMEOUT_MS = 15_000

const configListeners = new Set<() => void>()

/** Same-tab settings writes must revoke streams even when no log view is mounted. */
export function onAccConfigChange(listener: () => void): () => void {
  configListeners.add(listener)
  return () => { configListeners.delete(listener) }
}

function notifyAccConfigChange(): void {
  for (const listener of configListeners) listener()
}

export function getAccBase(): string {
  try {
    return localStorage.getItem(ACC_BASE_KEY) || ACC_DEFAULT_BASE
  } catch {
    return ACC_DEFAULT_BASE
  }
}

export function setAccBase(base: string): void {
  const trimmed = base.trim().replace(/\/+$/, '')
  try {
    if (trimmed) localStorage.setItem(ACC_BASE_KEY, trimmed)
    else localStorage.removeItem(ACC_BASE_KEY)
  } catch {
    /* 私有模式等场景下写入失败可忽略（下次回退默认值） */
  }
  notifyAccConfigChange()
}

export function getAccToken(): string {
  try {
    return localStorage.getItem(ACC_TOKEN_KEY) || ''
  } catch {
    return ''
  }
}

export function setAccToken(token: string): void {
  try {
    if (token.trim()) localStorage.setItem(ACC_TOKEN_KEY, token.trim())
    else localStorage.removeItem(ACC_TOKEN_KEY)
  } catch {
    /* 同上 */
  }
  notifyAccConfigChange()
}

export function getAccApprover(): string {
  try {
    return localStorage.getItem(ACC_APPROVER_KEY) || ''
  } catch {
    return ''
  }
}

export function setAccApprover(name: string): void {
  try {
    if (name.trim()) localStorage.setItem(ACC_APPROVER_KEY, name.trim())
    else localStorage.removeItem(ACC_APPROVER_KEY)
  } catch {
    /* 同上 */
  }
}

/** ACC 调用错误：携带 HTTP 状态码与响应体，便于 UI 直接展示服务端 message。 */
export class AccApiError extends Error {
  status: number
  body?: unknown
  constructor(status: number, message: string, body?: unknown) {
    super(message)
    this.name = 'AccApiError'
    this.status = status
    this.body = body
  }
}

/** 判断错误是否为「ACC 根本连不上」（网络层失败 / 超时），UI 据此提示检查地址。 */
export function isAccUnreachable(e: unknown): boolean {
  return e instanceof TimeoutError || (e instanceof AccApiError && e.status === 0)
}

/** 底层请求：JSON 解析 + Bearer 注入 + 统一错误。 */
export interface AccRequestAuthority {
  readonly base: string
  readonly token: string
  assertCurrent(): void
}

async function accRequest<T>(path: string, init?: {
  method?: string; body?: unknown; cache?: RequestCache; signal?: AbortSignal; authority?: AccRequestAuthority
}): Promise<T> {
  const authority = init?.authority
  authority?.assertCurrent()
  const base = authority?.base ?? getAccBase()
  const token = authority?.token ?? getAccToken()
  const headers: Record<string, string> = { Accept: 'application/json' }
  if (init?.body !== undefined) headers['Content-Type'] = 'application/json'
  if (token) headers.Authorization = `Bearer ${token}`

  let res: Response
  try {
    res = await fetchWithTimeout(
      `${base}${path}`,
      {
        method: init?.method ?? 'GET',
        cache: init?.cache,
        signal: init?.signal,
        ...(authority ? { redirect: 'error', credentials: 'omit' } : {}),
        headers,
        body: init?.body !== undefined ? JSON.stringify(init.body) : undefined,
      },
      // §951：每个出站 fetch 必须有上界；ACC 是本地服务，15s 足够。
      { timeoutMs: ACC_TIMEOUT_MS },
    )
  } catch (e) {
    if (e instanceof TimeoutError || (e instanceof Error && e.name === 'AbortError')) throw e
    throw new AccApiError(0, `无法连接 ACC（${base}）：${e instanceof Error ? e.message : String(e)}`)
  }

  authority?.assertCurrent()
  const text = await res.text()
  authority?.assertCurrent()
  let parsed: unknown = undefined
  if (text) {
    try {
      parsed = JSON.parse(text)
    } catch {
      parsed = undefined
    }
  }

  if (!res.ok) {
    const bodyObj = (parsed && typeof parsed === 'object' ? parsed : null) as Record<string, unknown> | null
    const msg =
      (typeof bodyObj?.message === 'string' && bodyObj.message) ||
      (typeof bodyObj?.error === 'string' && bodyObj.error) ||
      `ACC 返回 ${res.status}`
    throw new AccApiError(res.status, msg, parsed)
  }
  return parsed as T
}

export function readAcc<T>(authority: AccRequestAuthority, path: string, signal?: AbortSignal): Promise<T> {
  return accRequest<T>(path, { authority, signal, cache: 'no-store' })
}

// ---------------------------------------------------------------------------
// 数据模型（字段对齐 M0 契约；对实现的额外字段保持宽容 —— 见各 normalize）
// ---------------------------------------------------------------------------

/**
 * 节点状态（归一化后的九态）。
 * 原始值可能是 completed/running/failed/pending/waiting_approval/cancelled
 * 的任意近义词，统一在 normalizeNodeStatus 里收敛。
 * M2 任务13 新增 handoff_pending：delegate/reassign 把任务置为移交中。
 */
export type AccNodeStatus =
  | 'completed'
  | 'running'
  | 'failed'
  | 'pending'
  | 'waiting_approval'
  | 'handoff_pending'
  | 'cancelled'
  | 'skipped'
  | 'unknown'

/** run 级状态（归一化）。 */
export type AccRunStatus =
  | 'completed'
  | 'running'
  | 'failed'
  | 'pending'
  | 'waiting_approval'
  | 'handoff_pending'
  | 'cancelled'
  | 'unknown'

/** SOP 节点。M0 文档示例用 `id`，M1 任务书写的是 `node_id` —— 两者都收。 */
export interface AccSopNode {
  nodeId: string
  name: string
  type: string
  status: AccNodeStatus
  /** 归一化前的原始状态串（UI 调试可见）。 */
  rawStatus: string
  dependencies: string[]
  allowFailure: boolean
  /** 节点当前分配的主机（M0 契约 GET sop/runs 节点字段）。 */
  host?: string
  /** 移交包 id（M0 契约：节点移交中带 handoff_envelope_id）。 */
  handoffEnvelopeId?: string
  /** M1 gate 审批面若在节点上直接带 gate 字段则原样保留（以实现为准）。 */
  gate?: Record<string, unknown>
}

export interface AccSopRun {
  runId: string
  status: AccRunStatus
  rawStatus: string
  projectId?: string
  createdAt?: string
  nodes: AccSopNode[]
}

export interface AccSopTemplate {
  name: string
  nodes: string[]
}

/** gate 审批决定（M1 任务6 契约字段）。 */
export type AccGateDecisionValue = 'approve' | 'reject' | 'rollback'

export interface AccGateDecisionInput {
  decision: AccGateDecisionValue
  /** decision === 'rollback' 时必填：回退到的目标节点 id。 */
  rollbackTo?: string
  comment?: string
  approver?: string
}

// ---------------------------------------------------------------------------
// 状态归一化
// ---------------------------------------------------------------------------

const NODE_COMPLETED = new Set(['completed', 'complete', 'done', 'succeeded', 'success', 'ok'])
const NODE_RUNNING = new Set(['running', 'in_progress', 'in-progress', 'active', 'executing', 'dispatched'])
const NODE_FAILED = new Set(['failed', 'error', 'timeout', 'rejected', 'aborted'])
const NODE_WAITING = new Set([
  'waiting_approval',
  'awaiting_approval',
  'waiting-approval',
  'waiting_gate',
  'wait_gate',
  'gate',
  'human_gate',
  'paused',
  'blocked',
  'suspended',
])
const NODE_CANCELLED = new Set(['cancelled', 'canceled'])
const NODE_SKIPPED = new Set(['skipped', 'skip'])
// M2 任务13：移交中（delegate/reassign 生成 pending envelope 后任务的状态）。
const NODE_HANDOFF = new Set([
  'handoff_pending',
  'handoff-pending',
  'handoffpending',
  'handoff',
  'delegating',
  'delegation_pending',
  'transferring',
  'transfer_pending',
])

export function normalizeNodeStatus(raw: string | undefined | null): AccNodeStatus {
  const s = (raw ?? '').toLowerCase().trim()
  if (!s) return 'unknown'
  if (NODE_COMPLETED.has(s)) return 'completed'
  if (NODE_RUNNING.has(s)) return 'running'
  if (NODE_FAILED.has(s)) return 'failed'
  if (NODE_WAITING.has(s)) return 'waiting_approval'
  if (NODE_HANDOFF.has(s)) return 'handoff_pending'
  if (NODE_CANCELLED.has(s)) return 'cancelled'
  if (NODE_SKIPPED.has(s)) return 'skipped'
  return 'pending'
}

const RUN_COMPLETED = new Set(['completed', 'complete', 'done', 'succeeded', 'success'])
const RUN_RUNNING = new Set(['running', 'in_progress', 'in-progress', 'active', 'executing'])
const RUN_FAILED = new Set(['failed', 'error', 'timeout'])
// run 级也可能整体停在 HumanGate（M1 的 gate 节点会让 run 挂起等审批）。
const RUN_WAITING = NODE_WAITING
const RUN_CANCELLED = new Set(['cancelled', 'canceled'])
const RUN_HANDOFF = NODE_HANDOFF

export function normalizeRunStatus(raw: string | undefined | null): AccRunStatus {
  const s = (raw ?? '').toLowerCase().trim()
  if (!s) return 'unknown'
  if (RUN_COMPLETED.has(s)) return 'completed'
  if (RUN_RUNNING.has(s)) return 'running'
  if (RUN_FAILED.has(s)) return 'failed'
  if (RUN_WAITING.has(s)) return 'waiting_approval'
  if (RUN_HANDOFF.has(s)) return 'handoff_pending'
  if (RUN_CANCELLED.has(s)) return 'cancelled'
  return 'pending'
}

function normalizeNode(raw: Record<string, unknown>): AccSopNode {
  const nodeId = String(raw.node_id ?? raw.id ?? raw.nodeId ?? '')
  const name = String(raw.name ?? raw.title ?? nodeId)
  const rawStatus = typeof raw.status === 'string' ? raw.status : ''
  const gateRaw = raw.gate ?? raw.gates
  return {
    nodeId,
    name,
    type: String(raw.type ?? raw.kind ?? ''),
    status: normalizeNodeStatus(rawStatus),
    rawStatus,
    dependencies: Array.isArray(raw.dependencies)
      ? raw.dependencies.map((d) => String(d))
      : [],
    allowFailure: raw.allow_failure === true || raw.allowFailure === true,
    host: typeof raw.host === 'string' && raw.host ? raw.host : undefined,
    handoffEnvelopeId:
      typeof raw.handoff_envelope_id === 'string' && raw.handoff_envelope_id
        ? raw.handoff_envelope_id
        : typeof raw.handoffEnvelopeId === 'string' && raw.handoffEnvelopeId
          ? raw.handoffEnvelopeId
          : undefined,
    gate: gateRaw && typeof gateRaw === 'object' ? (gateRaw as Record<string, unknown>) : undefined,
  }
}

function normalizeRun(raw: Record<string, unknown>): AccSopRun {
  const rawStatus = typeof raw.status === 'string' ? raw.status : ''
  const nodesRaw = Array.isArray(raw.nodes) ? raw.nodes : []
  return {
    runId: String(raw.run_id ?? raw.runId ?? raw.id ?? ''),
    status: normalizeRunStatus(rawStatus),
    rawStatus,
    projectId: typeof raw.project_id === 'string' ? raw.project_id : undefined,
    createdAt:
      typeof raw.created_at === 'string'
        ? raw.created_at
        : typeof raw.createdAt === 'string'
          ? raw.createdAt
          : undefined,
    nodes: nodesRaw
      .map((n) => (n && typeof n === 'object' ? normalizeNode(n as Record<string, unknown>) : null))
      .filter((n): n is AccSopNode => n !== null && n.nodeId !== ''),
  }
}

// ---------------------------------------------------------------------------
// 已知 run 登记表（ACC 暂无列表端点时的兜底数据源）
// ---------------------------------------------------------------------------

export interface AccRunRegistryEntry {
  runId: string
  label?: string
  projectId?: string
  addedAt: string
}

function readRegistry(): AccRunRegistryEntry[] {
  try {
    const raw = localStorage.getItem(ACC_RUN_REGISTRY_KEY)
    if (!raw) return []
    const parsed: unknown = JSON.parse(raw)
    if (!Array.isArray(parsed)) return []
    return parsed
      .filter((e): e is Record<string, unknown> => !!e && typeof e === 'object')
      .map((e) => ({
        runId: String(e.runId ?? ''),
        label: typeof e.label === 'string' ? e.label : undefined,
        projectId: typeof e.projectId === 'string' ? e.projectId : undefined,
        addedAt: typeof e.addedAt === 'string' ? e.addedAt : new Date().toISOString(),
      }))
      .filter((e) => e.runId !== '')
  } catch {
    return []
  }
}

function writeRegistry(entries: AccRunRegistryEntry[]): void {
  try {
    // 上限 50 条：登记表只是「我见过哪些 run」的本地索引，不该无限增长。
    localStorage.setItem(ACC_RUN_REGISTRY_KEY, JSON.stringify(entries.slice(-50)))
  } catch {
    /* 写入失败可忽略 */
  }
}

/** 把一次成功拿到的 run 记进登记表（去重，新在后）。 */
export function registerAccRun(runId: string, meta?: { label?: string; projectId?: string }): void {
  if (!runId) return
  const entries = readRegistry().filter((e) => e.runId !== runId)
  entries.push({
    runId,
    label: meta?.label,
    projectId: meta?.projectId,
    addedAt: new Date().toISOString(),
  })
  writeRegistry(entries)
}

export function removeAccRunFromRegistry(runId: string): void {
  writeRegistry(readRegistry().filter((e) => e.runId !== runId))
}

export function listAccRunRegistry(): AccRunRegistryEntry[] {
  return readRegistry()
}

// ---------------------------------------------------------------------------
// 对外 API（与任务书一一对应）
// ---------------------------------------------------------------------------

/** GET /api/v2/sop/templates —— 内置 SOP 模板列表。 */
export async function getAccTemplates(): Promise<AccSopTemplate[]> {
  const res = await accRequest<unknown>('/api/v2/sop/templates')
  const arr = Array.isArray(res) ? res : extractArrayField(res, ['templates', 'data', 'items'])
  return arr
    .filter((t): t is Record<string, unknown> => !!t && typeof t === 'object')
    .map((t) => ({
      name: String(t.name ?? t.template ?? ''),
      nodes: Array.isArray(t.nodes) ? t.nodes.map((n) => String(n)) : [],
    }))
    .filter((t) => t.name !== '')
}

/** POST /api/v2/sop/runs —— 启动一次 SOP 执行（202）。 */
export async function createAccSopRun(input: {
  template?: string
  sopYaml?: string
  projectId: string
  variables?: Record<string, unknown>
}): Promise<{ runId: string; status: string }> {
  const body: Record<string, unknown> = { project_id: input.projectId }
  if (input.sopYaml) body.sop_yaml = input.sopYaml
  else if (input.template) body.template = input.template
  if (input.variables && Object.keys(input.variables).length > 0) body.variables = input.variables

  const res = await accRequest<Record<string, unknown>>('/api/v2/sop/runs', { method: 'POST', body })
  const runId = String(res.run_id ?? res.runId ?? '')
  if (!runId) throw new AccApiError(0, 'ACC 响应缺少 run_id')
  registerAccRun(runId, { projectId: input.projectId })
  return { runId, status: String(res.status ?? 'running') }
}

/** GET /api/v2/sop/runs/:run_id —— run 详情（节点级聚合）。 */
export async function getAccSopRun(runId: string, assertCurrent?: () => void): Promise<AccSopRun> {
  assertCurrent?.()
  const res = await accRequest<Record<string, unknown>>(
    `/api/v2/sop/runs/${encodeURIComponent(runId)}`,
    // Private live detail must not share an old in-flight/cache response across ABA.
    { cache: 'no-store' },
  )
  assertCurrent?.() // Also fence the registry side effect below, before exposing a result.
  const run = normalizeRun(res)
  if (!run.runId) run.runId = runId
  // 拿到详情顺手登记（下次看板列表就能看到它）。
  registerAccRun(run.runId, { projectId: run.projectId })
  return run
}

/**
 * GET /api/v2/sop/runs —— run 列表。
 *
 * M0 契约只有单查；若 ACC 后续实现了列表端点则直接用，404/405 时退回
 * 「本地登记表逐个单查」的兜底（MVP 行为）。单查失败的 run 不会让整个列表
 * 失败（可能已过期/被清理），以 unreachable 标记呈现。
 */
export interface AccSopRunListItem {
  run: AccSopRun | null
  error?: string
}

export async function listAccSopRuns(limit = 20): Promise<{ source: 'api' | 'registry'; items: AccSopRunListItem[] }> {
  try {
    const res = await accRequest<unknown>('/api/v2/sop/runs')
    const arr = Array.isArray(res) ? res : extractArrayField(res, ['runs', 'data', 'items'])
    const items = arr
      .filter((r): r is Record<string, unknown> => !!r && typeof r === 'object')
      .map((r) => ({ run: normalizeRun(r) }))
      .filter((i) => i.run.runId !== '')
    if (items.length > 0) return { source: 'api', items: items.slice(0, limit) }
    // 列表端点返回空也可能只是没实现（返回空对象），落到登记表兜底。
  } catch {
    /* 404/405/其它都走兜底 */
  }
  return listFromRegistry(limit)
}

async function listFromRegistry(limit: number): Promise<{ source: 'registry'; items: AccSopRunListItem[] }> {
  const entries = readRegistry().slice(-limit).reverse() // 新的在前
  const settled = await Promise.allSettled(entries.map((e) => getAccSopRun(e.runId)))
  const items: AccSopRunListItem[] = settled.map((r) =>
    r.status === 'fulfilled'
      ? { run: r.value }
      : { run: null, error: r.reason instanceof Error ? r.reason.message : String(r.reason) },
  )
  return { source: 'registry', items }
}

/**
 * POST /api/v2/sop/runs/:run_id/gates/:node_id/approve —— HumanGate 审批。
 *
 * M1 任务6 实现中；字段按任务书契约：decision / rollback_to / comment / approver。
 */
export async function approveAccGate(
  runId: string,
  nodeId: string,
  input: AccGateDecisionInput,
): Promise<Record<string, unknown>> {
  if (input.decision === 'rollback' && !input.rollbackTo) {
    throw new AccApiError(0, '退回（rollback）必须选择回退目标节点')
  }
  return accRequest<Record<string, unknown>>(
    `/api/v2/sop/runs/${encodeURIComponent(runId)}/gates/${encodeURIComponent(nodeId)}/approve`,
    {
      method: 'POST',
      body: {
        decision: input.decision,
        rollback_to: input.rollbackTo,
        comment: input.comment ?? '',
        approver: (input.approver ?? getAccApprover()) || 'mobile-user',
      },
    },
  )
}

/**
 * POST /api/v2/orchestration/runs/:run_id/cancel —— 终止 run。
 *
 * TODO(M1 任务7+): ACC 目前没有 SOP run 级的终止端点（现有 cancel 面只覆盖
 * orchestration sessions / runtime commands / flow executions）。这个调用按
 * 契约族的自然位置预留：若 ACC 404/405，UI 层降级为「预留按钮已到位」提示，
 * 不视为前端缺陷。
 */
export async function cancelAccSopRun(runId: string): Promise<void> {
  await accRequest<unknown>(`/api/v2/orchestration/runs/${encodeURIComponent(runId)}/cancel`, {
    method: 'POST',
    body: {},
  })
}

// ---------------------------------------------------------------------------
// M2 任务13：机器池 / 手动重派 / 跨主机移交 / 编排事件轮询
// ---------------------------------------------------------------------------

/** GET /api/v2/runtime/agents 返回的一台主机（机器池行）。 */
export interface AccRuntimeHost {
  hostId: string
  /** 展示名（hostname/name，缺省用 hostId）。 */
  name: string
  status: string
  /** 粗判在线（status 含 online/running/idle/healthy 之一）。 */
  online: boolean
  drivers: string[]
  rawStatus: string
}

/**
 * 机器池列表（主机选择器数据源）—— 重派/移交对话框的候选目标主机。
 *
 * ★ 端点回退（实测 2026-11-17）：ACC **没有** `/api/v2/runtime/agents` 这个端点。
 *   后端把 AgentDiscovery 挂在 `/api/v2/discovery` 组，TaskAssigner 的 `/agents`
 *   是另一个前缀、另一种语义（agent 池，不是主机池）；真实的主机池读面是
 *   `GET /api/v2/runtime/runtimes`（RuntimeControlHandler 的 fleet inventory）。
 *   M0 E2E 脚本同样在 agents 取不到后回退到 runtimes
 *   （tests/e2e/m0_handoff_test.sh 的 m0_count_hosts_in_list）。
 *   ⇒ 先试契约口径的 agents，取不到主机就回退 runtimes。
 *   ★ 只调 agents 的旧实现 ⇒ 目标主机下拉在真实 ACC 上永远是空的
 *   （这就是 acc-board 剩下的最后一环孤岛）。
 *
 * 行字段两种形态都要收：
 *   ① runtimes 的 fleet inventory 行：
 *      `{runtime_id, host_id, api_endpoint, last_heartbeat_at, agent_inventory:[…]}`
 *      —— **不带 status**，也**不带 drivers** ⇒ 在线按心跳新鲜度粗判，
 *      驱动清单从 agent_inventory 取；
 *   ② agents 形态的行：`{host_id|agent_id, status, drivers:[…]}`。
 *
 * 主机标识：`host_id` 优先（delegate 的 target_host 就是 host_id）；
 * 后端允许注册时省略 host_id，此时用 runtime_id 兜底，保证列表不为空。
 */
export async function listAccAgents(): Promise<AccRuntimeHost[]> {
  let firstError: unknown = null
  let reachable = false
  for (const path of RUNTIME_HOST_LIST_PATHS) {
    try {
      const hosts = await listRuntimeHosts(path)
      reachable = true
      if (hosts.length > 0) return hosts
    } catch (e) {
      // 记**第一个**错误：契约口径的端点先失败，它的错误才是根因
      // （agents 401 = 鉴权失败，比回退端点的 404 更可执行）。
      if (firstError === null) firstError = e
    }
  }
  // 至少一个端点连上了（只是没数据）⇒「ACC 上还没注册主机」，不是加载失败；
  // 两个端点都连不上 ⇒ 抛真实错误，让 UI 给出可执行的原因而不是空列表。
  if (!reachable && firstError) throw firstError
  return []
}

/** 主机池读面：契约口径优先，实现口径兜底。 */
const RUNTIME_HOST_LIST_PATHS = ['/api/v2/runtime/agents', '/api/v2/runtime/runtimes'] as const

/** 心跳新鲜度阈值（ms）：runtime 行不带 status，只能按 last_heartbeat_at 粗判在线。
 *  90s 与 ACC 侧 hosts 离线清理用的 cutoff 同量级（runtimecontrol/hosts.go 的 90s 用例）。 */
const RUNTIME_HEARTBEAT_FRESH_MS = 90_000

function runtimeHeartbeatFresh(value: unknown): boolean {
  if (typeof value !== 'string' || value.trim() === '') return false
  const ts = Date.parse(value)
  if (Number.isNaN(ts)) return false
  return Date.now() - ts <= RUNTIME_HEARTBEAT_FRESH_MS
}

/** 第一个非空字符串：后端会把缺省字段序列化成 ""，`??` 取不到下一个候选。 */
function firstNonEmpty(...values: unknown[]): string {
  for (const v of values) {
    if (typeof v === 'string' && v.trim() !== '') return v.trim()
  }
  return ''
}

/** 一个端点的响应 → 主机行（按 hostId 去重合并）。 */
async function listRuntimeHosts(path: string): Promise<AccRuntimeHost[]> {
  const res = await accRequest<unknown>(path)
  const arr = Array.isArray(res)
    ? res
    : extractArrayField(res, ['agents', 'hosts', 'runtimes', 'data', 'items'])
  const merged = new Map<string, AccRuntimeHost>()
  for (const raw of arr) {
    if (!raw || typeof raw !== 'object') continue
    const r = raw as Record<string, unknown>
    const hostId = firstNonEmpty(
      r.host_id,
      r.hostId,
      r.runtime_id,
      r.runtimeId,
      r.id,
      r.agent_id,
      r.agentId,
    )
    if (!hostId) continue
    const rawStatus = typeof r.status === 'string' ? r.status : ''
    const drivers = Array.isArray(r.drivers)
      ? r.drivers.map((d) => String(d)).filter((d) => d !== '')
      : Array.isArray(r.agent_inventory)
        ? r.agent_inventory
            .map((a) => {
              if (!a || typeof a !== 'object') return String(a)
              const row = a as Record<string, unknown>
              return firstNonEmpty(row.agent_id, row.agentId, row.agent_type)
            })
            .filter((d) => d !== '')
        : []
    const heartbeat = firstNonEmpty(r.last_heartbeat_at, r.lastHeartbeatAt)
    const fresh = runtimeHeartbeatFresh(heartbeat)
    const status = rawStatus
      ? normalizeHostStatus(rawStatus)
      : fresh
        ? 'online'
        : heartbeat
          ? 'stale'
          : 'registered'
    const online = rawStatus ? HOST_ONLINE.has(status) : fresh
    const existing = merged.get(hostId)
    if (!existing) {
      merged.set(hostId, {
        hostId,
        name: firstNonEmpty(r.hostname, r.name, r.label, r.host_id, r.hostId, hostId),
        status,
        online,
        drivers,
        rawStatus,
      })
      continue
    }
    // 同一主机上的多个 runtime 合成一行：在线取并集，驱动清单取并集。
    existing.online = existing.online || online
    if (online && !HOST_ONLINE.has(existing.status)) existing.status = 'online'
    existing.drivers = [...new Set([...existing.drivers, ...drivers])]
  }
  return [...merged.values()]
}

const HOST_ONLINE = new Set(['online', 'running', 'idle', 'healthy', 'ready', 'active'])

function normalizeHostStatus(raw: string): string {
  return raw.toLowerCase().trim()
}

/** reassign/delegate 成功响应（M0 §1/§6）。 */
export interface AccHandoffResult {
  envelopeId: string
  taskId: string
  /** pending / accepted / failed（创建时固定 pending）。 */
  status: string
  /** null = Auto balance。 */
  targetHost: string | null
  raw: Record<string, unknown>
}

function normalizeHandoff(res: Record<string, unknown>, fallbackTaskId: string, fallbackTarget: string | null): AccHandoffResult {
  const envelopeId = String(res.envelope_id ?? res.envelopeId ?? '')
  if (!envelopeId) throw new AccApiError(0, 'ACC 响应缺少 envelope_id')
  return {
    envelopeId,
    taskId: String(res.task_id ?? res.taskId ?? fallbackTaskId),
    status: String(res.status ?? 'pending'),
    targetHost:
      typeof res.target_host === 'string' && res.target_host
        ? res.target_host
        : typeof res.targetHost === 'string' && res.targetHost
          ? res.targetHost
          : fallbackTarget,
    raw: res,
  }
}

/**
 * POST /api/v2/tasks/:task_id/reassign —— 手动重派到指定主机（M0 §6）。
 *
 * 语义 = 固定 target_host 的 delegate；202 返回新 envelope_id；
 * 已有 pending envelope 或租约易主时 409（AccApiError.status === 409，UI 显式提示）。
 * targetHost 必填（重派就是「指定主机」——Auto balance 请用 delegate）。
 */
export async function reassignAccTask(taskId: string, targetHost: string): Promise<AccHandoffResult> {
  if (!taskId) throw new AccApiError(0, '缺少任务标识（run_id）')
  if (!targetHost) throw new AccApiError(0, '重派必须选择目标主机')
  const res = await accRequest<Record<string, unknown>>(
    `/api/v2/tasks/${encodeURIComponent(taskId)}/reassign`,
    { method: 'POST', body: { target_host: targetHost } },
  )
  return normalizeHandoff(res, taskId, targetHost)
}

/**
 * POST /api/v2/tasks/:task_id/delegate —— 移交到其他主机（M0 §1）。
 *
 * targetHost 缺省/空 = Auto balance（body 不带 target_host）；
 * 409 = 并发移交冲突（已有 pending envelope）。
 */
export async function delegateAccTask(
  taskId: string,
  input: { targetHost?: string; reason?: string } = {},
): Promise<AccHandoffResult> {
  if (!taskId) throw new AccApiError(0, '缺少任务标识（run_id）')
  const body: Record<string, unknown> = {}
  if (input.targetHost) body.target_host = input.targetHost
  if (input.reason) body.reason = input.reason
  const res = await accRequest<Record<string, unknown>>(
    `/api/v2/tasks/${encodeURIComponent(taskId)}/delegate`,
    { method: 'POST', body },
  )
  return normalizeHandoff(res, taskId, input.targetHost ?? null)
}

// ---------------------------------------------------------------------------
// 编排事件（LogStream 轮询替身 + SSE 帧的归一化模型）
// ---------------------------------------------------------------------------

/**
 * ACC 编排事件（acc-go orchestration.Event 的宽容投影）。
 * SSE 帧形状（acc-go orchestration_events_stream.go）：
 *   id: <sequence>\nevent: <event_type>\ndata: <Event JSON>
 * Event JSON 关键字段：event_id / event_type / sequence / run_id / task_id /
 * dispatch_id / old_state / new_state / occurred_at / source / payload。
 */
export interface AccOrchestrationEvent {
  /** run 内单调递增序号（SSE 的 id / 轮询 after 游标）。 */
  seq: number
  eventId: string
  eventType: string
  runId?: string
  taskId?: string
  dispatchId?: string
  oldState?: string
  newState?: string
  actorType?: string
  actorId?: string
  source?: string
  occurredAt?: string
  payload?: Record<string, unknown>
}

export function normalizeOrchestrationEvent(raw: Record<string, unknown>): AccOrchestrationEvent {
  const seq = Number(raw.sequence ?? raw.seq ?? 0)
  return {
    seq: Number.isFinite(seq) ? seq : 0,
    eventId: String(raw.event_id ?? raw.eventId ?? ''),
    eventType: String(raw.event_type ?? raw.eventType ?? raw.type ?? ''),
    runId: typeof raw.run_id === 'string' ? raw.run_id : undefined,
    taskId: typeof raw.task_id === 'string' ? raw.task_id : undefined,
    dispatchId: typeof raw.dispatch_id === 'string' ? raw.dispatch_id : undefined,
    oldState: typeof raw.old_state === 'string' ? raw.old_state : undefined,
    newState: typeof raw.new_state === 'string' ? raw.new_state : undefined,
    actorType: typeof raw.actor_type === 'string' ? raw.actor_type : undefined,
    actorId: typeof raw.actor_id === 'string' ? raw.actor_id : undefined,
    source: typeof raw.source === 'string' ? raw.source : undefined,
    occurredAt:
      typeof raw.occurred_at === 'string'
        ? raw.occurred_at
        : typeof raw.occurredAt === 'string'
          ? raw.occurredAt
          : undefined,
    payload: raw.payload && typeof raw.payload === 'object' ? (raw.payload as Record<string, unknown>) : undefined,
  }
}

/**
 * GET /api/v2/orchestration/runs/:run_id/events?after=<seq> —— 事件轮询。
 *
 * acc-go orchestration_native.go：{ok:true, events:[...], data:[...]}；
 * after 为上次收到的最大 sequence，服务端只回增量。该端点是 LogStream 的
 * 轮询降级路径（EventSource 带不了 Authorization 头时的替身）。
 */
export async function listAccRunEvents(runId: string, after = 0): Promise<AccOrchestrationEvent[]> {
  const query = after > 0 ? `?after=${encodeURIComponent(String(after))}` : ''
  const res = await accRequest<unknown>(
    `/api/v2/orchestration/runs/${encodeURIComponent(runId)}/events${query}`,
  )
  const arr = Array.isArray(res) ? res : extractArrayField(res, ['events', 'data', 'items'])
  return arr
    .filter((e): e is Record<string, unknown> => !!e && typeof e === 'object')
    .map(normalizeOrchestrationEvent)
    .filter((e) => e.eventType !== '' || e.eventId !== '')
}

// ---------------------------------------------------------------------------
// 小工具
// ---------------------------------------------------------------------------

/** 从 {field: [...]} 形状的响应里取数组字段（兼容 data/items 包装）。 */
function extractArrayField(res: unknown, fields: string[]): unknown[] {
  if (res && typeof res === 'object') {
    const obj = res as Record<string, unknown>
    for (const f of fields) {
      if (Array.isArray(obj[f])) return obj[f] as unknown[]
    }
  }
  return []
}
