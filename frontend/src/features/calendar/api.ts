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

/**
 * 解包 feed 响应体，**把降级信号一起带出来**（§118）。
 *
 * ⚠ 抽成纯函数且导出，不是为了整洁，是为了**可测**：
 *   `feed()` 自带 I/O，而「partial 有没有活过解包」是纯数据变换 ——
 *   源码扫描式判据量不到它（把 `=== true` 改成别的写法照样绿）。
 *   与 api/meetings.ts 的 normalizeSummary 同一理由。
 *
 * ⚠ 裸数组（`Array.isArray(body)`）那条分支**没有 partial 可言**，
 *   返回 false 而不是 undefined：那是「服务端根本没走信封」，
 *   不是「服务端说它没降级」。旧服务端没这个键时同理按 false 处理，
 *   因为旧服务端根本不会部分降级。
 */
export function unwrapFeed(body: unknown): { entries: CalendarEntry[]; partial: boolean } {
  return { entries: unwrapEntries(body), partial: (body as { partial?: unknown })?.partial === true }
}

export const calendarApi = {
  /**
   * 取 [from, to) 的统一 feed（日程 + 任务截止 + 定时任务）。
   *
   * 返回形状在 §118 变了：以前是裸数组，现在是 `{entries, partial}`。
   * 全仓只有一个调用方（store.ts 的 load），已同步。
   */
  async feed(from: number, to: number): Promise<{ entries: CalendarEntry[]; partial: boolean }> {
    const params = new URLSearchParams({ from: String(from), to: String(to) })
    const body = await http<unknown>(`${base}?${params.toString()}`)
    return unwrapFeed(body)
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