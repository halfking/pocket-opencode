/**
 * 日历条目聚合的回归护栏。
 *
 * Run: node --test src/features/calendar/calendar-feed.test.ts
 *
 * 重点护住三类「条目凭空消失 / 凭空多出」的缺陷：半开区间、跨天裁剪、
 * 以及各来源 id 撞车。
 */
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import {
  bucketForDay,
  computeOverdue,
  entriesOn,
  entryId,
  hasConflict,
  hasPending,
  indexByDay,
  layoutDayTimeline,
  overlapsDay,
  type CalendarEntry,
} from './calendar-feed.ts'
import { DEFAULT_TZ, unixAtLocal } from './calendar-math.ts'

const TZ = DEFAULT_TZ

function at(year: number, month: number, day: number, hour: number, minute = 0): number {
  return unixAtLocal(year, month, day, hour, minute, TZ)
}

function entry(over: Partial<CalendarEntry> & { startAt: number }): CalendarEntry {
  return {
    id: 'event:x',
    source: 'event',
    refId: 'x',
    title: '事项',
    endAt: over.startAt + 3600,
    allDay: false,
    ...over,
  }
}

describe('overlapsDay：半开区间决定条目会不会凭空消失', () => {
  it('09:00–10:00 的事件只落在开始那天', () => {
    const e = entry({ startAt: at(2026, 10, 6, 9) })
    assert.equal(overlapsDay(e, '2026-10-06', TZ), true)
    // 10:00 同时是终点和次日 00:00 的邻居，闭区间写法会把它算进 10-07。
    assert.equal(overlapsDay(e, '2026-10-07', TZ), false)
  })

  it('零长条目（任务到期点）仍然命中当天 —— 这是最常见的形态', () => {
    const e = entry({ startAt: at(2026, 10, 6, 9), endAt: at(2026, 10, 6, 9) })
    assert.equal(overlapsDay(e, '2026-10-06', TZ), true)
    assert.equal(overlapsDay(e, '2026-10-05', TZ), false)
  })

  it('跨天条目在首尾两天都命中', () => {
    const e = entry({ startAt: at(2026, 10, 6, 23), endAt: at(2026, 10, 8, 2) })
    assert.equal(overlapsDay(e, '2026-10-06', TZ), true)
    assert.equal(overlapsDay(e, '2026-10-07', TZ), true)
    assert.equal(overlapsDay(e, '2026-10-08', TZ), true)
    assert.equal(overlapsDay(e, '2026-10-05', TZ), false)
    assert.equal(overlapsDay(e, '2026-10-09', TZ), false)
  })

  it('全天条目（00:00 → 次日 00:00）只占一天', () => {
    const e = entry({
      startAt: at(2026, 10, 6, 0),
      endAt: at(2026, 10, 7, 0),
      allDay: true,
    })
    assert.equal(overlapsDay(e, '2026-10-06', TZ), true)
    assert.equal(overlapsDay(e, '2026-10-07', TZ), false)
  })
})

describe('indexByDay：跨天条目摊到每一天，单日条目只进一个桶', () => {
  it('索引的天数 = 事件真正覆盖的天数', () => {
    const entries = [
      entry({ id: 'a', startAt: at(2026, 10, 6, 23), endAt: at(2026, 10, 8, 2) }),
      entry({ id: 'b', startAt: at(2026, 10, 10, 9) }),
    ]
    const index = indexByDay(entries, TZ)
    assert.deepEqual([...index.keys()].sort(), ['2026-10-06', '2026-10-07', '2026-10-08', '2026-10-10'])
  })

  it('单日条目不会被复制到相邻天', () => {
    const index = indexByDay([entry({ id: 'b', startAt: at(2026, 10, 10, 9) })], TZ)
    assert.equal(entriesOn(index, '2026-10-09').length, 0)
    assert.equal(entriesOn(index, '2026-10-11').length, 0)
  })

  it('无数据的日期返回空数组而不是 undefined', () => {
    const index = indexByDay([], TZ)
    assert.deepEqual(entriesOn(index, '2026-10-06'), [])
  })

  it('同一天内：全天在前，计时按开始时间升序', () => {
    const index = indexByDay([
      entry({ id: 't2', title: 'B', startAt: at(2026, 10, 6, 15) }),
      entry({ id: 't1', title: 'A', startAt: at(2026, 10, 6, 9) }),
      entry({
        id: 'a1',
        title: '全天',
        startAt: at(2026, 10, 6, 0),
        endAt: at(2026, 10, 7, 0),
        allDay: true,
      }),
    ], TZ)
    assert.deepEqual(entriesOn(index, '2026-10-06').map((e) => e.id), ['a1', 't1', 't2'])
  })

  it('不同来源的 id 撞车不会互相覆盖', () => {
    // 任务 id 与日程事件 id 恰好都是 '42' —— 没有来源前缀就会丢一条。
    const index = indexByDay([
      entry({ id: entryId('task', '42'), source: 'task', refId: '42', startAt: at(2026, 10, 6, 9) }),
      entry({ id: entryId('event', '42'), source: 'event', refId: '42', startAt: at(2026, 10, 6, 10) }),
    ], TZ)
    assert.equal(entriesOn(index, '2026-10-06').length, 2)
    assert.notEqual(entryId('task', '42'), entryId('event', '42'))
  })
})

describe('bucketForDay', () => {
  it('分全天/计时两栏，total 为两者之和', () => {
    const bucket = bucketForDay([
      entry({
        id: 'a1',
        allDay: true,
        startAt: at(2026, 10, 6, 0),
        endAt: at(2026, 10, 7, 0),
      }),
      entry({ id: 't1', startAt: at(2026, 10, 6, 9) }),
    ], '2026-10-06', TZ)
    assert.equal(bucket.total, 2)
    assert.equal(bucket.allDay.length, 1)
    assert.equal(bucket.timed.length, 1)
    assert.equal(bucket.dayKey, '2026-10-06')
  })

  it('空档期返回空桶', () => {
    const bucket = bucketForDay([], '2026-10-06', TZ)
    assert.equal(bucket.total, 0)
  })

  it('跨天且有具体钟点的条目算 timed（它有时间轴位置）', () => {
    const bucket = bucketForDay(
      [entry({ startAt: at(2026, 10, 6, 23), endAt: at(2026, 10, 8, 2) })],
      '2026-10-06',
      TZ,
    )
    assert.equal(bucket.allDay.length, 0)
    assert.equal(bucket.timed.length, 1)
  })
})

describe('layoutDayTimeline：跨天条目的显示时长被裁到当天边界', () => {
  it('23:00 → 次日 02:00 在首日画 1 小时，不是 3 小时', () => {
    const slots = layoutDayTimeline(
      [entry({ startAt: at(2026, 10, 6, 23), endAt: at(2026, 10, 7, 2) })],
      '2026-10-06',
      TZ,
    )
    assert.equal(slots.length, 1)
    assert.equal(slots[0].topMinutes, 23 * 60)
    assert.equal(slots[0].heightMinutes, 60)
  })

  it('在次日画 2 小时（02:00 收尾）', () => {
    const slots = layoutDayTimeline(
      [entry({ startAt: at(2026, 10, 6, 23), endAt: at(2026, 10, 7, 2) })],
      '2026-10-07',
      TZ,
    )
    assert.equal(slots[0].topMinutes, 0)
    assert.equal(slots[0].heightMinutes, 120)
  })

  it('全天条目不进时间轴', () => {
    const slots = layoutDayTimeline(
      [entry({ allDay: true, startAt: at(2026, 10, 6, 0), endAt: at(2026, 10, 7, 0) })],
      '2026-10-06',
      TZ,
    )
    assert.equal(slots.length, 0)
  })

  it('最短高度保底 15 分钟，短条目不会被压成一条线', () => {
    const slots = layoutDayTimeline(
      [entry({ startAt: at(2026, 10, 6, 9), endAt: at(2026, 10, 6, 9, 1) })],
      '2026-10-06',
      TZ,
    )
    assert.equal(slots[0].heightMinutes, 15)
  })

  it('零长条目按 1 小时处理，不产生零高度', () => {
    const slots = layoutDayTimeline(
      [entry({ startAt: at(2026, 10, 6, 9), endAt: at(2026, 10, 6, 9) })],
      '2026-10-06',
      TZ,
    )
    assert.equal(slots[0].heightMinutes, 60)
  })

  it('条目按开始时间升序', () => {
    const slots = layoutDayTimeline(
      [
        entry({ id: 'b', startAt: at(2026, 10, 6, 15) }),
        entry({ id: 'a', startAt: at(2026, 10, 6, 9) }),
      ],
      '2026-10-06',
      TZ,
    )
    assert.deepEqual(slots.map((s) => s.entry.id), ['a', 'b'])
  })
})

describe('hasConflict：相邻不算冲突', () => {
  const a = entry({ startAt: at(2026, 10, 6, 9), endAt: at(2026, 10, 6, 10) })

  it('10:00 结束 / 10:00 开始是背靠背，不是撞车', () => {
    const b = entry({ startAt: at(2026, 10, 6, 10), endAt: at(2026, 10, 6, 11) })
    assert.equal(hasConflict(a, b), false)
  })

  it('真正重叠返回 true', () => {
    const b = entry({ startAt: at(2026, 10, 6, 9, 30), endAt: at(2026, 10, 6, 11) })
    assert.equal(hasConflict(a, b), true)
  })

  it('全天条目不参与冲突判定', () => {
    const allDay = entry({ allDay: true, startAt: at(2026, 10, 6, 0), endAt: at(2026, 10, 7, 0) })
    assert.equal(hasConflict(a, allDay), false)
  })

  it('零长到期点不算冲突（否则每条任务都「撞车」）', () => {
    const due = entry({ startAt: at(2026, 10, 6, 9, 30), endAt: at(2026, 10, 6, 9, 30) })
    assert.equal(hasConflict(a, due), false)
  })
})

describe('computeOverdue / hasPending', () => {
  const now = at(2026, 10, 6, 12)

  it('未完成且已过截止时刻 → 逾期', () => {
    assert.equal(computeOverdue(entry({ startAt: at(2026, 10, 6, 9) }), now), true)
  })

  it('已完成不算逾期', () => {
    assert.equal(computeOverdue(entry({ startAt: at(2026, 10, 6, 9), done: true }), now), false)
  })

  it('还没到不算逾期', () => {
    assert.equal(computeOverdue(entry({ startAt: at(2026, 10, 6, 15) }), now), false)
  })

  it('hasPending：当天有未完成条目即 true', () => {
    assert.equal(hasPending([entry({ startAt: at(2026, 10, 6, 9), done: true })]), false)
    assert.equal(hasPending([entry({ startAt: at(2026, 10, 6, 9) })]), true)
  })
})