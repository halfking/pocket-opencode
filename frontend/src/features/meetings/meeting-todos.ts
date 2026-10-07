import type { ActionItem } from './meetings-store'
import { dueAtToScheduleExpr, parseDueAt } from './meeting-due.ts'

export interface MeetingTodoDraft {
  text: string
  assignee?: string
  due?: string
}

export function draftsFromActionItems(items: ActionItem[] | null | undefined): MeetingTodoDraft[] {
  const seen = new Set<string>()
  const drafts: MeetingTodoDraft[] = []
  for (const item of items ?? []) {
    const text = item.text?.trim()
    if (!text || seen.has(text)) continue
    seen.add(text)
    const draft: MeetingTodoDraft = { text }
    const assignee = item.assignee?.trim()
    const due = item.due?.trim()
    if (assignee) draft.assignee = assignee
    if (due) draft.due = due
    drafts.push(draft)
  }
  return drafts
}

export function personShareText(draft: MeetingTodoDraft, meetingTitle: string): string {
  const bits = [`会议待办：${draft.text}`, `来源：${meetingTitle || '未命名会议'}`]
  if (draft.assignee) bits.push(`负责人：${draft.assignee}`)
  if (draft.due) bits.push(`期限：${draft.due}`)
  return bits.join('\n')
}

export function accHandoffInput(draft: MeetingTodoDraft, meetingTitle: string) {
  const title = `跟进：${draft.text.slice(0, 32)}`
  const prompt = [
    `请跟进会议「${meetingTitle || '未命名会议'}」的待办：${draft.text}`,
    draft.assignee ? `建议负责人：${draft.assignee}` : '',
    draft.due ? `建议期限：${draft.due}` : '',
  ].filter(Boolean).join('\n')
  // 有真实期限就按期限触发；解不出（无 due / 明确指向过去）才退回
  // 「1 分钟后」，让「转交」这个动作本身至少能立刻执行一次。
  // ⚠️ 此前这里恒为 now+60s，等于把用户说的「周五」完全无视。
  let scheduleExpr = new Date(Date.now() + 60_000).toISOString()
  if (draft.due) {
    try {
      const parsed = parseDueAt(draft.due)
      if (parsed) scheduleExpr = dueAtToScheduleExpr(parsed.at)
    } catch {
      // 坏期限不该让「转交」失败，退回 1 分钟后。
    }
  }
  return {
    name: title,
    description: draft.text,
    kind: 'redclaw_chat' as const,
    scheduleKind: 'at' as const,
    scheduleExpr,
    timezone: 'Asia/Shanghai',
    payload: { prompt, source: 'meeting-todo' },
    maxRuns: 1,
    enabled: true,
  }
}
