import { http, type ApiError } from '../../api/http'
import { decryptString, encryptString } from '../../native/crypto'
import { newOperationId, type SyncDb } from '../../native/portal-sync/contract.ts'
import { useCryptoConfig } from '../../stores/crypto-config'
import { captureNativeRssOperation } from './rss-native-ingest'
import { captureRssPortalOperation } from './rss-sync-store'

interface SourceFields {
  url: string
  title?: string
  enabled: boolean
  fetchInterval: string
}
interface SourceIntent extends SourceFields {
  operationId: string
  clientUpdatedAt: string
  portalEpoch: number | null
}
type Rejection = '' | 'invalid' | 'conflict'
interface QueueRow { op_id: string; request_json: string; rejection: Rejection }
interface Receipt { operation_id: string; source_id: string; user_id: string; workspace_id: string }

export const rssSourceQueueChanged = 'openpocket:rss-source-outbox-changed'
export const rssSourceCreated = 'openpocket:rss-source-created'
const table = `CREATE TABLE IF NOT EXISTS local_rss_source_outbox (
  owner_user_id TEXT NOT NULL, owner_workspace_id TEXT NOT NULL,
  op_id TEXT NOT NULL, request_json TEXT NOT NULL, rejection TEXT NOT NULL DEFAULT '',
  PRIMARY KEY(owner_user_id,owner_workspace_id,op_id))`

function notify(event: string) {
  if (typeof window !== 'undefined') window.dispatchEvent(new Event(event))
}

async function readIntent(row: QueueRow): Promise<SourceIntent> {
  const data = row.request_json.startsWith('e1:') ? await decryptString(row.request_json.slice(3)) : row.request_json
  const intent = JSON.parse(data) as SourceIntent
  if (intent?.operationId !== row.op_id || typeof intent.url !== 'string') throw new Error('Invalid saved RSS source intent')
  return intent
}

async function queueScope(db: SyncDb): Promise<[string, string]> {
  if (!db.owner) throw new Error('Verified RSS owner required')
  await db.run(table)
  db.assertCurrent()
  return [db.owner.userId, db.owner.workspaceId]
}

/** Save before any network attempt, keeping the original time and reset epoch. */
export async function queueRssSource(input: SourceFields, operation = captureRssPortalOperation()): Promise<void> {
  const url = input.url.trim()
  const address = new URL(url)
  if (!['http:', 'https:'].includes(address.protocol) || !address.hostname || address.username || address.password ||
      new TextEncoder().encode(url).length > 4096) throw new Error('Invalid RSS source URL')
  const intent: SourceIntent = {
    url, title: input.title ?? '', enabled: input.enabled, fetchInterval: input.fetchInterval,
    operationId: newOperationId(), clientUpdatedAt: new Date(operation.modifiedAt).toISOString(), portalEpoch: operation.epoch,
  }
  operation.assertCurrent()
  const { db } = operation
  const scope = await queueScope(db)
  const data = JSON.stringify(intent)
  const json = useCryptoConfig().shouldEncryptField() ? `e1:${await encryptString(data)}` : data
  operation.assertCurrent()
  await db.runInTransaction([{ statement: `INSERT INTO local_rss_source_outbox
    (owner_user_id,owner_workspace_id,op_id,request_json) VALUES(?,?,?,?)`, values: [...scope, intent.operationId, json] }])
  operation.assertCurrent()
  notify(rssSourceQueueChanged)
  notify('openpocket:rss-native-changed')
}

export async function listQueuedRssSources(operation = captureRssPortalOperation()) {
  operation.assertCurrent()
  const scope = await queueScope(operation.db)
  const rows = await operation.db.query<QueueRow>(`SELECT op_id,request_json,rejection FROM local_rss_source_outbox
    WHERE owner_user_id=? AND owner_workspace_id=? ORDER BY rowid`, scope)
  const result = await Promise.all(rows.map(async row => {
    const intent = await readIntent(row).catch(() => null)
    return { id: row.op_id, label: intent?.title || intent?.url || '', rejection: row.rejection }
  }))
  operation.assertCurrent()
  return result
}

export async function removeRejectedRssSource(id: string, operation = captureRssPortalOperation()) {
  operation.assertCurrent()
  const scope = await queueScope(operation.db)
  await operation.db.run(`DELETE FROM local_rss_source_outbox
    WHERE owner_user_id=? AND owner_workspace_id=? AND op_id=? AND rejection<>''`, [...scope, id])
  operation.assertCurrent()
  notify(rssSourceQueueChanged)
}

function knownRejection(error: unknown): Rejection {
  const failure = error as Partial<ApiError> | null
  if (failure?.status === 409 && failure.body?.error === 'rss_source_conflict') return 'conflict'
  if (failure?.status === 422 && failure.body?.error === 'rss_invalid_source_intent') return 'invalid'
  return ''
}

/** Only the initiating principal and an active cloud-policy lease may send. */
export async function flushQueuedRssSources(operation = captureNativeRssOperation()): Promise<void> {
  operation.assertCurrent()
  const { db } = operation
  const scope = await queueScope(db)
  const rows = await db.query<QueueRow>(`SELECT op_id,request_json,rejection FROM local_rss_source_outbox
    WHERE owner_user_id=? AND owner_workspace_id=? AND rejection='' ORDER BY rowid LIMIT 20`, scope)
  for (const row of rows) {
    const intent = await readIntent(row)
    operation.assertCurrent()
    try {
      const response = await http<Receipt>('/api/rss/sources/queued', {
        method: 'POST', syncModule: 'rss', body: JSON.stringify(intent), assertCurrent: operation.assertCurrent,
      })
      operation.assertCurrent()
      if (response?.operation_id !== row.op_id || response.user_id !== scope[0] || response.workspace_id !== scope[1] ||
        typeof response.source_id !== 'string' || !response.source_id) throw new Error('Invalid RSS source receipt')
    } catch (error) {
      operation.assertCurrent()
      const rejection = knownRejection(error)
      if (!rejection) throw error
      await db.run(`UPDATE local_rss_source_outbox SET rejection=? WHERE owner_user_id=? AND owner_workspace_id=? AND op_id=?`,
        [rejection, ...scope, row.op_id])
      notify(rssSourceQueueChanged)
      continue
    }
    await db.run(`DELETE FROM local_rss_source_outbox WHERE owner_user_id=? AND owner_workspace_id=? AND op_id=?`,
      [...scope, row.op_id])
    notify(rssSourceQueueChanged)
    notify(rssSourceCreated)
  }
}
