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
