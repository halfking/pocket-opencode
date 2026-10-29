import { captureEmailOwnership } from './email-ownership'
import { portalRecordStore } from '../../native/portal-sync/record-storage.ts'
import { captureModuleEpoch } from '../../native/portal-sync/epochs.ts'
import { versionKey, type Statement, type SyncDb } from '../../native/portal-sync/contract.ts'
import { portalMailAdapter, type PortalMailKind } from './portal-mail-adapter.ts'
import type { RecordSnapshot } from '../../native/portal-sync/record-store.ts'

export const mailRecordId = (kind: PortalMailKind, id: string) => `${kind}:${id}`
export const portalMailStore = (db: SyncDb) => portalRecordStore(db, portalMailAdapter)

export function mailAccountPayload(row: { email_address: string; display_name: string; last_synced_at?: number | null }) {
  const stamp = Number(row.last_synced_at)
  return { address: row.email_address, display_name: row.display_name,
    last_sync_at: Number.isFinite(stamp) && stamp > 0 ? new Date(stamp).toISOString() : '' }
}
export function mailSummaryPayload(row: { from_address: string; subject?: string | null; snippet?: string | null; ai_summary?: string | null; date: number }) {
  const stamp = Number(row.date)
  return { from: row.from_address, subject: row.subject ?? '', snippet: row.ai_summary ?? row.snippet ?? '',
    received_at: Number.isFinite(stamp) && stamp > 0 ? new Date(stamp).toISOString() : '' }
}

/** Capture the original owner and known reset generation before any business await. */
export function capturePortalMailWrite(database?: object) {
  const ownership = captureEmailOwnership(database)
  return { ...ownership, epoch: captureModuleEpoch(ownership.owner, 'email'), modifiedAt: Date.now() }
}
export type PortalMailWrite = ReturnType<typeof capturePortalMailWrite>

/** Older source snapshots cannot overwrite a newer portal projection. */
export function portalMailVersionGuard(operation: PortalMailWrite, kind: PortalMailKind, id: string, modifiedAt: number) {
  return {
    clause: `NOT EXISTS(SELECT 1 FROM local_portal_records WHERE owner_user_id=? AND owner_workspace_id=? AND module='email' AND ext_id=? AND version_key>?)`,
    values: [...operation.ownerValues, mailRecordId(kind, id), versionKey(new Date(modifiedAt).toISOString())],
  }
}

export interface MailCommit {
  kind: PortalMailKind; id: string; title: string; payload: Record<string, unknown>; modifiedAt?: number; created?: boolean; deleted?: boolean
  guard?: { clause: string; values: unknown[] }; syncError?: string
}

export async function commitPortalMailBatch(operation: PortalMailWrite, statements: Statement[], inputs: MailCommit[]): Promise<void> {
  const store = portalMailStore(operation.db)
  const entries: { snapshot: RecordSnapshot; created: boolean }[] = []
  for (const input of inputs) {
    const extId = mailRecordId(input.kind, input.id)
    const existing = await store.byExternalId(extId, input.kind)
    entries.push({ snapshot: { id: existing?.source_id ?? extId, extId, kind: input.kind,
      title: input.title, payload: input.payload, modifiedAt: input.modifiedAt ?? operation.modifiedAt,
      epoch: operation.epoch, deleted: input.deleted, guard: input.guard, syncError: input.syncError }, created: input.created === true })
  }
  await store.commitMany(statements, entries)
}

export async function commitPortalMail(operation: PortalMailWrite, statements: Statement[], input: MailCommit): Promise<void> {
  return commitPortalMailBatch(operation, statements, [input])
}

export async function listPortalMailMetadata(database?: object) {
  const { db, ownerValues } = captureEmailOwnership(database)
  const store = portalMailStore(db)
  await store.init()
  const rows = await db.query<{ source_id: string; item_json: string }>(`SELECT source_id,item_json FROM local_portal_records
    WHERE owner_user_id=? AND owner_workspace_id=? AND module='email' ORDER BY version_key DESC`, ownerValues)
  const items = await Promise.all(rows.map(async row => ({ id: row.source_id, item: await store.decode(row.item_json) })))
  db.assertCurrent()
  return items.filter(row => !row.item.deleted)
}

export async function editPortalMailMetadata(id: string, patch: { title: string; payload: Record<string, unknown> }, database?: object): Promise<void> {
  const operation = capturePortalMailWrite(database), store = portalMailStore(operation.db)
  const row = await store.find(id)
  if (!row) throw new Error('Email metadata no longer exists')
  const original = await store.decode(row.item_json)
  const payload = portalMailAdapter.payload(patch.payload, original.kind)
  await store.commit([], { id, extId: original.ext_id, kind: original.kind, title: patch.title, payload,
    modifiedAt: operation.modifiedAt, epoch: operation.epoch }, false)
}

export async function deletePortalMailMetadata(id: string, database?: object): Promise<void> {
  const operation = capturePortalMailWrite(database), store = portalMailStore(operation.db)
  const row = await store.find(id)
  if (!row) return
  const original = await store.decode(row.item_json)
  await store.commit([], { id, extId: original.ext_id, kind: original.kind, title: original.title, payload: {}, deleted: true,
    modifiedAt: operation.modifiedAt, epoch: operation.epoch }, false)
}
