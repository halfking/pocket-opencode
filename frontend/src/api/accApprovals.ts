/**
 * ACC Approvals API — ACC 派单带来的**待批项**。
 *
 * ★ 为什么需要这一层（2026-10-26 实测，不是推测）：
 *
 *   · agent-companion 的 ACP 权限门把工具审批转发给 ACC：
 *     POST /api/v2/runtime/commands/{dispatch_id}/permission。
 *   · 但 acc-go **从未实现**这条路由 —— 对权威 ACC 实测 GET/POST 都 404。
 *     （agent-companion 自己的 ACC_BRIDGE_SPEC.md 却写着这条，
 *     契约在，实现没有。）
 *   · 于是「委托给 ACC」的任务一碰到需要人工批准的工具，就必然
 *     permission_denied；而且原来连拒绝原因都不打日志，现场只剩一个
 *     没有解释的失败。
 *   · agent-companion 已改：端点「未接线」时，待批项停进 daemon 自己的
 *     干预队列（GET /api/v1/interventions），等人应答；没人应答仍然
 *     fail-closed。
 *
 *   本文件是那条队列在 App 里的读面与写面：
 *     GET  /api/acc/approvals
 *     POST /api/acc/approvals/{id}/decision
 *
 * 鉴权复用 http.ts（自动注入 Bearer token + 统一 JSON 错误处理）。
 */
import { http } from './http'

/** 智能体给出的一个可选项（id 原样回传给 daemon，不能自造）。 */
export interface AccApprovalOption {
  id: string
  title: string
  kind?: string
}

/** 一个待批项。 */
export interface AccApproval {
  id: string
  run_id?: string
  session_id?: string
  agent_kind?: string
  tool_call_id?: string
  /** 智能体要做的动作描述（例：mkdir -p /tmp/x）。 */
  title?: string
  kind?: string
  status?: string
  created_at?: string
  reason?: string
  decided_by?: string
  options: AccApprovalOption[]
}

/** 读待批项（默认只读 pending）。 */
export async function listAccApprovals(status = 'pending'): Promise<AccApproval[]> {
  const res = await http<{ approvals?: AccApproval[]; data?: AccApproval[] }>(
    `/api/acc/approvals?status=${encodeURIComponent(status)}`,
  )
  return res.approvals || res.data || []
}

/**
 * 应答一个待批项。
 *
 * @param id       待批项 id
 * @param optionId 智能体给出的选项 id（如 once / always / reject）
 * @param reason   备注（可选）
 */
export async function decideAccApproval(
  id: string,
  optionId: string,
  reason?: string,
): Promise<AccApproval> {
  const res = await http<{ approval?: AccApproval }>(
    `/api/acc/approvals/${encodeURIComponent(id)}/decision`,
    {
      method: 'POST',
      body: JSON.stringify({ option_id: optionId, reason }),
    },
  )
  return res.approval as AccApproval
}
