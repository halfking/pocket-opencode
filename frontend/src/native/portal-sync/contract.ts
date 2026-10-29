import { validEpoch } from './epochs.ts'
/** The portable notes projection deliberately contains no local media or credentials. */
export interface NotePayload extends Record<string, unknown> {
  schema_version: 1
  format: 'text' | 'html'
  content: string
  tags: string[]
  links: string[]
  domain: string
  summary: string
  content_type: string
  status: 'saved' | 'draft'
  daily_date: string
  created_at: number
}
export interface PortalItem {
  module: 'notes'
  kind: 'note'
  ext_id: string
  title: string
  payload: NotePayload | Record<string, never>
  client_updated_at: string
  op_id: string
  deleted: boolean
  epoch: number | null
}
export interface PortalChange extends PortalItem { change_seq: number }
export interface PortalAck {
  module: string
  ext_id: string
  op_id: string
  outcome: 'applied' | 'kept_existing' | 'duplicate' | 'rejected' | 'skipped_local'
  clock_skew?: boolean
  reason?: string
  current_epoch?: number
}
export interface ChangePage { changes: PortalChange[]; next_cursor: string; epochs: { notes: number } }
export interface Statement { statement: string; values: unknown[] }
export interface SyncDb {
  readonly schemaConnection?: object
  readonly owner: { readonly userId: string; readonly workspaceId: string } | null
  assertCurrent(): void
  run(sql: string, values?: unknown[]): Promise<unknown>
  query<T>(sql: string, values?: unknown[]): Promise<T[]>
  queryOne<T>(sql: string, values?: unknown[]): Promise<T | null>
  runInTransaction(statements: Statement[]): Promise<unknown>
}

export function newOperationId(): string {
  if (typeof crypto.randomUUID === 'function') return crypto.randomUUID()
  return Array.from(crypto.getRandomValues(new Uint8Array(16)), value => value.toString(16).padStart(2, '0')).join('')
}

function strings(value: unknown): string[] {
  if (value === undefined || value === null) return []
  if (!Array.isArray(value) || value.some(item => typeof item !== 'string')) throw new Error('Invalid notes string array')
  return [...value]
}
function string(value: unknown): string { return typeof value === 'string' ? value : '' }

export function notePayload(input: Record<string, unknown>): NotePayload {
  if (typeof input.content !== 'string' || !['text', 'html'].includes(String(input.format))) throw new Error('Invalid notes projection')
  return {
    schema_version: 1, format: input.format as 'text' | 'html', content: input.content,
    tags: strings(input.tags), links: strings(input.links), domain: string(input.domain),
    summary: string(input.summary), content_type: string(input.content_type),
    status: input.status === 'draft' ? 'draft' : 'saved', daily_date: string(input.daily_date),
    created_at: Number.isSafeInteger(input.created_at) && Number(input.created_at) >= 0 ? Number(input.created_at) : 0,
  }
}

export function remoteNotePayload(input: Record<string, unknown>): NotePayload {
  if (input.schema_version === undefined && Object.keys(input).every(key => ['content', 'tags'].includes(key))) {
    return notePayload({ ...input, format: 'text' })
  }
  if (input.schema_version !== 1) throw new Error('Unsupported notes projection version')
  const allowed = ['schema_version','format','content','tags','links','domain','summary','content_type','status','daily_date','created_at']
  if (Object.keys(input).some(key => !allowed.includes(key))) throw new Error('Unknown notes projection field; change cursor retained')
  return notePayload(input)
}

export function validateItem(item: PortalItem, allowLegacyOperation = false): void {
  const bytes = (value: string) => new TextEncoder().encode(value).length
  if (item.module !== 'notes' || item.kind !== 'note' || !item.ext_id || bytes(item.ext_id) > 512 || bytes(item.title) > 4096) throw new Error('Invalid portal notes identity')
  if (typeof item.op_id !== 'string' || (!allowLegacyOperation && !item.op_id) || bytes(item.op_id) > 200) throw new Error('Invalid portal notes version')
  versionKey(item.client_updated_at)
  if (!validEpoch(item.epoch)) throw new Error('Notes sync initialization is required; original operation retained')
  if (bytes(JSON.stringify(item.payload)) > 256 * 1024) throw new Error('Notes projection exceeds 256 KiB; the full content remains on this device')
  if (!item.deleted) {
    if (allowLegacyOperation) remoteNotePayload(item.payload)
    else if (item.payload.schema_version !== 1) throw new Error('Unsupported notes projection version')
    else notePayload(item.payload)
  }
}

export function compareVersions(a: Pick<PortalItem, 'client_updated_at' | 'op_id'>, b: Pick<PortalItem, 'client_updated_at' | 'op_id'>): number {
  const aKey = versionKey(a.client_updated_at), bKey = versionKey(b.client_updated_at)
  if (aKey !== bKey) return aKey > bKey ? 1 : -1
  const aOp = new TextEncoder().encode(a.op_id), bOp = new TextEncoder().encode(b.op_id)
  for (let index = 0; index < Math.min(aOp.length, bOp.length); index++) {
    if (aOp[index] !== bOp[index]) return aOp[index]! > bOp[index]! ? 1 : -1
  }
  return aOp.length === bOp.length ? 0 : aOp.length > bOp.length ? 1 : -1
}

/** Fixed-width sortable nanoseconds retain RFC3339 precision beyond JS Date. */
export function versionKey(value: string): string {
  const match = /^(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2})(?:\.(\d{1,9}))?(Z|[+-]\d{2}:\d{2})$/.exec(value)
  const ms = Date.parse(value)
  if (!match || !Number.isFinite(ms)) throw new Error('Invalid portal notes timestamp')
  const submillisecond = (match[2] ?? '').padEnd(9, '0').slice(3)
  return (BigInt(ms) * 1000000n + BigInt(submillisecond) + 10n ** 24n).toString().padStart(26, '0')
}

export function parsePage(raw: unknown): ChangePage {
  const page = raw as { changes?: unknown; next_cursor?: unknown; epochs?: { notes?: unknown } }
  if (!page || !Array.isArray(page.changes) || typeof page.next_cursor !== 'string' || page.next_cursor.length > 1024) throw new Error('Invalid portal change page')
  if (!validEpoch(page.epochs?.notes)) throw new Error('Portal notes reset epoch is missing or invalid')
  let sequence = 0
  const identities = new Set<string>()
  const changes = page.changes.map((value: Record<string, unknown>) => {
    const item = { ...value, op_id: value.last_op_id, epoch: page.epochs!.notes } as unknown as PortalChange
    if (!Number.isSafeInteger(item.change_seq) || item.change_seq <= sequence || typeof item.deleted !== 'boolean') throw new Error('Invalid portal change order')
    sequence = item.change_seq
    if (identities.has(item.ext_id)) throw new Error('Duplicate portal notes identity in change page')
    identities.add(item.ext_id)
    validateItem(item, true)
    if (!item.deleted) item.payload = remoteNotePayload(item.payload)
    return item
  })
  if (changes.length && !page.next_cursor) throw new Error('Missing portal change cursor')
  return { changes, next_cursor: page.next_cursor, epochs: { notes: page.epochs.notes } }
}
