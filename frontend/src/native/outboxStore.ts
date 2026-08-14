/**
 * outboxStore.ts — PR13 OutboxStorage 接口的 SQLite 落地（SEC-06）。
 *
 * 表结构见 schema.ts 的 local_outbox。payload 以 JSON 文本存储，读出时
 * 反序列化；其余列与 OutboxRecord 一一对应。行状态在写入端维护
 * （queued/inflight/succeeded/dead_letter），drain 循环见 outboxDrain.ts。
 */

import type { OutboxRecord, OutboxState, OutboxStorage } from '../utils/outbox.ts'
import type { SqlDb } from './sqlDb.ts'
import { isUniqueViolation } from './sqlDb.ts'

interface OutboxSqlRow {
  id: unknown
  idempotency_key: unknown
  workspace_id: unknown
  action: unknown
  payload: unknown
  created_at: unknown
  next_attempt_at: unknown
  attempts: unknown
  cursor: unknown
  last_error: unknown
  state: unknown
  ttl_ms: unknown
}

function toRecord(r: OutboxSqlRow): OutboxRecord {
  return {
    id: String(r.id),
    idempotencyKey: String(r.idempotency_key),
    workspaceId: String(r.workspace_id),
    action: String(r.action),
    payload: JSON.parse(String(r.payload ?? 'null')),
    createdAt: Number(r.created_at),
    nextAttemptAt: Number(r.next_attempt_at),
    attempts: Number(r.attempts),
    cursor: r.cursor === null || r.cursor === undefined ? undefined : String(r.cursor),
    lastError: r.last_error === null || r.last_error === undefined ? undefined : String(r.last_error),
    state: String(r.state) as OutboxState,
    ttlMs: Number(r.ttl_ms),
  }
}

function fromRecord(record: OutboxRecord): unknown[] {
  return [
    record.id,
    record.idempotencyKey,
    record.workspaceId,
    record.action,
    JSON.stringify(record.payload ?? null),
    record.createdAt,
    record.nextAttemptAt,
    record.attempts,
    record.cursor ?? null,
    record.lastError ?? null,
    record.state,
    record.ttlMs,
  ]
}

const OUTBOX_COLUMNS =
  'id, idempotency_key, workspace_id, action, payload, created_at, next_attempt_at, attempts, cursor, last_error, state, ttl_ms'

export class SqliteOutboxStore implements OutboxStorage {
  private readonly db: SqlDb

  constructor(db: SqlDb) {
    this.db = db
  }

  async put(record: OutboxRecord): Promise<void> {
    try {
      await this.db.run(
        `INSERT INTO local_outbox (${OUTBOX_COLUMNS}) VALUES (${OUTBOX_COLUMNS.split(',').map(() => '?').join(',')})`,
        fromRecord(record),
      )
      return
    } catch (err) {
      if (!isUniqueViolation(err)) throw err
    }
    // 幂等键冲突 = 同一动作已入队；更新为最新状态（重放安全）。
    await this.db.run(
      `UPDATE local_outbox SET
         workspace_id = ?, action = ?, payload = ?, next_attempt_at = ?, attempts = ?,
         cursor = ?, last_error = ?, state = ?, ttl_ms = ?
       WHERE idempotency_key = ?`,
      [
        record.workspaceId,
        record.action,
        JSON.stringify(record.payload ?? null),
        record.nextAttemptAt,
        record.attempts,
        record.cursor ?? null,
        record.lastError ?? null,
        record.state,
        record.ttlMs,
        record.idempotencyKey,
      ],
    )
  }

  async get(id: string): Promise<OutboxRecord | null> {
    const rows = await this.db.all(`SELECT ${OUTBOX_COLUMNS} FROM local_outbox WHERE id = ?`, [id])
    return rows.length > 0 ? toRecord(rows[0] as unknown as OutboxSqlRow) : null
  }

  async delete(id: string): Promise<void> {
    await this.db.run('DELETE FROM local_outbox WHERE id = ?', [id])
  }

  async listReady(now: number, limit: number): Promise<OutboxRecord[]> {
    const rows = await this.db.all(
      `SELECT ${OUTBOX_COLUMNS} FROM local_outbox
       WHERE state = 'queued' AND next_attempt_at <= ?
       ORDER BY created_at ASC LIMIT ?`,
      [now, limit],
    )
    return rows.map((r) => toRecord(r as unknown as OutboxSqlRow))
  }

  async countByState(states: OutboxState[]): Promise<number> {
    if (states.length === 0) return 0
    const placeholders = states.map(() => '?').join(',')
    const rows = await this.db.all(
      `SELECT COUNT(*) AS n FROM local_outbox WHERE state IN (${placeholders})`,
      states,
    )
    return Number(rows[0]?.n ?? 0)
  }

  /** 死信清理（宿主在用户确认或 TTL 过期后调用）。 */
  async purgeDeadLetters(): Promise<number> {
    return this.db.run(`DELETE FROM local_outbox WHERE state = 'dead_letter'`)
  }
}
