import { localDB } from '../local-db'
import { nowUnixSec } from './planner'
import { readJsonColumn, warnCorruptColumn } from '../sqlDb'
import { captureLocalConfigLease, type ConfigDb } from './operation'
import { ownerColumnValues, ownerFromColumns, ownerScopeSql, type ConfigOwner } from './ownership'

/**
 * 默认路径：捕获一次本地编辑租约，并从**同一个租约**取出归属。
 * ⚠ 归属与句柄必须同源，否则切账号瞬间会撕裂（见 settings-store 的同款说明）。
 */
const localOperation = () => captureLocalConfigLease()

/** 出队读需要的端口。 */
type OutboxReader = Pick<ConfigDb, 'query'>
/** 入队写需要的端口。 */
type OutboxWriter = Pick<ConfigDb, 'run'>

function resolveScope(
  owner: ConfigOwner | null | undefined,
  db: OutboxReader | OutboxWriter | undefined,
): { owner: ConfigOwner | null; db: OutboxReader & OutboxWriter } {
  if (owner === undefined && db === undefined) {
    const lease = localOperation()
    return { owner: lease.owner, db: lease.db }
  }
  if (owner === undefined) {
    return { owner: localOperation().owner, db: db as OutboxReader & OutboxWriter }
  }
  return { owner, db: (db ?? localOperation().db) as OutboxReader & OutboxWriter }
}

export interface ConfigOutboxRow {
  id: string
  namespace: string
  entityId: string
  payload: unknown
  secret?: string
  updatedAt: number
  attempts: number
  state: string
  /** 入队时的归属。`null` = 归属未知（历史行），永不上传。 */
  owner: ConfigOwner | null
}

function newId(): string {
  return `cfg_${Date.now().toString(36)}_${Math.random().toString(36).slice(2, 8)}`
}

export async function enqueueConfigPush(input: {
  namespace: string
  id: string
  payload: unknown
  secret?: string
  updatedAt: number
}, owner?: ConfigOwner | null, db?: Pick<typeof localDB, 'run'>): Promise<void> {
  if (!localDB.isReady()) return
  const resolved = resolveScope(owner, db)
  const [ownerUserId, ownerWorkspaceId] = ownerColumnValues(resolved.owner)
  await resolved.db.run(
    `INSERT INTO local_config_outbox
      (id, namespace, entity_id, payload, secret, updated_at, created_at, attempts, state, owner_user_id, owner_workspace_id)
     VALUES (?, ?, ?, ?, ?, ?, ?, 0, 'queued', ?, ?)`,
    [
      newId(),
      input.namespace,
      input.id,
      JSON.stringify(input.payload ?? {}),
      input.secret ?? '',
      input.updatedAt,
      nowUnixSec(),
      ownerUserId,
      ownerWorkspaceId,
    ],
  )
}

/**
 * 列出**属于给定归属**的待推送行。
 *
 * ⚠ 归属未知的行（历史行）**永远不返回**：未认领 ≠ 属于当前账号。
 *   切到别的账号时，A 的队列不会被 B 捡走 —— 这正是跨账号串号的根因。
 */
export async function listQueuedConfigPushes(
  owner?: ConfigOwner | null,
  db?: OutboxReader,
): Promise<ConfigOutboxRow[]> {
  if (!localDB.isReady()) return []
  const resolved = resolveScope(owner, db)
  const scope = ownerScopeSql(resolved.owner)
  const rows = await resolved.db.query<Record<string, unknown>>(
    `SELECT id, namespace, entity_id, payload, secret, updated_at, attempts, state, owner_user_id, owner_workspace_id
     FROM local_config_outbox WHERE state = 'queued' AND ${scope.clause} ORDER BY created_at`,
    scope.params,
  )
  // §878：损坏的行跳过，不拿 `{}` 冒充。
  // ⚠ 后果对比：旧的 `catch { payload = {} }` 会把一条坏推送**照样发出去**，
  //   只是内容变成空 —— 服务端收到一个「什么都没有」的实体更新，
  //   而本地记录会显示「已推送成功」（`markConfigPushDone`）。
  const out: ConfigOutboxRow[] = []
  for (const row of rows) {
    const payload = readJsonColumn<unknown>(row, 'payload', 'local_config_outbox')
    if (!payload.ok) {
      warnCorruptColumn('local_config_outbox', String(row.id), payload.reason)
      continue
    }
    out.push({
      id: String(row.id),
      namespace: String(row.namespace),
      entityId: String(row.entity_id),
      payload: payload.value,
      secret: String(row.secret ?? ''),
      updatedAt: Number(row.updated_at ?? 0),
      attempts: Number(row.attempts ?? 0),
      state: String(row.state),
      owner: ownerFromColumns(row.owner_user_id, row.owner_workspace_id),
    })
  }
  return out
}

export async function markConfigPushDone(id: string, db?: Pick<ConfigDb, 'run'>): Promise<void> {
  if (!localDB.isReady()) return
  const resolved = db ?? localOperation().db
  await resolved.run(`UPDATE local_config_outbox SET state = 'succeeded' WHERE id = ? AND state = 'queued'`, [id])
}

export async function markConfigPushFailed(id: string, error: string, db?: Pick<ConfigDb, 'run'>): Promise<void> {
  if (!localDB.isReady()) return
  const resolved = db ?? localOperation().db
  await resolved.run(
    `UPDATE local_config_outbox SET attempts = attempts + 1, last_error = ? WHERE id = ? AND state = 'queued'`,
    [error.slice(0, 240), id],
  )
}
