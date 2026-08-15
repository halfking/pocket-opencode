/**
 * approvalStore.ts — local_mobile_approvals 的 SQLite 落地（P1 遗留项：审批 pull 回填）。
 *
 * 三个写路径：
 *   - pull 回填（backfillApprovals）：GET /api/mobile/approvals 的 pending 快照
 *     落库；本地 pending 但服务端已不存在的行标记 expired（在别处已处理）。
 *   - 本地决定（saveDecision）：离线审批回复先记 decision，state 保持 pending，
 *     等 drain 成功后由 markReplied 置 sent（服务端确认前不显示"已批准"，08 §4.5）。
 *   - drain 回写（markReplied / markExpired）：outbox 发送成功 / 409 终态时回写。
 */

import type { SqlDb } from './sqlDb.ts'

export type MobileApprovalKind = 'permission' | 'question'
export type MobileApprovalState = 'pending' | 'sent' | 'expired'

export interface MobileApprovalRow {
  /** 服务端 request_id（per_xxx / que_xxx）。 */
  id: string
  workspaceId: string
  instanceId: string
  sessionId: string | null
  kind: MobileApprovalKind
  /** 服务端审批请求快照（原样 JSON）。 */
  payload: Record<string, unknown>
  /** 本地已做的决定（once/always/reject/answer）；null = 未决定。 */
  decision: string | null
  state: MobileApprovalState
  createdAt: number
  updatedAt: number
  repliedAt: number | null
}

/** GET /api/mobile/approvals 的响应视图。 */
export interface ServerPendingApprovals {
  permissions: Array<Record<string, unknown>>
  questions: Array<Record<string, unknown>>
}

interface ApprovalSqlRow {
  id: unknown
  workspace_id: unknown
  instance_id: unknown
  session_id: unknown
  kind: unknown
  payload: unknown
  decision: unknown
  state: unknown
  created_at: unknown
  updated_at: unknown
  replied_at: unknown
}

const APPROVAL_COLUMNS =
  'id, workspace_id, instance_id, session_id, kind, payload, decision, state, created_at, updated_at, replied_at'

function toApprovalRow(r: ApprovalSqlRow): MobileApprovalRow {
  let payload: Record<string, unknown> = {}
  try {
    const parsed = JSON.parse(String(r.payload ?? '{}')) as Record<string, unknown>
    if (parsed !== null && typeof parsed === 'object') payload = parsed
  } catch {
    // 快照损坏时保留空对象，行本身仍可展示/过期
  }
  return {
    id: String(r.id),
    workspaceId: String(r.workspace_id),
    instanceId: String(r.instance_id),
    sessionId:
      r.session_id === null || r.session_id === undefined ? null : String(r.session_id),
    kind: String(r.kind) === 'question' ? 'question' : 'permission',
    payload,
    decision:
      r.decision === null || r.decision === undefined ? null : String(r.decision),
    state: String(r.state) === 'sent' ? 'sent' : String(r.state) === 'expired' ? 'expired' : 'pending',
    createdAt: Number(r.created_at),
    updatedAt: Number(r.updated_at),
    repliedAt:
      r.replied_at === null || r.replied_at === undefined ? null : Number(r.replied_at),
  } as MobileApprovalRow
}

export class SqliteApprovalStore {
  private readonly db: SqlDb

  constructor(db: SqlDb) {
    this.db = db
  }

  async find(id: string): Promise<MobileApprovalRow | null> {
    const rows = await this.db.all(
      `SELECT ${APPROVAL_COLUMNS} FROM local_mobile_approvals WHERE id = ?`,
      [id],
    )
    return rows.length > 0 ? toApprovalRow(rows[0] as unknown as ApprovalSqlRow) : null
  }

  async listPending(workspaceId: string): Promise<MobileApprovalRow[]> {
    const rows = await this.db.all(
      `SELECT ${APPROVAL_COLUMNS} FROM local_mobile_approvals
       WHERE workspace_id = ? AND state = 'pending' ORDER BY created_at ASC`,
      [workspaceId],
    )
    return rows.map((r) => toApprovalRow(r as unknown as ApprovalSqlRow))
  }

  /**
   * pull 侧 upsert：本地已回复（sent / 有 decision）的行只刷新快照，
   * 不覆盖决定与状态——服务端 pending 列表不携带"已在别处处理"的终态。
   */
  async upsertSnapshot(row: MobileApprovalRow): Promise<void> {
    const existing = await this.find(row.id)
    if (existing !== null && (existing.state !== 'pending' || existing.decision !== null)) {
      await this.db.run(
        `UPDATE local_mobile_approvals SET payload = ?, updated_at = ? WHERE id = ?`,
        [JSON.stringify(row.payload), row.updatedAt, row.id],
      )
      return
    }
    await this.db.run(
      `INSERT INTO local_mobile_approvals
         (id, workspace_id, instance_id, session_id, kind, payload, decision, state, created_at, updated_at, replied_at)
       VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
       ON CONFLICT(id) DO UPDATE SET
         payload = excluded.payload,
         session_id = COALESCE(excluded.session_id, local_mobile_approvals.session_id),
         updated_at = excluded.updated_at`,
      [
        row.id,
        row.workspaceId,
        row.instanceId,
        row.sessionId,
        row.kind,
        JSON.stringify(row.payload),
        row.decision,
        row.state,
        row.createdAt,
        row.updatedAt,
        row.repliedAt,
      ],
    )
  }

  /** 离线决定：只写 decision，state 保持 pending（服务端确认前不置 sent）。 */
  async saveDecision(id: string, decision: string, now: number = Date.now()): Promise<void> {
    await this.db.run(
      `UPDATE local_mobile_approvals
       SET decision = ?, updated_at = ?, replied_at = ?
       WHERE id = ? AND state = 'pending'`,
      [decision, now, now, id],
    )
  }

  /** drain 成功后：服务端已确认决定。 */
  async markReplied(id: string, decision: string, now: number = Date.now()): Promise<void> {
    await this.db.run(
      `UPDATE local_mobile_approvals
       SET state = 'sent', decision = COALESCE(decision, ?), replied_at = COALESCE(replied_at, ?), updated_at = ?
       WHERE id = ?`,
      [decision, now, now, id],
    )
  }

  /** 409 / pull 侧消失：请求已过期或在别处处理。 */
  async markExpired(id: string, now: number = Date.now()): Promise<void> {
    await this.db.run(
      `UPDATE local_mobile_approvals SET state = 'expired', updated_at = ? WHERE id = ? AND state = 'pending'`,
      [now, id],
    )
  }
}

function snapshotFromServer(
  kind: MobileApprovalKind,
  item: Record<string, unknown>,
  args: { workspaceId: string; instanceId: string; now: number },
): MobileApprovalRow {
  const sessionId = typeof item.sessionID === 'string' ? item.sessionID : null
  return {
    id: String(item.id),
    workspaceId: args.workspaceId,
    instanceId: args.instanceId,
    sessionId,
    kind,
    payload: item,
    decision: null,
    state: 'pending',
    createdAt: args.now,
    updatedAt: args.now,
    repliedAt: null,
  }
}

/**
 * pull 回填：服务端 pending 快照落库 + 本地 pending 但服务端已消失的行过期。
 * 只处理单个 instance（GET 按 instance 查询，过期判定也限定在该 instance 内）。
 */
export async function backfillApprovals(
  store: SqliteApprovalStore,
  args: {
    workspaceId: string
    instanceId: string
    server: ServerPendingApprovals
    now?: number
  },
): Promise<{ upserted: number; expired: number }> {
  const now = args.now ?? Date.now()
  const serverIds = new Set<string>()
  let upserted = 0

  for (const item of args.server.permissions ?? []) {
    if (typeof item.id !== 'string' || item.id === '') continue
    serverIds.add(item.id)
    await store.upsertSnapshot(
      snapshotFromServer('permission', item, {
        workspaceId: args.workspaceId,
        instanceId: args.instanceId,
        now,
      }),
    )
    upserted++
  }
  for (const item of args.server.questions ?? []) {
    if (typeof item.id !== 'string' || item.id === '') continue
    serverIds.add(item.id)
    await store.upsertSnapshot(
      snapshotFromServer('question', item, {
        workspaceId: args.workspaceId,
        instanceId: args.instanceId,
        now,
      }),
    )
    upserted++
  }

  // 本地 pending（无本地决定）且服务端已不存在 → 在别处处理 / 过期。
  let expired = 0
  for (const row of await store.listPending(args.workspaceId)) {
    if (row.instanceId !== args.instanceId) continue
    if (row.decision !== null) continue
    if (serverIds.has(row.id)) continue
    await store.markExpired(row.id, now)
    expired++
  }
  return { upserted, expired }
}
