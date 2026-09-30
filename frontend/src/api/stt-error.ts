/**
 * STT 失败原因的展示规则（2026-10-01）。
 *
 * ## 为什么要有这个特例
 *
 * `api/error-message.ts` 的 `toUserMessage()` 按设计**优先用 i18n 通用文案、
 * 隐藏后端原文**（注释原话：「宁可降级为通用文案，绝不把原始错误能力甩给用户」）。
 * 那条策略对绝大多数场景是对的：后端原文往往是 `dial tcp ... i/o timeout`
 * 这种技术串，甩给用户毫无意义。
 *
 * 但语音转写是**反例**：后端对 STT 失败返回的是**经过整理的可行动中文原因**，
 * 且带稳定错误码前缀，例如：
 *
 *     stt_unavailable: 网关暂无可用的语音转写模型（gpt-audio=网关无上游 provider；
 *     mimo-v2.5-asr=网关无上游 provider）；另有 3 个候选同样不可用；
 *     外部语音转写服务未配置 API Key（设置 → 语音转写）
 *
 * 把它映射成 i18n 的 `errors.sttNotConfigured`（「语音转写服务尚未配置」），
 * 用户就完全不知道「网关列了模型但没开通 provider，去设置里换个外部服务」——
 * 而这恰恰是唯一能让他采取行动的信息。所以这里开一个**窄口径**特例。
 *
 * ## 窄在哪
 *
 * 只放行**带已知错误码**的文案（`stt_unavailable:`）。没有错误码的一律走
 * 通用兜底 —— 那些正是 `dial tcp` / panic / 超时这类技术串。这样既拿到可行动
 * 原因，又不会把技术噪音泄到界面上。
 */

/** 后端为 STT 不可用统一使用的错误码（同时是 i18n 映射表的 key）。 */
export const STT_UNAVAILABLE_CODE = 'stt_unavailable'

/** 已知可安全展示的后端错误码前缀。 */
const SHOWABLE_STT_CODES = [STT_UNAVAILABLE_CODE]

/**
 * 展示给用户的原因长度上限：超长说明后端没整理好，收敛避免撑爆界面。
 *
 * 2026-10-01 真机实测（Redmi，录音失败提示）：这条上限原来是**硬砍尾巴**，
 * 结果后端文案长这样——
 *   「网关暂无可用的语音转写模型（…逐候选诊断…）；另有 3 个候选同样不可用；
 *     外部语音转写服务未配置 API Key（设…」
 * 被砍掉的正好是「设置 → 语音转写」这个**唯一的行动指引**：用户看完整条仍然
 * 不知道该去哪儿，比只显示一句通用文案好不了多少。
 *
 * 所以改成**中间省略**：头部是「为什么失败」，尾部是「该去做什么」，两头都要留。
 */
const MAX_REASON_LEN = 160

/** 从各种异常形态里取出后端原文。 */
function rawText(err: unknown): string {
  if (!err) return ''
  if (typeof err === 'string') return err
  const e = err as { body?: unknown; message?: unknown }
  const body = e.body
  if (body && typeof body === 'object') {
    // 后端常见形态：{ error } / { code, message } / { msg }
    const b = body as Record<string, unknown>
    for (const k of ['error', 'message', 'msg']) {
      if (typeof b[k] === 'string' && b[k]) return b[k] as string
    }
  }
  return typeof e.message === 'string' ? e.message : ''
}

/**
 * sttFailureText 决定录音转写失败时给用户看什么。
 *
 * @param err      捕获到的异常（ApiError / Error / 字符串皆可）
 * @param fallback 无可展示原因时的通用兜底文案
 * @returns 带错误码时返回去掉错误码前缀的整理原因；否则返回 fallback
 */
export function sttFailureText(err: unknown, fallback: string): string {
  const raw = rawText(err).trim()
  if (!raw) return fallback
  for (const code of SHOWABLE_STT_CODES) {
    const prefix = `${code}:`
    if (!raw.startsWith(prefix)) continue
    // 后端可能把两条通道的原因拼在一起（auto 通道两条都不通），保留全文。
    const reason = raw.slice(prefix.length).trim()
    if (!reason) return fallback
    return truncate(reason, MAX_REASON_LEN)
  }
  // 没有稳定错误码 = 可能是技术串（超时 / DNS / panic），不展示。
  return fallback
}

/**
 * 收敛超长原因：**中间省略**，保留头部（失败原因）与尾部（行动指引）。
 *
 * 不能砍尾巴 —— 后端把「去设置里换外部服务」放在最后一句，砍掉就等于没给指引。
 * 也不能砍头部 —— 那才是用户判断「是网关的问题还是我的问题」的唯一依据。
 */
function truncate(s: string, max: number): string {
  const chars = Array.from(s)
  if (chars.length <= max) return s
  // 省略号占 1 格，两侧各留一半；头部多留 1 格，因为「为什么失败」比
  // 「失败清单的最后一个候选名」重要。
  const head = Math.ceil((max - 1) / 2) + 1
  const tail = max - 1 - head
  if (tail <= 0) return chars.slice(0, max).join('') + '…'
  return chars.slice(0, head).join('') + '…' + chars.slice(chars.length - tail).join('')
}
