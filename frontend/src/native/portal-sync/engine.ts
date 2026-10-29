import { parsePage, validateItem, type PortalAck, type PortalItem } from './contract.ts'
import { applyPage } from './apply.ts'
import { PortalNotesStore } from './store.ts'

export interface PortalTransport {
  changes(cursor: string): Promise<unknown>
  push(items: PortalItem[]): Promise<{ results: PortalAck[] }>
}

/** One bounded cycle; failures retain immutable operations and the last committed cursor. */
export async function syncNotesOnce(store: PortalNotesStore, transport: PortalTransport, assertCurrent: () => void,
  encodeBody?: Parameters<typeof applyPage>[2], onRemoteApplied?: (count: number) => void): Promise<number> {
  const pull = async () => {
    for (let pageNumber = 0; pageNumber < 10; pageNumber++) {
      assertCurrent()
      const cursor = await store.cursor()
      const page = parsePage(await transport.changes(cursor))
      assertCurrent()
      if (await store.observeEpoch(page.epochs.notes)) continue // Reset cursor before reading this generation.
      if (page.changes.length && page.next_cursor === cursor) throw new Error('Portal change cursor did not advance')
      await applyPage(store, page, encodeBody)
      if (page.changes.length) onRemoteApplied?.(page.changes.length)
      if (!page.changes.length) break
    }
  }
  await pull()
  const pending = await store.pending()
  for (const item of pending) validateItem(item)
  let acknowledged = 0
  if (pending.length) {
    assertCurrent()
    for (const item of pending) await store.requireEpoch(item.epoch)
    const response = await transport.push(pending)
    assertCurrent()
    if (!response || !Array.isArray(response.results)) throw new Error('Invalid portal push acknowledgement')
    const acknowledgements = new Map<string, PortalAck>()
    for (const ack of response.results) {
      if (!ack || typeof ack.op_id !== 'string' || acknowledgements.has(ack.op_id)) throw new Error('Invalid per-item portal acknowledgement')
      acknowledgements.set(ack.op_id, ack)
    }
    for (const item of pending) {
      const ack = acknowledgements.get(item.op_id)
      if (!ack) continue
      assertCurrent()
      if (await store.acknowledge(item, ack)) acknowledged++
    }
    await pull()
  }
  return acknowledged
}
