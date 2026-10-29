/**
 * 配置表的账号归属（2026-10-29）。
 *
 * ⚠ 为什么要这一层：local_user_settings / local_config_outbox 是**共享本地库**
 *   里的表（DB_NAME='lobster'，单库单连接），而切账号**不清理这个库**。
 *   加归属列之前，两张表都没有账号/工作区字段 ⇒
 *   A 账号离线排队的一条 chat_settings，会在 B 账号登录后被 drain 用 **B 的凭据**
 *   推上云端 —— A 的私密内容进了 B 的账号。
 *   （已在隔离浏览器里实测复现：PUT 请求体是 A 的 prompt，Authorization 是 B 的令牌。）
 *
 * 📌 三条不可让步的语义：
 *  1. **写入必须带归属**：新行记录当前**已验证**会话的 userId/workspaceId。
 *  2. **归属未知 = 未认领**：存量行两列为 NULL ⇒ 不上传、不被任何账号捡走、不删除。
 *     （用户已确认：未知归属的历史数据保持未认领，不得自动认领或上传。
 *      因此**禁止**用「迁移时回填当前账号」来「修复」它们。）
 *  3. **归属必须精确匹配**：userId 与 workspaceId 都要相同才算同一归属。
 *     工作区沿用 openpocket 工作区（同账号切换工作区 = 不同归属）。
 */

/** 一行的归属。`null` 表示**归属未知**（历史行），不是「任何账号」。 */
export interface ConfigOwner {
  readonly userId: string
  readonly workspaceId: string
}

/**
 * 归属在数据库里的存储值。
 *
 * ⚠ 用**空字符串**而不是 NULL 标记「未知」：
 *   local_user_settings 的主键是 (namespace, id, owner_user_id, owner_workspace_id)，
 *   而 SQLite 认为 NULL 互不相等 ⇒ 用 NULL 会让同一个 (namespace, id) 无限插新行。
 *   空串是普通的键分量：未认领的行彼此仍按 (namespace, id) 唯一，
 *   但**永远不匹配任何真实归属**（见 ownerScopeSql：owner 为 null 时返回 1 = 0）。
 */
export const UNKNOWN_OWNER = ''

export function ownerColumnValues(owner: ConfigOwner | null): [string, string] {
  return owner ? [owner.userId, owner.workspaceId] : [UNKNOWN_OWNER, UNKNOWN_OWNER]
}

/**
 * 把已验证会话折成归属。
 *
 * ⚠ 只有**已验证**的会话才有 userId：`readStoredSession` 明确写着
 *   「Persisted metadata is not a verified owner assertion」，恢复会话的 userId 是空的。
 *   ⇒ 恢复出来的会话**拿不到归属**，写下的行也就没有归属（保持未认领），
 *   这正是我们要的保守行为：宁可不上传，也不把行挂到可能错误的账号上。
 */
export function ownerFromAuth(auth: {
  readonly userId: string
  readonly workspaceId: string
}): ConfigOwner | null {
  const userId = (auth.userId ?? '').trim()
  const workspaceId = (auth.workspaceId ?? '').trim()
  // 两个标识缺一不可：只有 userId 不足以定位工作区，只有 workspaceId 不足以定位账号。
  if (!userId || !workspaceId) return null
  return Object.freeze({ userId, workspaceId })
}

/**
 * 判断一行是否属于给定归属。
 *
 * ⚠ 归属未知（null）**永远返回 false**：未认领 ≠ 属于当前账号。
 */
export function isOwnedBy(owner: ConfigOwner | null, candidate: ConfigOwner | null): boolean {
  if (!owner || !candidate) return false
  return owner.userId === candidate.userId && owner.workspaceId === candidate.workspaceId
}

/** SQL 片段：只选出属于给定归属的行（归属未知的行永远不入选）。 */
export function ownerScopeSql(
  owner: ConfigOwner | null,
  alias = '',
): { clause: string; params: unknown[] } {
  const prefix = alias ? `${alias}.` : ''
  if (!owner) {
    // 没有归属 ⇒ 什么都不匹配（而不是匹配 NULL —— 那会把未认领的行捞出来）。
    return { clause: '1 = 0', params: [] }
  }
  return {
    clause: `${prefix}owner_user_id = ? AND ${prefix}owner_workspace_id = ?`,
    params: [owner.userId, owner.workspaceId],
  }
}

/** 从数据库行读回归属；两列都是空串 ⇒ 归属未知。 */
export function ownerFromColumns(userId: unknown, workspaceId: unknown): ConfigOwner | null {
  const user = typeof userId === 'string' ? userId.trim() : ''
  const workspace = typeof workspaceId === 'string' ? workspaceId.trim() : ''
  if (!user || !workspace) return null
  return { userId: user, workspaceId: workspace }
}

