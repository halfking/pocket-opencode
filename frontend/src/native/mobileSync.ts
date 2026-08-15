/**
 * mobileSync.ts — P1 移动离线持久化：会话同步引擎。
 *
 * 模型（conflict-free，last-write-wins）：
 *   - 服务端行的版本号 server_rev = 上游 session time.updated（Unix ms）。
 *   - 本地行的版本 = updated_at（本地单调时钟）+ dirty 标记。
 *   - pull：仅当本地非 dirty 且 remote 版本更新时覆盖；dirty 行跳过
 *     （由 push 先收敛，push 的创建走幂等键，不会产生重复上游实体）。
 *   - push：dirty 行按序重放 —— 未同步创建（serverId 空）→ createSession；
 *     已同步的墓碑 → deleteSession；其余（纯本地元数据更新）→ 直接清 dirty。
 *   - 游标 = 已见到的最大 server_rev（从数据取高水位，不用服务端时钟，
 *     避免时钟偏移导致漏拉）。
 *
 * 引擎不直接触碰 SQL / fetch：SyncStore 由 mobileSyncStore.ts 的 SQLite
 * 实现提供，SyncTransport 由宿主用后端 /api/mobile/* 客户端提供，
 * 因此整个引擎可在 node:sqlite + 本地 HTTP 假服务下做集成测试。
 */

import type { SqlDb } from './sqlDb.ts'
import { isUniqueViolation } from './sqlDb.ts'

// ---------------------------------------------------------------------------
// 类型
// ---------------------------------------------------------------------------

export interface MobileSessionRow {
  /** 客户端行 id；离线创建为 loc_ 前缀，上游拉取的为上游 id。 */
  id: string
  /** 上游 session id；null = 尚未推送创建。 */
  serverId: string | null
  workspaceId: string
  instanceId: string
  title: string
  status: string
  /** 创建动作的幂等键；上游重放安全。 */
  idempotencyKey: string
  /** 本地修订号，每次本地更新 +1。 */
  clientRev: number
  /** 上游版本（time.updated Unix ms）；0 = 未同步。 */
  serverRev: number
  dirty: boolean
  createdAt: number
  updatedAt: number
  /** 墓碑：非 null 表示本地已删除，待 push DELETE。 */
  deletedAt: number | null
}

/** 上游会话的同步视图（GET /api/mobile/sessions 返回的单行）。 */
export interface UpstreamSession {
  id: string
  title: string
  status: string
  timeUpdatedMs: number
}

export interface MobileMessageRow {
  id: string
  sessionId: string
  workspaceId: string
  instanceId: string
  type: string
  text: string
  /** pending = 待 outbox 重放；sent = 上游确认；remote = pull 回填。 */
  state: 'pending' | 'sent' | 'failed' | 'remote'
  serverMessageId: string | null
  idempotencyKey: string | null
  createdAt: number
  updatedAt: number
}

export interface SyncTransport {
  /** sinceMs=0 表示全量。返回行必须按 timeUpdatedMs 升序稳定排序。 */
  listSessions(args: { instanceId: string; sinceMs: number }): Promise<{ sessions: UpstreamSession[] }>
  /** 幂等创建：相同 idempotencyKey 重放必须返回同一上游会话。 */
  createSession(args: { instanceId: string; idempotencyKey: string; title?: string }): Promise<UpstreamSession>
  deleteSession(args: { instanceId: string; serverId: string }): Promise<void>
}

export interface MobileSyncStore {
  getCursor(name: string): Promise<number | null>
  setCursor(name: string, value: number): Promise<void>
  /** 按 serverId 或行 id 定位（pull 匹配用）。 */
  findSessionByServerId(serverId: string): Promise<MobileSessionRow | null>
  findSessionById(id: string): Promise<MobileSessionRow | null>
  listDirtySessions(workspaceId: string): Promise<MobileSessionRow[]>
  insertSession(row: MobileSessionRow): Promise<void>
  updateSession(row: MobileSessionRow): Promise<void>
  deleteSessionRow(id: string): Promise<void>
  /** 离线消息落库（pending）。 */
  insertMessage(row: MobileMessageRow): Promise<void>
  /** prompt 重放成功后标记 sent 并回填上游 messageID。 */
  markMessageSentByIdempotencyKey(idempotencyKey: string, serverMessageId: string | null): Promise<void>
}

export interface SyncResult {
  pushedCreates: number
  pushedDeletes: number
  pulledUpserts: number
  pulledSkippedDirty: number
  cursor: number
}

// ---------------------------------------------------------------------------
// 纯函数：LWW merge
// ---------------------------------------------------------------------------

export function newLocalSessionId(): string {
  return `loc_${crypto.randomUUID()}`
}

/**
 * Conflict-free merge（pull 侧）。
 * 返回需要写回的行；null 表示本地无需变更。
 * 规则：本地 dirty 行不被 pull 覆盖；否则 remote server_rev 更新才覆盖。
 */
export function mergeSessionRow(
  local: MobileSessionRow | null,
  remote: UpstreamSession,
): MobileSessionRow | null {
  if (remote.id === '' || remote.timeUpdatedMs <= 0) return null
  if (local === null) {
    return {
      id: remote.id,
      serverId: remote.id,
      workspaceId: '',
      instanceId: '',
      title: remote.title,
      status: remote.status,
      idempotencyKey: '',
      clientRev: 1,
      serverRev: remote.timeUpdatedMs,
      dirty: false,
      createdAt: remote.timeUpdatedMs,
      updatedAt: remote.timeUpdatedMs,
      deletedAt: null,
    }
  }
  if (local.dirty) return null
  if (remote.timeUpdatedMs <= local.serverRev) return null
  return {
    ...local,
    title: remote.title,
    status: remote.status,
    serverRev: remote.timeUpdatedMs,
    updatedAt: remote.timeUpdatedMs,
    deletedAt: null,
  }
}

// ---------------------------------------------------------------------------
// 引擎
// ---------------------------------------------------------------------------

function cursorName(instanceId: string): string {
  return `mobile_sessions:${instanceId}`
}

/** push dirty 会话到上游。失败抛出，调用方（宿主）安排下次同步。 */
export async function pushSessions(
  store: MobileSyncStore,
  transport: SyncTransport,
  opts: { workspaceId: string; instanceId: string },
): Promise<{ creates: number; deletes: number }> {
  const dirtyRows = await store.listDirtySessions(opts.workspaceId)
  let creates = 0
  let deletes = 0
  for (const row of dirtyRows) {
    if (row.instanceId !== opts.instanceId) continue
    if (row.deletedAt !== null) {
      if (row.serverId !== null) {
        await transport.deleteSession({ instanceId: row.instanceId, serverId: row.serverId })
        deletes++
      }
      await store.deleteSessionRow(row.id)
      continue
    }
    if (row.serverId === null) {
      const created = await transport.createSession({
        instanceId: row.instanceId,
        idempotencyKey: row.idempotencyKey,
        title: row.title,
      })
      await store.updateSession({
        ...row,
        serverId: created.id,
        serverRev: created.timeUpdatedMs,
        dirty: false,
        updatedAt: Date.now(),
      })
      creates++
      continue
    }
    // 纯本地元数据更新（上游无对应写接口）：直接确认，保持 conflict-free。
    await store.updateSession({ ...row, dirty: false, updatedAt: Date.now() })
  }
  return { creates, deletes }
}

/** pull 上游增量并 LWW 合并。游标取数据高水位，避免服务端时钟偏移漏拉。 */
export async function pullSessions(
  store: MobileSyncStore,
  transport: SyncTransport,
  opts: { workspaceId: string; instanceId: string },
): Promise<{ upserts: number; skippedDirty: number; cursor: number }> {
  const name = cursorName(opts.instanceId)
  let cursor = (await store.getCursor(name)) ?? 0
  const { sessions } = await transport.listSessions({ instanceId: opts.instanceId, sinceMs: cursor })
  let upserts = 0
  let skippedDirty = 0
  for (const remote of sessions) {
    if (remote.timeUpdatedMs <= cursor) continue
    const local =
      (await store.findSessionByServerId(remote.id)) ?? (await store.findSessionById(remote.id))
    if (local !== null && local.dirty) {
      skippedDirty++
      // dirty 行不推进该行的数据，但游照常推进（push 后下轮 pull 自会收敛）。
    } else {
      const merged = mergeSessionRow(local, remote)
      if (merged !== null) {
        const row: MobileSessionRow = {
          ...merged,
          workspaceId: merged.workspaceId || opts.workspaceId,
          instanceId: merged.instanceId || opts.instanceId,
        }
        if (local === null) {
          await store.insertSession(row)
        } else {
          await store.updateSession(row)
        }
        upserts++
      }
    }
    cursor = Math.max(cursor, remote.timeUpdatedMs)
  }
  if (sessions.length > 0) {
    await store.setCursor(name, cursor)
  }
  return { upserts, skippedDirty, cursor }
}

/** 完整一轮：先 push（本地创建优先获得上游 id）再 pull。 */
export async function syncSessions(
  store: MobileSyncStore,
  transport: SyncTransport,
  opts: { workspaceId: string; instanceId: string },
): Promise<SyncResult> {
  const push = await pushSessions(store, transport, opts)
  const pull = await pullSessions(store, transport, opts)
  return {
    pushedCreates: push.creates,
    pushedDeletes: push.deletes,
    pulledUpserts: pull.upserts,
    pulledSkippedDirty: pull.skippedDirty,
    cursor: pull.cursor,
  }
}

// ---------------------------------------------------------------------------
// SQLite 实现（MobileSyncStore）
// ---------------------------------------------------------------------------

interface SessionSqlRow {
  id: unknown
  server_id: unknown
  workspace_id: unknown
  instance_id: unknown
  title: unknown
  status: unknown
  idempotency_key: unknown
  client_rev: unknown
  server_rev: unknown
  dirty: unknown
  created_at: unknown
  updated_at: unknown
  deleted_at: unknown
}

function toSessionRow(r: SessionSqlRow): MobileSessionRow {
  return {
    id: String(r.id),
    serverId: r.server_id === null || r.server_id === undefined ? null : String(r.server_id),
    workspaceId: String(r.workspace_id),
    instanceId: String(r.instance_id),
    title: r.title === null || r.title === undefined ? '' : String(r.title),
    status: r.status === null || r.status === undefined ? '' : String(r.status),
    idempotencyKey: String(r.idempotency_key ?? ''),
    clientRev: Number(r.client_rev ?? 1),
    serverRev: Number(r.server_rev ?? 0),
    dirty: Number(r.dirty) === 1,
    createdAt: Number(r.created_at),
    updatedAt: Number(r.updated_at),
    deletedAt: r.deleted_at === null || r.deleted_at === undefined ? null : Number(r.deleted_at),
  }
}

function fromSessionRow(row: MobileSessionRow): unknown[] {
  return [
    row.id,
    row.serverId,
    row.workspaceId,
    row.instanceId,
    row.title,
    row.status,
    row.idempotencyKey,
    row.clientRev,
    row.serverRev,
    row.dirty ? 1 : 0,
    row.createdAt,
    row.updatedAt,
    row.deletedAt,
  ]
}

const SESSION_COLUMNS =
  'id, server_id, workspace_id, instance_id, title, status, idempotency_key, client_rev, server_rev, dirty, created_at, updated_at, deleted_at'

export class SqliteMobileSyncStore implements MobileSyncStore {
  private readonly db: SqlDb

  constructor(db: SqlDb) {
    this.db = db
  }

  async getCursor(name: string): Promise<number | null> {
    const rows = await this.db.all('SELECT last_synced_rowid AS v FROM local_sync_state WHERE table_name = ?', [name])
    if (rows.length === 0 || rows[0].v === null || rows[0].v === undefined) return null
    return Number(rows[0].v)
  }

  async setCursor(name: string, value: number): Promise<void> {
    await this.db.run(
      `INSERT INTO local_sync_state (table_name, last_synced_at, last_synced_rowid, pending_changes)
       VALUES (?, ?, ?, 0)
       ON CONFLICT(table_name) DO UPDATE SET last_synced_at = excluded.last_synced_at, last_synced_rowid = excluded.last_synced_rowid`,
      [name, Date.now(), value],
    )
  }

  async findSessionByServerId(serverId: string): Promise<MobileSessionRow | null> {
    const rows = await this.db.all(
      `SELECT ${SESSION_COLUMNS} FROM local_mobile_sessions WHERE server_id = ?`,
      [serverId],
    )
    return rows.length > 0 ? toSessionRow(rows[0] as unknown as SessionSqlRow) : null
  }

  async findSessionById(id: string): Promise<MobileSessionRow | null> {
    const rows = await this.db.all(
      `SELECT ${SESSION_COLUMNS} FROM local_mobile_sessions WHERE id = ?`,
      [id],
    )
    return rows.length > 0 ? toSessionRow(rows[0] as unknown as SessionSqlRow) : null
  }

  async listCachedSessions(workspaceId: string, instanceId: string): Promise<MobileSessionRow[]> {
    const rows = await this.db.all(
      `SELECT ${SESSION_COLUMNS} FROM local_mobile_sessions
       WHERE workspace_id = ? AND instance_id = ? AND deleted_at IS NULL
       ORDER BY updated_at DESC`,
      [workspaceId, instanceId],
    )
    return rows.map((r) => toSessionRow(r as unknown as SessionSqlRow))
  }

  async listDirtySessions(workspaceId: string): Promise<MobileSessionRow[]> {
    const rows = await this.db.all(
      `SELECT ${SESSION_COLUMNS} FROM local_mobile_sessions
       WHERE workspace_id = ? AND dirty = 1 ORDER BY created_at ASC`,
      [workspaceId],
    )
    return rows.map((r) => toSessionRow(r as unknown as SessionSqlRow))
  }

  async insertSession(row: MobileSessionRow): Promise<void> {
    try {
      await this.db.run(
        `INSERT INTO local_mobile_sessions (${SESSION_COLUMNS}) VALUES (${SESSION_COLUMNS.split(',').map(() => '?').join(',')})`,
        fromSessionRow(row),
      )
    } catch (err) {
      // 幂等：并发 insert 同一 server_id 视为已存在（LWW 语义下可安全忽略）。
      if (isUniqueViolation(err)) return
      throw err
    }
  }

  async updateSession(row: MobileSessionRow): Promise<void> {
    await this.db.run(
      `UPDATE local_mobile_sessions SET
         server_id = ?, workspace_id = ?, instance_id = ?, title = ?, status = ?,
         idempotency_key = ?, client_rev = ?, server_rev = ?, dirty = ?,
         created_at = ?, updated_at = ?, deleted_at = ?
       WHERE id = ?`,
      [...fromSessionRow(row).slice(1), row.id],
    )
  }

  async deleteSessionRow(id: string): Promise<void> {
    await this.db.run('DELETE FROM local_mobile_sessions WHERE id = ?', [id])
  }

  async insertMessage(row: MobileMessageRow): Promise<void> {
    await this.db.run(
      `INSERT INTO local_mobile_messages
         (id, session_id, workspace_id, instance_id, type, text, state, server_message_id, idempotency_key, created_at, updated_at)
       VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
      [
        row.id,
        row.sessionId,
        row.workspaceId,
        row.instanceId,
        row.type,
        row.text,
        row.state,
        row.serverMessageId,
        row.idempotencyKey,
        row.createdAt,
        row.updatedAt,
      ],
    )
  }

  async markMessageSentByIdempotencyKey(idempotencyKey: string, serverMessageId: string | null): Promise<void> {
    await this.db.run(
      `UPDATE local_mobile_messages
       SET state = 'sent', server_message_id = ?, updated_at = ?
       WHERE idempotency_key = ? AND state = 'pending'`,
      [serverMessageId, Date.now(), idempotencyKey],
    )
  }
}
