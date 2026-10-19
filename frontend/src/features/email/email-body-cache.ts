import { listSnapshots } from '../../native/list-sync/snapshot-store'
import { pickEmailDetailBody } from './email-body-pick'

export { pickEmailDetailBody }

const NS = 'email_bodies'

/**
 * 逐封独立 key（2026-09-30 真机审计 P1）。
 *
 * 原实现把**整个邮箱的 HTML 正文**塞进 snapshot-store 的单个 localStorage
 * key（`pocket:snapshots:email_bodies`）。由此产生两个必然故障：
 *   1. 读任意一封正文都要 JSON.parse 全量；写任意一封都要整包重新
 *      stringify + setItem —— 正文是几十到几百 KB 的大对象，详情页
 *      每开一封就是一次全量序列化。
 *   2. localStorage 在 Android WebView 里总配额约 5–10MB，一旦写超，
 *      snapshot-store 的 saveNS 会 **静默吞掉**（catch 里注释写着
 *      「快照属尽力而为」）。结果是正文一条都没存下来，缓存永久失效，
 *      每次进详情页都重新走网络；网络一慢正文区就一直空着。
 *
 * 改为按 id 分 key 后：读只解析一封，写只覆盖一封，某一封写失败不会
 * 牵连其它邮件。
 * ⚠⚠⚠§873/D43：上面那句「**也不再有「整体超配额」的悬崖**」**是错的**
 *   （2026-10-18 取证推翻）。分 key 只消除了「**单次**写入超配额」这一个
 *   失败模式，同时**制造**了「逐封无限累积」——而删除路径只有
 *   `email-soft-delete.ts`（用户删邮件）一处。
 *   读数：`EmailInboxView::open()` 每次点开一封会缓存**三封**正文
 *   （`prefetchEmailBody(id)` + `prefetchEmailBodySeries(ahead, …, 2)`）。
 *   按本文件自己写的「正文几十到几百 KB」算，一次点击约 300KB；
 *   5–10MB 的配额 ⇒ **点过十几到三十几封邮件，localStorage 就满了**。
 *   ⇒ 之后本仓**所有**写入静默失败：登录会话（§867/D33）、AI 对话历史
 *   （§866/D30）、闪卡队列（§868/D36）、加密 salt（§867/D35）……全中。
 * 修法：见下方 INDEX_KEY —— 加一个**有序索引 + 字节/条数双上限 + 淘汰**。
 */
const LEGACY_NS_KEY = 'pocket:snapshots:email_bodies'
const PREFIX = 'pocket:email_body:'

/**
 * ⚠§873/D43 淘汰策略。两个上限都留着，因为**单独任何一个都不够**：
 *   · 只限条数：一封 500KB 的正文 × 60 封 = 30MB，照样撑爆；
 *   · 只限字节：几封超长正文就能把额度吃掉，用户后面连会话都存不下。
 * 字节用 `body.length`（UTF-16 码点数）近似，真身约 ×2 —— 留足余量。
 */
const INDEX_KEY = 'pocket:email_body_index'
const MAX_ENTRIES = 60
const MAX_TOTAL_CHARS = 1_000_000

function readIndex(store: Storage): string[] {
  try {
    const raw = store.getItem(INDEX_KEY)
    if (!raw) return []
    const parsed = JSON.parse(raw)
    return Array.isArray(parsed) ? parsed.filter((x) => typeof x === 'string') : []
  } catch {
    // 索引本身坏了：宁可当成空（会重新建立），也不要因为它读不出就放弃缓存
    return []
  }
}

function writeIndex(store: Storage, ids: string[]): boolean {
  try {
    store.setItem(INDEX_KEY, JSON.stringify(ids))
    return true
  } catch (err) {
    console.warn('[email-body-cache] 缓存索引未能写入（此后不再淘汰，正文缓存会无上限增长）：', err)
    return false
  }
}

/** 从最旧开始淘汰，直到回到两个上限之内。返回被淘汰的封数。 */
function evictOverBudget(store: Storage, ids: string[]): number {
  let total = 0
  const sizes = new Map<string, number>()
  for (const id of ids) {
    const n = (store.getItem(PREFIX + id) ?? '').length
    sizes.set(id, n)
    total += n
  }
  let evicted = 0
  while (ids.length > MAX_ENTRIES || total > MAX_TOTAL_CHARS) {
    const victim = ids.shift()
    if (victim === undefined) break
    total -= sizes.get(victim) ?? 0
    try {
      store.removeItem(PREFIX + victim)
      evicted++
    } catch { /* 删不掉就只是额度白占，索引仍然前进 */ }
  }
  return evicted
}

function storage(): Storage | null {
  try {
    if (typeof localStorage === 'undefined') return null
    return localStorage
  } catch {
    return null
  }
}

function keyFor(id: string): string {
  return PREFIX + id
}

export async function readEmailBodyLocal(id: string): Promise<string> {
  if (!id) return ''
  const store = storage()
  if (!store) return ''
  try {
    const perItem = store.getItem(keyFor(id))
    if (perItem !== null) return perItem
  } catch { /* 落到旧 key */ }
  // 兼容迁移：旧版全量 key 命中即顺手拆成逐封 key 并清理。
  try {
    const legacy = await listSnapshots<{ body: string }>(NS)
    const hit = legacy.find((row) => row.id === id)
    if (!hit?.payload?.body) return ''
    writeEmailBodyLocal(id, hit.payload.body)
    return hit.payload.body
  } catch {
    return ''
  }
}

export async function writeEmailBodyLocal(id: string, body: string): Promise<void> {
  if (!id || !body) return
  const store = storage()
  if (!store) return
  try {
    store.setItem(keyFor(id), body)
  } catch {
    // 单封仍超配额：清掉旧的全量 key 再试一次，释放空间。
    // 只在确实写失败时执行，不影响正常路径的性能。
    try {
      store.removeItem(LEGACY_NS_KEY)
      store.setItem(keyFor(id), body)
    } catch { /* 仍失败：降级为不缓存，详情页回落到远端正文 */ }
    return
  }
  // ⚠⚠⚠§873/D43：写成功**不等于**完事 —— 没有上限的话，这个缓存会一直
  // 涨到把配额吃光，然后连带打挂本仓**所有**写入（会话 / 对话历史 /
  // 闪卡队列 / 加密 salt）。所以每次写入后立刻记账 + 淘汰。
  const ids = readIndex(store).filter((x) => x !== id)
  ids.push(id)
  if (writeIndex(store, ids)) {
    const evicted = evictOverBudget(store, ids)
    if (evicted > 0) {
      console.warn(`[email-body-cache] 正文缓存已满，淘汰最旧的 ${evicted} 封（上限 ${MAX_ENTRIES} 封 / 约 ${MAX_TOTAL_CHARS} 字符）`)
    }
  }
  // 写入成功即认为已迁移到逐封 key：旧的全量 blob 不再是数据源，
  // 留着只会占配额。删除失败无副作用。
  try {
    store.removeItem(LEGACY_NS_KEY)
  } catch { /* 忽略 */ }
}

export async function clearEmailBodyLocal(id: string): Promise<void> {
  if (!id) return
  const store = storage()
  if (!store) return
  try {
    store.removeItem(keyFor(id))
  } catch { /* 无所谓 */ }
  // ⚠§873/D43：索引里也要去掉，否则它占着一个名额却已经没有正文，
  //   淘汰时会白白把别的封挤出去。
  try {
    const ids = readIndex(store)
    const at = ids.indexOf(id)
    if (at >= 0) {
      ids.splice(at, 1)
      writeIndex(store, ids)
    }
  } catch { /* 无所谓 */ }
}
