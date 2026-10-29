import { versionKey } from './contract.ts'
import { validEpoch, type RecordModule } from './epochs.ts'
import type { Statement } from './contract.ts'

export interface RecordItem {
  module: RecordModule
  kind: string
  ext_id: string
  title: string
  payload: Record<string, unknown>
  client_updated_at: string
  op_id: string
  deleted: boolean
  epoch: number | null
}
export interface RecordChange extends RecordItem {
  change_seq: number
}
export interface RecordPage {
  changes: RecordChange[]
  next_cursor: string
  epoch: number
}
export interface RecordAdapter {
  module: RecordModule
  kind: string
  kinds?: string[]
  payload(input: Record<string, unknown>, kind?: string): Record<string, unknown>
  materialize(
    item: RecordItem,
    sourceId: string,
    guard: { clause: string; values: unknown[] },
    owner: { userId: string; workspaceId: string },
  ): Statement[]
}
export function validateRecordItem(adapter: RecordAdapter, item: RecordItem, remote = false): void {
  const bytes = (s: string) => new TextEncoder().encode(s).length
  if (
    item.module !== adapter.module ||
    ![...(adapter.kinds ?? [adapter.kind]), ...(remote ? ['item'] : [])].includes(item.kind) ||
    typeof item.ext_id !== 'string' ||
    !item.ext_id.trim() ||
    bytes(item.ext_id) > 512 ||
    typeof item.title !== 'string' ||
    bytes(item.title) > 4096
  )
    throw new Error('Invalid module sync identity')
  if (
    typeof item.op_id !== 'string' ||
    (!remote && !item.op_id) ||
    item.op_id !== item.op_id.trim() ||
    bytes(item.op_id) > 200
  )
    throw new Error('Invalid module sync operation')
  versionKey(item.client_updated_at)
  if (!validEpoch(item.epoch)) throw new Error('epoch_unknown')
  if (
    !item.payload ||
    typeof item.payload !== 'object' ||
    Array.isArray(item.payload) ||
    bytes(JSON.stringify(item.payload)) > 256 * 1024
  )
    throw new Error('Invalid or oversized module sync projection; original retained')
  if (!item.deleted) adapter.payload(item.payload, item.kind)
}
export function parseRecordPage(adapter: RecordAdapter, raw: unknown): RecordPage {
  const page = raw as { changes?: unknown; next_cursor?: unknown; epochs?: Record<string, unknown> }
  if (
    !page ||
    !Array.isArray(page.changes) ||
    typeof page.next_cursor !== 'string' ||
    page.next_cursor.length > 1024 ||
    !validEpoch(page.epochs?.[adapter.module])
  )
    throw new Error('Invalid module change page')
  const epoch = page.epochs[adapter.module] as number
  let sequence = 0
  const seen = new Set<string>()
  const changes = page.changes.map((value) => {
    const item = { ...value, op_id: value.last_op_id, epoch } as RecordChange
    const identity = JSON.stringify([item.kind, item.ext_id])
    if (
      !Number.isSafeInteger(item.change_seq) ||
      item.change_seq <= sequence ||
      typeof item.deleted !== 'boolean' ||
      seen.has(identity)
    )
      throw new Error('Invalid module change order')
    validateRecordItem(adapter, item, true)
    sequence = item.change_seq
    seen.add(identity)
    if (!item.deleted) item.payload = adapter.payload(item.payload, item.kind)
    return item
  })
  if (changes.length && !page.next_cursor) throw new Error('Missing module change cursor')
  return { changes, epoch, next_cursor: page.next_cursor }
}
