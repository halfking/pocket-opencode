/** 邮件详情：回复 / 转发 / 转 Todo 的输入框默认关闭，点导航栏按钮才打开。 */
export type ComposeKind = 'hidden' | 'reply' | 'forward' | 'todo'

export function toggleCompose(current: ComposeKind, next: ComposeKind): ComposeKind {
  if (next === 'hidden') return 'hidden'
  return current === next ? 'hidden' : next
}

export function composeSheetTitle(kind: ComposeKind, fromName: string): string {
  if (kind === 'reply') return `回复 ${fromName || '发件人'}`
  if (kind === 'forward') return '转发'
  if (kind === 'todo') return '转 Todo'
  return ''
}

export function defaultReplySubject(subject: string): string {
  const s = subject.trim() || '(无主题)'
  return /^re:/i.test(s) ? s : `Re: ${s}`
}

export function defaultForwardSubject(subject: string): string {
  const s = subject.trim() || '(无主题)'
  return /^fwd:/i.test(s) ? s : `Fwd: ${s}`
}

export function todoFromDraft(text: string, fallbackTitle: string): {
  title: string
  description: string
} {
  const lines = text.replace(/\r\n/g, '\n').trim().split('\n')
  const title = (lines[0] || '').trim() || fallbackTitle || '(无主题)'
  const description = lines.slice(1).join('\n').trim()
  return { title, description }
}
