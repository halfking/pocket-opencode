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
 * 牵连其它邮件，也不再有「整体超配额」的悬崖。
 * 旧的全量 key 保留一次性读取做兼容迁移，读完即删。
 */
const LEGACY_NS_KEY = 'pocket:snapshots:email_bodies'
const PREFIX = 'pocket:email_body:'

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
  // 写入成功即认为已迁移到逐封 key：旧的全量 blob 不再是数据源，
  // 留着只会占配额。删除失败无副作用。
  try {
    store.removeItem(LEGACY_NS_KEY)
  } catch { /* 忽略 */ }
}

export async function clearEmailBodyLocal(id: string): Promise<void> {
  if (!id) return
  try {
    storage()?.removeItem(keyFor(id))
  } catch { /* 无所谓 */ }
}
