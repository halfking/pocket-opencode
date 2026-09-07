/**
 * 人话执行计划 ↔ cron/interval/at。
 * Run: node --test src/features/scheduled-tasks/schedule-plan.test.ts
 */
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import {
  defaultSchedulePlan,
  describeSchedule,
  encodeSchedule,
  parseSchedule,
} from './schedule-plan.ts'

describe('encodeSchedule', () => {
  it('one-shot becomes RFC3339 at in Asia/Shanghai', () => {
    const encoded = encodeSchedule({
      ...defaultSchedulePlan(),
      mode: 'once',
      date: '2026-09-10',
      time: '09:30',
    })
    assert.deepEqual(encoded, {
      scheduleKind: 'at',
      scheduleExpr: '2026-09-10T09:30:00+08:00',
    })
  })

  it('weekdays 09:00 becomes cron 0 9 * * 1-5', () => {
    const encoded = encodeSchedule({
      ...defaultSchedulePlan(),
      mode: 'repeat',
      repeatKind: 'weekdays',
      time: '09:00',
    })
    assert.deepEqual(encoded, { scheduleKind: 'cron', scheduleExpr: '0 9 * * 1-5' })
  })

  it('daily 18:05 becomes cron 5 18 * * *', () => {
    const encoded = encodeSchedule({
      ...defaultSchedulePlan(),
      mode: 'repeat',
      repeatKind: 'daily',
      time: '18:05',
    })
    assert.deepEqual(encoded, { scheduleKind: 'cron', scheduleExpr: '5 18 * * *' })
  })

  it('weekly Mon+Wed becomes cron weekday list', () => {
    const encoded = encodeSchedule({
      ...defaultSchedulePlan(),
      mode: 'repeat',
      repeatKind: 'weekly',
      weekdays: [1, 3],
      time: '09:00',
    })
    assert.deepEqual(encoded, { scheduleKind: 'cron', scheduleExpr: '0 9 * * 1,3' })
  })

  it('monthly day 1 becomes cron DOM', () => {
    const encoded = encodeSchedule({
      ...defaultSchedulePlan(),
      mode: 'repeat',
      repeatKind: 'monthly',
      monthDay: 1,
      time: '09:00',
    })
    assert.deepEqual(encoded, { scheduleKind: 'cron', scheduleExpr: '0 9 1 * *' })
  })

  it('interval minutes/hours/days map to Go duration', () => {
    assert.deepEqual(
      encodeSchedule({
        ...defaultSchedulePlan(),
        mode: 'repeat',
        repeatKind: 'interval',
        intervalValue: 30,
        intervalUnit: 'm',
      }),
      { scheduleKind: 'interval', scheduleExpr: '30m' },
    )
    assert.deepEqual(
      encodeSchedule({
        ...defaultSchedulePlan(),
        mode: 'repeat',
        repeatKind: 'interval',
        intervalValue: 6,
        intervalUnit: 'h',
      }),
      { scheduleKind: 'interval', scheduleExpr: '6h' },
    )
    assert.deepEqual(
      encodeSchedule({
        ...defaultSchedulePlan(),
        mode: 'repeat',
        repeatKind: 'interval',
        intervalValue: 2,
        intervalUnit: 'd',
      }),
      { scheduleKind: 'interval', scheduleExpr: '48h' },
    )
  })
})

describe('parseSchedule', () => {
  it('round-trips the default weekday morning plan', () => {
    const plan = parseSchedule('cron', '0 9 * * 1-5')
    assert.equal(plan.mode, 'repeat')
    assert.equal(plan.repeatKind, 'weekdays')
    assert.equal(plan.time, '09:00')
    assert.equal(plan.custom, false)
  })

  it('parses RFC3339 one-shot into date and time', () => {
    const plan = parseSchedule('at', '2026-09-10T09:30:00+08:00')
    assert.equal(plan.mode, 'once')
    assert.equal(plan.date, '2026-09-10')
    assert.equal(plan.time, '09:30')
    assert.equal(plan.custom, false)
  })

  it('marks unknown cron as custom fallback', () => {
    const plan = parseSchedule('cron', '*/7 3 2 4 1')
    assert.equal(plan.custom, true)
    assert.equal(plan.customExpr, '*/7 3 2 4 1')
    assert.equal(plan.customKind, 'cron')
  })

  it('parses 48h interval as every 2 days', () => {
    const plan = parseSchedule('interval', '48h')
    assert.equal(plan.repeatKind, 'interval')
    assert.equal(plan.intervalValue, 2)
    assert.equal(plan.intervalUnit, 'd')
    assert.equal(plan.custom, false)
  })
})

describe('describeSchedule', () => {
  it('uses everyday Chinese, not cron jargon', () => {
    assert.equal(
      describeSchedule(parseSchedule('cron', '0 9 * * 1-5')),
      '工作日 09:00',
    )
    assert.equal(
      describeSchedule(parseSchedule('cron', '0 9 * * 1,3')),
      '每周一、三 09:00',
    )
    assert.equal(
      describeSchedule(parseSchedule('at', '2026-09-10T09:30:00+08:00')),
      '2026-09-10 09:30 执行一次',
    )
    assert.equal(
      describeSchedule(parseSchedule('interval', '30m')),
      '每隔 30 分钟',
    )
    assert.match(describeSchedule(parseSchedule('cron', '*/7 3 2 4 1')), /自定义/)
  })
})
