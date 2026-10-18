import { localDB } from '../local-db'
import { nowUnixSec } from './planner'
import { readJsonColumn, warnCorruptColumn } from '../sqlDb'

export interface ConfigOutboxRow {
  id: string
  namespace: string
  entityId: string
  payload: unknown
  secret?: string
  updatedAt: number
  attempts: number
  state: string
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
}): Promise<void> {
  if (!localDB.isReady()) return
  await localDB.run(
    `INSERT INTO local_config_outbox
      (id, namespace, entity_id, payload, secret, updated_at, created_at, attempts, state)
     VALUES (?, ?, ?, ?, ?, ?, ?, 0, 'queued')`,
    [
      newId(),
      input.namespace,
      input.id,
      JSON.stringify(input.payload ?? {}),
      input.secret ?? '',
      input.updatedAt,
      nowUnixSec(),
    ],
  )
}

export async function listQueuedConfigPushes(): Promise<ConfigOutboxRow[]> {
  if (!localDB.isReady()) return []
  const rows = await localDB.query<Record<string, unknown>>(
    `SELECT id, namespace, entity_id, payload, secret, updated_at, attempts, state
     FROM local_config_outbox WHERE state = 'queued' ORDER BY created_at`,
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
    })
  }
  return out
}

export async function markConfigPushDone(id: string): Promise<void> {
  if (!localDB.isReady()) return
  await localDB.run(`UPDATE local_config_outbox SET state = 'succeeded' WHERE id = ?`, [id])
}

export async function markConfigPushFailed(id: string, error: string): Promise<void> {
  if (!localDB.isReady()) return
  await localDB.run(
    `UPDATE local_config_outbox SET attempts = attempts + 1, last_error = ? WHERE id = ?`,
    [error.slice(0, 240), id],
  )
}
