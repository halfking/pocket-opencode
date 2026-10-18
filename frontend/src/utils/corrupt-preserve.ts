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
 * 把损坏的原始串另存到 `<ns>:<kind>:<scope>:<ts>`。
 *
 * ⚠ `ns` 是**命名空间前缀**，必须由调用方给全（`pocket:ai-chat:corrupt` /
 *   `pocket:crypto:corrupt` …）。理由同文件头：同一个 key 命名空间里若出现两份
 *   键名实现就会各自漂移；而**跨命名空间也不能各写各的函数** ——
 *   §859/D26 加了第二个命名空间之后，沿用同一个函数才让「留证」这件事只有一个落点。
 *
 * @returns 留证键；没写下去（无内容 / 配额满 / localStorage 不可用）返回 null。
 */
export function preserveCorruptRaw(ns: string, kind: string, raw: string | null): string | null {
  if (!raw) return null
  const key = `${ns}:${kind}:${storageScope()}:${Date.now()}`
  try {
    localStorage.setItem(key, raw)
    return key
  } catch {
    // 容量满时连备份都写不下 —— 调用方必须在界面上说明，别静默。
    return null
  }
}
