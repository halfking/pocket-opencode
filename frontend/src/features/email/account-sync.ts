/**
 * account-sync.ts — 邮箱账户配置的 LWW（last-write-wins）双向同步。
 *
 * 协议：
 *   - 服务端 SSOT：email_accounts.updated_at 字段由任何写路径刷新；
 *   - 客户端本地：local_email_accounts.updated_at 镜像服务端时间；
 *   - 启动 / 显式 syncAccounts() 时，遍历服务端账户：
 *       服务端 updated_at > 本地 updated_at → 覆盖本地（下行）
 *       否则跳过，让本地的离线修改继续上行（在 EmailAccountSetup 调用
 *       cloud updateAccount 时服务端已写入更高时间戳）。
 *
 * 客户端「未配置网络 / 服务端不可达」时不抛错：本地库即 SSOT 副本，列表照常。
 */
import { emailApi, type EmailAccount as ServerAccount } from '../../api/email'
import { writeAccountIfNewer } from './emails-store'

export interface SyncReport {
  /** 服务端下行的总条数 */
  fetched: number
  /** 实际覆盖本地的条数（服务端更新时 wins） */
  applied: number
  /** 跳过：本地版本不低于服务端（离线编辑未上行的情况） */
  skipped: number
  /** 是否访问到了服务端；false 表示走降级（本地列表） */
  online: boolean
  error?: string
}

/**
 * 拉服务端账户列表做 LWW 合并；任意错误回落到 online=false。
 *
 * 调用方：EmailSettingsView 进入页面时；账户列表为空时主动 sync 一次；
 *        EmailAccountSetup 提交云端保存后再次 sync 让本地立刻可见。
 */
export async function syncAccountsFromServer(): Promise<SyncReport> {
  try {
    const res = await emailApi.listAccounts()
    const remote = res.accounts ?? []
    let applied = 0
    let skipped = 0
    for (const a of remote) {
      const updatedAt = a.updatedAt ?? a.createdAt ?? 0
      const won = await writeAccountIfNewer({
        id: a.id,
        displayName: a.displayName,
        emailAddress: a.emailAddress,
        imapHost: a.imapHost,
        imapPort: a.imapPort,
        authType: a.authType,
        syncIntervalMin: a.syncIntervalMin ?? 15,
        enabled: !!a.enabled,
        updatedAt,
      })
      if (won) applied++
      else skipped++
    }
    return { fetched: remote.length, applied, skipped, online: true }
  } catch (e: any) {
    return { fetched: 0, applied: 0, skipped: 0, online: false, error: e?.message }
  }
}

/**
 * 把本地账户上行到服务端（仅用于 LWW 推断：本地比服务端更新）。
 *
 * 本仓库默认是 admin 单租户 + 服务端 SSOT 形态，正常创建账户走
 * EmailAccountSetup 的 emailApi.addAccount；本函数保留以便后续多端
 * 离线编辑窗口扩展时复用。
 */
export async function pushAccountToServer(_a: ServerAccount): Promise<boolean> {
  try {
    await emailApi.updateAccount(_a.id, {
      displayName: _a.displayName,
      syncIntervalMin: _a.syncIntervalMin,
      enabled: _a.enabled,
    })
    return true
  } catch {
    return false
  }
}