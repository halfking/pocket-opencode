import { http } from '../../api/http'
import { detectLang, translateTargetLang } from '../../native/detect-lang'

export async function liveTranslate(text: string, sourceLang?: string): Promise<string> {
  const src = sourceLang || detectLang(text)
  const target = translateTargetLang(src)
  const prompt = src === 'mixed'
    ? `将下面中英混合发言译成对照（中文句译英、英文句译中）。只输出译文，不要解释。\n\n${text}`
    : `把下面文本翻译成${target === 'zh' ? '中文' : 'English'}。只输出译文。\n\n${text}`
  try {
    const res = await http<{ content: string }>('/api/llm/chat', {
      method: 'POST',
      body: JSON.stringify({
        kind: 'live_translate',
        messages: [{ role: 'user', content: prompt }],
      }),
    })
    return (res.content || '').trim()
  } catch {
    return ''
  }
}
