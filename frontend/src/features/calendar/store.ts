/**
 * 日历状态。
 *
 * 只存**条目**和**当前视图窗口**，不存派生出来的月宫格：宫格是纯函数
 * （calendar-math.buildMonthGrid）的输出，存进 store 就多了一份会与「今天」
 * 漂移的真相。视图自己算，store 只负责数据。
 */
import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { calendarApi } from './api'
import type { CalendarEntry, CalendarEventInput, CalendarSource } from './types'
import { CALENDAR_SOURCES } from './types'
import { deviceTimeZone, type DayKey } from './calendar-math.ts'
import { overlapsDay } from './calendar-feed.ts'

export const useCalendarStore = defineStore('calendar', () => {
  const entries = ref<CalendarEntry[]>([])
  const loading = ref(false)
  const saving = ref(false)
  /**
   * 原始异常对象，**不要**在这里转成字符串。
   *
   * check:raw-error 卡口禁止把 e.message 直接上屏（红米真机曾把
   * `rss_unavailable: store not configured` 渲染给用户）。本仓的做法是
   * store 原样保留异常，由视图用 apiError(err, 'errors.xxx') 映射成人话。
   */
  const error = ref<unknown>(null)

  /** 当前取数窗口 [from, to)。翻月时改这里。 */
  const windowFrom = ref(0)
  const windowTo = ref(0)

  /** 隐藏的来源。空数组 = 全部显示。 */
  const hiddenSources = ref<CalendarSource[]>([])

  /** 用户所在时区。取不到时回落默认时区（日历仍可用）。 */
  const timeZone = ref(deviceTimeZone())

  /** 可见条目 = 去掉被隐藏来源后的集合。 */
  const visibleEntries = computed(() => {
    if (!hiddenSources.value.length) return entries.value
    const hidden = new Set(hiddenSources.value)
    return entries.value.filter((e) => !hidden.has(e.source))
  })

  /** 某一天的条目数，用于月格角标。跨天条目在它覆盖的每一天都算一次。 */
  function countOn(day: DayKey, source?: CalendarSource): number {
    return visibleEntries.value.filter((entry) => {
      if (source && entry.source !== source) return false
      return overlapsDay(entry, day, timeZone.value)
    }).length
  }

  function toggleSource(source: CalendarSource): void {
    const current = hiddenSources.value
    hiddenSources.value = current.includes(source)
      ? current.filter((s) => s !== source)
      : [...current, source]
  }

  function isSourceVisible(source: CalendarSource): boolean {
    return !hiddenSources.value.includes(source)
  }

  /** 加载窗口。窗口没变时不重复请求，避免翻回来时闪烁。 */
  async function load(from: number, to: number, force = false): Promise<void> {
    if (!force && from === windowFrom.value && to === windowTo.value && entries.value.length) {
      return
    }
    loading.value = true
    error.value = null
    try {
      const rows = await calendarApi.feed(from, to)
      entries.value = rows
      windowFrom.value = from
      windowTo.value = to
    } catch (e) {
      error.value = e
    } finally {
      loading.value = false
    }
  }

  async function createEvent(input: CalendarEventInput): Promise<boolean> {
    saving.value = true
    error.value = null
    try {
      await calendarApi.create(input)
      // 重取而不是本地插入：事件可能落在当前窗口之外，且服务端会补默认值
      // （id / createdAt / 时区），本地插入会让下一次刷新前后不一致。
      await load(windowFrom.value, windowTo.value, true)
      return true
    } catch (e) {
      error.value = e
      return false
    } finally {
      saving.value = false
    }
  }

  async function updateEvent(id: string, input: CalendarEventInput): Promise<boolean> {
    saving.value = true
    error.value = null
    try {
      await calendarApi.update(id, input)
      await load(windowFrom.value, windowTo.value, true)
      return true
    } catch (e) {
      error.value = e
      return false
    } finally {
      saving.value = false
    }
  }

  async function deleteEvent(id: string): Promise<boolean> {
    saving.value = true
    error.value = null
    try {
      await calendarApi.remove(id)
      await load(windowFrom.value, windowTo.value, true)
      return true
    } catch (e) {
      error.value = e
      return false
    } finally {
      saving.value = false
    }
  }

  function clearError(): void {
    error.value = null
  }

  return {
    entries,
    loading,
    saving,
    error,
    windowFrom,
    windowTo,
    hiddenSources,
    timeZone,
    visibleEntries,
    allSources: CALENDAR_SOURCES,
    countOn,
    toggleSource,
    isSourceVisible,
    load,
    createEvent,
    updateEvent,
    deleteEvent,
    clearError,
  }
})