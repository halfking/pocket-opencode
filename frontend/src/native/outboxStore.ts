/**
 * outboxStore.ts — PR13 OutboxStorage 接口的 SQLite 落地（SEC-06）。
 *
 * 表结构见 schema.ts 的 local_outbox。payload 以 JSON 文本存储，读出时
 * 反序列化；其余列与 OutboxRecord 一一对应。行状态在写入端维护
 * （queued/inflight/succeeded/dead_letter），drain 循环见 outboxDrain.ts。
 */

import type { OutboxRecord, OutboxState, OutboxStorage } from '../utils/outbox.ts'
import type { SqlDb } from './sqlDb.ts'
import { isUniqueViolation, readJsonColumn, warnCorruptColumn } from './sqlDb.ts'

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

/**
 * §878：行 → 记录。**payload 损坏时返回 null（跳过该行），不再抛。**
 *
 * ⚠⚠ 修之前的形状是裸 `JSON.parse(...)` 直接抛，而 `listReady` 的调用方
 *   `drainOutbox` 外面**没有** try/catch，`listReady` 又是
 *   `ORDER BY created_at ASC LIMIT ?` ⇒ 一条排在队首的坏 payload
 *   会让**每一次** drain 整个 reject ⇒ **整个离线队列（session.create /
 *   session.prompt / approval.reply）永久停摆**，且没有任何提示。
 *   ⇒ 这是「一条坏数据连累同批全部」的家族，与 §876/D45「损坏谎报成空」
 *   同源但**后果更重**：那一条谎报，这一条是**永久堵死**。
 *
 * ⚠ 损坏的行**留在表里不删**：原始字节本身就是证据（§876 的原则是
 *   「损坏不许覆写」），删了就再也查不出当时坏成什么样。
 */
function toRecord(r: OutboxSqlRow): OutboxRecord | null {
  const payload = readJsonColumn<unknown>(r as unknown as Record<string, unknown>, 'payload', 'local_outbox')
  if (!payload.ok) {
    warnCorruptColumn('local_outbox', String(r.id), payload.reason)
    return null
  }
  return {
    id: String(r.id),
    idempotencyKey: String(r.idempotency_key),
    workspaceId: String(r.workspace_id),
    action: String(r.action),
    payload: payload.value,
    createdAt: Number(r.created_at),
    nextAttemptAt: Number(r.next_attempt_at),
    attempts: Number(r.attempts),
    cursor: r.cursor === null || r.cursor === undefined ? undefined : String(r.cursor),
    lastError: r.last_error === null || r.last_error === undefined ? undefined : String(r.last_error),
    state: String(r.state) as OutboxState,
    ttlMs: Number(r.ttl_ms),
  }
}

/** §878：点查。**损坏必须抛**，不能报成「没有这一行」。 */
function toRecordOrThrow(r: OutboxSqlRow): OutboxRecord {
  const rec = toRecord(r)
  if (rec === null) {
    throw new Error(
      `[outbox_corrupt] local_outbox 的行 ${String(r.id)} 的 payload 已损坏；`
      + '该行仍在表里（未删未改写），请检查 localStorage/localDB 后再重试。',
    )
  }
  return rec
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
    if (rows.length === 0) return null
    return toRecordOrThrow(rows[0] as unknown as OutboxSqlRow)
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
    // §878：逐行解码，损坏的跳过 ⇒ 同批的其它记录照常重放（不再整批堵死）。
    const out: OutboxRecord[] = []
    for (const row of rows) {
      const rec = toRecord(row as unknown as OutboxSqlRow)
      if (rec !== null) out.push(rec)
    }
    return out
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
