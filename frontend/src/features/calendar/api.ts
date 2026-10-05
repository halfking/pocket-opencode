/**
 * 日历 HTTP 客户端。构建在共享 http() 之上，鉴权头与 401 续期行为与全仓一致。
 */
import { http } from '../../api/http'
import type {
  CalendarEntry,
  CalendarEvent,
  CalendarEventInput,
  CalendarFeedResponse,
} from './types'

const base = '/api/calendar/events'

/** 服务端可能直接返回数组，也可能返回信封；两种都要能吃下。 */
function unwrapEntries(body: unknown): CalendarEntry[] {
  if (Array.isArray(body)) return body as CalendarEntry[]
  if (body && typeof body === 'object' && Array.isArray((body as { entries?: unknown }).entries)) {
    return (body as { entries: CalendarEntry[] }).entries
  }
  return []
}

export const calendarApi = {
  /** 取 [from, to) 的统一 feed（日程 + 任务截止 + 定时任务）。 */
  async feed(from: number, to: number): Promise<CalendarEntry[]> {
    const params = new URLSearchParams({ from: String(from), to: String(to) })
    const body = await http<unknown>(`${base}?${params.toString()}`)
    return unwrapEntries(body)
  },

  async create(input: CalendarEventInput): Promise<CalendarEvent> {
    return http<CalendarEvent>(base, { method: 'POST', body: JSON.stringify(input) })
  },

  async update(id: string, input: CalendarEventInput): Promise<CalendarEvent> {
    return http<CalendarEvent>(`${base}/${encodeURIComponent(id)}`, {
      method: 'PATCH',
      body: JSON.stringify(input),
    })
  },

  async remove(id: string): Promise<void> {
    await http(`${base}/${encodeURIComponent(id)}`, { method: 'DELETE' })
  },
}

export type { CalendarFeedResponse }