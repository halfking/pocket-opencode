// live-summary-refresh-gate.test.mjs — §418：滚动摘要**「这一轮要不要跑」**的闸。
//
// ── 为什么这道门存在（且以前一道都没有）──
//
// `useLiveSummary.refresh()` 里那条闸此前是**内联的一行**：
//
//     if (!force && !topicChanged && newCount < 3 && elapsed < 30_000) return
//
// 三个条件、两个常量都在，但**没有任何行为断言**验证过
// 「哪个条件单独就能触发」。这与 §52 在 `planRefine` 上修掉的是**同一个形状**：
// 决策内联在 composable 里 ⇒ 它一行行为门都没有 ⇒ 改坏了没有任何测试会响。
// 本轮把决策提成纯函数 `shouldRefreshSummary` 并用这道门钉住它的语义。
//
// ⭐ 钉的是**四选一的或**，不是「每 30 秒一次」：
//
//     force || topicChanged || newCount >= 3 || elapsed >= 30_000
//
// 这条事实有三处用户可见的后果，所以值得钉：
//   ① 密集讨论时实际节奏快于 30 秒（仓库里有两处曾写成单一的「每 30 秒」）；
//   ② **每轮都跟着跑一次 `/recommend` 的 LLM 调用** ⇒ 调用次数由分段到达速度决定；
//   ③ 「主题切换时立刻刷新」是产品特性 —— 把它误改成纯节流会悄悄砍掉它。
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, it } from 'node:test'

import { shouldRefreshSummary, SUMMARY_INTERVAL_MS, SUMMARY_SEGMENT_THRESHOLD } from '../live-summary-gate.ts'

const HERE = path.dirname(fileURLToPath(import.meta.url))
const SRC = path.resolve(HERE, '..', '..', '..')
const LIVE_SRC = readFileSync(path.join(SRC, 'composables', 'useLiveSummary.ts'), 'utf8')

/** 一组「四个条件都不成立」的基线入参，逐条只放开一个。 */
const QUIET = { force: false, topicChanged: false, newCount: 1, elapsedMs: 1_000 }

describe('§418 · 滚动摘要的触发闸', () => {
  it('前提自证：基线确实一个条件都不成立', () => {
    assert.equal(shouldRefreshSummary(QUIET), false,
      '基线就触发了 ⇒ 下面「各自单独触发」那几条证明不了任何东西')
  })

  it('★ 四个触发条件必须**各自单独**成立', () => {
    const cases = [
      { what: 'force（换场/强制）', patch: { force: true } },
      { what: '主题切换', patch: { topicChanged: true } },
      // elapsed=0：只有 newCount 这一条可能放行它 ⇒ 证明不是靠时间兜的底
      { what: '新增 ≥3 段', patch: { newCount: 3, elapsedMs: 0 } },
      { what: '距上次 ≥30s', patch: { newCount: 0, elapsedMs: 30_000 } },
    ]
    for (const { what, patch } of cases) {
      assert.equal(shouldRefreshSummary({ ...QUIET, ...patch }), true,
        `只满足「${what}」时不刷新 ⇒ 这一条触发条件是死的`)
    }
  })

  it('★ 边界：差一个都不行，差一个都必须行', () => {
    // ⚠ 边界值跟着**导出的常量**走，不写死数字：
    //   否则有人把阈值从 3 改成 5 时，这条判据会红，而那未必是缺陷 ——
    //   判据该在「语义被改坏」时红，不在「数字被改了」时红。
    const T = SUMMARY_SEGMENT_THRESHOLD
    const I = SUMMARY_INTERVAL_MS
    assert.equal(T, 3, `默认阈值变成了 ${T} —— 本判据的边界值按 3 推的，请一并复核`)
    assert.equal(I, 30_000, `默认间隔变成了 ${I} —— 同上`)
    assert.equal(shouldRefreshSummary({ ...QUIET, newCount: T - 1, elapsedMs: I - 1 }), false,
      `差一个（${T - 1} 段 / ${I - 1}ms）就刷新 ⇒ 阈值形同虚设，成本会翻倍`)
    assert.equal(shouldRefreshSummary({ ...QUIET, newCount: T, elapsedMs: 0 }), true)
    assert.equal(shouldRefreshSummary({ ...QUIET, newCount: 0, elapsedMs: I }), true)
  })

  it('首轮那一轮必然刷新（elapsed = now - 0 ⇒ 极大值）', () => {
    // 首轮 lastUpdateAt = 0 ⇒ elapsed 是 Date.now() ⇒ 远大于阈值。
    // 这里用一个小值模拟「刚刚」以证明闸门对 elapsed 是**单调**的。
    assert.equal(shouldRefreshSummary({ newCount: 1, elapsedMs: 30_000 }), true)
  })

  it('阈值可注入（测试与将来的 A/B 复核不必改常量）', () => {
    assert.equal(
      shouldRefreshSummary({ force: false, topicChanged: false, newCount: 10, elapsedMs: 0 }),
      true, '默认 3 段应触发')
    assert.equal(
      shouldRefreshSummary({ force: false, topicChanged: false, newCount: 10, elapsedMs: 0, segmentThreshold: 20 }),
      false, '把阈值抬到 20 段之后，10 段不该触发 ⇒ 形参没接上')
    assert.equal(
      shouldRefreshSummary({ force: false, topicChanged: false, newCount: 0, elapsedMs: 60_000, intervalMs: 120_000 }),
      false, '把间隔抬到 2 分钟之后，60s 不该触发 ⇒ 形参没接上')
  })

  it('★★ 接线层：refresh() 必须真的走这个闸（否则纯函数是第二个 listMeetingTodos）', () => {
    // §402 那条判据的同款理由：`mergeSummaryReferences` 完全可能变成
    // 「有测试、有导出、生产侧没人调」的死函数。
    assert.match(LIVE_SRC, /shouldRefreshSummary\(/,
      'useLiveSummary 里找不到 shouldRefreshSummary 的调用')
    const inline = LIVE_SRC.match(/if\s*\(!force\s*&&\s*!topicChanged/)
    assert.equal(inline, null,
      '内联旧闸还在：两处闸并存 ⇒ 改一处就会与另一处不一致，且看不出哪条生效')
  })

  it('★★ 负控：把闸误改成「纯 30s 节流」（与而不是或）必须判红', () => {
    // 同形负控：样本与真实语义**形状一致**（同样四个字段、同样的比较），
    // 只是把「或」写成「与」—— 这正是最可能被"顺手优化"出来的形态。
    const andNotOr = (i) => Boolean(i.force && i.topicChanged && i.newCount >= 3 && i.elapsedMs >= 30_000)
    for (const patch of [{ force: true }, { topicChanged: true }, { newCount: 3, elapsedMs: 0 }]) {
      assert.equal(andNotOr({ ...QUIET, ...patch }), false,
        '负控前提失效：样本竟然会触发')
      assert.equal(shouldRefreshSummary({ ...QUIET, ...patch }), true,
        '真实现与负控同形 ⇒ 量具坏了，不是判据坏了')
    }
  })
})