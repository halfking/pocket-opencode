/**
 * ACC Tasks API — 把任务委托给 ACC（Agent Control Center）。
 *
 * 后端通道（pocketd）：
 *   POST /api/tasks/delegate   → 把任务委派给 ACC
 *     · 2026-10-25 起，**不需要**再自己准备 run_id：后端会
 *       「建 ACC 任务 → 派发到智能体 → 把绑定写回本地任务行」。
 *       此前端点强制要求 run_id，而前端从不发 ⇒ 移动端这个按钮每次都 400。
 *   GET  /api/acc/agents        → ACC 上注册/在线的智能体
 *   GET  /api/acc/tasks         → ACC 的 canonical 任务
 *   GET  /api/acc/sessions      → ACC 上报的会话
 *   GET  /api/acc/projects      → ACC 的项目
 *
 * 鉴权复用 http.ts（自动注入 Bearer token + 统一 JSON 错误处理）。
 */
import { http } from './http'

/**
 * 委托给 ACC 的客户端超时预算。
 *
 * 服务端这条路由的整体预算是 90s（handleDelegateTask 里的 WithTimeout）：
 * 它要跟 ACC 往返两次（建任务 + 派发），而 ACC 的 runtime-control 面
 * 实测会抖到 22~67s。
 *
 * 不变式（见 api/__tests__/api-timeout-budget-table.test.mjs）：
 * **客户端预算必须大于服务端预算**，否则服务端还在跑、客户端已经报超时。
 * 120s > 90s ✓（相等即错）。
 */
export const ACC_DELEGATE_TIMEOUT_MS = 120_000

export interface AccTask {
  id: string
  title: string
  description?: string
  kind?: string
  status?: string
  createdAt?: string
  updatedAt?: string
  [key: string]: any
}

/** ACC 智能体（/api/acc/agents 的一条）。 */
export interface AccAgent {
  id: string
  name?: string
  type?: string
  status?: string
  [key: string]: any
}

/**
 * 后端委托响应的信封。
 *
 * ★ task_id 是**本地任务 id**（后续「停止」要用的就是它），
 *   acc_task_id 才是 ACC 侧的任务 id。这两个 id 长得不一样，混用会让
 *   「停止」打到一个不存在的本地任务上（404）。
 */
export interface AccDelegateResult {
  source: string
  task_id: string
  acc_task_id?: string
  run_id?: string
  dispatch_id?: string
  status?: string
  agent_id?: string
  runtime_id?: string
  correlation_id?: string
  raw?: any
  [key: string]: any
}

/**
 * 创建一个 ACC 任务（委托给 ACC）。
 *
 * @param input.title       任务标题（必填）
 * @param input.description 任务描述（可选）
 * @param input.kind        任务类型（可选，例：'feature' / 'bug' / 'docs'）
 * @param input.agentId     指定派给哪个智能体（可选；不指定由后端选一个可派的）
 * @returns                委托信封（本地 task_id / acc_task_id / run_id / dispatch_id）
 */
export function createAccTask(input: {
  title: string
  description?: string
  kind?: string
  agentId?: string
}): Promise<AccDelegateResult> {
  const body: Record<string, unknown> = {
    title: input.title,
    description: input.description,
    kind: input.kind,
  }
  if (input.agentId) body.agent_id = input.agentId
  return http<AccDelegateResult>('/api/tasks/delegate', {
    method: 'POST',
    body: JSON.stringify(body),
    timeoutMs: ACC_DELEGATE_TIMEOUT_MS,
  })
}

/** 读 ACC 上的智能体清单。 */
export async function listAccAgents(): Promise<AccAgent[]> {
  const res = await http<{ agents?: AccAgent[]; data?: AccAgent[] }>('/api/acc/agents')
  return res.agents || res.data || []
}

/** 读 ACC 的 canonical 任务清单。 */
export async function listAccTasks(): Promise<AccTask[]> {
  const res = await http<{ tasks?: AccTask[]; data?: AccTask[] }>('/api/acc/tasks')
  return res.tasks || res.data || []
}

/** 读 ACC 上报的会话清单。 */
export async function listAccSessions(): Promise<any[]> {
  const res = await http<{ sessions?: any[]; data?: any[] }>('/api/acc/sessions')
  return res.sessions || res.data || []
}

/**
 * 任务历史里的一条 run（agent-companion 的 GET /api/v1/runs）。
 *
 * ★ 2026-10-27 新增。此前**没有任何出口**：daemon 侧对 ACC 派单不留痕
 *   （agent-companion 已修），pocketd 侧也没有路由用 CompanionClient.ListRuns
 *   ⇒ 手机上「任务历史」始终是空的。
 */
export interface AccRun {
  run_id: string
  dispatch_id?: string
  task_id?: string
  agent_id?: string
  kind?: string
  state?: string
  session_id?: string
  updates?: number
  last_event?: string
  retained?: boolean
  started_at?: string
  ended_at?: string
  answer?: string
}

/** 一条 run 产出的文件（= 「生成的文件」）。 */
export interface AccRunFile {
  name: string
  size?: number
  sha256?: string
  read_path?: string
}

/** 一条 run 调用的工具及其输出。 */
export interface AccRunArtifact {
  tool_call_id?: string
  tool_title?: string
  tool_kind?: string
  content?: string
  at?: string
}

/** 一条 run 的产出全貌：工具 + 文件。 */
export interface AccRunArtifacts {
  run_id: string
  artifacts: AccRunArtifact[]
  count: number
  dropped: number
  truncated: boolean
  files: AccRunFile[]
  file_count: number
}

/** 读任务历史（agent-companion 的 run 列表）。 */
export async function listAccRuns(limit = 50): Promise<AccRun[]> {
  const res = await http<{ runs?: AccRun[]; data?: AccRun[] }>(`/api/acc/runs?limit=${limit}`)
  return res.runs || res.data || []
}

/**
 * 读一条 run 的产出：它调用的工具，以及它留下的文件。
 *
 * 一次请求拿全两面 —— 用户问「这次跑出了什么」时想要一个答案，不是两个。
 */
export async function listAccRunArtifacts(runId: string): Promise<AccRunArtifacts> {
  const res = await http<Partial<AccRunArtifacts>>(
    `/api/acc/runs/${encodeURIComponent(runId)}/artifacts`,
  )
  return {
    run_id: res.run_id ?? runId,
    artifacts: res.artifacts ?? [],
    count: res.count ?? 0,
    dropped: res.dropped ?? 0,
    truncated: res.truncated ?? false,
    files: res.files ?? [],
    file_count: res.file_count ?? 0,
  }
}

/**
 * 读一个产出文件的内容。
 *
 * 后端把字节包成 JSON（{content, sha256, …}）—— 移动端的 http 客户端只解
 * JSON，裸的 text/plain 会让它解不出来。
 *
 * sha256 是 daemon 侧给的摘要：完成包声明的是 content_hash，App 拿到字节
 * 后能当场对账，而不是"看起来像那个文件"。
 */
export async function getAccArtifactContent(
  dispatchId: string,
  name: string,
): Promise<{ content: string; sha256: string; size?: number }> {
  const res = await http<{ content?: string; sha256?: string; size?: number }>(
    `/api/acc/artifacts/${encodeURIComponent(dispatchId)}/${encodeURIComponent(name)}`,
  )
  return { content: res?.content ?? '', sha256: res?.sha256 ?? '', size: res?.size }
}
