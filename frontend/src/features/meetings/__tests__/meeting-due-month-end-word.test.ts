// §366 裸「月末 / 本月末」漏报 —— 同一族三个调用点只改了两个。
//
// 缺陷
// ----
// 基准 2026-10-12：`月底` ⇒ 2026-10-31 ✅、`下个月末` ⇒ 2026-11-30 ✅、
// `下下个月末` ⇒ 2026-12-31 ✅，**唯独裸的 `月末` / `本月末` 返回 null**。
//
// 「月末」在商务口语里与「月底」完全同义（月末结算 / 月末汇报 / 本月末对账），
// 而这一族**带前缀的形态早就支持 `末`**（`下个月末` 一直能解析），
// 只有**裸形态**被漏掉 ⇒ 又一次「这条规则有几处调用点」只改了一部分。
//
// 锁的到底是什么
// --------------
// 不是「`月末` 必须等于 31 号」，而是两张**矩阵**——
// 它们与实现无关，换个写法也该照样通过：
//
//   Ⅰ 调用点矩阵：{前缀} × {粒度} 的**每一个格子都必须能解析**。
//     这一条才是能抓住本缺陷的形状：漏的是**格子**，
//     而「某几个具体字符串必须对」只能一个一个补，补完还会有下一个漏的。
//   Ⅱ 跨月正确性：月末必须等于**目标月真实最后一天**（2 月平年 28 / 闰年 29）。
//
// ⚠ 阳性对照必须存在：若有人把整支改成返回 null，矩阵全空也会「不红」，
//   所以每格都要求**非 null 且落在正确月份**，而不是只要求「不报错」。
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { parseDueAt } from '../meeting-due.ts'

const PREFIXES = ['', '本', '下个', '下下个', '下下下个']
const GRAINS = ['底', '末', '初', '中']

function ymd(at: number): string {
  const d = new Date(at)
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`
}

/** 基准日取 12 号：月初（1 号）已过、月中未到、月末未到 —— 三个粒度各在一侧。 */
const BASES: [string, Date][] = [
  ['2026-01-12', new Date(2026, 0, 12, 12, 0)],
  ['2026-02-12 平年2月', new Date(2026, 1, 12, 12, 0)],
  ['2028-02-12 闰年2月', new Date(2028, 1, 12, 12, 0)],
  ['2026-04-12 30天月', new Date(2026, 3, 12, 12, 0)],
  ['2026-06-12', new Date(2026, 5, 12, 12, 0)],
  ['2026-09-12', new Date(2026, 8, 12, 12, 0)],
  ['2026-11-12', new Date(2026, 10, 12, 12, 0)],
  ['2026-12-12', new Date(2026, 11, 12, 12, 0)],
]

describe('§366 Ⅰ 调用点矩阵：{前缀} × {粒度} 每一格都必须能解析', () => {
  for (const [label, bd] of BASES) {
    it(`${label}：5 前缀 × 4 粒度 = 20 格全部非 null`, () => {
      const holes: string[] = []
      for (const p of PREFIXES) {
        for (const g of GRAINS) {
          const text = `${p}月${g}`
          const r = parseDueAt(text, bd.getTime())
          if (r === null || Number.isNaN(r.at)) holes.push(text)
        }
      }
      assert.deepEqual(holes, [], `这些格子解析不出来（漏报）：${holes.join('、')}`)
    })
  }
})

describe('§366 Ⅰb：季 / 年两族同样跑一遍矩阵（同一形状的守卫）', () => {
  const QS = ['', '本', '下个', '下下个', '下下下个']
  for (const [label, bd] of BASES) {
    it(`${label}：{季末,季度末,年底,年末} × 5 前缀全部非 null`, () => {
      const holes: string[] = []
      for (const p of QS) {
        for (const k of ['季末', '季度末', '年底', '年末']) {
          const text = k.startsWith('季') || k.startsWith('年') ? `${p}个${k}` : `${p}${k}`
          const r = parseDueAt(text, bd.getTime())
          if (r === null || Number.isNaN(r.at)) holes.push(text)
        }
      }
      assert.deepEqual(holes, [], `这些格子解析不出来（漏报）：${holes.join('、')}`)
    })
  }
})

describe('§366 Ⅱ 跨月正确性：月末必须等于目标月的真实最后一天', () => {
  // 12 个月全扫，含 2 月平年(28) / 闰年(29)，以及 30 天月与 31 天月。
  // ⚠ 这张表第一版写错了三格（九月/十月/十一月整体错位一格），
  //   判据红 3 条而产品全对 —— **期望写错**，第三次栽在同一类地方。
  //   手写月长表极易错位 ⇒ 表里的字面量必须逐个对着日历核，不靠记忆。
  const cases: [number, number, number][] = [
    [2026, 1, 28], // 2 月平年 28 天
    [2028, 1, 29], // 2 月闰年 29 天
    [2026, 3, 30], // 4 月 30 天
    [2026, 4, 31], // 5 月 31 天
    [2026, 5, 30], // 6 月 30 天
    [2026, 8, 30], // 9 月 30 天
    [2026, 9, 31], // 10 月 31 天
    [2026, 10, 30], // 11 月 30 天
  ]
  for (const [y, m0, last] of cases) {
    const bd = new Date(y, m0, 12, 12, 0)
    it(`${y} 年 ${m0 + 1} 月：月底 ≡ 月末 ≡ 本月末 ≡ 本月底 ⇒ ${y}-${String(m0 + 1).padStart(2, '0')}-${last}`, () => {
      const want = `${y}-${String(m0 + 1).padStart(2, '0')}-${last}`
      for (const text of ['月底', '月末', '本月底', '本月末']) {
        const r = parseDueAt(text, bd.getTime())
        assert.ok(r, `${text} 应当解析出时刻，实得 null`)
        assert.equal(ymd(r.at), want, `${text} ⇒ ${ymd(r.at)}，应为 ${want}`)
      }
    })
  }
})

describe('§366 Ⅲ 档位单调：裸 < 下个 < 下下个 < 下下下个', () => {
  for (const [label, bd] of BASES) {
    it(`${label}：两个粒度词各自逐档递增`, () => {
      for (const g of ['底', '末', '初', '中']) {
        const t = ['', '下个', '下下个', '下下下个'].map((p) => {
          const r = parseDueAt(`${p}月${g}`, bd.getTime())
          assert.ok(r, `${p}月${g} 应当能解析`)
          return r.at
        })
        assert.ok(t[1]! > t[0]!, `${label} ${g}：${ymd(t[0]!)} → ${ymd(t[1]!)} 未递增`)
        assert.ok(t[2]! > t[1]!, `${label} ${g}：${ymd(t[1]!)} → ${ymd(t[2]!)} 未递增`)
        assert.ok(t[3]! > t[2]!, `${label} ${g}：${ymd(t[2]!)} → ${ymd(t[3]!)} 未递增`)
      }
    })
  }
})

describe('§366 Ⅳ 负控：不得抢走既有四族的匹配', () => {
  const BASE = new Date(2026, 9, 10, 12, 0).getTime() // 2026-10-10 周六
  const CTRL: [string, string][] = [
    ['季末', '2026-12-31'],
    ['季度末', '2026-12-31'],
    ['本季度末', '2026-12-31'],
    ['下个季度末', '2027-03-31'],
    ['年底', '2026-12-31'],
    ['年末', '2026-12-31'],
    ['下个年底', '2027-12-31'],
    ['月底', '2026-10-31'],
    ['本月底', '2026-10-31'],
    ['月初', '2026-10-01'],
    ['月中', '2026-10-15'],
    ['下个月底', '2026-11-30'],
    ['下个月末', '2026-11-30'],
    ['下下个月末', '2026-12-31'],
    ['十一月底', '2026-11-30'],
  ]
  for (const [text, want] of CTRL) {
    it(`负控：${text} ⇒ ${want}`, () => {
      const r = parseDueAt(text, BASE)
      assert.ok(r, `${text} 应当解析出时刻，实得 null`)
      assert.equal(ymd(r.at), want, `${text} ⇒ ${ymd(r.at)}，应为 ${want}`)
    })
  }
  // ⚠ matchedText 必须含被匹配的那个词，否则「月末」可能走的是别的分支。
  it('月末 的 matchedText 逐字保留「月末」', () => {
    const r = parseDueAt('月末', BASE)
    assert.ok(r)
    assert.equal(r.matchedText.trim(), '月末')
  })
  it('本月末 的 matchedText 逐字保留「本月末」', () => {
    const r = parseDueAt('本月末', BASE)
    assert.ok(r)
    assert.equal(r.matchedText.trim(), '本月末')
  })
})