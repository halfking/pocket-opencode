import type { ActionItem, LocalMeeting } from './meetings-store'

export type MeetingStudioAction = 'archive' | 'restore' | 'classify' | 'dispatch-acc' | 'delete'

export interface MeetingStudioMenuItem {
  id: MeetingStudioAction
  label: string
  danger?: boolean
}

export function studioMenuItems(meeting: Pick<LocalMeeting, 'archivedAt'>): MeetingStudioMenuItem[] {
  const archived = meeting.archivedAt != null && meeting.archivedAt > 0
  return [
    { id: archived ? 'restore' : 'archive', label: archived ? '恢复' : '归档' },
    { id: 'classify', label: '分类' },
    { id: 'dispatch-acc', label: '下达任务给 ACC' },
    { id: 'delete', label: '删除', danger: true },
  ]
}

export function meetingActionItems(meeting: Pick<LocalMeeting, 'liveSummary'>): ActionItem[] {
  return meeting.liveSummary?.actionItems ?? []
}

export function canDispatchMeeting(meeting: Pick<LocalMeeting, 'summary' | 'liveSummary'>): boolean {
  const summary = meeting.summary?.trim()
  return Boolean(summary) || meetingActionItems(meeting).length > 0
}

export function meetingAccDispatchInput(meeting: Pick<LocalMeeting, 'title' | 'topic' | 'summary' | 'liveSummary'>) {
  const title = meeting.title?.trim() || '未命名会议'
  const items = meetingActionItems(meeting)
  const todoLines = items.map((item) => {
    const bits = [`- ${item.text}`]
    if (item.assignee) bits.push(`负责人 ${item.assignee}`)
    if (item.due) bits.push(`期限 ${item.due}`)
    return bits.join('，')
  })
  const prompt = [
    `请根据会议「${title}」下达并跟进任务。`,
    meeting.topic?.trim() ? `主题：${meeting.topic.trim()}` : '',
    meeting.summary?.trim() ? `纪要：${meeting.summary.trim()}` : '',
    todoLines.length ? `待办：\n${todoLines.join('\n')}` : '请从纪要中拆解可执行任务并跟进。',
  ].filter(Boolean).join('\n')
  return {
    name: `会议任务：${title.slice(0, 24)}`,
    description: meeting.summary?.trim() || title,
    kind: 'redclaw_chat' as const,
    scheduleKind: 'at' as const,
    scheduleExpr: new Date(Date.now() + 60_000).toISOString(),
    timezone: 'UTC',
    payload: { prompt, source: 'meeting-dispatch' },
    maxRuns: 1,
    enabled: true,
  }
}
