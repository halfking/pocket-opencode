/**
 * 本地 user_settings 镜像（SQLCipher）。
 */
import { localDB } from '../local-db'
import { nowUnixSec } from './planner'
import { readJsonColumn, warnCorruptColumn } from '../sqlDb'

export interface LocalSetting {
  namespace: string
  id: string
  payload: unknown
  secretEncrypted: string
  updatedAt: number
  dirty: number
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
  }
}

export async function listLocalSettings(): Promise<LocalSetting[]> {
  if (!localDB.isReady()) return []
  const rows = await localDB.query<Record<string, unknown>>(
    'SELECT namespace, id, payload, secret_encrypted, updated_at, dirty FROM local_user_settings',
  )
  // §878：损坏的行被跳过（返回 null 的是它）⇒ 其余设置照常返回。
  return rows.map(rowToSetting).filter((r): r is LocalSetting => r !== null)
}

export async function getLocalSetting(namespace: string, id: string): Promise<LocalSetting | null> {
  if (!localDB.isReady()) return null
  const row = await localDB.queryOne<Record<string, unknown>>(
    'SELECT namespace, id, payload, secret_encrypted, updated_at, dirty FROM local_user_settings WHERE namespace = ? AND id = ?',
    [namespace, id],
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
}): Promise<LocalSetting> {
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
    return { namespace: input.namespace, id: input.id, payload, secretEncrypted: secret, updatedAt, dirty }
  }
  await localDB.run(
    `INSERT INTO local_user_settings (namespace, id, payload, secret_encrypted, updated_at, dirty)
     VALUES (?, ?, ?, ?, ?, ?)
     ON CONFLICT(namespace, id) DO UPDATE SET
       payload = excluded.payload,
       secret_encrypted = CASE WHEN excluded.secret_encrypted = '' THEN local_user_settings.secret_encrypted ELSE excluded.secret_encrypted END,
       updated_at = excluded.updated_at,
       dirty = excluded.dirty`,
    [input.namespace, input.id, payload, secret, updatedAt, dirty],
  )
  return {
    namespace: input.namespace,
    id: input.id,
    payload: input.payload ?? {},
    secretEncrypted: secret,
    updatedAt,
    dirty,
  }
}

export async function writeLocalIfNewer(input: {
  namespace: string
  id: string
  payload: unknown
  secretEncrypted?: string
  updatedAt: number
}): Promise<boolean> {
  const local = await getLocalSetting(input.namespace, input.id)
  if (local && local.updatedAt >= input.updatedAt) return false
  await writeLocalSetting({ ...input, dirty: 0 })
  return true
}

export async function markSettingClean(namespace: string, id: string): Promise<void> {
  if (!localDB.isReady()) return
  await localDB.run(
    'UPDATE local_user_settings SET dirty = 0 WHERE namespace = ? AND id = ?',
    [namespace, id],
  )
}

/** 删除本地镜像行（增量同步墓碑：其他端已删除的行不再留在本地）。 */
export async function deleteLocalSetting(namespace: string, id: string): Promise<void> {
  if (!localDB.isReady()) return
  await localDB.run(
    'DELETE FROM local_user_settings WHERE namespace = ? AND id = ?',
    [namespace, id],
  )
}
