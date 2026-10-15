// meeting-due-anchor.test.ts — 门禁：「月底/月初/季末/年底」必须能解析成时刻。
//
// 2026-10-07 真网关实测（TestLiveGatewayRollingSummary）抓到的断点，
// 打在用户需求「将一些时间点自动加入到计划日程中」上：
//
//	转写原文「另外续期合同要在**十一月底之前**签完，这是硬指标」
//	模型 due  auto   → "十一月底之前" / "十一月底前" / "11月底"
//	      glm-5.2 → "十一月底之前" / "十一月底前"
//	解析器    ✗ **一个都解析不出来** ⇒ due_at 为空 ⇒ 不建提醒
//
// ⇒ 待办照样入库、界面照常显示、**不报任何错** —— 时间点就是没进日程。
// 这是「合法但空」家族（§21）的又一次：不是失败，是静默地什么都没发生。
//
// ★ 用例里的字符串是**真网关吐出来的原文**，不是我编的。
//   照着模型实际会给的措辞写用例，而不是照着我以为的措辞 ——
//   「夹具形状必须照抄真实源」（§14/§25.1）。
//
// 基准时间固定为 2026-10-06（周二）15:00 +08:00，本地时区解释。
import { describe, it } from 'node:test'
import assert from 'node:assert/strict'
import { parseDueAt } from '../meeting-due.ts'
import { resolveTodoDue } from '../../meetings/meeting-due-plan.ts'

const NOW = new Date('2026-10-06T07:00:00Z').getTime() // 2026-10-06 15:00 CST 周二
const fmt = (at: number) => {
  const d = new Date(at)
  const p = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`
}

describe('月度/季度锚点（真网关 due 实测样本）', () => {
  // ── 第一组：模型真实产出过的字符串，一个都该解析得出来 ──
  it('模型实际产出的 due 全部解析得出来（真网关 2026-10-07 实测）', () => {
    const fromModels = [
      { due: '十一月底之前', why: 'glm-5.2 轮3' },
      { due: '十一月底前', why: '两臂都产出' },
      { due: '十一月底', why: 'auto 轮3/4' },
      { due: '11月底', why: 'auto 轮5' },
    ]
    for (const c of fromModels) {
      const r = parseDueAt(c.due, NOW)
      assert.ok(r, `解析失败：${c.due}（${c.why}）—— 硬期限进不了日程`)
      assert.equal(fmt(r.at), '2026-11-30 09:00', `${c.due} 解析成了 ${fmt(r.at)}`)
    }
  })

  it('resolveTodoDue 这一层同样透出（前端真正用的是它）', () => {
    const r = resolveTodoDue('十一月底之前', NOW)
    assert.ok(r, 'resolveTodoDue 也没解析出来')
    assert.equal(fmt(r.at), '2026-11-30 09:00')
  })

  // ── 第二组：边界。⚠ 第一版这里全是坏的（只测了主路径）──
  it('底/末 = 该月最后一天，初 = 1 号，中 = 15 号', () => {
    const cases: Array<[string, string]> = [
      ['十一月底', '2026-11-30 09:00'],
      ['12月底', '2026-12-31 09:00'],
      ['二月初', '2027-02-01 09:00'],
      ['三月中', '2027-03-15 09:00'],
    ]
    for (const [raw, want] of cases) {
      const r = parseDueAt(raw, NOW)
      assert.ok(r, `解析失败：${raw}`)
      assert.equal(fmt(r.at), want, `${raw} → ${fmt(r.at)}，期望 ${want}`)
    }
  })

  it('季末 / 年底 / 本月 各自取正确的锚点', () => {
    // 2026-10 是 Q4 ⇒ 季末 = 12/31
    assert.equal(fmt(parseDueAt('季末', NOW)!.at), '2026-12-31 09:00')
    assert.equal(fmt(parseDueAt('年底', NOW)!.at), '2026-12-31 09:00')
    assert.equal(fmt(parseDueAt('本月底', NOW)!.at), '2026-10-31 09:00')
    // ⚠ 「本月初」已过去但**不得滚年**：10 月 6 日说「本月初」就是 10/1，
    //   滚到明年十月是荒谬的。第一版把它滚成了 2027-10-01。
    assert.equal(fmt(parseDueAt('本月初', NOW)!.at), '2026-10-01 09:00')
  })

  // ⚠ 这条用例第一版**期望就写错了**：以为 12/15 时 Q4 已过。
  //   12/31 还没到，答案本来就是 12/31 —— 是量具错，不是代码错。
  //   「判据红了先问量具坏了吗」，本会话第四次。
  it('季末已过则滚一个季度（不是滚到明年同一天）', () => {
    const onQuarterEnd = new Date('2026-12-31T02:00:00Z').getTime() // 12/31 当天，Q4 已到最后一刻
    const r = parseDueAt('季末', onQuarterEnd)
    assert.ok(r)
    assert.equal(fmt(r.at), '2027-03-31 09:00', '12/31 说「季末」应指明年 3/31')
  })

  it('季度末必须是该季真实最后一天（9 月/6 月只有 30 天）', () => {
    // ⚠ 第一版把季度末写死 dd=31，JS 把 9/31 静默溢出成 10/1 ——
    //   实测 2026-09-30 说「季末」得到的是 10/1，一个完全错误的日子。
    const sep30 = new Date('2026-09-30T02:00:00Z').getTime()
    assert.equal(fmt(parseDueAt('季末', sep30)!.at), '2026-12-31 09:00')
    const mar31 = new Date('2027-03-31T02:00:00Z').getTime()
    assert.equal(fmt(parseDueAt('季末', mar31)!.at), '2027-06-30 09:00', '6 月只有 30 天')
  })

  it('2 月底按目标年算（闰年 29 天 / 平年 28 天）', () => {
    const in2027 = new Date('2027-01-15T02:00:00Z').getTime()
    assert.equal(fmt(parseDueAt('二月底', in2027)!.at), '2027-02-28 09:00')
    const in2028 = new Date('2028-01-15T02:00:00Z').getTime()
    assert.equal(fmt(parseDueAt('二月底', in2028)!.at), '2028-02-29 09:00', '2028 是闰年')
  })

  it('⚠ 目标年 ≠ 基准年时，月末天数必须按**目标年**算', () => {
    // 这条是补判据洞补出来的：第一版用例里 yy 恰好等于 y
    // （1 月说「二月底」，2 > 1 不滚年），于是把「用基准年算」那个变异
    // 也判成通过 —— **判据不判别**，等于没测。
    //
    // 3 月说「二月底」⇒ 目标年是**明年**。
    // 2028-03 说 → 2029-02-28（平年 28 天）；若错用基准年 2028 → 02-29，多一天。
    const mar2028 = new Date('2028-03-15T02:00:00Z').getTime()
    assert.equal(fmt(parseDueAt('二月底', mar2028)!.at), '2029-02-28 09:00')
    // 3 月说「1 月底」同理：2028-03 → 2029-01-31
    assert.equal(fmt(parseDueAt('1月底', mar2028)!.at), '2029-01-31 09:00')
  })

  it('月度锚点要保留原文里的钟点（不能把「下午三点」吞掉）', () => {
    const r = parseDueAt('十一月底下午三点', NOW)
    assert.ok(r, '带钟点的月底没解析出来')
    assert.equal(fmt(r.at), '2026-11-30 15:00', '钟点被吞了')
  })

  it('月份已过则滚到明年', () => {
    const dec = new Date('2026-12-15T07:00:00Z').getTime()
    assert.equal(fmt(parseDueAt('1月底', dec)!.at), '2027-01-31 09:00')
    assert.equal(fmt(parseDueAt('本月底', dec)!.at), '2026-12-31 09:00')
  })

  it('非法月份必须判 null（宁可漏，不要错）', () => {
    for (const bad of ['0月底', '13月底', '十三月底', '本月十三号']) {
      assert.equal(parseDueAt(bad, NOW), null, `${bad} 不该解析出时刻`)
    }
  })

  // ── 第三组：不能带跑偏既有分支 ──
  it('原有 11 类分支读数不变', () => {
    const cases: Array<[string, string]> = [
      ['明天', '2026-10-07 09:00'],
      ['后天', '2026-10-08 09:00'],
      ['3天后', '2026-10-09 09:00'],
      ['下周三', '2026-10-14 09:00'],
      ['周三', '2026-10-07 09:00'],
      ['12月20日', '2026-12-20 09:00'],
      ['3月5号', '2027-03-05 09:00'],
      ['20号', '2026-10-20 09:00'],
      ['下午三点', '2026-10-06 15:00'],
      ['15:30', '2026-10-06 15:30'],
      ['2026-10-09', '2026-10-09 09:00'],
    ]
    for (const [raw, want] of cases) {
      const r = parseDueAt(raw, NOW)
      assert.ok(r, `原本能解析的 ${raw} 现在解析失败了`)
      assert.equal(fmt(r.at), want, `${raw} → ${fmt(r.at)}，期望 ${want}`)
    }
  })

  it('「下周三下午三点前」仍按原来的周X 分支走（模型另一臂的产出）', () => {
    const r = parseDueAt('下周三下午三点前', NOW)
    assert.ok(r)
    assert.equal(fmt(r.at), '2026-10-14 15:00', '带钟点的周X 不能被月度分支抢走')
  })

  it('空串 / null 仍返回 null', () => {
    assert.equal(parseDueAt('', NOW), null)
    assert.equal(resolveTodoDue(null, NOW), null)
  })
})
