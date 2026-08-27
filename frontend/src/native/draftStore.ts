/**
 * draftStore.ts — 会话输入草稿的存储层（P1 输入系统，契约 §4）。
 *
 * 表结构见 schema.ts 的 local_drafts（session_id 主键 + text + updated_at）。
 * 写法对齐 outboxStore / approvalStore：基于 SqlDb 抽象，Node 测试跑
 * node:sqlite，生产传 localDbAsSql(localDB)。
 *
 * 两个实现：
 *   - SqliteDraftStore：真机 / 已解锁龙虾库（持久化，杀进程不丢）
 *   - MemoryDraftStore：web / SQLite 不可用时的降级（同进程共享单例，
 *     由 useSessionDrafts 持有，切页不丢、刷新丢失——可接受的降级）
 */

import type { SqlDb } from './sqlDb.ts'

export interface SessionDraftRow {
  sessionId: string
  text: string
  /** Unix 毫秒，每次 saveDraft 刷新。 */
  updatedAt: number
}

/** 草稿存储接口（SQLite / 内存两实现共用，便于 composable 注入测试）。 */
export interface DraftStore {
  getDraft(sessionId: string): Promise<SessionDraftRow | null>
  saveDraft(sessionId: string, text: string, now?: number): Promise<void>
  clearDraft(sessionId: string): Promise<void>
}

interface DraftSqlRow {
  session_id: unknown
  text: unknown
  updated_at: unknown
}

function toDraftRow(r: DraftSqlRow, sessionId: string): SessionDraftRow {
  return {
    sessionId: String(r.session_id ?? sessionId),
    text: r.text === null || r.text === undefined ? '' : String(r.text),
    updatedAt: Number(r.updated_at ?? 0),
  }
}

export class SqliteDraftStore implements DraftStore {
  private readonly db: SqlDb

  constructor(db: SqlDb) {
    this.db = db
  }

  async getDraft(sessionId: string): Promise<SessionDraftRow | null> {
    const rows = await this.db.all(
      'SELECT session_id, text, updated_at FROM local_drafts WHERE session_id = ?',
      [sessionId],
    )
    return rows.length > 0 ? toDraftRow(rows[0] as unknown as DraftSqlRow, sessionId) : null
  }

  /** upsert：同会话重复保存只刷新 text 与 updated_at（防抖落盘的最终值）。 */
  async saveDraft(sessionId: string, text: string, now: number = Date.now()): Promise<void> {
    await this.db.run(
      `INSERT INTO local_drafts (session_id, text, updated_at) VALUES (?, ?, ?)
       ON CONFLICT(session_id) DO UPDATE SET
         text = excluded.text,
         updated_at = excluded.updated_at`,
      [sessionId, text, now],
    )
  }

  /** 发送后清除（空草稿 = 无草稿，不留空行）。 */
  async clearDraft(sessionId: string): Promise<void> {
    await this.db.run('DELETE FROM local_drafts WHERE session_id = ?', [sessionId])
  }
}

/** 内存降级实现：接口行为与 SQLite 版逐条一致（web QC / 测试用）。 */
export class MemoryDraftStore implements DraftStore {
  private readonly rows = new Map<string, SessionDraftRow>()

  async getDraft(sessionId: string): Promise<SessionDraftRow | null> {
    return this.rows.get(sessionId) ?? null
  }

  async saveDraft(sessionId: string, text: string, now: number = Date.now()): Promise<void> {
    this.rows.set(sessionId, { sessionId, text, updatedAt: now })
  }

  async clearDraft(sessionId: string): Promise<void> {
    this.rows.delete(sessionId)
  }
}
