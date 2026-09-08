import { emailApi } from '../../api/email'
import { DEFAULT_LIST_PAGE_SIZE } from '../../native/list-sync/page'
import { shouldRetryFullListPull } from './email-fetch-plan'
import { syncAccountsFromServer } from './account-sync'
import { inboxListFilter } from './email-inbox-filter'
import * as emailsStore from './emails-store'
import type { LocalEmail } from './emails-store'

export { inboxHasMore, inboxListFilter } from './email-inbox-filter'

export async function readInboxPage(category: string, offset: number): Promise<LocalEmail[]> {
  return emailsStore.listEmails({
    ...inboxListFilter(category),
    limit: DEFAULT_LIST_PAGE_SIZE,
    offset,
  })
}

/** 只拉账户和列表；IMAP 收信走原生插件或 syncInboxFromServer。 */
export async function pullInboxFromServer(): Promise<void> {
  try {
    await syncAccountsFromServer()
  } catch (e: unknown) {
    console.warn('[email] account sync:', e instanceof Error ? e.message : e)
  }
  try {
    const since = await emailsStore.maxEmailUpdatedAt()
    const pulled = await emailsStore.syncEmailsFromServer(200, since)
    const local = await emailsStore.listEmails({ limit: 1, offset: 0 })
    if (shouldRetryFullListPull(local.length, pulled, since)) {
      await emailsStore.syncEmailsFromServer(200, 0)
    }
  } catch (e: unknown) {
    console.warn('[email] sync from server:', e instanceof Error ? e.message : e)
  }
}

export async function syncInboxFromServer(): Promise<string> {
  await pullInboxFromServer()
  let hint = ''
  try {
    const r = await emailApi.syncNow()
    const fail = r.failed?.length ? `，失败 ${r.failed.length}` : ''
    hint = `已同步 ${r.synced ?? 0} 个账户，新邮件 ${r.new ?? 0}${fail}`
  } catch (e: unknown) {
    hint = e instanceof Error && e.message ? `同步失败：${e.message}` : '同步失败'
  }
  try {
    const since = await emailsStore.maxEmailUpdatedAt()
    await emailsStore.syncEmailsFromServer(200, since)
  } catch (e: unknown) {
    console.warn('[email] sync from server:', e instanceof Error ? e.message : e)
  }
  return hint
}
