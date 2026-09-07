import { localDB } from '../local-db'
import { nowUnixSec } from './planner'

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
  return rows.map((row) => {
    let payload: unknown = {}
    try { payload = JSON.parse(String(row.payload ?? '{}')) } catch { payload = {} }
    return {
      id: String(row.id),
      namespace: String(row.namespace),
      entityId: String(row.entity_id),
      payload,
      secret: String(row.secret ?? ''),
      updatedAt: Number(row.updated_at ?? 0),
      attempts: Number(row.attempts ?? 0),
      state: String(row.state),
    }
  })
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
