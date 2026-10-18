/**
 * notes-fts-ready.ts — 单例包装层（§879 之后这里只剩「接线」）。
 *
 * 原本这个文件既 import `localDB` 又 import `decryptString`，还把整个回填流程
 * 写在里面 ⇒ **回填的成败判定在 node 里跑不到**（§820 的 `resolvePersistedAudio`
 * 是同一个道理：要在 node 里验的东西必须放在不依赖单例的模块里）。
 *
 * ⇒ 可测核心搬到 `notes-fts-backfill.ts`（只依赖注入的 db 与 decrypt）；
 *   本文件只负责把生产单例接上去，并把三种结局说成人话。
 */
import { localDB } from '../../native/local-db.ts'
import { decryptString } from '../../native/crypto.ts'
import { runNotesIndexBackfill } from './notes-fts-backfill.ts'

export { parseTags, NOTES_FTS_MIGRATION, isMissingFtsTable } from './notes-fts-backfill.ts'
export type { NotesIndexDb, BackfillResult } from './notes-fts-backfill.ts'

/**
 * 回填 search_text（含汉字双字词）并重灌 FTS。
 *
 * ⚠ 重灌失败时**不写迁移版本号** —— 下次调用会整轮重试。
 *   代价（无退避地重复跑整轮回填）登记在简报第 100 条。
 */
export async function ensureNotesSearchIndex(): Promise<void> {
  if (!localDB.isReady()) return
  try {
    const r = await runNotesIndexBackfill(localDB, decryptString)
    if (r.lockedSkipped > 0) {
      console.warn(`[notes] search index backfill: ${r.lockedSkipped} 条加密笔记待解锁后重试`)
    } else if (!r.stamped) {
      console.warn('[notes] search index backfill: FTS 重灌未完成 ⇒ 不写迁移版本号，下次搜索会重试')
    }
  } catch (e) {
    console.warn('[notes] search index backfill skipped:', e)
  }
}
