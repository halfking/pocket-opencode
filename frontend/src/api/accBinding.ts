/**
 * accBinding — Pocket↔ACC canonical ID 绑定在前端 payload 中的透传。
 *
 * Task（api/client.ts）上的 accTaskId/accRunId/accDispatchId/accSourceRef/
 * accCorrelationId 是服务端权威绑定（task store 的 acc_* 列）的只读镜像。
 * 审批回复 / 取消等调用在已知绑定时把它带进 payload，便于服务端审计对账。
 *
 * 注意：后端移动端 handler 用 DisallowUnknownFields 解码 body，所以透传
 * 字段必须是显式契约化的 snake_case（acc_task_id …），且只透传非空值，
 * 避免给未绑定会话的请求塞空键。
 *
 * 本模块保持零依赖（不 import Vue/Pinia/fetch），以便 node:test 直接验证。
 */

/** Task 上 ACC 绑定字段的子集视图（与 Task 接口同形）。 */
export interface AccBindingRef {
  accTaskId?: string
  accRunId?: string
  accDispatchId?: string
  accSourceRef?: string
  accCorrelationId?: string
  accHolderId?: string
}

/**
 * 把绑定字段转换为 payload 的 snake_case 透传键；空值一律省略。
 * 传入 null/undefined 或全空绑定时返回空对象（不污染 payload）。
 */
export function accBindingPassthrough(
  binding?: AccBindingRef | null,
): Record<string, string> {
  if (!binding) return {}
  const out: Record<string, string> = {}
  if (binding.accTaskId) out.acc_task_id = binding.accTaskId
  if (binding.accRunId) out.acc_run_id = binding.accRunId
  if (binding.accDispatchId) out.acc_dispatch_id = binding.accDispatchId
  if (binding.accSourceRef) out.acc_source_ref = binding.accSourceRef
  if (binding.accCorrelationId) out.acc_correlation_id = binding.accCorrelationId
  if (binding.accHolderId) out.acc_holder_id = binding.accHolderId
  return out
}

/** 从 Task 形状的对象提取绑定视图（不认识的对象返回空视图）。 */
export function accBindingOf(
  task?: Partial<AccBindingRef> | null,
): AccBindingRef {
  if (!task) return {}
  const out: AccBindingRef = {}
  if (task.accTaskId) out.accTaskId = task.accTaskId
  if (task.accRunId) out.accRunId = task.accRunId
  if (task.accDispatchId) out.accDispatchId = task.accDispatchId
  if (task.accSourceRef) out.accSourceRef = task.accSourceRef
  if (task.accCorrelationId) out.accCorrelationId = task.accCorrelationId
  if (task.accHolderId) out.accHolderId = task.accHolderId
  return out
}
