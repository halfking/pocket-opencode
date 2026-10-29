/**
 * 本地 user_settings 镜像（SQLCipher）。
 */
import { localDB } from '../local-db'
import { nowUnixSec } from './planner'
import { readJsonColumn, warnCorruptColumn } from '../sqlDb'
import { captureLocalConfigLease, type ConfigDb } from './operation'
import { ownerColumnValues, ownerScopeSql, type ConfigOwner } from './ownership'

/**
 * 默认路径：捕获一次本地编辑租约，并从**同一个租约**取出归属。
 *
 * ⚠ 归属与这次写入必须是同一代会话 ⇒ 一起取，不分开读 auth store。
 */
const localOperation = () => captureLocalConfigLease()

/**
 * 解析「归属 + 数据库句柄」这一对。
 *
 * ⚠ 两者**必须来自同一个租约**：如果归属来自会话、句柄来自另一次租约，
 *   切账号的瞬间就可能出现「归属是 A、租约是 B」的撕裂 ⇒ 行被写到 A 名下
 *   却带着 B 的租约（或反之）。
 *
 * ⚠ `owner` 省略时 = 从租约取；`db` 省略时 = 新捕获一个租约。
 *   两个都省略 ⇒ 同一个租约，两者天然一致。
 */
function resolveScope(
  owner: ConfigOwner | null | undefined,
  db: ConfigDb | undefined,
): { owner: ConfigOwner | null; db: ConfigDb } {
  if (owner === undefined && db === undefined) {
    const lease = localOperation()
    return { owner: lease.owner, db: lease.db }
  }
  if (owner === undefined) {
    return { owner: localOperation().owner, db: db! }
  }
  return { owner, db: db ?? localOperation().db }
}

export interface LocalSetting {
  namespace: string
  id: string
  payload: unknown
  secretEncrypted: string
  updatedAt: number
  dirty: number
  /** 该行的归属。`null` = 归属未知（历史行），永不上传。 */
  owner: ConfigOwner | null
}

function rowOwner(row: Record<string, unknown>): ConfigOwner | null {
  const userId = String(row.owner_user_id ?? '')
  const workspaceId = String(row.owner_workspace_id ?? '')
  if (!userId || !workspaceId) return null
  return { userId, workspaceId }
}

/**
 * §878：行 → 设置。**payload 损坏时返回 null（跳过该行），不再冒充 `{}`。**
 *
 * ⚠ 修之前是 `catch { payload = {} }`。后果：调用方
 *   `scheduled-tasks/store.ts` 做 `row.payload as ScheduledTask`
 *   ⇒ 列表里出现一个**既没有 id 也没有标题的空任务**；
 *   `stores/chatAgentStore.ts` 同理，出现一个空角色。
 *   两者都**零提示** —— 用户看到的是「有一个条目，但里面什么都没有」。
 *
 * ⚠ 为什么不降级成 `{}`（§870 的判据）：降级**可能破坏** ——
 *   一份设置凭空变成空对象，后续任何「以本地为准」的合并都可能拿它当事实。
 *   ⇒ 这一列损坏时，**最诚实的降级是「这一行没有」**，且原始字节留在表里。
 */
function rowToSetting(row: Record<string, unknown>): LocalSetting | null {
  const parsed = readJsonColumn<unknown>(row, 'payload', 'local_user_settings')
  if (!parsed.ok) {
    warnCorruptColumn('local_user_settings', `${String(row.namespace)}/${String(row.id)}`, parsed.reason)
    return null
  }
  return {
    namespace: String(row.namespace),
    id: String(row.id),
    payload: parsed.value,
    secretEncrypted: String(row.secret_encrypted ?? ''),
    updatedAt: Number(row.updated_at ?? 0),
    dirty: Number(row.dirty ?? 0),
    owner: rowOwner(row),
  }
}

/**
 * 列出**属于给定归属**的设置。
 *
 * ⚠ 归属未知的行（历史行）**永远不返回**：未认领 ≠ 属于当前账号。
 *   归属未知时（没有已验证会话）返回空列表 —— 宁可不显示，也不要把
 *   别的账号的行显示或上传出去。
 */
export async function listLocalSettings(
  owner?: ConfigOwner | null,
  db?: ConfigDb,
): Promise<LocalSetting[]> {
  if (!localDB.isReady()) return []
  const resolved = resolveScope(owner, db)
  const scope = ownerScopeSql(resolved.owner)
  const rows = await resolved.db.query<Record<string, unknown>>(
    `SELECT namespace, id, payload, secret_encrypted, updated_at, dirty, owner_user_id, owner_workspace_id
     FROM local_user_settings WHERE ${scope.clause}`,
    scope.params,
  )
  // §878：损坏的行被跳过（返回 null 的是它）⇒ 其余设置照常返回。
  return rows.map(rowToSetting).filter((r): r is LocalSetting => r !== null)
}

export async function getLocalSetting(
  namespace: string,
  id: string,
  owner?: ConfigOwner | null,
  db?: ConfigDb,
): Promise<LocalSetting | null> {
  if (!localDB.isReady()) return null
  const resolved = resolveScope(owner, db)
  const scope = ownerScopeSql(resolved.owner)
  const row = await resolved.db.queryOne<Record<string, unknown>>(
    `SELECT namespace, id, payload, secret_encrypted, updated_at, dirty, owner_user_id, owner_workspace_id
     FROM local_user_settings WHERE namespace = ? AND id = ? AND ${scope.clause}`,
    [namespace, id, ...scope.params],
  )
  if (!row) return null
  // ⚠ 点查：返回的 null 只表示「**没有这一行**」⇒ 损坏必须能被区分。
  //   这里损坏时同样返回 null，但已经 warn 过一次；调用方拿到的 null 是
  //   「这一行不可用」，与「这一行不存在」在**接口上无法区分** ——
  //   与 outboxStore.get 的处理**故意不同**：那里是队列消息，宁可抛。
  //   为什么不统一：见设计文档 §878.3（登记为待拍板第 98 条）。
  return rowToSetting(row)
}

export async function writeLocalSetting(input: {
  namespace: string
  id: string
  payload: unknown
  secretEncrypted?: string
  updatedAt?: number
  dirty?: number
}, owner?: ConfigOwner | null, db?: ConfigDb): Promise<LocalSetting> {
  const resolved = resolveScope(owner, db)
  const updatedAt = input.updatedAt ?? nowUnixSec()
  const dirty = input.dirty ?? 1
  const payload = JSON.stringify(input.payload ?? {})
  const secret = input.secretEncrypted ?? ''
  // BUG-AP：本文件里唯一没有 isReady() 守卫的写函数（listLocalSettings /
  // getLocalSetting / markSettingClean / deleteLocalSetting 都有）。
  // 本地库未就绪（未解锁、迁移未完成）时 localDB.run 会抛，
  // 而调用方（scheduled-tasks store）把这种「本地缓存写失败」
  // 误判成「服务端创建失败」，进而伪造一条不同 id 的草稿并排队推送，
  // 结果服务端出现重复任务。缓存写失败应当降级而不是炸掉整条链路。
  if (!localDB.isReady()) {
    return { namespace: input.namespace, id: input.id, payload, secretEncrypted: secret, updatedAt, dirty, owner: resolved.owner }
  }
  // ⚠ 归属写死在行上：主键是 (namespace, id)，所以**同一个 namespace+id 在不同
  //   账号下会互相覆盖**。这既是数据丢失，也是跨账号串号。
  //   ⇒ 这里只在「已有行的归属与本次归属一致」时才更新；归属不同则**另起一行**，
  //     代价是主键需要带归属 —— 见下面的 owner-scoped 主键迁移。
  const [ownerUserId, ownerWorkspaceId] = ownerColumnValues(resolved.owner)
  await resolved.db.run(
    `INSERT INTO local_user_settings (namespace, id, payload, secret_encrypted, updated_at, dirty, owner_user_id, owner_workspace_id)
     VALUES (?, ?, ?, ?, ?, ?, ?, ?)
     ON CONFLICT(namespace, id, owner_user_id, owner_workspace_id) DO UPDATE SET
       payload = excluded.payload,
       secret_encrypted = CASE WHEN excluded.secret_encrypted = '' THEN local_user_settings.secret_encrypted ELSE excluded.secret_encrypted END,
       updated_at = excluded.updated_at,
       dirty = excluded.dirty
     WHERE local_user_settings.owner_user_id IS excluded.owner_user_id
       AND local_user_settings.owner_workspace_id IS excluded.owner_workspace_id`,
    [input.namespace, input.id, payload, secret, updatedAt, dirty, ownerUserId, ownerWorkspaceId],
  )
  return {
    namespace: input.namespace,
    id: input.id,
    payload: input.payload ?? {},
    secretEncrypted: secret,
    updatedAt,
    dirty,
    owner: resolved.owner,
  }
}

/**
 * 只有归属完全一致才更新（远端 LWW 落盘）。
 *
 * ⚠ 归属不同 ⇒ **不写入**：远端那条记录属于别的账号/工作区，
 *   写进当前账号的镜像就是串号。
 */
export async function writeLocalIfNewer(input: {
  namespace: string
  id: string
  payload: unknown
  secretEncrypted?: string
  updatedAt: number
}, owner?: ConfigOwner | null, db?: ConfigDb): Promise<boolean> {
  if (!localDB.isReady()) return false
  const resolved = resolveScope(owner, db)
  const [ownerUserId, ownerWorkspaceId] = ownerColumnValues(resolved.owner)
  const changed = await resolved.db.run(
    `INSERT INTO local_user_settings (namespace, id, payload, secret_encrypted, updated_at, dirty, owner_user_id, owner_workspace_id)
     VALUES (?, ?, ?, ?, ?, 0, ?, ?)
     ON CONFLICT(namespace, id, owner_user_id, owner_workspace_id) DO UPDATE SET
       payload = excluded.payload,
       secret_encrypted = CASE WHEN excluded.secret_encrypted = '' THEN local_user_settings.secret_encrypted ELSE excluded.secret_encrypted END,
       updated_at = excluded.updated_at, dirty = 0
     WHERE local_user_settings.updated_at < excluded.updated_at
       AND local_user_settings.owner_user_id IS excluded.owner_user_id
       AND local_user_settings.owner_workspace_id IS excluded.owner_workspace_id`,
    [input.namespace, input.id, JSON.stringify(input.payload ?? {}), input.secretEncrypted ?? '', input.updatedAt,
      ownerUserId, ownerWorkspaceId],
  )
  return changed > 0
}

/**
 * 标记为「已是最新」。
 *
 * ⚠ 除版本与内容外**还校验归属**：归属不同的行不是本次上传的那一行，
 *   绝不能被清成 clean（否则它会永远不再上传）。
 */
export async function markSettingClean(
  namespace: string,
  id: string,
  expected: LocalSetting,
  owner?: ConfigOwner | null,
  db?: ConfigDb,
): Promise<void> {
  if (!localDB.isReady()) return
  const resolved = resolveScope(owner, db)
  const scope = ownerScopeSql(resolved.owner)
  await resolved.db.run(
    `UPDATE local_user_settings SET dirty = 0
     WHERE namespace = ? AND id = ? AND updated_at = ? AND payload = ? AND secret_encrypted = ?
       AND ${scope.clause}`,
    [namespace, id, expected.updatedAt, JSON.stringify(expected.payload ?? {}), expected.secretEncrypted, ...scope.params],
  )
}

/** 删除本地镜像行（增量同步墓碑：其他端已删除的行不再留在本地）。 */
export async function deleteLocalSetting(
  namespace: string,
  id: string,
  owner?: ConfigOwner | null,
  db?: ConfigDb,
): Promise<void> {
  if (!localDB.isReady()) return
  const resolved = resolveScope(owner, db)
  const scope = ownerScopeSql(resolved.owner)
  await resolved.db.run(
    `DELETE FROM local_user_settings WHERE namespace = ? AND id = ? AND ${scope.clause}`,
    [namespace, id, ...scope.params],
  )
}
