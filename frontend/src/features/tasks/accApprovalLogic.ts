/**
 * ACC 待批项的纯逻辑（零依赖，可在 node:test 里加载）。
 *
 * ★ 为什么单独抽出来：待批项的 option id **必须**来自智能体给出的那一份。
 *   daemon 的 intervention.Decide 会逐个比对（"option %q was not offered by
 *   the agent"），自造的 id 一律拒；而 daemon 又按 option 的 **kind** 决定
 *   允许还是拒绝（reject_once/reject_always/deny ⇒ 拒绝），不是按 id。
 *   ⇒ 前端「允许一次」按钮必须挑一个 kind 不是拒绝类的选项，
 *     而且找不到就不渲染 —— 渲染一个必然被拒的按钮是假动作。
 *
 * 同一份 kind 判定在 daemon 侧（internal/intervention.isDenyKind），
 * 这里保持一致：新增的拒绝 kind 必须两边同时改。
 */

export interface AccApprovalOptionLike {
  id: string
  title?: string
  kind?: string
}

export interface AccApprovalLike {
  id: string
  title?: string
  tool_call_id?: string
  options?: AccApprovalOptionLike[]
}

/** 拒绝类的 option kind（与 daemon 的 isDenyKind 一致）。 */
const DENY_KINDS = ['reject_once', 'reject_always', 'deny']

/** 这个 option 是不是「拒绝」。 */
export function isAccDenyOption(option: AccApprovalOptionLike): boolean {
  return DENY_KINDS.includes(String(option.kind || '').toLowerCase())
}

/**
 * 挑出「允许一次」该用的 option id。
 *
 * 优先 kind 为 allow_once 的；没有就退到 kind 看起来是允许、且 id 形如
 * once/allow 的；再没有 ⇒ null（**不猜、不自造**）。
 */
export function pickAccAllowOptionId(item: AccApprovalLike): string | null {
  const options = item.options || []
  const byKind = options.find((o) => String(o.kind || '').toLowerCase() === 'allow_once')
  if (byKind) return byKind.id
  const byId = options.find(
    (o) => !isAccDenyOption(o) && /^(once|allow|allow_once|always)$/i.test(String(o.id || '')),
  )
  return byId ? byId.id : null
}

/**
 * 挑出「拒绝」该用的 option id。
 *
 * 优先 kind 是拒绝类的；没有就退到 id 形如 reject/deny 的。
 */
export function pickAccRejectOptionId(item: AccApprovalLike): string | null {
  const options = item.options || []
  const byKind = options.find((o) => isAccDenyOption(o))
  if (byKind) return byKind.id
  const byId = options.find((o) => /^(reject|reject_once|deny|deny_once)$/i.test(String(o.id || '')))
  return byId ? byId.id : null
}

/**
 * 待批项的一行摘要：智能体要做的动作。
 *
 * 没有 title 时退回 tool_call_id，再退回 id —— 三者都没有才显示占位符，
 * 但**不能**显示成空白（用户会以为没加载出来）。
 */
export function accApprovalSummary(item: AccApprovalLike): string {
  return (item.title || item.tool_call_id || item.id || '（未描述的审批请求）').trim()
}
