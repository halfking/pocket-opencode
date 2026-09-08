import type { LocalMeeting, MeetingStatus } from './meetings-store'

export type MeetingListFilter = 'active' | 'archived'

export const MEETING_LIST_FILTERS: Array<{ id: MeetingListFilter; label: string }> = [
  { id: 'active', label: '进行中' },
  { id: 'archived', label: '已归档' },
]

export function isArchived(meeting: Pick<LocalMeeting, 'archivedAt'>): boolean {
  return meeting.archivedAt != null && meeting.archivedAt > 0
}

export function filterMeetings(
  meetings: LocalMeeting[],
  filter: MeetingListFilter,
): LocalMeeting[] {
  return meetings.filter((m) => (filter === 'archived' ? isArchived(m) : !isArchived(m)))
}

export function statusText(status: MeetingStatus): string {
  const map: Record<MeetingStatus, string> = {
    recording: '录音中',
    completed: '已完成',
    processing: '处理中',
    refined: '已精翻',
  }
  return map[status] ?? status
}

export function formatMeetingWhen(ts: number): string {
  return new Date(ts).toLocaleString('zh-CN', {
    month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit',
  })
}

export function formatDuration(ms: number): string {
  if (!ms || ms < 0) return ''
  const m = Math.floor(ms / 60000)
  const s = Math.floor((ms % 60000) / 1000)
  return m > 0 ? `${m}分${s}秒` : `${s}秒`
}

export function formatParticipants(names: string[] | null | undefined): string {
  const list = (names ?? []).map((n) => n.trim()).filter(Boolean)
  if (list.length === 0) return ''
  if (list.length <= 3) return list.join('、')
  return `${list.slice(0, 3).join('、')} 等${list.length}人`
}

export function formatLocationLine(location: string | null | undefined): string {
  const text = location?.trim()
  return text ? `📍 ${text}` : ''
}
