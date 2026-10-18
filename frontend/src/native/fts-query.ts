/**
 * fts-query.ts — FTS5 查询构造与 LIKE 降级（**纯函数，零依赖**）。
 *
 * ## 为什么要从 `features/notes/note-fts-query.ts` 搬到这里
 *
 * §886/D56：`asset-store.search()` 把 `q.fts` **原样**喂给 `MATCH ?`，
 * 而**能走到那儿的用户输入有 4 条路径**（pkm 搜索框、notes 的两处 enex 搜索、
 * 以及任何将来的调用方）⇒ 按「修法落在能走到坏状态的路径数那一层」，
 * 防护必须在 `asset-store` 里，而它的构造器必须在 `native/` 可达
 * —— `native/` 反向依赖 `features/` 是错的分层。
 *
 * 实测（真 SQLite + fts5，16 个探针）原样喂入的后果：
 *   手机号 `138-0013-8000` → ❌ `no such column: 0013`
 *   `-quarterly`           → ❌ `no such column: quarterly`（`-` 是 NOT 前缀）
 *   `database AND`         → ❌ `fts5: syntax error near ""`
 *   `"database`            → ❌ `unterminated string`
 *   `(a` / `*` / `---`     → ❌ syntax error
 *   中文「数据库」「数据」  → **0 行**（不报错）
 *
 * ⇒ 「语法安全」与「中文能搜到」是**两件不同的事**，都要在这里解决。
 */

/**
 * 剥掉注释用的辅助（不导出：调用方各自有自己的 strip）。
 * 本文件保持零 import 依赖，因此不能从别处借。
 */

const CJK = /[\u3400-\u9fff]/

/** unicode61 把连续汉字当成一个 token，必须把双字词写进索引列才能 MATCH。 */
export function buildFtsQuery(raw: string): string {
  const parts: string[] = []
  const latin = raw.match(/[A-Za-z0-9_]+/g) ?? []
  const skip = new Set(['AND', 'OR', 'NOT', 'NEAR'])
  for (const w of latin) {
    if (skip.has(w.toUpperCase())) continue
    parts.push(`"${w.replace(/"/g, '""')}"`)
  }

  const cjk = (raw.match(/[\u3400-\u9fff]+/g) ?? []).join('')
  if (cjk.length === 1) parts.push(`"${cjk}"`)
  else if (cjk.length > 1) {
    const grams: string[] = []
    for (let i = 0; i < cjk.length - 1 && grams.length < 8; i++) {
      grams.push(`"${cjk.slice(i, i + 2)}"`)
    }
    parts.push(grams.join(' OR '))
  }

  if (parts.length === 0) {
    const fallback = raw.trim().replace(/["*]/g, '')
    return fallback ? `"${fallback}"` : ''
  }
  return parts.join(' ')
}

/** 输入里有没有中文。中文在 unicode61 下不可靠 ⇒ 需要并一层 LIKE。 */
export function needsCjkLike(raw: string): boolean {
  return CJK.test(raw)
}

/**
 * LIKE 的 `%...%` 模式。
 *
 * ⚠ 这里**删除** `%` / `_` 而不是转义它们 —— 这是 `features/notes/notes-search.ts`
 *   既有做法（`likePattern`），本文件刻意对齐它而不是自作主张改成转义。
 *   代价（已实测）：搜 `100%` 实际搜的是 `100`，搜 `a_b` 实际搜的是 `ab`。
 *   改成「转义 + ESCAPE」是**行为变更**，属产品口径（简报第 108 条），
 *   要两侧一起改才有意义，本节不代拍。
 */
export function likePattern(q: string): string {
  return `%${q.replace(/[%_]/g, '')}%`
}
