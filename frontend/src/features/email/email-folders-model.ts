/**
 * email-folders-model.ts — 自定义邮件目录的纯逻辑（无 DB / 网络 / Vue 依赖）。
 *
 * 拆出独立文件的原因：node --test 直跑 ESM 时无法解析无扩展名的相对导入，
 * 带 localDB 依赖的 store 文件测不了；而展示名、幂等键、服务端目录行映射
 * 是列表页/详情页/目录页三处共用的约定，必须可单测。模式对齐
 * account-mirror-write.ts（纯函数文件 + .test.mjs）。
 */

export interface LocalFolder {
  id: string
  accountId: string
  name: string
  displayName: string
  special: string
  source: string
  serverSynced: boolean
  emailCount: number
  createdAt: number
  updatedAt: number
}

export interface LocalOpsEntry {
  id: string
  accountId: string
  emailId: string
  uid: number
  action: 'move' | 'delete'
  targetFolder: string
  subject: string
  status: 'pending' | 'pushed' | 'applied' | 'failed'
  error: string
  createdAt: number
  updatedAt: number
}

/** 目录展示名：取层级分隔符后的最后一段。 */
export function folderDisplayName(name: string): string {
  const base = (name || '').split(/[\\/]/).pop() || name
  return base || name
}

/**
 * 生成幂等键：同一封邮件的同一种操作永远同键（本地去重 + 与服务端
 * InsertOpsLogScoped 的无键兜底派生规则保持同一形态，重放不产生重复）。
 */
export function opsIdempotencyKey(entry: Pick<LocalOpsEntry, 'emailId' | 'action' | 'targetFolder'>): string {
  return `ops:${entry.emailId}:${entry.action}:${entry.targetFolder}`
}

/** 服务端文件夹行 → 本地镜像行（无 createdAt 时用 now）。 */
export function mapServerFolder(
  f: { id: string; accountId: string; name: string; displayName?: string; special?: string; source?: string; serverSynced?: boolean; extra?: { emailCount?: number } },
  now: number,
): LocalFolder {
  return {
    id: f.id,
    accountId: f.accountId,
    name: f.name,
    displayName: f.displayName || folderDisplayName(f.name),
    special: f.special || '',
    source: f.source || 'server',
    serverSynced: !!f.serverSynced,
    emailCount: f.extra?.emailCount ?? 0,
    createdAt: now,
    updatedAt: now,
  }
}
