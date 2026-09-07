import { pageHasMore } from '../../native/list-sync/page.ts'

export function inboxListFilter(category: string) {
  if (category === '__important') return { importance: 'high' as const }
  if (category === '__spam') return { category: 'spam' }
  if (category === '__none') return { uncategorized: true }
  return category ? { category } : {}
}

export function inboxHasMore(pageLen: number): boolean {
  return pageHasMore(pageLen)
}
