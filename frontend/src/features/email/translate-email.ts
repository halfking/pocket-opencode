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
    '不要添加说明、标题或代码围栏，只输出译文本身。',
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
