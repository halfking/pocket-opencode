import { emailApi } from '../../api/email'
import { DEFAULT_LIST_PAGE_SIZE, pageHasMore } from '../../native/list-sync/page'
import { syncAccountsFromServer } from './account-sync'
import * as emailsStore from './emails-store'
import type { LocalEmail } from './emails-store'

export function inboxListFilter(category: string) {
  if (category === '__important') return { importance: 'high' as const }
  if (category === '__spam') return { category: 'spam' }
  return category ? { category } : {}
}

export async function readInboxPage(category: string, offset: number): Promise<LocalEmail[]> {
  return emailsStore.listEmails({
    ...inboxListFilter(category),
    limit: DEFAULT_LIST_PAGE_SIZE,
    offset,
  })
}

export async function syncInboxFromServer(): Promise<string> {
  try {
    await syncAccountsFromServer()
  } catch (e: unknown) {
    console.warn('[email] account sync:', e instanceof Error ? e.message : e)
  }
  let hint = ''
  try {
    const r = await emailApi.syncNow()
    const fail = r.failed?.length ? `，失败 ${r.failed.length}` : ''
    hint = `已同步 ${r.synced ?? 0} 个账户，新邮件 ${r.new ?? 0}${fail}`
  } catch (e: unknown) {
    hint = e instanceof Error && e.message ? `同步失败：${e.message}` : '同步失败'
  }
  try {
    await emailsStore.syncEmailsFromServer(200)
  } catch (e: unknown) {
    console.warn('[email] sync from server:', e instanceof Error ? e.message : e)
  }
  return hint
}

export function inboxHasMore(pageLen: number): boolean {
  return pageHasMore(pageLen)
}
