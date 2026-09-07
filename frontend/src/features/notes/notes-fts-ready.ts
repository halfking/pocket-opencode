import { localDB } from '../../native/local-db'
import { decryptString } from '../../native/crypto'
import { buildSearchText } from './note-storage-policy'

const MIGRATION = '2026-09-08-notes-fts-v2'

interface NoteIndexRow {
  id: string
  title: string | null
  content: string
  tags: string | null
  search_text: string | null
  encrypted_content: number
}

function parseTags(raw: string | null): string[] {
  if (!raw) return []
  try {
    const parsed = JSON.parse(raw) as unknown
    return Array.isArray(parsed) ? parsed.filter((t): t is string => typeof t === 'string') : []
  } catch {
    return []
  }
}

/**
 * 回填 search_text（含汉字双字词）并重灌 FTS。Web 无 FTS5，重灌失败可忽略。
 *
 * 不能用外部内容表的 'rebuild'：它会按 FTS 列名从 local_notes 重读原始 content
 * 列（加密行是密文、file 档只有摘要），且语料与触发器写入的
 * COALESCE(search_text, content) 失配，后续触发器的 'delete' 命令会损坏索引。
 * 因此这里先 'delete-all'，再按触发器同款表达式显式重灌。
 */
export async function ensureNotesSearchIndex(): Promise<void> {
  if (!localDB.isReady()) return
  try {
    const done = await localDB.queryOne<{ version: string }>(
      'SELECT version FROM _schema_migrations WHERE version = ?',
      [MIGRATION],
    )
    if (done) return

    const rows = await localDB.query<NoteIndexRow>(
      `SELECT id, title, content, tags, search_text, encrypted_content
         FROM local_notes WHERE deleted_at IS NULL`,
    )
    let changed = 0
    let lockedSkipped = 0
    for (const row of rows) {
      let plain = row.content ?? ''
      if (row.encrypted_content === 1) {
        try {
          plain = await decryptString(row.content)
        } catch {
          // 主密码尚未解锁：记下并整轮不写迁移版本号，下次 listNotes 再重试
          lockedSkipped++
          continue
        }
      }
      const next = buildSearchText(row.title, plain, parseTags(row.tags))
      if (next === (row.search_text ?? '')) continue
      await localDB.run('UPDATE local_notes SET search_text = ? WHERE id = ?', [next, row.id])
      changed++
    }

    if (changed > 0) {
      try {
        await localDB.execute("INSERT INTO local_notes_fts(local_notes_fts) VALUES('delete-all')")
        await localDB.execute(
          `INSERT INTO local_notes_fts(rowid, title, content)
             SELECT rowid, title, COALESCE(NULLIF(search_text, ''), content)
               FROM local_notes WHERE deleted_at IS NULL`,
        )
      } catch {
        // Web/sql.js 没有 FTS5；Android SQLCipher 带 FTS5，重灌后 MATCH 才跟得上 search_text。
      }
    }

    if (lockedSkipped === 0) {
      await localDB.run(
        `INSERT OR IGNORE INTO _schema_migrations (version, description, applied_at) VALUES (?, ?, ?)`,
        [MIGRATION, '回填 search_text 双字词并重灌 FTS', Date.now()],
      )
    } else {
      console.warn(`[notes] search index backfill: ${lockedSkipped} 条加密笔记待解锁后重试`)
    }
  } catch (e) {
    console.warn('[notes] search index backfill skipped:', e)
  }
}
