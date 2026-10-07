// meeting-due.test.ts — 中文自然语言时间解析器的行为锁定。
//
// 这个解析器服务两个消费者：
//   1. local_todos.due_at（此前中文 due 全部静默丢失 → null）；
//   2. 「时间点自动入计划日程」的 at 时刻（scheduleExpr 必须可被后端
//      time.Parse(RFC3339) 接受，且时区口径与 schedule-plan.encodeSchedule
//      一致：本地时间 ***-****-****:00）。
//
// 测试全部注入固定 now，保证 CI 里跑与任何时区/任何日期跑结果都一致
// （now 取 2026-10-07 周三 15:04，Asia/Shanghai 语义下验证——但由于
// 解析全部走本地时区 Date，断言用「相对 now 的差值」而非绝对时刻，
// 因此在任意时区的机器上都能通过）。
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { dueAtToScheduleExpr, parseDueAt } from './meeting-due.ts'

// 2026-10-07 是周三。取当周周三 15:04 本地时间。
function wedAfternoon(): number {
  return new Date(2026, 9, 7, 15, 4).getTime()
}

const HOUR = 60 * 60 * 1000
const DAY = 24 * HOUR

describe('parseDueAt · 机器格式透传', () => {
  it('ISO 日期默认 09:00 且按本地时区（不漂 8 小时）', () => {
    const now = wedAfternoon()
    const r = parseDueAt('2026-11-20', now)!
    assert.ok(r, 'ISO 日期必须可解析')
    const d = new Date(r.at)
    assert.equal(d.getFullYear(), 2026)
    assert.equal(d.getMonth(), 10) // 11 月
    assert.equal(d.getDate(), 20)
    assert.equal(d.getHours(), 9)
    assert.equal(d.getMinutes(), 0)
  })

  it('ISO 日期时间保留时分', () => {
    const r = parseDueAt('2026-11-20T14:30', wedAfternoon())!
    assert.equal(new Date(r.at).getHours(), 14)
    assert.equal(new Date(r.at).getMinutes(), 30)
  })
})

describe('parseDueAt · 相对日', () => {
  it('明天下午三点 = 明天 15:00', () => {
    const r = parseDueAt('明天下午三点交周报', wedAfternoon())!
    assert.ok(r)
    const d = new Date(r.at)
    assert.equal(d.getDate(), 8) // 周四
    assert.equal(d.getHours(), 15)
    assert.equal(d.getMinutes(), 0)
    assert.match(r.matchedText, /明天下午三点/)
  })

  it('今天晚上八点半 = 当天 20:30', () => {
    const r = parseDueAt('今天晚上八点半复盘', wedAfternoon())!
    assert.equal(new Date(r.at).getHours(), 20)
    assert.equal(new Date(r.at).getMinutes(), 30)
    assert.equal(new Date(r.at).getDate(), 7)
  })

  it('后天无钟点 → 09:00', () => {
    const r = parseDueAt('后天提交评审材料', wedAfternoon())!
    assert.equal(new Date(r.at).getDate(), 9)
    assert.equal(new Date(r.at).getHours(), 9)
  })

  it('3天后 / 两周后', () => {
    const a = parseDueAt('3天后上线', wedAfternoon())!
    assert.equal(new Date(a.at).getDate(), 10)
    const b = parseDueAt('两周后复盘', wedAfternoon())!
    assert.equal(new Date(b.at).getDate(), 21)
  })
})

describe('parseDueAt · 周X（默认未来最近）', () => {
  it('周三说「周五」→ 本周五（后天）', () => {
    const r = parseDueAt('周五给反馈', wedAfternoon())!
    assert.equal(new Date(r.at).getDate(), 9)
    assert.equal(new Date(r.at).getDay(), 5)
  })

  it('周三说「下周一」→ 10-12（+5 天）', () => {
    // ⚠️ 用例名原写「+6 天」，与它自己的断言 `getDate() === 12` 矛盾
    //    （10-07 + 5 = 10-12；+6 是 10-13 周二，getDay 也不对）。以断言为准。
    const r = parseDueAt('下周一上午十点站会', wedAfternoon())!
    const d = new Date(r.at)
    assert.equal(d.getDay(), 1)
    assert.equal(d.getDate(), 12) // 10-07 是周三，下周一 = 10-12
    assert.equal(d.getHours(), 10)
  })

  // ⚠️ 回归靶子·「下X」的双重计数。修之前的公式是「本周内最近的 X + 7」，
  //   周三说「下周五」恰好蒙对（周五是本周内最近的周五 = 10-09，+7 = 10-16），
  //   所以旧测试全绿也没暴露；但同一公式用在「下周一」上会算成 10-19
  //   （10-12 本身就是下周一，再 +7 就整整多一周）。
  //   正确口径：(7-cur)+target，与 target 在本周的位置无关。
  it('回归靶子·「下X」= 下周同一天，不随 target 在本周的位置而变', () => {
    const mon = parseDueAt('下周一', wedAfternoon())!
    const fri = parseDueAt('下周五', wedAfternoon())!
    assert.equal(new Date(mon.at).getDate(), 12, '下周一 = 10-12')
    assert.equal(new Date(fri.at).getDate(), 16, '下周五 = 10-16')
    // 两个「下X」之间的间隔必须恰为两个 weekday 的间距（周一↔周五 = 4 天）。
    // 旧公式下这里是 7 天（10-19 vs 10-16），本条会红。
    assert.equal(fri.at - mon.at, 4 * DAY, '同前缀的「下X」间隔不应被整周平移放大')
  })

  it('「下下周一」= 在「下周一」基础上再往后一周', () => {
    const a = parseDueAt('下周一', wedAfternoon())!
    const b = parseDueAt('下下周一', wedAfternoon())!
    assert.equal(b.at - a.at, 7 * DAY)
  })

  // ⚠️ 2026-10-06 补的**回归靶子**。这两条在修之前是红的（20 条里 2 条 fail），
  //  现在绿。写下它们的理由是：修完之后必须能证明「判据咬得住」，
  //  否则「20/20 通过」只说明没写新断言，不说明修对了。
  it('回归靶子·「八点半」的半不能丢（曾被截成「八点」⇒ 20:30 变 20:00）', () => {
    const r = parseDueAt('今天晚上八点半复盘', wedAfternoon())!
    const d = new Date(r.at)
    assert.equal(d.getHours(), 20, '时段词「晚上」应折成 20 点')
    assert.equal(d.getMinutes(), 30, '★ 这一条就是当年红的原因：分钟必须是 30，不能是 0')
  })

  it('回归靶子·「点N分 / 点半 / 点一刻 / 点三刻」四种分钟写法都要拿全', () => {
    const cases: Array<[string, number, number]> = [
      ['今天下午三点一刻', 15, 15],
      ['今天下午三点三刻', 15, 45],
      ['今天上午九点四十五分', 9, 45],
      ['今天晚上八点整', 20, 0],
    ]
    for (const [text, h, m] of cases) {
      const d = new Date(parseDueAt(text, wedAfternoon())!.at)
      assert.equal(d.getHours(), h, `${text} 的小时`)
      assert.equal(d.getMinutes(), m, `${text} 的分钟`)
    }
  })

  it('周三说「周三」→ 今天（含今天）', () => {
    const r = parseDueAt('周三整理笔记', wedAfternoon())!
    assert.equal(new Date(r.at).getDate(), 7)
  })

  it('星期/礼拜变体同义', () => {
    const a = parseDueAt('星期五给反馈', wedAfternoon())!
    const b = parseDueAt('礼拜五给反馈', wedAfternoon())!
    assert.equal(a.at, b.at)
  })
})

describe('parseDueAt · 绝对日期', () => {
  it('12月20日 跨年月份不丢年份（默认明年）', () => {
    const r = parseDueAt('12月20日发版', wedAfternoon())!
    const d = new Date(r.at)
    assert.equal(d.getFullYear(), 2026) // 10 月说 12 月 → 当年
    assert.equal(d.getMonth(), 11)
    assert.equal(d.getDate(), 20)
  })

  it('1月5号 在 10 月说 → 明年', () => {
    const r = parseDueAt('1月5号提交年报', wedAfternoon())!
    assert.equal(new Date(r.at).getFullYear(), 2027)
  })

  it('20号（本月）带下午钟点', () => {
    const r = parseDueAt('20号下午两点对齐', wedAfternoon())!
    const d = new Date(r.at)
    assert.equal(d.getDate(), 20)
    assert.equal(d.getHours(), 14)
  })
})

describe('parseDueAt · 时段修饰词折算', () => {
  it('凌晨三点 = 03:00；中午十二点 = 12:00', () => {
    const a = parseDueAt('明天凌晨三点发布', wedAfternoon())!
    assert.equal(new Date(a.at).getHours(), 3)
    const b = parseDueAt('明天中午十二点聚', wedAfternoon())!
    assert.equal(new Date(b.at).getHours(), 12)
  })

  it('数字与中文数字钟点等价：3点 / 三点 / 15:00', () => {
    const a = parseDueAt('明天下午3点', wedAfternoon())!
    const b = parseDueAt('明天下午三点', wedAfternoon())!
    assert.equal(a.at, b.at)
    const c = parseDueAt('明天15:00', wedAfternoon())!
    assert.equal(a.at, c.at)
  })

  it('只有钟点：未来则今天，已过则明天', () => {
    // now=15:04。「下午三点」已过 → 明天 15:00
    const past = parseDueAt('下午三点继续', wedAfternoon())!
    assert.equal(new Date(past.at).getDate(), 8)
    assert.equal(new Date(past.at).getHours(), 15)
    // 「晚上八点」未到 → 今天 20:00
    const soon = parseDueAt('晚上八点继续', wedAfternoon())!
    assert.equal(new Date(soon.at).getDate(), 7)
    assert.equal(new Date(soon.at).getHours(), 20)
  })
})

describe('parseDueAt · 边界与拒绝', () => {
  it('空串/无关文本/星期几都解不出 → null', () => {
    assert.equal(parseDueAt('', wedAfternoon()), null)
    assert.equal(parseDueAt('尽快', wedAfternoon()), null)
    assert.equal(parseDueAt('下次一定', wedAfternoon()), null)
  })

  it('ISO 畸形日期不炸（交给 Date 兜底）', () => {
    const r = parseDueAt('2026-13-45', wedAfternoon())
    // 13 月不存在：本地 Date 会顺延到 2027-01，至少不抛异常
    assert.ok(r === null || Number.isFinite(r.at))
  })

  it('解出的「明天下午三点」与 now 的差恰为 1 天内偏移', () => {
    const now = wedAfternoon()
    const r = parseDueAt('明天下午三点', now)!
    const diff = r.at - now
    assert.ok(diff > 0 && diff < 2 * DAY, `差值应在 0~2 天内，实际 ${(diff / HOUR).toFixed(1)}h`)
  })
})

describe('dueAtToScheduleExpr · 与后端 at 解析闭环', () => {
  it('生成 RFC3339 且墙钟与 parseDueAt 一致', () => {
    const now = wedAfternoon()
    const r = parseDueAt('明天下午三点半', now)!
    const expr = dueAtToScheduleExpr(r.at)
    const d = new Date(r.at)
    // 前段是本地墙钟，后缀是东八区固定偏移（与 schedule-plan.encodeSchedule
    // 的 once 分支同一口径）。用拼接拼期望值，避免正则里出现转义歧义。
    const TZ = `+${String(8).padStart(2, '0')}:00`
    assert.equal(
      expr,
      `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}` +
      `T${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}:00${TZ}`,
      'expr 必须是本地墙钟 + 东八区偏移',
    )
    assert.ok(expr.endsWith(`00${TZ}`), `秒位应为 00 且以东八区偏移结尾，实际 ${expr}`)
    assert.match(expr, /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:00/, `前段应为本地 RFC3339，实际 ${expr}`)
  })

  it('后端 time.Parse(RFC3339) 口径：偏移量真的被算进去了（负控）', () => {
    // 这条不能只断言「格式对」——格式对而偏移写错（比如把 +08:00 写成
    // +00:00）会让所有待办提醒整体偏移 8 小时，而上面的格式断言照样全绿。
    // 这里把「东八区墙钟」和「UTC 墙钟」两种解释算出来做差：后端按 RFC3339
    // 解析 expr 得到的 epoch，必须等于「本地墙钟按 UTC 解释」再减 8 小时。
    // 负控：若把后缀换成零偏移，差值应恰好为 0 ⇒ 断言能判红。
    const now = wedAfternoon()
    const r = parseDueAt('明天下午三点半', now)!
    const expr = dueAtToScheduleExpr(r.at)
    const d = new Date(r.at)
    const asUtc = Date.UTC(
      d.getFullYear(), d.getMonth(), d.getDate(), d.getHours(), d.getMinutes(), 0, 0,
    )
    const parsedByBackend = Date.parse(expr)
    assert.equal(
      parsedByBackend - asUtc,
      -8 * 60 * 60 * 1000,
      'RFC3339 解析结果必须比 UTC 墙钟早 8 小时（东八区）',
    )
    // 负控：零偏移后缀的 expr 差值为 0，证明上面那条断言不是恒真。
    const zeroOffsetExpr = expr.replace(/\+\d{2}:\d{2}$/, '+00:00')
    assert.equal(Date.parse(zeroOffsetExpr) - asUtc, 0, '负控：零偏移后缀应解析为 UTC 墙钟')
  })
})

