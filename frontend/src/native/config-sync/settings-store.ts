/**
 * 本地 user_settings 镜像（SQLCipher）。
 */
import { localDB } from '../local-db'
import { nowUnixSec } from './planner'

export interface LocalSetting {
  namespace: string
  id: string
  payload: unknown
  secretEncrypted: string
  updatedAt: number
  dirty: number
}

function rowToSetting(row: Record<string, unknown>): LocalSetting {
  let payload: unknown = {}
  try {
    payload = JSON.parse(String(row.payload ?? '{}'))
  } catch {
    payload = {}
  }
  return {
    namespace: String(row.namespace),
    id: String(row.id),
    payload,
    secretEncrypted: String(row.secret_encrypted ?? ''),
    updatedAt: Number(row.updated_at ?? 0),
    dirty: Number(row.dirty ?? 0),
  }
}

export async function listLocalSettings(): Promise<LocalSetting[]> {
  if (!localDB.isReady()) return []
  const rows = await localDB.query<Record<string, unknown>>(
    'SELECT namespace, id, payload, secret_encrypted, updated_at, dirty FROM local_user_settings',
  )
  return rows.map(rowToSetting)
}

export async function getLocalSetting(namespace: string, id: string): Promise<LocalSetting | null> {
  if (!localDB.isReady()) return null
  const row = await localDB.queryOne<Record<string, unknown>>(
    'SELECT namespace, id, payload, secret_encrypted, updated_at, dirty FROM local_user_settings WHERE namespace = ? AND id = ?',
    [namespace, id],
  )
  return row ? rowToSetting(row) : null
}

export async function writeLocalSetting(input: {
  namespace: string
  id: string
  payload: unknown
  secretEncrypted?: string
  updatedAt?: number
  dirty?: number
}): Promise<LocalSetting> {
  const updatedAt = input.updatedAt ?? nowUnixSec()
  const dirty = input.dirty ?? 1
  const payload = JSON.stringify(input.payload ?? {})
  const secret = input.secretEncrypted ?? ''
  await localDB.run(
    `INSERT INTO local_user_settings (namespace, id, payload, secret_encrypted, updated_at, dirty)
     VALUES (?, ?, ?, ?, ?, ?)
     ON CONFLICT(namespace, id) DO UPDATE SET
       payload = excluded.payload,
       secret_encrypted = CASE WHEN excluded.secret_encrypted = '' THEN local_user_settings.secret_encrypted ELSE excluded.secret_encrypted END,
       updated_at = excluded.updated_at,
       dirty = excluded.dirty`,
    [input.namespace, input.id, payload, secret, updatedAt, dirty],
  )
  return {
    namespace: input.namespace,
    id: input.id,
    payload: input.payload ?? {},
    secretEncrypted: secret,
    updatedAt,
    dirty,
  }
}

export async function writeLocalIfNewer(input: {
  namespace: string
  id: string
  payload: unknown
  secretEncrypted?: string
  updatedAt: number
}): Promise<boolean> {
  const local = await getLocalSetting(input.namespace, input.id)
  if (local && local.updatedAt >= input.updatedAt) return false
  await writeLocalSetting({ ...input, dirty: 0 })
  return true
}

export async function markSettingClean(namespace: string, id: string): Promise<void> {
  if (!localDB.isReady()) return
  await localDB.run(
    'UPDATE local_user_settings SET dirty = 0 WHERE namespace = ? AND id = ?',
    [namespace, id],
  )
}

/** 删除本地镜像行（增量同步墓碑：其他端已删除的行不再留在本地）。 */
export async function deleteLocalSetting(namespace: string, id: string): Promise<void> {
  if (!localDB.isReady()) return
  await localDB.run(
    'DELETE FROM local_user_settings WHERE namespace = ? AND id = ?',
    [namespace, id],
  )
}
