/** WebView 不直连 IMAP；原生或 H5 只委托 pocketd。 */
export function formatFetchHint(syncHint: string, classified: number): string {
  if (classified > 0) return `${syncHint}，已归类 ${classified}`
  return syncHint
}

export function shouldRunBackgroundFetch(now: number, lastAt: number, minGapMs = 15 * 60_000): boolean {
  return lastAt <= 0 || now - lastAt >= minGapMs
}

export type EmailFetchStage = 'native-sync' | 'js-sync' | 'pull-list'

/** 真机走原生 HTTP；H5 走 JS fetch。两边都只打 pocketd，再拉列表。 */
export function emailFetchStages(nativeAvailable: boolean): EmailFetchStage[] {
  return nativeAvailable ? ['native-sync', 'pull-list'] : ['js-sync', 'pull-list']
}

/** Capacitor 上 resolveApiBase 可能被空覆盖成 ''；原生收信必须有绝对地址。 */
export function resolveFetchApiBase(resolved: string, nativeFallback: string): string {
  const base = (resolved || '').trim()
  if (base) return base.replace(/\/$/, '')
  return (nativeFallback || '').replace(/\/$/, '')
}

type TFn = (key: string, params?: Record<string, unknown>) => string

/**
 * sanitizeFetchHint —— 把后台收信/归类提示里的原始错误翻译成可展示文案。
 *
 * 后端原始错误负载绝不能直接上屏。实测真机：/api/emails/classify 在未配置
 * 分类器时返回 503，收件箱顶部会原样显示
 *   同步失败：HTTP 503 {"error":"classifier not configured"}
 * 既是英文内部术语又是一串 JSON。之前的实现只挡了 "Failed to fetch"，
 * 这条路径整个漏掉了。
 *
 * t 由调用方注入（生产用 i18n.global.t），保持本模块纯函数、可单测。
 */
export function sanitizeFetchHint(raw: string, t?: TFn): string {
  if (!raw) return ''
  const tr: TFn = t ?? ((k, p) => {
    const fallback = DEFAULT_HINTS[k]
    if (fallback == null) return k
    return p?.code != null ? fallback.replace('{code}', String(p.code)) : fallback
  })
  if (/failed to fetch/i.test(raw) || /networkerror/i.test(raw)) {
    return tr('email.fetchHintNetwork')
  }
  const m = /HTTP\s+(\d{3})\s*(\{[\s\S]*\})?/.exec(raw)
  if (m) {
    const code = m[1]
    let err = ''
    if (m[2]) {
      try { err = String(JSON.parse(m[2]).error || '') } catch { err = '' }
    }
    if (/classif/i.test(err)) return tr('email.fetchHintClassifierOff')
    if (code === '401' || code === '403') return tr('email.fetchHintAuth')
    if (code === '503') return tr('email.fetchHintUnavailable')
    return tr('email.fetchHintHttpError', { code })
  }
  return raw
}

/** t 缺席时的兜底文案（单测与 SSR 场景）。键名与语言包保持一致。 */
const DEFAULT_HINTS: Record<string, string> = {
  'email.fetchHintNetwork': '后台收信未完成，已显示已同步邮件',
  'email.fetchHintClassifierOff': '自动归类服务未启用，邮件已同步但未自动分类',
  'email.fetchHintAuth': '登录状态已失效，请重新登录',
  'email.fetchHintUnavailable': '后台服务暂不可用，邮件已同步',
  'email.fetchHintHttpError': '后台收信未完成（{code}），已显示已同步邮件',
}

export function shouldRetryFullListPull(localCount: number, pulled: number, since: number): boolean {
  return localCount <= 0 && pulled <= 0 && since > 0
}
