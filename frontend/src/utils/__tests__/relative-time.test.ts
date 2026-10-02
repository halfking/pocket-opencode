/**
 * relative-time.test.ts — 列表行相对时间与会议时长的纯函数测试。
 *
 * 运行：cd frontend && node --experimental-strip-types --test \
 *        src/utils/__tests__/relative-time.test.ts
 *
 * 背景：仓库里此前有三份各自硬编码中文的"X 分钟前"（NoteCard / EmailCard /
 * SessionCard）。本轮把两个新 Hub 接进来时不能再复制第四份，于是抽了这个
 * 带 i18n 的公共实现。用例重点锁三类容易错的地方：单位换算的边界、
 * 未来时间（不能显示「-3 分钟前」）、以及 7 天后回退到月/日的宽度。
 */
import { strict as assert } from 'node:assert'
import { describe, it } from 'node:test'

import { formatDuration, formatRelative } from '../relative-time.ts'

/** 假翻译：把 key 直接变成可断言的串，省掉对 vue-i18n 的依赖。 */
const t = (key: string, named?: Record<string, unknown>) =>
  named ? `${key}:${JSON.stringify(named)}` : key

const NOW_MS = 1_700_000_000_000
const nowSec = Math.floor(NOW_MS / 1000)

describe('formatRelative', () => {
  it('60 秒内 → justNow', () => {
    assert.equal(formatRelative(nowSec - 5, t, NOW_MS), 'timefmt.justNow')
    assert.equal(formatRelative(nowSec - 59, t, NOW_MS), 'timefmt.justNow')
  })

  it('恰好 60 秒算「1 分钟前」而不是「刚刚」', () => {
    // 边界值：写成 `<` 而不是 `<=` 会让 59s→1min、60s→justNow 出现 1 分钟错位。
    assert.equal(formatRelative(nowSec - 60, t, NOW_MS), 'timefmt.minutesAgo:{"count":1}')
  })

  it('分钟 / 小时 / 天 的进位正确', () => {
    assert.equal(formatRelative(nowSec - 59 * 60, t, NOW_MS), 'timefmt.minutesAgo:{"count":59}')
    assert.equal(formatRelative(nowSec - 60 * 60, t, NOW_MS), 'timefmt.hoursAgo:{"count":1}')
    assert.equal(formatRelative(nowSec - 23 * 60 * 60, t, NOW_MS), 'timefmt.hoursAgo:{"count":23}')
    assert.equal(formatRelative(nowSec - 24 * 60 * 60, t, NOW_MS), 'timefmt.daysAgo:{"count":1}')
    assert.equal(formatRelative(nowSec - 6 * 24 * 60 * 60, t, NOW_MS), 'timefmt.daysAgo:{"count":6}')
  })

  it('未来时间按「刚刚」处理，不显示负数', () => {
    // 服务器时钟超前 / nextDueAt 这类场景。显示「-3 分钟前」对用户没有意义。
    assert.equal(formatRelative(nowSec + 180, t, NOW_MS), 'timefmt.justNow')
  })

  it('7 天以上回退到「月/日」，宽度可控', () => {
    const out = formatRelative(nowSec - 30 * 24 * 60 * 60, t, NOW_MS)
    assert.match(out, /^\d{1,2}\/\d{1,2}$/)
  })

  it('0 / 负数时间戳返回空串（不渲染「刚刚」骗人）', () => {
    // 缺失时间戳 ≠ 刚刚发生。LocalNote.createdAt 在导入路径上可能是 0。
    assert.equal(formatRelative(0, t, NOW_MS), '')
    assert.equal(formatRelative(-1, t, NOW_MS), '')
  })
})

describe('formatDuration', () => {
  it('不足 1 小时 → M:SS', () => {
    assert.equal(formatDuration(0), '')
    assert.equal(formatDuration(5_000), '0:05')
    assert.equal(formatDuration(65_000), '1:05')
    assert.equal(formatDuration(59 * 60_000 + 59_000), '59:59')
  })

  it('满 1 小时 → H:MM:SS，且秒位补零', () => {
    assert.equal(formatDuration(60 * 60_000), '1:00:00')
    assert.equal(formatDuration(3661_000), '1:01:01')
  })

  it('负数 / 非有限值返回空串', () => {
    assert.equal(formatDuration(-1), '')
    assert.equal(formatDuration(NaN), '')
  })
})
