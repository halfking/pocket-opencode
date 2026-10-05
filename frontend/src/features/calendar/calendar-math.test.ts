/**
 * 日历日期数学的回归护栏。
 *
 * Run: node --test src/features/calendar/calendar-math.test.ts
 *
 * 这里的每条用例都对应一种**已经真实错过的写法**。最要紧的是
 * 「东八区跨天」和「美国夏令时切换」两组 —— 它们在 UTC 里算全是绿的。
 */
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import {
  addDays,
  buildMonthGrid,
  DEFAULT_TZ,
  dayKey,
  daysInMonth,
  diffDays,
  endOfLocalDay,
  formatClock,
  fromLocalInputValue,
  localHour,
  localMinute,
  monthKey,
  parseDayKey,
  parseMonthKey,
  shiftMonth,
  startOfLocalDay,
  toLocalInputValue,
  tzOffsetSeconds,
  unixAtLocal,
  unixAtLocalDayKey,
  weekdayOf,
} from './calendar-math.ts'

describe('dayKey：时刻 → 当地那一天', () => {
  it('东八区 23:30 已��是「第二天」', () => {
    // 2026-10-06T15:30:00Z == 2026-10-06 23:30 +08:00
    const unix = Math.floor(Date.UTC(2026, 9, 6, 15, 30, 0) / 1000)
    assert.equal(dayKey(unix, DEFAULT_TZ), '2026-10-06')
  })

  it('东八区 00:30 归当天，而不是前一天（UTC 陷阱）', () => {
    // 2026-10-06T16:30:00Z == 2026-10-07 00:30 +08:00
    const unix = Math.floor(Date.UTC(2026, 9, 6, 16, 30, 0) / 1000)
    // UTC 下这一天是 10-06，本地是 10-07 —— toISOString().slice 会答错。
    assert.equal(new Date(unix * 1000).toISOString().slice(0, 10), '2026-10-06')
    assert.equal(dayKey(unix, DEFAULT_TZ), '2026-10-07')
  })

  it('纽约 20:00 与上海 08:00 是同一天的两种说法', () => {
    const unix = Math.floor(Date.UTC(2026, 9, 7, 0, 0, 0) / 1000)
    assert.equal(dayKey(unix, DEFAULT_TZ), '2026-10-07')
    assert.equal(dayKey(unix, 'America/New_York'), '2026-10-06')
  })

  it('0（未设置）不伪装成 1970 年那一天', () => {
    assert.equal(dayKey(0, DEFAULT_TZ), '')
  })
})

describe('unixAtLocal：当地时刻 → unix（dayKey 的反函数）', () => {
  it('往返一致（东八区全天抽样）', () => {
    for (const hour of [0, 1, 7, 12, 23]) {
      const unix = unixAtLocal(2026, 10, 6, hour, 30, DEFAULT_TZ)
      assert.equal(dayKey(unix, DEFAULT_TZ), '2026-10-06', `hour=${hour}`)
      assert.equal(localHour(unix, DEFAULT_TZ), hour)
      assert.equal(localMinute(unix, DEFAULT_TZ), 30)
    }
  })

  it('东八区 00:00 落回 -08:00 前一天 16:00Z', () => {
    const unix = unixAtLocal(2026, 10, 6, 0, 0, DEFAULT_TZ)
    assert.equal(new Date(unix * 1000).toISOString(), '2026-10-05T16:00:00.000Z')
  })

  it('跨月边界不串日：01-01 00:00 属于一月', () => {
    const unix = unixAtLocal(2027, 1, 1, 0, 0, DEFAULT_TZ)
    assert.equal(dayKey(unix, DEFAULT_TZ), '2027-01-01')
  })

  it('闰年 02-29 存在且当天 23:59 仍归 2 月 29 日', () => {
    assert.equal(daysInMonth(2028, 2), 29)
    const unix = unixAtLocal(2028, 2, 29, 23, 59, DEFAULT_TZ)
    assert.equal(dayKey(unix, DEFAULT_TZ), '2028-02-29')
  })

  it('平年 2 月只有 28 天（不假装有 29 日）', () => {
    assert.equal(daysInMonth(2026, 2), 28)
  })
})

describe('夏令时：偏移量变了也不能把事件挪到别的日子', () => {
  it('纽约 2026-03-08 夏令时开始当天，事件仍属于 3 月 8 日', () => {
    // DST 起始点当天 01:30 与 03:30 都存在；任一时刻的 dayKey 都必须是 03-08。
    for (const [hour, minute] of [[0, 30], [1, 30], [3, 30], [23, 30]] as const) {
      const unix = unixAtLocal(2026, 3, 8, hour, minute, 'America/New_York')
      assert.equal(dayKey(unix, 'America/New_York'), '2026-03-08', `${hour}:${minute}`)
    }
  })

  it('切换前后偏移量确实不同（证明上一条不是恒真）', () => {
    const before = tzOffsetSeconds(unixAtLocal(2026, 3, 7, 12, 0, 'America/New_York'), 'America/New_York')
    const after = tzOffsetSeconds(unixAtLocal(2026, 3, 9, 12, 0, 'America/New_York'), 'America/New_York')
    assert.equal(before, -5 * 3600)
    assert.equal(after, -4 * 3600)
  })

  it('秋季回拨日（纽约 11-01）同样是 25 小时那一天，且两端不丢事件', () => {
    const start = startOfLocalDay('2026-11-01', 'America/New_York')
    const end = endOfLocalDay('2026-11-01', 'America/New_York')
    assert.equal(dayKey(start, 'America/New_York'), '2026-11-01')
    assert.equal(dayKey(end, 'America/New_York'), '2026-11-02')
    // 25 小时的回拨日：固定 24h 的假设会漏掉最后一个小时。
    assert.equal(end - start, 25 * 3600)
  })

  it('春季前跳日（纽约 03-08）只有 23 小时，右开端点仍然正确', () => {
    const start = startOfLocalDay('2026-03-08', 'America/New_York')
    const end = endOfLocalDay('2026-03-08', 'America/New_York')
    assert.equal(end - start, 23 * 3600)
    assert.equal(dayKey(end, 'America/New_York'), '2026-03-09')
  })
})

describe('endOfLocalDay 用右开端点，不用 23:59:59', () => {
  it('范围是半开区间：最后一秒仍在当天内', () => {
    const end = endOfLocalDay('2026-10-06', DEFAULT_TZ)
    const lastSecond = end - 1
    assert.equal(dayKey(lastSecond, DEFAULT_TZ), '2026-10-06')
  })

  it('右开端点本身属于下一天（所以要用 < 而不是 <=）', () => {
    const end = endOfLocalDay('2026-10-06', DEFAULT_TZ)
    assert.equal(dayKey(end, DEFAULT_TZ), '2026-10-07')
  })
})

describe('addDays / diffDays：纯日期运算不受 DST 影响', () => {
  it('跨夏令时切换日 +1 天仍是同一个钟点', () => {
    const next = unixAtLocalDayKey(addDays('2026-03-07', 1), 9, 0, 'America/New_York')
    assert.equal(dayKey(next, 'America/New_York'), '2026-03-08')
    assert.equal(localHour(next, 'America/New_York'), 9)
  })

  it('跨年 +1 天', () => {
    assert.equal(addDays('2026-12-31', 1), '2027-01-01')
    assert.equal(addDays('2027-01-01', -1), '2026-12-31')
  })

  it('闰年 02-28 +1 天是 02-29，平年不是', () => {
    assert.equal(addDays('2028-02-28', 1), '2028-02-29')
    assert.equal(addDays('2026-02-28', 1), '2026-03-01')
  })

  it('diffDays 跨月跨年都对', () => {
    assert.equal(diffDays('2026-10-01', '2026-10-31'), 30)
    assert.equal(diffDays('2026-12-25', '2027-01-05'), 11)
    assert.equal(diffDays('2026-03-01', '2026-03-01'), 0)
    assert.equal(diffDays('2026-03-05', '2026-03-01'), -4)
  })
})

describe('weekdayOf', () => {
  it('2026-10-06 是周二', () => {
    assert.equal(weekdayOf('2026-10-06'), 2)
  })

  it('2026-10-04 是周日', () => {
    assert.equal(weekdayOf('2026-10-04'), 0)
  })
})

describe('buildMonthGrid', () => {
  const grid = buildMonthGrid(2026, 10, '2026-10-06', 1)

  it('固定 42 格，翻月不跳高度', () => {
    assert.equal(grid.length, 42)
  })

  it('首格是周一（weekStart=1）', () => {
    assert.equal(grid[0].dayKey, '2026-09-28')
    assert.equal(weekdayOf(grid[0].dayKey), 1)
  })

  it('相邻月补白标记为 inMonth=false', () => {
    assert.equal(grid[0].inMonth, false)
    assert.equal(grid.filter((c) => c.inMonth).length, 31)
  })

  it('今天被正确标出，且只有一个', () => {
    assert.equal(grid.filter((c) => c.isToday).length, 1)
    assert.equal(grid.find((c) => c.isToday)?.dayKey, '2026-10-06')
  })

  it('周末被标出（周一为首日时是第 6/7 列）', () => {
    const weekend = grid.filter((c) => c.isWeekend)
    assert.equal(weekend.length, 12)
    assert.equal(weekend.every((c) => weekdayOf(c.dayKey) === 0 || weekdayOf(c.dayKey) === 6), true)
  })

  it('网格里的日期连续无缺口无重复', () => {
    const keys = grid.map((c) => c.dayKey)
    assert.equal(new Set(keys).size, 42)
    keys.forEach((key, index) => {
      if (index === 0) return
      assert.equal(diffDays(keys[index - 1], key), 1, `${keys[index - 1]} -> ${key}`)
    })
  })

  it('weekStart=0（周日起）时首格是周日', () => {
    const sundayGrid = buildMonthGrid(2026, 10, '2026-10-06', 0)
    assert.equal(weekdayOf(sundayGrid[0].dayKey), 0)
    assert.equal(sundayGrid[0].dayKey, '2026-09-27')
  })

  it('整月都落在同一格里的月份（2026-02：2/1 是周日）仍满 42 格', () => {
    const feb = buildMonthGrid(2026, 2, '2026-02-14', 1)
    assert.equal(feb.length, 42)
    assert.equal(feb.filter((c) => c.inMonth).length, 28)
    assert.equal(feb.find((c) => c.isToday)?.dayKey, '2026-02-14')
  })
})

describe('shiftMonth 跨年', () => {
  it('12 月 +1 → 下一年 1 月', () => {
    assert.equal(shiftMonth('2026-12', 1), '2027-01')
  })

  it('1 月 -1 → 上一年 12 月', () => {
    assert.equal(shiftMonth('2027-01', -1), '2026-12')
  })

  it('跨两年回退', () => {
    assert.equal(shiftMonth('2026-01', -1), '2025-12')
    assert.equal(shiftMonth('2026-01', -13), '2024-12')
  })

  it('前后可逆', () => {
    assert.equal(shiftMonth(shiftMonth('2026-07', 5), -5), '2026-07')
  })

  it('monthKey 补零到两位', () => {
    assert.equal(monthKey(2026, 3), '2026-03')
    // 深比较：node:assert/strict 里 assert.equal 是引用相等，对象字面量必然不等。
    assert.deepEqual(parseMonthKey('2026-03'), { year: 2026, month: 3 })
  })
})

describe('datetime-local 输入框往返', () => {
  it('unix → 输入框 → unix 保持一致', () => {
    const unix = unixAtLocal(2026, 10, 6, 14, 5, DEFAULT_TZ)
    const value = toLocalInputValue(unix, DEFAULT_TZ)
    assert.equal(value, '2026-10-06T14:05')
    assert.equal(fromLocalInputValue(value, DEFAULT_TZ), unix)
  })

  it('空值往返为 0（表示「未设置」而不是 1970）', () => {
    assert.equal(fromLocalInputValue('', DEFAULT_TZ), 0)
    assert.equal(toLocalInputValue(0, DEFAULT_TZ), '')
  })

  it('跨时区：同一 unix 在上海是 09:00、在纽约是前一天 21:00', () => {
    const unix = unixAtLocal(2026, 10, 6, 9, 0, DEFAULT_TZ)
    assert.equal(toLocalInputValue(unix, 'America/New_York'), '2026-10-05T21:00')
  })
})

describe('formatClock 与 parseDayKey 的边界', () => {
  it('formatClock 用 24 小时制且补零', () => {
    const unix = unixAtLocal(2026, 10, 6, 9, 5, DEFAULT_TZ)
    assert.equal(formatClock(unix, DEFAULT_TZ), '09:05')
    const evening = unixAtLocal(2026, 10, 6, 19, 0, DEFAULT_TZ)
    assert.equal(formatClock(evening, DEFAULT_TZ), '19:00')
  })

  it('formatClock(0) 返回空串而不是 00:00', () => {
    assert.equal(formatClock(0, DEFAULT_TZ), '')
  })

  it('parseDayKey 对畸形输入不抛异常', () => {
    assert.deepEqual(parseDayKey('not-a-date'), { year: 1970, month: 1, day: 1 })
  })
})