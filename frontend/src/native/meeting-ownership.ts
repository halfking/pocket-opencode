/**
 * 会议/录音/待办/声纹的**行归属**（2026-10-29）。
 *
 * ⚠ 为什么单独一个模块，而不是直接复用 `config-sync/ownership.ts`：
 *   config 那套管的是**配置镜像**（`local_user_settings` / `local_config_outbox`），
 *   它的语义是「云端镜像可重拉 ⇒ 旧行丢掉不算数据损失」。
 *   会议/录音**不可从云端恢复**（原始音频分片、本地 ASR 结果、声纹 embedding）
 *   ⇒ 旧行必须**搬运**到新表，只是归属置空串（未认领）。
 *   两者的迁移策略不同，但**归属语义完全一致** ⇒ 这里复用同一套语义函数，
 *   避免同一个概念有两份实现、两份判断。
 *
 * 📌 三条不可让步的语义（与 config 切片一致）：
 *  1. **写入必须带归属**：新行记录当前**已验证**会话的 userId/workspaceId。
 *  2. **归属未知 = 未认领**：存量行两列为空串 ⇒ 不上传、不被任何账号捡走、不删除。
 *  3. **归属必须精确匹配**：userId 与 workspaceId 都要相同才算同一归属。
 */

import type { localDB } from './local-db'

/** 一行的归属。`null` 表示**归属未知**（历史行），不是「任何账号」。 */
export interface MeetingOwner {
  readonly userId: string
  readonly workspaceId: string
}

/**
 * 归属在数据库里的存储值。
 *
 * ⚠ 用**空字符串**而不是 NULL 标记「未知」：这几张表的主键含归属列，
 *   而 SQLite 认为 NULL 互不相等 ⇒ 用 NULL 会让同一个 id 无限插新行。
 *   空串是普通的键分量：未认领的行彼此仍按 id 唯一，
 *   但**永远不匹配任何真实归属**（见 ownerScopeSql：owner 为 null 时返回 1 = 0）。
 */
export const UNKNOWN_MEETING_OWNER = ''

export function ownerColumnValues(owner: MeetingOwner | null): [string, string] {
  return owner ? [owner.userId, owner.workspaceId] : [UNKNOWN_MEETING_OWNER, UNKNOWN_MEETING_OWNER]
}

/**
 * 把已验证会话折成归属。
 *
 * ⚠ 只有**已验证**的会话才有 userId（`readStoredSession` 明确写着
 *   「Persisted metadata is not a verified owner assertion」）。
 *   ⇒ 恢复出来的会话**拿不到归属**，写下的行也就保持未认领。
 */
export function ownerFromAuth(auth: {
  readonly userId: string
  readonly workspaceId: string
}): MeetingOwner | null {
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
export function isOwnedBy(owner: MeetingOwner | null, candidate: MeetingOwner | null): boolean {
  if (!owner || !candidate) return false
  return owner.userId === candidate.userId && owner.workspaceId === candidate.workspaceId
}

/** SQL 片段：只选出属于给定归属的行（归属未知的行永远不入选）。 */
export function ownerScopeSql(
  owner: MeetingOwner | null,
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
export function ownerFromColumns(userId: unknown, workspaceId: unknown): MeetingOwner | null {
  const user = typeof userId === 'string' ? userId.trim() : ''
  const workspace = typeof workspaceId === 'string' ? workspaceId.trim() : ''
  if (!user || !workspace) return null
  return { userId: user, workspaceId: workspace }
}

/**
 * 捕获一次**会议链路的本地编辑租约**，并从**同一个租约**取出归属。
 *
 * ⚠ 为什么归属取自租约而不是直接读 auth store：租约的 owner 由
 *   `requestOwnerActivation` 在**已验证**登录时写入，切账号会换 epoch。
 *   从租约取归属 ⇒ 归属与「这次写操作属于哪一代会话」**原子一致**，
 *   不会出现「租约是 A 的、归属却是 B 的」这种撕裂。
 *
 * ⚠ 与 `captureRecordingDb()` 的区别：那个只给句柄；这里**同时**给出归属，
 *   因为会议表的写入必须把归属**写进行里**（不是过滤读）。
 *
 * ⚠ 为什么 `database` 是**参数**而不是本模块直接 import：
 *   本模块是**纯模块**（零运行时依赖），与 `config-sync/ownership.ts` 同款。
 *   若这里 `import { localDB } from './local-db'`，则每个用 esbuild 桩
 *   测 `native/local-db` 的行为门都会**顺着这条链把 capacitor 真件拖进来**
 *   （桩按 import 说明匹配，`'./local-db'` 与 `'../../native/local-db'`
 *   是两个不同的说明 ⇒ 桩拦不住）⇒ 那些门在 `Could not resolve
 *   "@capacitor-community/sqlite"` 上整体崩掉（实测：§87/§147/§153/§393
 *   四组共 20 余条用例被 `cancelledByParent` 连带取消）。
 *   ⇒ 由调用方把它自己那份（可被桩替换的）`localDB` 传进来。
 */
export function captureMeetingOwnership(database: typeof localDB): {
  db: ReturnType<typeof localDB.bindOperation>
  owner: MeetingOwner | null
} {
  const lease = database.captureOperation({ kind: 'local-edit' })
  const principal = lease.owner.principal
  return {
    db: database.bindOperation(lease),
    owner: principal ? { userId: principal.userId, workspaceId: principal.workspaceId } : null,
  }
}

/**
 * 从**已绑定的操作句柄**取归属。
 *
 * ⚠ 为什么从句柄取而不是重新捕获租约：调用方已经持有句柄（它授权了这次写入）
 *   ⇒ 归属必须与这**同一个**租约一致。若这里重新捕获，就可能出现
 *   「句柄是 A 的、归属是 B 的」（切账号瞬间）。
 */
export function ownerOf(db: { readonly owner: MeetingOwner | null }): MeetingOwner | null {
  return db.owner
}
