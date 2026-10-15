/**
 * meeting-due-anchor-gap.test.ts — §229.2：`parseDueAt` 月度/季度锚点的**缺口**。
 *
 * ── 这道门守的缺口 ──
 *
 * 既有门 `meeting-due-anchor.test.ts`（§37 真网关实测建的）只钉了
 * **带月份数字**的形态（「十一月底」「12月底」）与**带「本」字**的形态
 * （「本月底」「本月初」「本季度末」）。口语里更常见的几种却全部落空：
 *
 *   探针实测（`/tmp/opstt/probe-nextmeeting-226.ts`，2026-10-08，
 *   基准 2026-10-07 周三 10:00，阳性对照 5/5 先自证量具）：
 *     「季度末」   ✗   而「季末」   ✅   ← 正则枚举里 `季末` 不是 `季度末` 的子串
 *     「月底」     ✗   而「本月底」 ✅   ← 口语说「月底前」远比「本月底前」多
 *     「月初」「月中」 ✗
 *     「下个月底」 ✗   而且补上裸「月底」之后会被答成**本月**底（更糟）
 *
 * 三条都发生在 `next_meeting` / `action_items[].due` 上 ⇒ 待办照样入库、
 * 界面照常显示、不报任何错 —— 时间点就是没进日程（§21「合法但空」家族）。
 *
 * ⚠ 用例里的字符串分两类，**来源不同，别混**：
 *   · §37 真网关实测：沿用既有门那份（标注「真网关」）；
 *   · 本轮探针候选：中文口语的常见说法（标注「口语」），
 *     **不是模型实测产出** —— 没有真机就没有真实分布，
 *     所以这里只断言「能不能解析成正确时刻」，不声称任何频率。
 */

import { describe, it } from 'node:test'
import assert from 'node:assert/strict'
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { parseDueAt } from '../meeting-due.ts'
import { resolveTodoDue } from '../meeting-due-plan.ts'

const HERE = path.dirname(fileURLToPath(import.meta.url))
const SRC = path.resolve(HERE, '..', '..', '..') // → frontend/src

const NOW = new Date(2026, 9, 7, 10, 0, 0).getTime() // 2026-10-07 周三 10:00 CST
const fmt = (at: number) => {
  const d = new Date(at)
  const p = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`
}
/** 断绝对值；解析不出来时直接报错，而不是让 `.at` 抛在 undefined 上。 */
function at(raw: string, now = NOW): string {
  const r = parseDueAt(raw, now)
  assert.ok(r, `解析失败：${raw} —— 口语里常见的时间点进不了日程`)
  return fmt(r.at)
}

describe('§229.2 A · 裸「月底/月初/月中」（口语，非真网关实测）', () => {
  it('裸「月底」= 本月底（2026-10 → 10/31）', () => {
    assert.equal(at('月底'), '2026-10-31 09:00')
    assert.equal(at('月底前'), '2026-10-31 09:00')
    // 与既有形态读数必须逐字相同：口径不能因为少了「本」字就变。
    assert.equal(at('月底'), at('本月底'), '裸「月底」与「本月底」必须同解')
  })

  it('裸「月初」= 1 号、「月中」= 15 号', () => {
    assert.equal(at('月初'), '2026-10-01 09:00')
    assert.equal(at('月中'), '2026-10-15 09:00')
    assert.equal(at('月初'), at('本月初'))
  })

  it('★ 月末天数按**当月**真实天数算（8 月 31 天 / 4 月 30 天）', () => {
    // ⚠ 第一版这条写成「1 月说月底 ⇒ 2/28」——**量具错了**：
    //   裸「月底」是**当月**月底，1 月说月底就是 1/31，永远不可能是 2 月。
    //   （2 月那格属于「下个月底」，在 C 组用基准年 1 月测。）
    const inAug = new Date(2026, 7, 10, 10, 0, 0).getTime()
    assert.equal(at('月底', inAug), '2026-08-31 09:00', '8 月有 31 天，写死 30 会少一天')
    const inApr = new Date(2026, 3, 10, 10, 0, 0).getTime()
    assert.equal(at('月底', inApr), '2026-04-30 09:00', '4 月只有 30 天，写死 31 会溢出到 5/1')
  })

  it('★ 保留原话里的钟点（「月底前下午三点」不能被吞成 09:00）', () => {
    assert.equal(at('月底前下午三点'), '2026-10-31 15:00')
  })
})

describe('§229.2 B · 「季度末」（口语，非真网关实测）', () => {
  it('「季度末」与「季末」同解（2026-10 是 Q4 ⇒ 12/31）', () => {
    assert.equal(at('季度末'), '2026-12-31 09:00')
    // ⚠ 这一条是本节的核心：修复前 `季末` 在枚举里，而 `季度末` 不是它的子串。
    assert.equal(at('季度末'), at('季末'), '「季度末」与「季末」必须同解')
  })

  it('带后缀也要认（「季度末之后」—— 提示词要求保留用户原话）', () => {
    assert.equal(at('季度末之后'), '2026-12-31 09:00')
  })

  it('已过则滚一个季度，且 6 月末按 30 天算', () => {
    const mar31 = new Date(2027, 2, 31, 10, 0, 0).getTime()
    assert.equal(at('季度末', mar31), '2027-06-30 09:00', '6 月只有 30 天')
    const dec31 = new Date(2026, 11, 31, 10, 0, 0).getTime()
    assert.equal(at('季度末', dec31), '2027-03-31 09:00', '12/31 说季度末应指明年 3/31')
  })
})

describe('§229.2 C · 「下个?月X」（口语，非真网关实测）', () => {
  it('「下个月底」= **下个月**的最后一天，不是本月', () => {
    // ⚠ 这一条是「错答案比没答案更糟」的那一格：补上裸「月底」之后，
    //   正则会从左扫到「下个月底」的位置 2 命中「月底」⇒ 答成本月底。
    //   ⇒ 必须断言它是 11-30 而不是 10-31。
    assert.equal(at('下个月底'), '2026-11-30 09:00')
    assert.notEqual(at('下个月底'), at('月底'), '「下个月底」被答成了本月底 —— 建错日程比不建更糟')
  })

  it('「下个月初」= 下月 1 号 /「下个月中」= 下月 15 号', () => {
    assert.equal(at('下个月初'), '2026-11-01 09:00')
    assert.equal(at('下个月中'), '2026-11-15 09:00')
  })

  it('跨年：12 月说「下个月底」= 明年 1 月 31 日', () => {
    const dec = new Date(2026, 11, 10, 10, 0, 0).getTime()
    assert.equal(at('下个月底', dec), '2027-01-31 09:00')
  })

  it('★ 下个月是闰年 2 月时月末按 29 天算', () => {
    // 「用基准年算月末天数」会让这一条差一天 —— 与既有门那条同款陷阱。
    const jan2028 = new Date(2028, 0, 10, 10, 0, 0).getTime()
    assert.equal(at('下个月底', jan2028), '2028-02-29 09:00', '2028 是闰年')
    const jan2027 = new Date(2027, 0, 10, 10, 0, 0).getTime()
    assert.equal(at('下个月底', jan2027), '2027-02-28 09:00', '2027 是平年')
  })

  it('负控：不得带跑偏既有周X 分支（「下周三」不是「下个周」）', () => {
    assert.equal(at('下周三下午三点'), '2026-10-14 15:00')
  })
})

describe('§229.2 D · 既有读数不得退化（§37 真网关实测那批）', () => {
  it('真网关产出的四个 due 仍解出同一个时刻', () => {
    for (const due of ['十一月底之前', '十一月底前', '十一月底', '11月底']) {
      assert.equal(at(due), '2026-11-30 09:00', `${due} 的读数退化了`)
    }
  })

  it('带月份数字的四种锚点不变', () => {
    assert.equal(at('十一月底'), '2026-11-30 09:00')
    assert.equal(at('12月底'), '2026-12-31 09:00')
    assert.equal(at('二月初'), '2027-02-01 09:00')
    assert.equal(at('三月中'), '2027-03-15 09:00')
  })

  it('非法月份仍然判 null（补覆盖不能把「宁可漏」变成「瞎猜」）', () => {
    // ⚠ 既有门 meeting-due-anchor.test.ts:129 已钉这批；这里再钉一次是因为
    //   我在同一个正则的枚举里加了词 —— 加词最容易撞出「13 月底」这种。
    for (const bad of ['0月底', '13月底', '十三月底', '本月十三号', '25月底']) {
      assert.equal(parseDueAt(bad, NOW), null, `${bad} 不该解析出时刻`)
    }
  })

  it('§229.1 的「不猜」负控仍然成立（没锚点的短句不得被硬解）', () => {
    // ⚠ 补解析覆盖时最容易犯的错：为了让「解不出」变少而给无锚点短句兜个底。
    for (const vague of ['下次再聊', '尽快', '有空的时候', '待定', '很快']) {
      assert.equal(parseDueAt(vague, NOW), null, `${vague} 没有时间锚点，不该被硬解`)
    }
  })
})

/**
 * 同步门：anchor 正则第 ③ 组的枚举集合 == 代码里 `a3 ===` 的分支集合。
 *
 * ⚠ 为什么需要这一格：③ 组最后是个 catch-all（`else` 按「月中」处理）。
 *   日后有人往正则里加了新词而忘了加分支 ⇒ 新词**静默**按「月中」算，
 *   没有任何门会红。这一格把「枚举」与「分支」钉成同一个集合。
 *
 * 形态是源码断言（枚举集合只能在源码里取），但**双向**：
 * 正则里有而分支里没有 ⇒ 红；分支里有而正则里没有 ⇒ 也红。
 */
describe('§229.2 E · 正则枚举 == 分支枚举（双向）', () => {
  const src = fs.readFileSync(path.resolve(SRC, 'features/meetings/meeting-due.ts'), 'utf8')

  /**
   * 取出 anchor 正则「季末/年底/…」那一支的枚举词。
   *
   * ⚠ 这段改写过一次（§356）。原版硬编码了 `|((季末|…))` 这样的**字面量**，
   *   §356 把那一支改成命名组并在前��加了前缀支，它就红成「正则被改写了」。
   *   中间还试过「扫最外层括号组」—— 连续两次都错在 `(?:下个?)+` 的 `?:`
   *   上（它含 `?`，把整支误排除，后续扫描随之错位）。
   *
   * ⇒ 现在改用**语义探针**：从 anchor 那一行抽出所有 2~4 字中文候选词，
   *   逐个问 `parseDueAt` 认不认。
   *   ⚠ 这**不是放松**，反而更强：
   *     · 原版只问「源码里列了哪些词」；
   *     · 现在问「**解析器实际认哪些词**」—— 若某个词在正则里但被前置分支抢走
   *       （这正是 §356 那类 bug 的形状），它会在这里露出来。
   *     · 与 `③ 组必须用命名组 anchorStr 取值` 那条合起来，
   *       两侧（正则枚举 / 分支枚举）都按**行为**而非字面量对齐。
   */
  function anchorWords(): string[] {
    const line = src.split('\n').find((l) => l.includes('const anchor = '))
    assert.ok(line, '在 meeting-due.ts 里找不到 anchor 正则那一行')
    const cands = [...new Set(line.match(/[一-龥]{2,4}/g) || [])]
    assert.ok(cands.length >= 10, `anchor 那一行只抽出 ${cands.length} 个候选词，正则可能被改写成认不出的形态`)
    const recognized = cands.filter((w) => parseDueAt(w, NOW) !== null)
    assert.ok(recognized.length >= 10,
      `只有 ${recognized.length} 个候选词被 parseDueAt 认出来 —— ③ 组可能被前置分支抢走了`)
    return recognized
  }

  /** ③ 组那一支必须用命名组 `anchorStr` 取值 —— 位置索引已因前缀组右移。 */
  it('★ ③ 组必须用命名组 anchorStr 取值（位置索引已右移）', () => {
    assert.ok(/\(\?<anchorStr>/.test(src),
      'anchor 正则的 ③ 组没有命名组 anchorStr ⇒ §356 那次新增前缀组会把它的位置索引挤掉')
    assert.ok(/const a3 = gs\.qk \|\| gs\.yk \|\| gs\.anchorStr \|\| anchor\[3\]/.test(src),
      'a3 的取值没有优先走命名组 ⇒ 位置索引错位时「十一月底」等形态会返回 Invalid Date')
  })

  /** 切出 ③ 组的处理块（从它的 `if` 到它自己的 return）。 */
  function anchorBranchBlock(): string {
    // ⚠ 原版按 `if (anchor[3]) {` 切；§356 改成 `if (a3) {`。
    //   这里按「判定 a3 的那一行」切，两种写法都能切到。
    const start = Math.max(...['if (anchor[3]) {', 'if (a3) {'].map((n) => src.indexOf(n)).filter((i) => i > 0))
    const end = src.indexOf('return { at: dayAt(localTime(yy, mm, dd, 0, 0), c)', start)
    assert.ok(start > 0 && end > start, '切不出 ③ 组处理块 —— 判据与代码结构脱节了')
    return src.slice(start, end)
  }

  it('③ 组的每个词都必须有对应的 a3 分支', () => {
    const words = anchorWords()
    assert.ok(words.length >= 10, `③ 组枚举只有 ${words.length} 个词，与判据预期不符`)
    const block = anchorBranchBlock()
    for (const w of words) {
      assert.ok(block.includes(`a3 === '${w}'`),
        `anchor 正则新增了「${w}」却没有对应分支 ⇒ 它会静默落进最后的 else（按「月中」算）`)
    }
  })

  it('每个 a3 分支都必须在 ③ 组枚举里（反向：别留孤儿分支）', () => {
    const words = new Set(anchorWords())
    const block = anchorBranchBlock()
    // catch-all 里的 '月中' 刻意不写 `a3 ===`（它是 else），
    // 所以只查显式分支里的孤儿。
    for (const hit of block.matchAll(/a3 === '([^']+)'/g)) {
      assert.ok(words.has(hit[1]), `分支处理了「${hit[1]}」但正则 ③ 组不认它 —— 分支永远走不到`)
    }
  })

  it('★ 「下个?月X」分支必须排在 anchor 分支之前（否则答成本月）', () => {
    // ⚠ 这是 §229.2 最容易回归的一格：把两个分支的顺序对调，
    //   「下个月底」会重新变成本月底，而**所有值断言仍会绿**——
    //   除了 C 组里那条 `notEqual(at('下个月底'), at('月底'))`。
    //   这一格是让那个顺序问题在源码层也显形的冗余护栏。
    const nextAt = src.indexOf('const nextMonthAnchor =')
    const anchorAt = src.indexOf('const anchor =')
    assert.ok(nextAt > 0 && anchorAt > 0, '找不到这两个分支 —— 判据与代码结构脱节了')
    assert.ok(nextAt < anchorAt,
      '「下个?月X」分支排在 anchor 之后 ⇒ 「下个月底」会被裸「月底」抢走并答成**本月**')
  })
})

describe('§229.2 F · resolveTodoDue 透出（前端真正用的是它）', () => {
  it('口语锚点经 resolveTodoDue 同样解出', () => {
    for (const raw of ['月底前', '季度末', '下个月底', '月初']) {
      const r = resolveTodoDue(raw, NOW)
      assert.ok(r, `resolveTodoDue 没解析出来：${raw}`)
      assert.equal(fmt(r.at), at(raw), `${raw} 两层读数不一致`)
    }
  })
})