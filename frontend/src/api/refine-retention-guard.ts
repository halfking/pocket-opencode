/**
 * refine-retention-guard.ts — 「精校结果**不得删除原文有的内容**」这条契约的**强制**（纯函数）。
 *
 * ── 契约是真的存在的（§831）──
 *   服务端提示词 `buildRefinePrompt`（`server_meeting.go`）明写第 3 条：
 *
 *     「**不得删除原文有的内容**。」
 *
 *   而整条链路的职责被限定为**纠错而非改写**（同一函数的注释里记着第一版用
 *   「润色」二字时，模型把「CI」改成「101」、把正确句改写掉的真网关实测）。
 *
 * ── 但这条契约此前**零强制** ──
 *   全仓检索没有任何一处比较「精校后」与「精校前」的体量：
 *   · 服务端只防**截断**（`refine_truncation_test.go` 用真实抓包扫了 3867 个截断点，
 *     钉住「要么解析失败、要么正文逐字节完整」）—— 那防的是**响应不完整**；
 *   · 客户端只防**空值**（`refineFromLlmContent` 的 `hasRefined` 空值闸）。
 *   ⇒ **中间那一档 —— 「响应完整、但模型把精校做成了摘要」—— 无人看守。**
 *
 *   后果形态与本会话挖到的多处同族：**用户看到一份少了大段内容的正文，
 *   徽章却写着「已精校」**（`resolveRefinedView` 在 `status==='refined'` 时
 *   直接展示 `refinedTranscript`）。
 *
 * ⚠ 阈值沿用 §783 已有的口径（`full < segText*0.5 ⇒ 拒绝采纳），
 *   并额外加一道**绝对下限**：原始转写太短时不判短 —— 否则
 *   「10 字变 4 字」会把正常的精校判成失败。
 *
 * ⚠ 抽成纯模块而不是写在 `api/meetings.ts` 里：那个文件的顶层 import 会拖进
 *   `api/http` 等浏览器/HTTP 代码，在 node 里 import 直接失败（§52）。
 *   而这份判定**必须在两个调用点都生效**（`normalizeRefine` 与
 *   `refineFromLlmContent`），任何一处漏掉都会让守卫形同虚设。
 */

/** 精校后至少要保留的比例（沿用 §783 的 `0.5` 口径）。 */
export const REFINE_RETENTION_RATIO = 0.5

/** 原始转写短于这个长度时不做比例判定（避免小样本误判）。 */
export const REFINE_RETENTION_MIN_INPUT = 200

export interface RefineRetentionInput {
  /** 送进 LLM 的那份正文（原始转写 / 分段渲染）。 */
  original: string
  /** 模型回传的 `refined_transcript`。 */
  refined: string
}

export interface RefineRetentionVerdict {
  /** 精校结果是否可以用。false ⇒ 调用方必须退回原文并标记降级。 */
  ok: boolean
  /** 可观测的原因（便于排障区分「空 / 截断式变短 / 正常」）。 */
  reason: string
}

const len = (s: unknown): number => (typeof s === 'string' ? s.trim().length : 0)

/**
 * 判断精校结果是否**丢内容丢到不该接受**。
 *
 * ⚠ 只挡「明显不成比例」的那一档：模型的正常纠错会让正文**变长**（`[发言人]`
 *   前缀）或**基本持平**；只有「把精校做成了摘要」才会掉到一半以下。
 */
export function assessRefineRetention(input: RefineRetentionInput): RefineRetentionVerdict {
  const original = len(input.original)
  const refined = len(input.refined)
  if (refined === 0) return { ok: false, reason: 'refined-empty' }
  if (original === 0) return { ok: true, reason: 'original-empty' }
  if (original < REFINE_RETENTION_MIN_INPUT) return { ok: true, reason: `input-too-short(${original})` }
  const floor = Math.floor(original * REFINE_RETENTION_RATIO)
  if (refined < floor) {
    return { ok: false, reason: `refine-too-short(${refined}<${floor}，原文 ${original})` }
  }
  return { ok: true, reason: 'ok' }
}
