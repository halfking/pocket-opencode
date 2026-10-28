import { emailApi } from '../../api/email'
import { DEFAULT_LIST_PAGE_SIZE } from '../../native/list-sync/page'
import { syncAccountsFromServer } from './account-sync'
import { inboxListFilter } from './email-inbox-filter'
import { healGap, pullWithPlan } from './email-cache-heal-run'
import { MAX_BACKFILL_PAGES, SERVER_BACKFILL_DAYS } from './email-cache-heal-run'
import type { HealerDeps } from './email-cache-heal-run'
import * as emailsStore from './emails-store'
import type { LocalEmail } from './emails-store'
import { readCachedModuleMode, recordSyncReportEntry } from '../../stores/syncPolicyStore'
import { captureEmailSyncOperation, type EmailSyncOperation } from './email-sync-guard'

export { inboxHasMore, inboxListFilter } from './email-inbox-filter'
export { MAX_BACKFILL_PAGES, SERVER_BACKFILL_DAYS } from './email-cache-heal-run'

/**
 * 读一页本地邮件。folder 语义与服务端一致：
 * '' = 收件箱（默认视图，不在任何目录里的邮件）；具体目录名 = 该目录。
 */
export async function readInboxPage(category: string, offset: number, folder = ''): Promise<LocalEmail[]> {
  return emailsStore.listEmails({
    ...inboxListFilter(category),
    ...(folder ? { folder } : {}),
    limit: DEFAULT_LIST_PAGE_SIZE,
    offset,
  })
}

/**
 * 真实依赖装配。
 *
 * 编排逻辑全在 email-cache-heal-run（可注入假 IO 测），这里只负责把
 * SQLite / HTTP 接到它需要的那几个函数上。
 */
function sessionDeps(operation: EmailSyncOperation): HealerDeps {
  const assertCanApply = () => operation.assertCurrent()
  return {
    countLocal: () => emailsStore.countLocalEmails(operation.db),
    maxUpdatedAt: () => emailsStore.maxEmailUpdatedAt(operation.db),
    newestDate: () => emailsStore.newestLocalEmailDate(operation.db),
    serverCount: async () => {
      assertCanApply()
      const res = await emailApi.listEmails({ limit: 200 }, { syncModule: 'email' })
      assertCanApply()
      // total 是服务端 COUNT(*)，不受 limit 影响。退回 emails.length 是有损的：
      // 邮箱超过 limit 时它恒等于 limit，「服务端领先」信号会被抹平 —— 而那
      // 正是要靠这个信号发现邮件丢失的场合。
      return typeof res.total === 'number' ? res.total : (res.emails ?? []).length
    },
    syncPage: async (limit, since) => {
      const r = await emailsStore.syncEmailsFromServerDetailed(limit, since, operation)
      return {
        inserted: r.inserted,
        received: r.received,
        tombstones: r.tombstones,
        oldestDateMs: r.oldestDateMs,
      }
    },
    serverBackfill: async (days) => {
      assertCanApply()
      const r = await emailApi.backfill({ days })
      const accounts = r.accounts ?? []
      return {
        saved: accounts.reduce((n, a) => n + (a.saved ?? 0), 0),
        errors: accounts.filter((a) => a.error).map((a) => `${a.accountId}: ${a.error}`),
      }
    },
    log: {
      info: (m) => console.info(m),
      warn: (m) => console.warn(m),
    },
  }
}

/**
 * 下拉刷新：只拉账户和列表。
 *
 * 这里刻意**不**升级到服务端 IMAP 回补——下拉是高频手势，不能让它连真 IMAP。
 * 缺口判定与时间窗回补照做（纯本地读 + 一次 HTTP 列表）。
 * IMAP 收信走原生插件或 syncInboxFromServer。
 */
export async function pullInboxFromServer(operation?: EmailSyncOperation): Promise<void> {
  if (skipCloudSync()) return
  const sync = operation ?? captureEmailSyncOperation()
  const assertCanApply = () => sync.assertCurrent()
  try {
    await syncAccountsFromServer(sync)
  } catch (e: unknown) {
    console.warn('[email] account sync:', e instanceof Error ? e.message : e)
  }
  try {
    if (skipCloudSync()) return
    assertCanApply()
    await pullWithPlan(sessionDeps(sync))
  } catch (e: unknown) {
    console.warn('[email] sync from server:', e instanceof Error ? e.message : e)
  }
}

/**
 * 主动同步：先让服务端前进，再补历史缺口。
 *
 * syncNow 只处理「上次同步之后到达」的新邮件；历史缺口靠 healGap 分两级补
 * （客户端时间窗回补 → 服务端 IMAP 回补）。少任何一级，用户报的「一天前的
 * 邮件看不到」都会在服务端库里根本没有那批邮件时静默复现。
 */
export async function syncInboxFromServer(): Promise<string> {
  if (skipCloudSync()) return '仅本地模式，已跳过云端同步'
  const operation = captureEmailSyncOperation()
  const assertCanApply = () => operation.assertCurrent()
  await pullInboxFromServer(operation)
  if (skipCloudSync()) return '仅本地模式，已跳过云端同步'
  assertCanApply()
  let hint = ''
  try {
    const r = await emailApi.syncNow()
    const fail = r.failed?.length ? `，失败 ${r.failed.length}` : ''
    hint = `已同步 ${r.synced ?? 0} 个账户，新邮件 ${r.new ?? 0}${fail}`
  } catch (e: unknown) {
    hint = e instanceof Error && e.message ? `同步失败：${e.message}` : '同步失败'
  }
  try {
    if (skipCloudSync()) return hint
    assertCanApply()
    const healed = await healGap(sessionDeps(operation))
    if (healed.filled > 0) hint += `，回补 ${healed.filled} 封`
  } catch (e: unknown) {
    console.warn('[email] cache heal:', e instanceof Error ? e.message : e)
  }
  return hint
}

function skipCloudSync(): boolean {
  if (readCachedModuleMode('email') !== 'local') return false
  recordSyncReportEntry({ module: 'email', status: 'skipped' })
  return true
}
