import { decryptString, encryptString } from '../../native/crypto'
import { portalNotesStore } from '../../native/portal-sync/storage'
import { newOperationId, notePayload, versionKey, type PortalItem } from '../../native/portal-sync/contract'
import { captureNoteWriteOperation } from './note-operation'
import { readNoteBody } from './note-files'
import { parseTags } from './notes-row'

export type LegacyNoteSource = 'note' | 'asset'

const sources = {
  note: {
    table: 'local_notes',
    eligible: '1=1',
    fields: `title,content AS body,encrypted_content AS encrypted,storage_tier AS tier,body_path,created_at,updated_at,
      json_object('tags',tags,'domain',domain,'summary',summary,'content_type',content_type,'status',status) AS metadata`,
  },
  asset: {
    table: 'local_assets',
    eligible: "kind='note' AND sync_mode='e2ee_local_first'",
    fields: `title,body_text AS body,body_encrypted AS encrypted,source AS tier,NULL AS body_path,created_at,updated_at,
      meta_json AS metadata`,
  },
} as const

interface Candidate {
  title: string | null
  body: string | null
  encrypted: number
  tier: string | null
  body_path: string | null
  created_at: number
  updated_at: number
  metadata: string | null
  revision: string
}

function legacyCandidate(source: LegacyNoteSource) {
  const spec = sources[source]
  return `SELECT c.*,json_array(title,body,encrypted,tier,body_path,created_at,updated_at,metadata) AS revision
    FROM (SELECT ${spec.fields} FROM ${spec.table}
      WHERE id=? AND workspace_id=? AND deleted_at IS NULL AND ${spec.eligible}) c
    WHERE NOT EXISTS (SELECT 1 FROM local_portal_notes WHERE source=? AND source_id=?)`
}

export async function canClaimLegacyNote(source: LegacyNoteSource, id: string): Promise<boolean> {
  const { db } = captureNoteWriteOperation({ backgroundCloud: false })
  if (!db.owner) return false
  await portalNotesStore(db).init()
  const candidate = await db.queryOne(`SELECT 1 FROM (${legacyCandidate(source)})`, [id, db.owner.workspaceId, source, id])
  db.assertCurrent()
  return candidate !== null
}

function objectMetadata(raw: string | null): Record<string, unknown> {
  const value: unknown = JSON.parse(raw ?? '{}')
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error('Invalid original note metadata')
  return value as Record<string, unknown>
}

/** Capture the original row and owner before the caller opens its confirmation. */
export async function prepareLegacyNoteClaim(source: LegacyNoteSource, id: string) {
  const { db } = captureNoteWriteOperation({ backgroundCloud: false })
  const owner = db.owner
  if (!owner) throw new Error('Verified note owner required')
  const store = portalNotesStore(db)
  await store.init()
  const where = [id, owner.workspaceId, source, id]
  const select = legacyCandidate(source)
  const row = await db.queryOne<Candidate>(select, where)
  if (!row) throw new Error('Historical note is unavailable or already owned')
  const { revision, updated_at: modifiedAt } = row
  const timestamp = new Date(modifiedAt).toISOString()
  const meta = objectMetadata(row.metadata)
  let body = row.body ?? ''
  if (source === 'note' && (row.tier === 'file' || row.body_path)) {
    const fileBody = await readNoteBody(row.body_path, db)
    if (fileBody === null) throw new Error('Complete historical note body is unavailable')
    body = fileBody
  } else if (row.encrypted === 1 && body) {
    body = await decryptString(body)
  }
  db.assertCurrent()
  const format = source === 'note' ? 'text' : row.tier === 'enex_import' ? 'markdown' : 'html'
  const item: PortalItem = {
    module: 'notes', kind: 'note', ext_id: `${source}:legacy:${newOperationId()}`,
    title: row.title ?? '', client_updated_at: timestamp, op_id: newOperationId(), deleted: false, epoch: null,
    payload: notePayload({
      format, content: body, tags: source === 'note' ? parseTags(meta.tags as string | null) : meta.tags,
      links: meta.links, domain: meta.domain, summary: meta.summary, content_type: meta.content_type,
      status: meta.status, daily_date: meta.dailyDate, import_id: meta.enexId, created_at: row.created_at,
    }),
  }
  const json = row.encrypted === 1
    ? `e1:${await encryptString(JSON.stringify(item))}`
    : await store.encode(item)
  db.assertCurrent()
  return {
    workspaceId: owner.workspaceId,
    async claim() {
      db.assertCurrent()
      // The projection is an ownership link. An ordinary later edit may publish it through the normal outbox.
      await db.runInTransaction([
        { statement: `INSERT INTO local_portal_note_write_guards(allowed)
            SELECT 0 WHERE NOT EXISTS (SELECT 1 FROM (${select}) WHERE revision=?)`,
          values: [...where, revision] },
        { statement: `INSERT INTO local_portal_notes
            (owner_user_id,owner_workspace_id,ext_id,source,source_id,item_json,version_ms,version_key,op_id,reset_epoch)
            VALUES (?,?,?,?,?,?,?,?,?,NULL)`,
          values: [...store.scope, item.ext_id, source, id, json, modifiedAt, versionKey(timestamp), item.op_id] },
      ])
    },
  }
}
