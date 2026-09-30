/**
 * 后端错误 → 用户可读文案。
 *
 * 缺陷背景（真机 Redmi 14R 5G 实测）：
 *   RSS 页把后端返回的原始错误直接渲染给用户，屏幕上显示的是
 *     rss_unavailable: store not configured
 *   —— 英文技术标识 + 内部术语，既没��过 i18n，也让用户完全无法据此行动
 *   （他不知道该去哪儿配置 RSS 源）。同类写法在 src 下有 33 个文件、82 处。
 *
 * 这里给出统一入口：调用方把捕获到的异常丢进来，得到一句能指导下一步的话。
 * 设计取舍：
 *   - **宁可降级为通用文案，也不把原始错误码抛给用户**。原始信息不丢，
 *     统一 console.warn 留档，便于排查。
 *   - 映射表用「错误码前缀 + 语义分类」两层：后端错误码形态是
 *     `rss_unavailable: store not configured`（码: 说明），取码做精确匹配，
 *     匹配不到再按语义（未配置 / 未授权 / 超时 / 网络）归类。
 *   - 本模块刻意不 import 任何运行时依赖（包括 http.ts），保持纯函数、可单测；
 *     文案由调用方的 i18n key 注入（见 resolveErrorI18nKey），各语言包自行提供翻译。
 *     超时判定用 name 字段而非 instanceof：打包或多副本场景下 instanceof 会失效，
 *     而 name 是我们自己在 http.ts 里显式赋的值。
 */

/** 已知后端错误码 → i18n key。key 统一挂在 errors.* 命名空间下。 */
export const ERROR_CODE_I18N_KEYS: Record<string, string> = {
  rss_unavailable: 'errors.rssNotConfigured',
  rss_source_missing: 'errors.rssSourceMissing',
  email_unavailable: 'errors.emailNotConfigured',
  email_account_missing: 'errors.emailAccountMissing',
  imap_unauthorized: 'errors.imapUnauthorized',
  imap_error: 'errors.imapFailed',
  stt_unavailable: 'errors.sttNotConfigured',
  llm_unavailable: 'errors.llmNotConfigured',
  model_not_found: 'errors.modelNotFound',
  gateway_unreachable: 'errors.gatewayUnreachable',
  workspace_not_found: 'errors.workspaceNotFound',
  not_configured: 'errors.notConfigured',
}

/** 语义兜底：按错误文本里的关键词归类，避免每个码都硬编码。 */
const SEMANTIC_RULES: Array<{ re: RegExp; key: string }> = [
  { re: /not\s*configured|no\s*store|unavailable/i, key: 'errors.notConfigured' },
  // 覆盖 IMAP/SMTP 的典型措辞：后端常回 "535 auth failed"、"authentication required"
  { re: /unauthorized|forbidden|invalid\s*credential|auth\s*failed|auth\s*error|authentication|401|403/i, key: 'errors.unauthorized' },
  { re: /not\s*found|404/i, key: 'errors.notFound' },
  { re: /timeout|timed?\s*out|deadline/i, key: 'errors.timeout' },
  { re: /network|failed\s*to\s*fetch|econnrefused|offline/i, key: 'errors.network' },
  { re: /quota|limit\s*exceeded|429/i, key: 'errors.rateLimited' },
  { re: /conflict|409|already\s*exists/i, key: 'errors.conflict' },
  { re: /server\s*error|500|502|503/i, key: 'errors.server' },
]

/** 从任意异常里挖出后端给的错误字符串。 */
function extractRaw(err: unknown): string {
  if (!err) return ''
  if (typeof err === 'string') return err
  const e = err as { body?: unknown; message?: unknown }
  const body = e.body
  if (body && typeof body === 'object') {
    const b = body as Record<string, unknown>
    // 后端常见形态：{ error } / { code, message } / { msg }
    for (const k of ['error', 'message', 'msg', 'code', 'detail']) {
      if (typeof b[k] === 'string' && b[k]) return b[k] as string
    }
  }
  return typeof e.message === 'string' ? e.message : ''
}

/**
 * 取出错误码。后端形态为 `code: human readable`（如
 * `rss_unavailable: store not configured`），前半段才是稳定标识。
 */
export function extractErrorCode(raw: string): string {
  const head = raw.split(':')[0]?.trim() ?? ''
  return /^[a-z0-9_]+$/.test(head) ? head : ''
}

/**
 * 解析成 i18n key。返回 null 表示「不认识」，调用方应使用自己的通用文案。
 * 超时（TimeoutError）单独识别，不走后端错误码路径。
 */
export function resolveErrorI18nKey(err: unknown): string | null {
  const name = (err as { name?: unknown })?.name
  if (name === 'TimeoutError') return 'errors.timeout'
  const raw = extractRaw(err)
  if (!raw) return null

  const code = extractErrorCode(raw)
  if (code && ERROR_CODE_I18N_KEYS[code]) return ERROR_CODE_I18N_KEYS[code]

  for (const rule of SEMANTIC_RULES) {
    if (rule.re.test(raw)) return rule.key
  }
  return null
}

/**
 * 最终给用户看的文案。
 *
 * @param err     捕获到的异常
 * @param translate i18n 的 t 函数（由调用方从 useI18n() 传入，便于单测注入）
 * @param fallback 调用方的领域通用文案，如「加载订阅源失败」
 */
export function toUserMessage(
  err: unknown,
  translate: (key: string) => string,
  fallback: string,
): string {
  const key = resolveErrorI18nKey(err)
  const raw = extractRaw(err)
  // 原始错误一律留档：用户看到的是可执行指引，排查靠的是控制台
  if (raw && (!key || key === 'errors.network')) {
    console.warn('[api] 请求失败（原始信息）:', raw)
  }
  if (!key) return fallback
  const translated = translate(key)
  // i18n 缺词时会回显 key 本身，这种情况下宁可退回通用文案
  if (!translated || translated === key) return fallback
  return translated
}
