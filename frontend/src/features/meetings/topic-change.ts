/**
 * 议题变化：新句与当前主题 overlap 过低则强制刷新滚动摘要。
 */
const STOP = new Set(['的', '了', '和', '在', '是', '我', '你', '我们', 'the', 'a', 'to', 'of', 'and', 'is'])

export function topicTokens(text: string): string[] {
  const out: string[] = []
  for (const t of text.toLowerCase().split(/[^\p{L}\p{N}]+/u)) {
    if (t.length < 2 || STOP.has(t)) continue
    if (/^[\u4e00-\u9fff]+$/.test(t)) continue
    out.push(t)
  }
  const cjk = (text.match(/[\u4e00-\u9fff]+/g) || []).join('')
  for (let i = 0; i < cjk.length - 1; i++) {
    const gram = cjk.slice(i, i + 2)
    if (!STOP.has(gram)) out.push(gram)
  }
  return out
}

export function topicShift(prevTopic: string | undefined, newText: string): boolean {
  if (!prevTopic?.trim()) return false
  const a = new Set(topicTokens(prevTopic))
  const b = topicTokens(newText)
  if (a.size === 0 || b.length === 0) return false
  const hit = b.filter((t) => a.has(t)).length
  return hit / b.length < 0.15
}
