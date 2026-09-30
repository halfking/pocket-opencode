/** 邮件正文语言切换：原文即时回切，其它语言走 AI 并缓存。 */
export type EmailLang =
  | 'original'
  | 'zh-CN'
  | 'zh-TW'
  | 'en-US'
  | 'ja-JP'
  | 'ko-KR'

export interface EmailLangOption {
  code: EmailLang
  label: string
  short: string
}

export const EMAIL_LANGS: EmailLangOption[] = [
  { code: 'original', label: '原文', short: '原文' },
  { code: 'zh-CN', label: '简体中文', short: '中' },
  { code: 'zh-TW', label: '繁體中文', short: '繁' },
  { code: 'en-US', label: 'English', short: 'EN' },
  { code: 'ja-JP', label: '日本語', short: '日' },
  { code: 'ko-KR', label: '한국어', short: '한' },
]

/**
 * 需求：「增加翻译成指定语言的功能，默认为中文」。
 * 默认目标语言就是简体中文；正文已是中文时不翻（见 isMostlyChinese）。
 */
export const DEFAULT_EMAIL_LANG: EmailLang = 'zh-CN'

/**
 * 判断正文是否已以中文为主。
 *
 * 为什么要判：默认语言是中文，若正文本来就是中文，再送去「翻译成中文」既浪费
 * token，又可能让 LLM 顺手改写措辞（用户会看到自己写的中文被「翻译」了一遍）。
 *
 * 阈值取 0.15 而不是「含任意一个汉字」：技术邮件常中英混排，含几个汉字不代表
 * 主要语言是中文；反过来纯英文通知里带一个产品中文名也不该触发翻译。
 *
 * **必须先排除日文/韩文**：日语大量使用汉字（「請求書」「発行」），单看汉字占比
 * 会把日文正文判成中文，于是「翻译成中文」变成把日文改写成中文——恰好是用户
 * 最不想要的结果。假名（ひらがな/カタカナ）或谚文（한글）一旦出现就足以定性。
 */
export function isMostlyChinese(text: string, threshold = 0.15): boolean {
  if (!text) return false
  // 去掉 HTML 标签与常见实体，避免标签名/属性里的字符干扰统计。
  const plain = text
    .replace(/<[^>]*>/g, ' ')
    .replace(/&[a-z]+;|&#\d+;/gi, ' ')
  // 假名 / 谚文 / 谚文 → 该正文不是中文。
  if (/[\u3040-\u30ff\uac00-\ud7af]/.test(plain)) return false
  const han = (plain.match(/[\u4e00-\u9fff]/g) || []).length
  if (han === 0) return false
  // 用「非空白字符数」做分母，忽略排版空白。
  const letters = (plain.match(/[\p{L}\p{N}]/gu) || []).length
  if (letters === 0) return false
  return han / letters >= threshold
}

export function langShortLabel(code: EmailLang): string {
  return EMAIL_LANGS.find((l) => l.code === code)?.short ?? '原文'
}

export function langFullLabel(code: EmailLang): string {
  return EMAIL_LANGS.find((l) => l.code === code)?.label ?? '原文'
}

export function buildTranslatePrompt(body: string, target: EmailLang): string {
  const name = langFullLabel(target)
  return [
    `请把下面这封邮件翻译成${name}。`,
    '必须保留原有格式：换行、段落、列表、引用，以及 HTML 标签与属性（如有）。',
    '规则：',
    '1. <img> 等标签的 src 必须原样保留，不要改写、不要删除。',
    '2. 不要翻译人名、公司名、订单号、金额、日期与 URL。',
    '3. 不要添加说明、标题或代码围栏，只输出译文本身。',
    '',
    body,
  ].join('\n')
}

export function extractTranslatedBody(raw: string): string {
  let text = raw.replace(/\r\n/g, '\n').trim()
  const fenced = text.match(/^```[a-zA-Z0-9]*\n([\s\S]*?)\n```$/)
  if (fenced) text = fenced[1].trim()
  text = text.replace(/^如下是译文[：:]\s*\n+/i, '').trim()
  text = text.replace(/\n+（已保留格式）\s*$/i, '').trim()
  return text
}

export function resolveDisplayBody(
  original: string,
  cache: Record<string, string>,
  lang: EmailLang,
): string {
  if (lang === 'original') return original
  return cache[lang] || original
}

export async function translateEmailBody(
  body: string,
  target: EmailLang,
  chat: (prompt: string) => Promise<string>,
): Promise<string> {
  if (target === 'original' || !body.trim()) return body
  const raw = await chat(buildTranslatePrompt(body, target))
  return extractTranslatedBody(raw) || body
}
