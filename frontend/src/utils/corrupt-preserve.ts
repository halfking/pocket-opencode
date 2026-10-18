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

const CORRUPT_PREFIX = 'pocket:ai-chat:corrupt'

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
 * 把损坏的原始串另存到 `pocket:ai-chat:corrupt:<kind>:<scope>:<ts>`。
 *
 * @returns 留证键；没写下去（无内容 / 配额满 / localStorage 不可用）返回 null。
 */
export function preserveCorruptRaw(kind: string, raw: string | null): string | null {
  if (!raw) return null
  const key = `${CORRUPT_PREFIX}:${kind}:${storageScope()}:${Date.now()}`
  try {
    localStorage.setItem(key, raw)
    return key
  } catch {
    // 容量满时连备份都写不下 —— 调用方必须在界面上说明，别静默。
    return null
  }
}
