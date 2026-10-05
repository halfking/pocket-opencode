/**
 * 月视图呈现层逻辑的回归护栏。
 *
 * Run: node --test src/features/calendar/calendar-view.test.ts
 *
 * 第一组用例（跨天条目占几格）对应一个**已经写错并被修掉**的缺陷：
 * 组件里的 chipOn 对计时条目只比 `entryDayKey === day`，于是三天的会议
 * 在月视图上只出现在第一天，第二天第三天是空的 —— 而月视图正是用来
 * 「一眼看出哪天忙」的。
 */
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import {
  chipTimeLabel,
  chipsForDay,
  countOnDay,
  dayTitle,
  fetchWindowForMonth,
  monthTitle,
  resolveSelectedDay,
  weekdayLabels,
} from './calendar-view.ts'
import { dayKey, shiftMonth, unixAtLocal } from './calendar-math.ts'
import type { CalendarEntry } from './types.ts'

const TZ = 'Asia/Shanghai'

function at(year: number, month: number, day: number, hour: number, minute = 0): number {
  return unixAtLocal(year, month, day, hour, minute, TZ)
}

function entry(over: Partial<CalendarEntry> & { startAt: number }): CalendarEntry {
  return {
    id: 'e', source: 'event', refId: 'e', title: 'x',
    endAt: over.startAt + 3600, allDay: false, timezone: TZ, ...over,
  }
}

describe('chipsForDay：跨天条目覆盖它真正占用的每一天', () => {
  it('三天的会议在三天里都出现', () => {
    const conf = entry({ id: 'c', startAt: at(2026, 10, 6, 9), endAt: at(2026, 10, 9, 17) })
    for (const day of ['2026-10-06', '2026-10-07', '2026-10-08', '2026-10-09']) {
      assert.equal(countOnDay([conf], day, TZ), 1, `${day} 应显示会议色块`)
    }
    assert.equal(countOnDay([conf], '2026-10-05', TZ), 0)
    assert.equal(countOnDay([conf], '2026-10-10', TZ), 0)
  })

  it('结束时刻正好落在某天 00:00 时，那天不占位（半开区间）', () => {
    // 会议到 10-09 00:00 结束 ⇒ 09 日不再显示。用闭区间会多占一格。
    const conf = entry({ id: 'c2', startAt: at(2026, 10, 6, 9), endAt: at(2026, 10, 9, 0) })
    assert.equal(countOnDay([conf], '2026-10-08', TZ), 1)
    assert.equal(countOnDay([conf], '2026-10-09', TZ), 0)
  })

  it('跨天条目不会在相邻天重复出现（半开区间）', () => {
    // 09:00–10:00 的单日事件：闭区间写法会让它在第二天也出现。
    const e = entry({ startAt: at(2026, 10, 6, 9), endAt: at(2026, 10, 6, 10) })
    assert.equal(countOnDay([e], '2026-10-06', TZ), 1)
    assert.equal(countOnDay([e], '2026-10-07', TZ), 0)
  })

  it('零长到期点仍然占当天（任务最常见形态）', () => {
    const due = entry({ startAt: at(2026, 10, 6, 9), endAt: at(2026, 10, 6, 9) })
    assert.equal(countOnDay([due], '2026-10-06', TZ), 1)
  })

  it('全天条目只占一天', () => {
    const allDay = entry({
      startAt: at(2026, 10, 6, 0), endAt: at(2026, 10, 7, 0), allDay: true,
    })
    assert.equal(countOnDay([allDay], '2026-10-06', TZ), 1)
    assert.equal(countOnDay([allDay], '2026-10-07', TZ), 0)
  })

  it('超出的条目折叠成 +N，且不重复计数', () => {
    const many = Array.from({ length: 5 }, (_, i) =>
      entry({ id: `e${i}`, startAt: at(2026, 10, 6, 9 + i) }),
    )
    const { visible, overflow } = chipsForDay(many, '2026-10-06', TZ, 3)
    assert.equal(visible.length, 3)
    assert.equal(overflow, 2)
    assert.equal(visible.length + overflow, 5, 'visible + overflow 必须等于总数')
  })

  it('未超限时 overflow 为 0，不出现「+0」', () => {
    const one = [entry({ startAt: at(2026, 10, 6, 9) })]
    const { visible, overflow } = chipsForDay(one, '2026-10-06', TZ, 3)
    assert.equal(overflow, 0)
    assert.equal(visible.length, 1)
  })

  it('空数组不炸', () => {
    const { visible, overflow } = chipsForDay([], '2026-10-06', TZ, 3)
    assert.deepEqual(visible, [])
    assert.equal(overflow, 0)
  })
})

describe('fetchWindowForMonth：窗口必须覆盖整张 42 格宫格', () => {
  it('10 月的窗口从 9-28 开始、到 11-08 结束（周一为周首）', () => {
    const { from, to, grid } = fetchWindowForMonth('2026-10', TZ, '2026-10-06')
    assert.equal(grid.length, 42)
    assert.equal(grid[0].dayKey, '2026-09-28')
    assert.equal(grid[41].dayKey, '2026-11-08')
    assert.equal(dayKey(from, TZ), '2026-09-28')
    assert.equal(dayKey(to, TZ), '2026-11-09', '右开端点是最后一天的次日 00:00')
  })

  it('窗口跨天条目不会在翻月后消失', () => {
    // 一条 10-31 到 11-02 的事件，窗口必须同时覆盖两天。
    const spanning = entry({ startAt: at(2026, 10, 31, 20), endAt: at(2026, 11, 2, 10) })
    const win = fetchWindowForMonth('2026-10', TZ, '2026-10-06')
    const inWindow = spanning.startAt < win.to && spanning.endAt > win.from
    assert.equal(inWindow, true, '跨月事件应完整落在取数窗口内')
  })

  it('只取本月会漏掉补白格 —— 这就是窗口要取 42 格的原因', () => {
    const win = fetchWindowForMonth('2026-10', TZ, '2026-10-06')
    assert.ok(win.from < at(2026, 10, 1, 0), '窗口必须早于本月 1 号')
    assert.ok(win.to > at(2026, 10, 31, 23), '窗口必须晚于本月最后一天')
  })
})

describe('resolveSelectedDay：点补白格要跟着翻月', () => {
  const grid = fetchWindowForMonth('2026-10', TZ, '2026-10-06').grid

  it('点本月内的格子不翻月', () => {
    const r = resolveSelectedDay('2026-10-15', '2026-10', grid)
    assert.equal(r.selectedDay, '2026-10-15')
    assert.equal(r.month, '2026-10')
  })

  it('点上月补白格翻到 9 月（否则点了像没反应）', () => {
    const r = resolveSelectedDay('2026-09-28', '2026-10', grid)
    assert.equal(r.month, '2026-09')
    assert.equal(r.selectedDay, '2026-09-28')
  })

  it('点下月补白格翻到 11 月', () => {
    const r = resolveSelectedDay('2026-11-08', '2026-10', grid)
    assert.equal(r.month, '2026-11')
  })

  it('翻过去的月份再翻回来，窗口能重新覆盖新月份', () => {
    const nov = fetchWindowForMonth('2026-11', TZ, '2026-10-06')
    assert.equal(nov.grid[0].dayKey, '2026-10-26')
    assert.equal(nov.grid[41].dayKey, '2026-12-06')
  })
})

describe('标题与表头本地化', () => {
  it('月标题随 locale 变化', () => {
    assert.equal(monthTitle('2026-10', 'en-US'), 'October 2026')
    assert.match(monthTitle('2026-10', 'zh-CN'), /2026/)
  })

  it('weekdayLabels 随周首旋转且长度为 7', () => {
    const monday = weekdayLabels('en-US', 1)
    const sunday = weekdayLabels('en-US', 0)
    assert.equal(monday.length, 7)
    assert.equal(sunday.length, 7)
    assert.notDeepEqual(monday, sunday)
    assert.equal(monday[0], 'Mon')
    assert.equal(sunday[0], 'Sun')
  })

  it('表头在东八区不会整体偏一天（锚点用 UTC 推进）', () => {
    // 若锚点改用本地午夜，负时区下第一列可能退成上一周。
    const labels = weekdayLabels('en-US', 1)
    assert.equal(labels[0], 'Mon')
    assert.equal(labels[6], 'Sun')
  })

  it('dayTitle 不受运行时时区影响', () => {
    // 12:00 UTC 构造，任何时区下都还是同一天。
    assert.match(dayTitle('2026-10-06', 'en-US'), /6/)
  })

  it('畸形日期不抛异常', () => {
    assert.doesNotThrow(() => dayTitle('garbage', 'en-US'))
    assert.doesNotThrow(() => monthTitle('garbage', 'en-US'))
  })
})

describe('chipTimeLabel', () => {
  const labels = { allDay: '全天', dueBy: '截止' }

  it('全天条目标「全天」', () => {
    const e = entry({ allDay: true, startAt: at(2026, 10, 6, 0), endAt: at(2026, 10, 7, 0) })
    assert.equal(chipTimeLabel(e, TZ, labels), '全天')
  })

  it('计时条目显示 HH:mm', () => {
    const e = entry({ startAt: at(2026, 10, 6, 14, 5) })
    assert.equal(chipTimeLabel(e, TZ, labels), '14:05')
  })

  it('缺开始时间时回落「截止」而不是 00:00', () => {
    assert.equal(chipTimeLabel(entry({ startAt: 0, endAt: 0 }), TZ, labels), '截止')
  })
})

describe('shiftMonth 仍能驱动连续翻月', () => {
  it('10 → 11 → 12 → 1', () => {
    let m = '2026-10'
    const seen: string[] = [m]
    for (let i = 0; i < 3; i += 1) {
      m = shiftMonth(m, 1)
      seen.push(m)
    }
    assert.deepEqual(seen, ['2026-10', '2026-11', '2026-12', '2027-01'])
  })
})
