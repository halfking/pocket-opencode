import { newOperationId, versionKey, type PortalAck, type Statement } from './contract.ts'
import { recordEpochGuard } from './record-epoch.ts'
import { parseRecordPage, validateRecordItem, type RecordItem, type RecordPage } from './record-contract.ts'
import { PortalRecordStore } from './record-store.ts'

export async function applyRecordPage(store: PortalRecordStore, page: RecordPage): Promise<void> {
  if (!store.db.owner) throw new Error('Verified module owner required')
  await store.init()
  await store.observeEpoch(page.epoch)
  const admitted = recordEpochGuard(store.db, store.adapter.module, page.epoch),
    statements: Statement[] = []
  for (const item of page.changes) {
    validateRecordItem(store.adapter, item, true)
    const existing = await store.byExternalId(item.ext_id, item.kind),
      id = existing?.source_id ?? `portal-${newOperationId()}`,
      key = versionKey(item.client_updated_at)
    statements.push({
      statement: `INSERT INTO local_portal_records(owner_user_id,owner_workspace_id,module,kind,ext_id,source_id,item_json,version_key,op_id,reset_epoch)
      SELECT ?,?,?,?,?,?,?,?,?,? WHERE ${admitted.clause} ON CONFLICT(owner_user_id,owner_workspace_id,module,kind,ext_id)
      DO UPDATE SET item_json=excluded.item_json,version_key=excluded.version_key,op_id=excluded.op_id,reset_epoch=excluded.reset_epoch
      WHERE excluded.reset_epoch>COALESCE(local_portal_records.reset_epoch,0) OR (excluded.reset_epoch=COALESCE(local_portal_records.reset_epoch,0)
        AND (excluded.version_key>local_portal_records.version_key OR (excluded.version_key=local_portal_records.version_key AND excluded.op_id>local_portal_records.op_id)))`,
      values: [
        ...store.scope,
        item.module,
        item.kind,
        item.ext_id,
        id,
        await store.encode(item),
        key,
        item.op_id,
        page.epoch,
        ...admitted.values,
      ],
    })
    const guard = {
      clause: `EXISTS(SELECT 1 FROM local_portal_records WHERE owner_user_id=? AND owner_workspace_id=? AND module=? AND kind=? AND ext_id=? AND op_id=? AND version_key=? AND reset_epoch=?) AND ${admitted.clause}
      AND NOT EXISTS(SELECT 1 FROM local_portal_record_outbox WHERE owner_user_id=? AND owner_workspace_id=? AND module=? AND kind=? AND ext_id=? AND (reset_epoch IS NULL OR reset_epoch<>?))`,
      values: [
        ...store.scope,
        item.module,
        item.kind,
        item.ext_id,
        item.op_id,
        key,
        page.epoch,
        ...admitted.values,
        ...store.scope,
        item.module,
        item.kind,
        item.ext_id,
        page.epoch,
      ],
    }
    statements.push(...store.adapter.materialize(item, id, guard, store.db.owner))
  }
  statements.push(store.cursorStatement(page.next_cursor, page.epoch))
  await store.db.runInTransaction(statements)
  await store.requireEpoch(page.epoch)
}
export async function syncRecordsOnce(
  store: PortalRecordStore,
  transport: {
    changes(cursor: string): Promise<unknown>
    push(items: RecordItem[]): Promise<{ results: PortalAck[] }>
  },
  assertCurrent: () => void,
  onApplied?: (count: number) => void,
): Promise<number> {
  const pull = async () => {
    for (let n = 0; n < 10; n++) {
      assertCurrent()
      const cursor = await store.cursor(),
        page = parseRecordPage(store.adapter, await transport.changes(cursor))
      assertCurrent()
      if (await store.observeEpoch(page.epoch)) continue
      if (page.changes.length && page.next_cursor === cursor) throw new Error('Module change cursor did not advance')
      await applyRecordPage(store, page)
      if (page.changes.length) onApplied?.(page.changes.length)
      if (!page.changes.length) break
    }
  }
  await pull()
  const pending = await store.pending()
  for (const item of pending) validateRecordItem(store.adapter, item)
  let count = 0
  if (pending.length) {
    assertCurrent()
    for (const item of pending) await store.requireEpoch(item.epoch)
    const response = await transport.push(pending)
    assertCurrent()
    if (!response || !Array.isArray(response.results)) throw new Error('Invalid module push acknowledgement')
    const byId = new Map<string, PortalAck>()
    for (const ack of response.results) {
      if (!ack || typeof ack.op_id !== 'string' || byId.has(ack.op_id))
        throw new Error('Invalid module acknowledgement')
      byId.set(ack.op_id, ack)
    }
    for (const item of pending) {
      const ack = byId.get(item.op_id)
      if (ack) {
        assertCurrent()
        if (await store.acknowledge(item, ack)) count++
      }
    }
    await pull()
  }
  return count
}
