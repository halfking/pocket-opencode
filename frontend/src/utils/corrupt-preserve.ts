/**
 * 损坏留证的唯一落点（§858/D21 起，§859/D24 复用）。
 *
 * 为什么要抽出来：§858 在 `aiChatStore` 里留证，§859 发现**同一个 key 的写侧**
 * （`native/config-sync/apply.ts` 的 `applyChatSettings`）解析失败会静默丢掉
 * 整份远端设置。两处都在 `pocket:ai-chat:*` 这一个命名空间里各自拼键名 ——
 * 而键名约定一旦有两份实现，就会各自漂移，最后谁也找不到谁的留证。
 *
 * ⚠ 不要在这里「顺手」做恢复或清洗：本函数只保证
 *   ① 原始字节被原样保留（不做 JSON.parse、不做截断、不做美化）；
 *   ② 写入失败不抛（配额满时连留证都写不下，由调用方负责对用户说话）。
 */

/** AI 对话侧的留证命名空间（§858/D21 起）。crypto 侧见 §859/D26 的调用点。 */
export const AI_CHAT_CORRUPT_NS = 'pocket:ai-chat:corrupt'

/** 与 `aiChatStore.storageScope()` 同口径：workspace > user > 'local'。 */
export function storageScope(): string {
  try {
    const workspace = localStorage.getItem('pocket_workspace_id') || ''
    const user = localStorage.getItem('pocket_user') || ''
    return encodeURIComponent(workspace || user || 'local')
  } catch {
    return 'local'
  }
}

/**
 * 把损坏的原始串另存到 `<ns>:<kind>:<scope>:<ts>-<seq>`。
 *
 * ⚠ `ns` 是**命名空间前缀**，必须由调用方给全（`pocket:ai-chat:corrupt` /
 *   `pocket:crypto:corrupt` …）。理由同文件头：同一个 key 命名空间里若出现两份
 *   键名实现就会各自漂移；而**跨命名空间也不能各写各的函数** ——
 *   §859/D26 加了第二个命名空间之后，沿用同一个函数才让「留证」这件事只有一个落点。
 *
 * ⚠⚠⚠§874/D44：键名末尾原来只有 `Date.now()`，后果有两个：
 *   ① **无上限、永不删除** —— 每一次「读到损坏」就**复制一整份原始数据**
 *     （`aiChatStore` 那边是**最多 40 轮对话全文**）永久留下。
 *     §873/D43 刚证明 localStorage 的配额是有限的，而且已经被邮件正文缓存
 *     吃掉了大头 ⇒ 这一族是**第二个**只写不删的累加器，打的是同一个器官。
 *   ② **同毫秒碰撞** —— 同一个 `ns:kind:scope` 在同一毫秒内触发两次，
 *     键名相同 ⇒ 后写的**静默覆盖**先写的证据。
 *     这不是理论：`loadSettings`（读侧）与 `applyChatSettings`（写侧）
 *     留的是**同一个 kind**、同一个源 key。
 * ⇒ 现在键名带一个单调递增的 `seq`，且每个 `ns:kind` 只保留最近
 *   `PRESERVE_MAX_PER_KIND` 份，更老的**真的 removeItem 掉**。
 *
 * @returns 留证键；没写下去（无内容 / 配额满 / localStorage 不可用）返回 null。
 */

/**
 * ⚠§874/D44：每个 `ns:kind` 保留几份。
 * 取 3 是折中：足够对比「最近三次的损坏形态」，又不会随天数线性增长。
 * ⚠ 原文件头写着「不要在这里顺手做清洗」—— 那条针对的是**恢复**与
 *   **内容改写**；**按数量淘汰旧证据**不改任何一份证据的内容，
 *   也不影响取证价值，所以本节做了。
 */
const PRESERVE_MAX_PER_KIND = 3

/** 模块级单调计数：保证同一毫秒内的两次留证键名不同。 */
let preserveSeq = 0

/** 列出某个 `ns:kind` 下已有的留证键，按时间升序（旧 → 新）。 */
function listPreserveKeys(ns: string, kind: string): string[] {
  const prefix = `${ns}:${kind}:`
  const out: { key: string; ts: number }[] = []
  try {
    for (let i = 0; i < localStorage.length; i++) {
      const key = localStorage.key(i)
      if (!key || !key.startsWith(prefix)) continue
      const tail = key.slice(prefix.length)
      const ts = Number(tail.slice(tail.lastIndexOf(':') + 1).split('-')[0])
      out.push({ key, ts: Number.isFinite(ts) ? ts : 0 })
    }
  } catch {
    return []
  }
  return out.sort((a, b) => a.ts - b.ts).map((x) => x.key)
}

function enforcePreserveBudget(ns: string, kind: string): void {
  const keys = listPreserveKeys(ns, kind)
  if (keys.length <= PRESERVE_MAX_PER_KIND) return
  const doomed = keys.slice(0, keys.length - PRESERVE_MAX_PER_KIND)
  let dropped = 0
  for (const k of doomed) {
    try {
      localStorage.removeItem(k)
      dropped++
    } catch { /* 删不掉就只是额度白占 */ }
  }
  if (dropped > 0) {
    console.warn(`[corrupt-preserve] ${ns}:${kind} 的留证超过 ${PRESERVE_MAX_PER_KIND} 份，已丢弃最旧的 ${dropped} 份`)
  }
}

export function preserveCorruptRaw(ns: string, kind: string, raw: string | null): string | null {
  if (!raw) return null
  preserveSeq += 1
  const key = `${ns}:${kind}:${storageScope()}:${Date.now()}-${preserveSeq}`
  try {
    localStorage.setItem(key, raw)
  } catch {
    // 容量满时连备份都写不下 —— 调用方必须在界面上说明，别静默。
    return null
  }
  // ⚠§874/D43 的教训就在这里：**写成功不等于这件事完了**。
  //   不记账的话，这一族会和邮件正文缓存一样，把配额吃光之后
  //   让本仓**所有**写入静默失败（会话 / 对话历史 / 闪卡队列 / 加密 salt）。
  enforcePreserveBudget(ns, kind)
  return key
}
