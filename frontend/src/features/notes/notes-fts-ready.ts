import { localDB } from '../../native/local-db'
import { buildSearchText } from './note-storage-policy'

const MIGRATION = '2026-09-08-notes-fts-v2'

interface NoteIndexRow {
  id: string
  title: string | null
  content: string
  tags: string | null
  search_text: string | null
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

/** 回填 search_text（含汉字双字词）并尽量重建 FTS。Web 无 FTS5，重建失败可忽略。 */
export async function ensureNotesSearchIndex(): Promise<void> {
  if (!localDB.isReady()) return
  try {
    const done = await localDB.queryOne<{ version: string }>(
      'SELECT version FROM _schema_migrations WHERE version = ?',
      [MIGRATION],
    )
    if (done) return

    const rows = await localDB.query<NoteIndexRow>(
      `SELECT id, title, content, tags, search_text FROM local_notes WHERE deleted_at IS NULL`,
    )
    for (const row of rows) {
      const next = buildSearchText(row.title, row.content ?? '', parseTags(row.tags))
      if (next === (row.search_text ?? '')) continue
      await localDB.run('UPDATE local_notes SET search_text = ? WHERE id = ?', [next, row.id])
    }

    try {
      await localDB.execute("INSERT INTO local_notes_fts(local_notes_fts) VALUES('rebuild')")
    } catch {
      // Web/sql.js 没有 FTS5；Android SQLCipher 带 FTS5，重建后 MATCH 才跟得上 search_text。
    }

    await localDB.run(
      `INSERT OR IGNORE INTO _schema_migrations (version, description, applied_at) VALUES (?, ?, ?)`,
      [MIGRATION, '回填 search_text 双字词并重建 FTS', Date.now()],
    )
  } catch (e) {
    console.warn('[notes] search index backfill skipped:', e)
  }
}
