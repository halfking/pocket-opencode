import { captureEmailOwnership } from './email-ownership'
export { pickEmailDetailBody } from './email-body-pick'

const PREFIX = 'pocket:email_body:v2:'
const MAX_ENTRIES = 60
const MAX_TOTAL_CHARS = 1_000_000

/** Legacy keys have no proven owner and are deliberately neither read nor claimed. */
export function captureEmailBodyContext() {
  const { db, owner } = captureEmailOwnership()
  return {
    key: owner ? JSON.stringify([owner.userId, owner.workspaceId]) : '',
    assertCurrent: () => db.assertCurrent(),
  }
}
export type BodyContext = ReturnType<typeof captureEmailBodyContext>

function storage(): Storage | null {
  try { return typeof localStorage === 'undefined' ? null : localStorage } catch { return null }
}
function keys(context: BodyContext, id: string) {
  const prefix = PREFIX + encodeURIComponent(context.key) + ':'
  return { body: prefix + encodeURIComponent(id), index: prefix + 'index', prefix }
}
function readIndex(store: Storage, key: string): string[] {
  try {
    const rows = JSON.parse(store.getItem(key) ?? '[]')
    return Array.isArray(rows) ? rows.filter(row => typeof row === 'string') : []
  } catch { return [] }
}

export async function readEmailBodyLocal(id: string, context = captureEmailBodyContext()): Promise<string> {
  context.assertCurrent()
  if (!id || !context.key) return ''
  const store = storage()
  if (!store) return ''
  try { return store.getItem(keys(context, id).body) ?? '' } catch { return '' }
}

export async function writeEmailBodyLocal(id: string, body: string, context = captureEmailBodyContext()): Promise<void> {
  context.assertCurrent()
  if (!id || !body || !context.key) return
  const store = storage()
  if (!store) return
  const key = keys(context, id)
  try {
    const ids = readIndex(store, key.index).filter(row => row !== id)
    ids.push(id)
    store.setItem(key.body, body)
    let total = ids.reduce((sum, row) => sum + (store.getItem(keys(context, row).body) ?? '').length, 0)
    while (ids.length > MAX_ENTRIES || total > MAX_TOTAL_CHARS) {
      const victim = ids.shift()!
      const victimKey = keys(context, victim).body
      total -= (store.getItem(victimKey) ?? '').length
      store.removeItem(victimKey)
    }
    store.setItem(key.index, JSON.stringify(ids))
  } catch (error) {
    // A cache failure may not fill global session storage without a working index.
    store.removeItem(key.body)
    console.warn('[email-body-cache] 正文未能缓存：', error)
  }
}

export async function clearEmailBodyLocal(id: string, context = captureEmailBodyContext()): Promise<void> {
  context.assertCurrent()
  if (!id || !context.key) return
  const store = storage()
  if (!store) return
  const key = keys(context, id)
  try {
    store.removeItem(key.body)
    store.setItem(key.index, JSON.stringify(readIndex(store, key.index).filter(row => row !== id)))
  } catch (error) { console.warn('[email-body-cache] 正文缓存未能清除：', error) }
}
