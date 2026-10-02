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

import { formatDuration, formatRelative, toEpochSeconds } from '../relative-time.ts'

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

describe('toEpochSeconds', () => {
  // 这组用例是审计发现的真实缺陷的钉子。
  //
  // 本地库的 *_at 单位不统一：LocalNote.updatedAt / LocalMeeting.startedAt /
  // PkmNote.updatedAt / LocalEmail.date 都是**毫秒**（写入用 Date.now()），
  // 而 Notification.created_at 与 RSS 的解析结果是**秒**。
  // 混进 formatRelative 时 diff 恒为大负数 → 每一行都显示「刚刚」；
  // 混进排序时毫秒项恒大于秒项 → 「统一时间线」变成按来源分组的假时间线。
  // 两种错都**不报错**，只是看起来不对，是最难被验收抓到的一类。
  const NOW_SEC = 1_700_000_000
  const NOW_MS = NOW_SEC * 1000

  it('毫秒 → 秒（笔记 / 会议 / PKM / 邮件四个来源）', () => {
    assert.equal(toEpochSeconds(NOW_MS), NOW_SEC)
  })

  it('秒 → 秒，原样（任务消息 / RSS 两个来源）', () => {
    assert.equal(toEpochSeconds(NOW_SEC), NOW_SEC)
  })

  it('同一真实时刻的毫秒与秒表示归一后相等 —— 这正是排序能混排的前提', () => {
    assert.equal(toEpochSeconds(NOW_MS), toEpochSeconds(NOW_SEC))
  })

  it('归一后 formatRelative 对两种单位给出一致结果', () => {
    const t = (k: string, n?: Record<string, unknown>) => (n ? `${k}:${JSON.stringify(n)}` : k)
    const asMs = formatRelative(toEpochSeconds(NOW_MS - 3 * 86400 * 1000), t, NOW_MS)
    const asSec = formatRelative(toEpochSeconds(NOW_SEC - 3 * 86400), t, NOW_MS)
    assert.equal(asMs, asSec)
    assert.equal(asMs, 'timefmt.daysAgo:{"count":3}')
  })

  it('阈值两侧按同一条规则判定（与 emails-store 的 emailDateToMs 对齐）', () => {
    // 1e12 毫秒 = 2001-09-09，1e12 秒 = 公元 33658 年。
    // 所以本 App 会产出的任何数据（2020 年以后，ms ≥ 1.58e12）都稳稳落在
    // 阈值之上，毫秒/秒判别不会出错。
    //
    // **这条判据的真实边界要说准**：它不是精确的单位探测。
    // 2001-09-09 之前的**毫秒**时间戳（< 1e12）会被误判成秒。
    // 那是 2001 年的数据，本产品不可能产生，仓库里也没有——
    // 所以这里接受这个边界，但把它钉成显式用例，免得将来有人
    // 把阈值调小或改判据时不知道自己在动什么。
    assert.equal(toEpochSeconds(1_000_000_000_000), 1_000_000_000) // == 1e12 → 视为毫秒
    assert.equal(toEpochSeconds(1_700_000_000_000), 1_700_000_000) // 2023 ms
    assert.equal(toEpochSeconds(1_700_000_000), 1_700_000_000)      // 2023 s
    // 已知边界：小于阈值的毫秒会被误判为秒（2001 年以前才会出现）
    assert.equal(toEpochSeconds(978_307_200_000), 978_307_200_000)
  })

  it('0 / 负数 / NaN / Infinity → 0（无效值不得被当成纪元）', () => {
    assert.equal(toEpochSeconds(0), 0)
    assert.equal(toEpochSeconds(-1), 0)
    assert.equal(toEpochSeconds(NaN), 0)
    assert.equal(toEpochSeconds(Infinity), 0)
  })
})
