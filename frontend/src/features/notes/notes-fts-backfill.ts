/**
 * notes-fts-backfill.ts — 笔记全文索引回填的**可测核心**（§879）。
 *
 * ⚠ 为什么要单独一个文件：`notes-fts-ready.ts` 顶层 import 了 `localDB` 单例与
 *   `native/crypto`，那条 import 链在 node 里跑不起来（无扩展名 import 那一族，
 *   简报第 94 条）⇒ **决策与编排没法在 node 里被测**。
 *   手法与 §820 的 `resolvePersistedAudio` 同一：把要在 node 里验的东西放进
 *   一个不依赖单例的模块，单例那一侧只留一层薄包装。
 *
 * ⚠ 本模块**不得** import `localDB` / `crypto` / 任何浏览器 API ——
 *   它只依赖注入进来的 `db` 与 `decrypt`。
 */

import { buildSearchText } from './note-storage-policy.ts'

export const NOTES_FTS_MIGRATION = '2026-09-08-notes-fts-v2'

/** 回填逻辑依赖的最小 DB 端口。生产传 `localDB`，测试传真 `node:sqlite`。 */
export interface NotesIndexDb {
  isReady(): boolean
  queryOne<T>(sql: string, values?: unknown[]): Promise<T | null>
  query<T>(sql: string, values?: unknown[]): Promise<T[]>
  run(sql: string, values?: unknown[]): Promise<number>
  execute(statements: string, transaction?: boolean): Promise<number>
}

export interface NoteIndexRow {
  id: string
  title: string | null
  content: string
  tags: string | null
  search_text: string | null
  encrypted_content: number
  storage_tier?: 'inline' | 'file' | null
}

export interface BackfillResult {
  /** 是否把这次迁移记成了「已完成」。 */
  stamped: boolean
  /** 改写了 search_text 的行数。 */
  changed: number
  /** 因主密码未解锁而跳过的加密笔记数。 */
  lockedSkipped: number
  /** FTS 重灌是否成功（虚表不存在时为 false，但那是**预期降级**，不阻塞完成）。 */
  ftsOk: boolean
  /** 该安装是否**根本没有** FTS5（web / sql.js）—— 重试再多次也一样。 */
  ftsAbsent?: boolean
}

export function parseTags(raw: string | null): string[] {
  if (!raw) return []
  try {
    const parsed = JSON.parse(raw) as unknown
    return Array.isArray(parsed) ? parsed.filter((t): t is string => typeof t === 'string') : []
  } catch {
    return []
  }
}

/** Web / sql.js 上没有 FTS5：虚表不存在是**预期降级**，不算重灌失败。 */
export function isMissingFtsTable(e: unknown): boolean {
  const msg = e instanceof Error ? e.message : String(e ?? '')
  return /no such table/i.test(msg)
}

/**
 * 回填 `search_text` 并重灌 FTS，返回这次迁移到底算不算完成。
 *
 * ⚠⚠§879 的全部要害：**重灌的成败必须参与「要不要写版本号」这个判断**。
 *   旧流程里版本号只看 `lockedSkipped`，于是重灌失败时索引已被 `delete-all`
 *   清空、重灌没补上，而 `_schema_migrations` 照样记成「已完成」
 *   ⇒ 这次回填**再也不会重跑**，笔记全文检索永久残缺且零提示。
 *
 * ⚠ 两条 FTS 语句**没有**放在同一个事务里（见设计文档 §879.4）：
 *   能不能合，取决于 `executeSet` / `execute(sql, true)` 对 **FTS 虚表命令**
 *   的行为，本仓从未在任何 `execute` 调用上用过 `transaction=true`，真机未实测。
 *   ⇒ 本节只把「失败不许被记成完成」这件事做实，原子性登记为待拍板第 100 条。
 */
export async function runNotesIndexBackfill(
  db: NotesIndexDb,
  decrypt: (cipher: string) => Promise<string>,
): Promise<BackfillResult> {
  const done = await db.queryOne<{ version: string }>(
    'SELECT version FROM _schema_migrations WHERE version = ?',
    [NOTES_FTS_MIGRATION],
  )
  if (done) return { stamped: true, changed: 0, lockedSkipped: 0, ftsOk: true, ftsAbsent: false }

  const rows = await db.query<NoteIndexRow>(
    `SELECT id, title, content, tags, search_text, encrypted_content, storage_tier
       FROM local_notes WHERE deleted_at IS NULL`,
  )
  let changed = 0
  let lockedSkipped = 0
  for (const row of rows) {
    // file 层笔记的 content 只是摘要，全文语料创建时已写入 search_text；
    // 用 content 重建会把全文检索降级成摘要检索，跳过已有语料的行。
    // 这是永久性跳过（非解锁后可重试），不计入 lockedSkipped。
    if (row.storage_tier === 'file' && (row.search_text ?? '') !== '') continue
    let plain = row.content ?? ''
    if (row.encrypted_content === 1) {
      try {
        plain = await decrypt(row.content)
      } catch {
        // 主密码尚未解锁：整轮不写迁移版本号，下次再重试
        lockedSkipped++
        continue
      }
    }
    const next = buildSearchText(row.title, plain, parseTags(row.tags))
    if (next === (row.search_text ?? '')) continue
    await db.run('UPDATE local_notes SET search_text = ? WHERE id = ?', [next, row.id])
    changed++
  }

  // ⚠⚠⚠§879 第二层：重灌**不能**用 `changed > 0` 当闸门。
  //   `changed` 说的是「这一轮有没有改写 search_text」，与「索引是不是好的」**无关**：
  //   第一轮改了 search_text 却重灌失败，第二轮 `changed === 0` ⇒ 重建被整个跳过
  //   ⇒ 索引仍然是空的，而这一轮**会**把迁移记成「已完成」⇒ 永久残缺。
  //   ⇒ 闸门只能是「这张 FTS 表在不在」（web 上永远不在，那是预期降级）。
  let ftsAbsent = false
  let ftsOk = true
  try {
    await db.execute("INSERT INTO local_notes_fts(local_notes_fts) VALUES('delete-all')")
    await db.execute(
      `INSERT INTO local_notes_fts(rowid, title, content)
         SELECT rowid, title, COALESCE(NULLIF(search_text, ''), content)
           FROM local_notes WHERE deleted_at IS NULL`,
    )
  } catch (e) {
    ftsOk = false
    // Web/sql.js 没有 FTS5 ⇒ 虚表不存在，属**预期降级**：
    //   那不是「待重试的失败」，重试一万次也一样 ⇒ 照样可以记成完成。
    ftsAbsent = isMissingFtsTable(e)
    if (!ftsAbsent) {
      console.warn('[notes] FTS 重灌失败（索引已被 delete-all 清空且未补回）：', e)
    }
  }

  if (lockedSkipped === 0 && (ftsOk || ftsAbsent)) {
    await db.run(
      `INSERT OR IGNORE INTO _schema_migrations (version, description, applied_at) VALUES (?, ?, ?)`,
      [NOTES_FTS_MIGRATION, '回填 search_text 双字词并重灌 FTS', Date.now()],
    )
    return { stamped: true, changed, lockedSkipped, ftsOk, ftsAbsent }
  }
  return { stamped: false, changed, lockedSkipped, ftsOk, ftsAbsent }
}
