/**
 * §§886/D56：`buildFtsQuery` / `needsCjkLike` 已搬到 `native/fts-query.ts`（
 * `likePattern` 是新增的，从 `notes-search.ts` 的同名私有函数提上来），
 * 这里改为 **re-export**，不是复制。
 *
 * 为什么必须搬、而不是让 `native/asset-store` 反向依赖 `features/`：
 * `asset-store.search()` 是**所有**资产类搜索的收口 —— PKM 搜索框
 * （`PkmTodayView.vue:132`）与 notes 侧的两处 enex 搜索（`notes-search.ts:58/76`）
 * 都走它，共 4 条路径能拿到用户自由输入
 * ⇒ 防护必须落在那一层（§884 的「修法落在能走到坏状态的路径数那一层」），
 * 而它的构造器必须在 `native/` 可达 —— `native → features` 是错的分层。
 *
 * ⚠ 用 re-export 而不是就地删掉再改 import：既有的 `notes-search.ts` 与
 *   `note-fts-query.test.ts` 都从这里 import，改动面为零，
 *   且判据仍在测**同一份实现**（不是两份会漂移的拷贝）。
 */
export { buildFtsQuery, needsCjkLike } from '../../native/fts-query.ts'

/** unicode61 把连续汉字当成一个 token，必须把双字词写进 search_text 才能 MATCH。 */
export function cjkBigramTokens(text: string): string {
  const chars = (text.match(/[\u3400-\u9fff]/g) ?? []).join('')
  if (chars.length < 2) return chars
  const grams: string[] = []
  for (let i = 0; i < chars.length - 1 && grams.length < 80; i++) {
    grams.push(chars.slice(i, i + 2))
  }
  return grams.join(' ')
}

export function mergeSearchHits<T extends { note: { id: string } }>(
  primary: T[],
  extra: T[],
  limit: number,
): T[] {
  const seen = new Set(primary.map((r) => r.note.id))
  const out = [...primary]
  for (const r of extra) {
    if (seen.has(r.note.id)) continue
    seen.add(r.note.id)
    out.push(r)
  }
  return out.slice(0, limit)
}

export function extractiveBriefing(
  query: string,
  notes: Array<{ title?: string | null; content: string; summary?: string | null }>,
): string {
  if (notes.length === 0) return '没有找到相关笔记。'
  const lines = notes.slice(0, 3).map((n, i) => {
    const title = (n.title || '').trim() || `笔记 ${i + 1}`
    const body = (n.summary || n.content || '').trim()
    const sentence = body.split(/[。.!！?\n]/)[0]?.trim() || body.slice(0, 40)
    return `${i + 1}. ${title}：${sentence}`
  })
  return `围绕「${query.trim()}」找到 ${notes.length} 条笔记。\n${lines.join('\n')}`
}
