// §365 周族「个」字缺口 + 月日族月表达式缺口 + 月日越界静默溢出。
//
// 这批缺陷为什么此前没人看见
// ----------------------
// §355 普查过三族前缀，把周族判成「`下下下周一` 只支持两档，**非误报**」。
// 本轮把它挖成三个真缺陷，靠的是两件事：
//
//   ① **基准日必须扫全 7 个星期几**。固定在周六时，「周一…周五」这五天里
//      裸「周X」与「下周X」**恰好同一天**（cur=5、target≤4 时两个公式相等）
//      ⇒「下个周一」被吞成裸「周一」这个缺陷，在五个工作日上**完全不可见**，
//      只有「六/日/天」才暴露。§355 大概率就是被这个巧合放行的。
//      ⇒ 本文件的基准日集合刻意覆盖 7 个星期几 + 月初/月末/年初/年末。
//   ② **「个」字是口语里「下个周一」的标准写法**，比 §355 当时测的
//      「下下下周一」常见得多，而它当时**根本不被识别**。
//
// 锁的到底是什么
// --------------
// 不是「某个字符串必须被解析成某天」，而是三条**结构不变式**——
// 它们不含日期，所以换基准日不需要重推，也不会因为我手算错而误判产品：
//
//   Ⅰ「个」是可选修饰词：下个周X ≡ 下周X，下下个星期X ≡ 下下星期X
//   Ⅱ 档位必须被计数（§356 不变式① 的周族版）：第 n 档 ≡ 第 n-1 档 + 7 天
//   Ⅲ 月前缀不得被吞：下(N)个月D号 必须落在第 N 个月
//
// ⚠ 期望值不得由 parseDueAt 的输出反推：§365 第一版真值表里
//   我把「下个星期一」手工推成 10-19（应为 10-12），**是我的期望写错了**，
//   而那份读数会被人读成「产品多修了一处」。改成结构不变式后不存在这类风险。
//
// ⚠ 阳性对照必须存在：若有人把整支改成返回 null，下面所有负向断言都会变绿。
//   每组都有「仍能解析」的正向臂守着。
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { parseDueAt } from '../meeting-due.ts'

const DAY_MS = 86_400_000
const WD = ['一', '二', '三', '四', '五', '六', '日', '天']
const UNITS = ['周', '星期', '礼拜']

/** 7 个星期几各一天，外加月初/月末/年初/年末 —— 缺一就会出现巧合掩盖。 */
const BASES: [string, Date][] = [
  ['周一', new Date(2026, 9, 12, 12, 0)],
  ['周二', new Date(2026, 9, 13, 12, 0)],
  ['周三', new Date(2026, 9, 14, 12, 0)],
  ['周四', new Date(2026, 9, 15, 12, 0)],
  ['周五', new Date(2026, 9, 16, 12, 0)],
  ['周六', new Date(2026, 9, 17, 12, 0)],
  ['周日', new Date(2026, 9, 18, 12, 0)],
  ['月初', new Date(2026, 9, 1, 12, 0)],
  ['月末', new Date(2026, 9, 31, 12, 0)],
  ['年初', new Date(2026, 0, 5, 12, 0)],
  ['年末', new Date(2026, 11, 28, 12, 0)],
]

function at(text: string, base: number): number | null {
  const r = parseDueAt(text, base)
  return r ? r.at : null
}
function ymd(x: number | null): string {
  if (x === null) return 'null'
  const d = new Date(x)
  if (Number.isNaN(d.getTime())) return 'INVALID'
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`
}

describe('§365 Ⅰ：「个」是可选修饰词 —— 下个周X ≡ 下周X', () => {
  for (const [bn, bd] of BASES) {
    it(`${bn}：三种周单位 × 八个星期字，「下个」与「下」等价`, () => {
      const bad: string[] = []
      for (const u of UNITS) {
        for (const w of WD) {
          const a = at(`下${u}${w}`, bd.getTime())
          const b = at(`下个${u}${w}`, bd.getTime())
          if (a === null || b === null || a !== b) {
            bad.push(`下${u}${w}=${ymd(a)} vs 下个${u}${w}=${ymd(b)}`)
          }
        }
      }
      assert.deepEqual(bad, [], `以下形态不等价：\n${bad.join('\n')}`)
    })
  }
})

describe('§365 Ⅰb：「下下个周X」≡「下下周X」', () => {
  for (const [bn, bd] of BASES) {
    it(`${bn}：三种周单位 × 八个星期字，「下下个」与「下下」等价`, () => {
      const bad: string[] = []
      for (const u of UNITS) {
        for (const w of WD) {
          const a = at(`下下${u}${w}`, bd.getTime())
          const b = at(`下下个${u}${w}`, bd.getTime())
          if (a === null || b === null || a !== b) {
            bad.push(`下下${u}${w}=${ymd(a)} vs 下下个${u}${w}=${ymd(b)}`)
          }
        }
      }
      assert.deepEqual(bad, [], `以下形态不等价：\n${bad.join('\n')}`)
    })
  }
})

describe('§365 Ⅱ：周族档位必须被计数（§356 不变式① 的周族版）', () => {
  for (const [bn, bd] of BASES) {
    it(`${bn}：下周X → 下下周X → 下下下周X → 下下下下周X 逐档 +7 天`, () => {
      const bad: string[] = []
      for (const w of WD) {
        const t = [1, 2, 3, 4].map((n) => at(`${'下'.repeat(n)}周${w}`, bd.getTime()))
        for (let i = 0; i < t.length; i++) {
          if (t[i] === null) { bad.push(`下${'下'.repeat(i)}周${w} = null`); continue }
          if (i > 0 && t[i] !== t[i - 1]! + 7 * DAY_MS) {
            bad.push(`下${'下'.repeat(i)}周${w}: ${ymd(t[i - 1])} → ${ymd(t[i])}（应 +7 天）`)
          }
        }
      }
      assert.deepEqual(bad, [], `以下形态档位没有被计数：\n${bad.join('\n')}`)
    })
  }
})

describe('§365 Ⅲ：月前缀不得被吞 —— 下(N)个月D号 落在第 N 个月', () => {
  for (const [bn, bd] of BASES) {
    it(`${bn}：档位 1/2/3 × 五种日号，落在正确的月与正确的日`, () => {
      const mo = bd.getMonth() + 1
      const bad: string[] = []
      for (const lead of [1, 2, 3]) {
        const wantMo = ((mo - 1 + lead) % 12) + 1
        for (const dn of ['1', '5', '15', '20', '28']) {
          const text = `${'下'.repeat(lead)}个月${dn}号`
          const r = at(text, bd.getTime())
          if (r === null || new Date(r).getMonth() + 1 !== wantMo || new Date(r).getDate() !== Number(dn)) {
            bad.push(`${text} = ${ymd(r)}（应 ${wantMo} 月 ${dn} 日）`)
          }
        }
      }
      assert.deepEqual(bad, [], `以下形态月前缀被吞：\n${bad.join('\n')}`)
    })

    it(`${bn}：中文数字日同样不得被吞（下个月十五号不是 null）`, () => {
      // ⚠ 第一版这里写的是 `(bd.getMonth() % 12) + 1` —— `getMonth()` 是 **0 基**，
      //   拿它当 1 基月去算「下个月」，在每个基准日上都差一个月 ⇒ 11 条全红。
      //   产品给的 2026-02-01（1 月说下个月）/ 2027-01-01（12 月说下个月）都正确。
      //   这是**期望写错**，不是门没抓住 —— 记在这里是因为它极易被误读成产品缺陷。
      const mo = bd.getMonth() + 1
      const wantMo = (mo % 12) + 1
      const bad: string[] = []
      for (const cn of ['一号', '五号', '十五号', '二十号', '二十八号']) {
        const r = at(`下个月${cn}`, bd.getTime())
        if (r === null || new Date(r).getMonth() + 1 !== wantMo) {
          bad.push(`下个月${cn} = ${ymd(r)}（应 ${wantMo} 月）`)
        }
      }
      assert.deepEqual(bad, [], `中文数字日被整条漏掉：\n${bad.join('\n')}`)
    })

    // ⚠ 阳性对照：裸「N月D号」不许被新前缀支抢走（§356 不变式② 的月日版）。
    it(`${bn}：裸「12月20号」「3月5号」逐字不变`, () => {
      for (const [m, d] of [['12', '20'], ['3', '5']] as [string, string][]) {
        const r = parseDueAt(`${m}月${d}号`, bd.getTime())
        assert.ok(r, `${m}月${d}号 应当仍能解析`)
        assert.equal(new Date(r.at).getMonth() + 1, Number(m), `${m}月${d}号 的月份不对`)
        assert.equal(new Date(r.at).getDate(), Number(d), `${m}月${d}号 的日不对`)
      }
    })
  }
})

describe('§365 Ⅳ：月日越界必须 fail-closed，不得被 JS Date 静默归一', () => {
  // ⚠ 这一族在 §365 之前完全没有校验：`localTime(y, 9, 31, …)` 会被 JS
  //   归一成 10/1 ⇒「9月31日」实测得到 **2027-10-01**（还顺带滚了一年）。
  for (const bad of ['9月31日', '9月31号', '11月31日', '4月31号', '2月30日', '6月31号', '1月32日']) {
    it(`${bad} 不得溢出到下个月`, () => {
      const m = Number(bad.match(/(\d{1,2})月/)![1])
      for (const [bn, bd] of BASES) {
        const r = parseDueAt(bad, bd.getTime())
        if (r !== null && new Date(r.at).getMonth() + 1 !== m) {
          assert.fail(`${bn}：${bad} 溢出到 ${ymd(r.at)}（${m} 月只有 ${new Date(2026, m, 0).getDate()} 天）`)
        }
      }
    })
  }
})

describe('§365 Ⅴ：既有四族不得回归（§356 负控 + 阳性对照）', () => {
  const CTRL: [string, string][] = [
    ['下个月底', '2026-11-30'],
    ['下下个月底', '2026-12-31'],
    ['下下下个月底', '2027-01-31'],
    ['下下个月初', '2026-12-01'],
    ['下个季度末', '2027-03-31'],
    ['下下个季度末', '2027-06-30'],
    ['下下下个季度末', '2027-09-30'],
    ['下个年底', '2027-12-31'],
    ['本月底', '2026-10-31'],
    ['本月初', '2026-10-01'],
    ['月中', '2026-10-15'],
    ['周一', '2026-10-12'],
    ['下周日', '2026-10-18'],
    ['下下周日', '2026-10-25'],
    ['这周一', '2026-10-12'],
    ['本周五', '2026-10-16'],
    ['12月20日', '2026-12-20'],
    ['3月5号', '2027-03-05'],
    ['15号', '2026-10-15'],
    ['下周三下午三点', '2026-10-14'],
  ]
  // 基准 2026-10-10（周六），与 §356 判据同一个基准，便于逐条对照。
  const BASE = new Date(2026, 9, 10, 12, 0).getTime()
  for (const [text, want] of CTRL) {
    it(`负控：${text} ⇒ ${want}`, () => {
      const r = parseDueAt(text, BASE)
      assert.ok(r, `${text} 应当解析出时刻，实得 null`)
      assert.equal(ymd(r.at), want, `${text} 的日期不对`)
    })
  }
  // ⚠ matchedText 也要守：周族改用命名组后，「这周一」的匹配串不能退化成「周一」。
  it('这周一的 matchedText 仍含「这」', () => {
    const r = parseDueAt('这周一', BASE)
    assert.ok(r)
    assert.equal(r.matchedText.trim(), '这周一')
  })
})