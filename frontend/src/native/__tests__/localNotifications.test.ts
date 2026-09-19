/**
 * localNotifications — 闪卡每日复习提醒封装 smoke test。
 *
 * 不依赖真实 Android 设备：直接断言 buildDailyReviewPayload 产生的
 * @capacitor/local-notifications 载荷是否符合契约。
 *
 * 使用 node:test 以与本仓库其他单测一致（biometric-errors.test.ts 等）。
 */
import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  DAILY_REVIEW_ID,
  buildDailyReviewPayload,
  notificationIdFor,
  type DailyReviewPayload,
} from '../localNotifications.ts'

test('DAILY_REVIEW_ID is a positive int32 to satisfy the plugin limit', () => {
  assert.ok(Number.isInteger(DAILY_REVIEW_ID))
  assert.ok(DAILY_REVIEW_ID > 0)
  assert.ok(DAILY_REVIEW_ID <= 0x7fff_ffff, 'must fit in signed int32')
})

test('scheduleDailyReview payload — Android + exact alarm granted', () => {
  const payload: DailyReviewPayload = buildDailyReviewPayload({
    hour: 9,
    minute: 30,
    dueCount: 42,
    onAndroid: true,
    exactDenied: false,
  })

  assert.equal(payload.id, DAILY_REVIEW_ID)
  assert.equal(payload.title, '复习时间到')
  assert.match(payload.body, /42/, 'body must interpolate dueCount')
  assert.match(payload.body, /卡片待复习/, 'body must be in the agreed Chinese copy')

  assert.equal(payload.schedule.repeats, true)
  assert.equal(payload.schedule.on.hour, 9)
  assert.equal(payload.schedule.on.minute, 30)

  assert.equal(payload.isExactNotification, true)
  assert.equal(payload.schedule.allowWhileIdle, undefined, 'exact alarm does not need allowWhileIdle fallback')

  assert.deepEqual(payload.extra, { deepLink: '/flashcards/review' })
})

test('scheduleDailyReview payload — Android + SCHEDULE_EXACT_ALARM denied → inexact fallback', () => {
  const payload = buildDailyReviewPayload({
    hour: 20,
    minute: 0,
    dueCount: 5,
    onAndroid: true,
    exactDenied: true,
  })

  assert.equal(payload.isExactNotification, false)
  assert.equal(payload.schedule.allowWhileIdle, false, 'inexact alarm must respect Doze batching')

  assert.equal(payload.schedule.on.hour, 20)
  assert.equal(payload.schedule.on.minute, 0)
  assert.match(payload.body, /5/)
})

test('scheduleDailyReview payload — iOS path omits isExactNotification (Android-only field)', () => {
  const payload = buildDailyReviewPayload({
    hour: 8,
    minute: 15,
    dueCount: 7,
    onAndroid: false,
    exactDenied: false,
  })

  assert.equal(payload.isExactNotification, undefined)
  assert.equal(payload.schedule.allowWhileIdle, undefined)
  assert.equal(payload.schedule.on.hour, 8)
  assert.equal(payload.schedule.on.minute, 15)
  assert.match(payload.body, /7/)
})

test('notificationIdFor is stable and positive-int32', () => {
  const a = notificationIdFor('daily-review:9:30')
  const b = notificationIdFor('daily-review:9:30')
  const c = notificationIdFor('daily-review:21:0')
  assert.equal(a, b, 'same seed must produce same id')
  assert.notEqual(a, c, 'different seed should usually produce different id')
  assert.ok(a > 0)
  assert.ok(a <= 0x7fff_ffff)
})