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
    // ⚠ 别写成「已完成」：那会把失败藏进成功桶里。
    'refine-failed': '精翻未生效',
  }
  return map[status] ?? status
}

export function formatMeetingWhen(ts: number): string {
  return new Date(ts).toLocaleString('zh-CN', {
    month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit',
  })
}

export function formatDuration(ms: number): string {
  // ⚠ §885/D55：原来是 `if (!ms || ms < 0)`。`!ms` 挡得住 0 与 NaN（两者 falsy），
  //   **但挡不住 Infinity** —— 它是 truthy 且不小于 0 ⇒ 会落进下面，
  //   `Math.floor(Infinity / 60000)` = Infinity、`(Infinity % 60000)` = NaN
  //   ⇒ 渲染出「Infinity分NaN秒」。改成显式判有限性，与 utils/relative-time.ts
  //   和 features/tasks/health.ts 对齐（§880.6 记的：判据先扫出来的就是这个）。
  if (!Number.isFinite(ms) || ms <= 0) return ''
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
