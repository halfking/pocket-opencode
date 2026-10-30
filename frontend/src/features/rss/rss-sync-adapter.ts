import type { RecordAdapter } from '../../native/portal-sync/record-contract.ts'

function text(input: Record<string, unknown>, key: string): string {
  if (input[key] === undefined) return ''
  if (typeof input[key] !== 'string') throw new Error(`Invalid RSS ${key}`)
  return input[key]
}

function address(value: string, required: boolean): string {
  if (!value && !required) return ''
  let url: URL
  try { url = new URL(value) } catch { throw new Error('Invalid RSS URL') }
  if (!['http:', 'https:'].includes(url.protocol)) throw new Error('RSS URL must use HTTP or HTTPS')
  if (url.username || url.password) throw new Error('RSS URL credentials cannot enter the portable projection')
  return value
}

/** Portable metadata is a separate read model, never an executable feed subscription. */
export const rssSyncAdapter: RecordAdapter = {
  module: 'rss', kind: 'source', kinds: ['source', 'item', 'item-state'],
  externalId: (id, kind) => kind === 'item-state' ? id.replace(/^state:/, '') : `rss-${kind}:${id}`,
  validate(item) {
    if (item.kind === 'item-state' && (item.deleted ? Object.keys(item.payload).length !== 0 : item.payload.item_ext_id !== item.ext_id)) throw new Error('RSS state target must match its identity')
  },
  payload(input, kind = 'source') {
    if (kind === 'item-state') {
      if (Object.keys(input).some(key => !['item_ext_id','status'].includes(key)) || typeof input.item_ext_id !== 'string' || !input.item_ext_id.trim() || new TextEncoder().encode(input.item_ext_id).length > 512 || typeof input.status!=='string' || !['unread','read','starred','archived'].includes(input.status)) throw new Error('Invalid RSS item state')
      return {item_ext_id:input.item_ext_id,status:input.status}
    }
    const allowed = kind === 'source' ? ['url', 'category'] : ['source', 'summary', 'url']
    if (Object.keys(input).some(key => !allowed.includes(key))) throw new Error('Unknown RSS projection field; original retained')
    if (kind === 'source') return { url: address(text(input, 'url'), true), category: text(input, 'category') }
    if (kind !== 'item') throw new Error('Unsupported RSS projection kind')
    return { source: text(input, 'source'), summary: text(input, 'summary'), url: address(text(input, 'url'), false) }
  },
  // The encrypted record sidecar is the metadata read model. Applying a portal
  // record must not create/enable pocketd subscriptions or fetch arbitrary URLs.
  materialize() { return [] },
}
