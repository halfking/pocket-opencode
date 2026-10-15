// §352.5 判据：`下下个月X` 被截成 `下个月X` —— 一个真实缺陷的行为锁定。
//
// 缺陷
// ----
// `meeting-due.ts` 的 nextMonthAnchor 正则是 `/下个?\s*月\s*(底|末|初|中)/`，
// 它**没有下下分支**。于是文本里的「下下个月底」被从**第二个「下」**开始匹配，
// 命中「下个月底」，把「下下个月」答成「下个月」。
//
// 2026-10-10 说「下下个月底」应当是 2026-12-31，实测给 2026-11-30 —— **差一个月**。
// 三种粒度全中：底 / 初 / 中。「下下下个月底」也只被吃掉一个「下」。
//
// ⚠ 这个错**比漏报严重**：它返回一个**看起来完全合法**的日期。
// 用户点开日程看到 11-30，不会怀疑「我说的明明是十二月」。
//
// 判据纪律
// --------
//  1. **期望值独立推算**，不从 parseDueAt 的输出反推。
//  2. **阳性对照必须同时锁住没坏的那一档**（`下个月底` = 2026-11-30），
//     否则「全部返回 null」也能让所有负向断言变绿。
//  3. **变异自证**：把 nextMonthAnchor 改成能吃「下下」的版本后，
//     判据必须转红 —— 否则这条门可能恒真。
//  4. 注入固定 now，任何时区/任何日期跑结果一致。
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { parseDueAt } from '../meeting-due.ts'

// 2026-10-10 是周六。用 new Date(y, m, d) 走本地时区，与该文件其余测试一致。
function now(): number {
  return new Date(2026, 9, 10, 12, 0).getTime()
}

function day(at: number | undefined): string | null {
  if (at === undefined) return null
  const d = new Date(at)
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`
}

describe('§352.5 下下个月X 不得被截成下个月X', () => {
  // ⚠ 这三组期望值全部是**手工推算**的：
  //   今天 2026-10-10 ⇒ 下个月 = 11 月，下下个月 = 12 月，下下下个月 = 1 月（明年）。
  //   月底 = 该月最后一天（12 月 31 天 / 1 月 31 天）。
  const cases: [string, string][] = [
    ['下下个月底', '2026-12-31'],
    ['下下个月初', '2026-12-01'],
    ['下下个月中', '2026-12-15'],
    ['下下下个月底', '2027-01-31'],
  ]
  for (const [text, want] of cases) {
    it(`${text} ⇒ ${want}`, () => {
      const r = parseDueAt(text, now())
      assert.ok(r, `${text} 应当解析出时刻，实得 null`)
      assert.equal(day(r.at), want)
    })
  }

  // 阳性对照：没坏的那一档必须仍然对。
  // 若 nextMonthAnchor 被改成整体返回 null，这条会红 ⇒ 对照锁住了「没全坏」。
  it('阳性对照 · 下个月底仍是 2026-11-30', () => {
    const r = parseDueAt('下个月底', now())
    assert.ok(r)
    assert.equal(day(r.at), '2026-11-30')
  })
  it('阳性对照 · 下下个月底的 matchedText 必须含整个「下下个月」', () => {
    // 只断言「月份对」不够：解析器可能靠别的分支算对但匹配串是错的。
    const r = parseDueAt('下下个月底', now())
    assert.ok(r)
    assert.equal(r.matchedText.trim(), '下下个月底')
  })
})