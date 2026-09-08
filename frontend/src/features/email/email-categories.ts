export type InboxChip = { label: string; value: string }

export const INBOX_CATEGORY_CHIPS: InboxChip[] = [
  { label: '全部', value: '' },
  { label: '未分类', value: '__none' },
  { label: '重要', value: '__important' },
  { label: '工作', value: 'work' },
  { label: '账单', value: 'bill' },
  { label: '私人', value: 'personal' },
  { label: '通知', value: 'notification' },
  { label: '广告', value: 'marketing' },
  { label: '垃圾', value: '__spam' },
]

const LABELS: Record<string, string> = {
  work: '工作',
  bill: '账单',
  notification: '通知',
  personal: '私人',
  marketing: '广告',
  spam: '垃圾',
}

const ALIASES: Record<string, string> = {
  ad: 'marketing',
  ads: 'marketing',
  advertisement: 'marketing',
  promo: 'marketing',
}

export const EMAIL_CATEGORIES = ['work', 'bill', 'notification', 'personal', 'marketing', 'spam'] as const

export function catLabel(c: string | null | undefined): string {
  if (!c) return ''
  return LABELS[c] || c
}

export function normalizeEmailCategory(raw: string | null | undefined): string {
  const key = (raw ?? '').trim().toLowerCase()
  if (!key) return ''
  const mapped = ALIASES[key] || key
  return (EMAIL_CATEGORIES as readonly string[]).includes(mapped) ? mapped : 'personal'
}
