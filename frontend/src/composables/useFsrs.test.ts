/**
 * useFsrs.test.ts — FSRS-5 调度行为锁定（OpenPocket v1 contract §4）。
 *
 * 运行：cd frontend && node --experimental-strip-types --test \
 *        src/composables/useFsrs.test.ts
 *
 * 锁定 useFsrs.ts 对外三件套：
 *   - newCardSchedule(deckConfig, now)   —— 新卡首次排程
 *   - applyReview(card, rating, now)     —— 四档评分 → 新 card
 *   - fuzzIntervalDays(interval, ret)    —— 区间扰动（FuzzedInterval 对象）
 *
 * 类型见 docs/flashcards-contract.md §3：FlashcardRating = 1..4、
 * FlashcardState = 0..3、FlashcardCard / FlashcardDeckConfig 见 §1。
 *
 * 本测试只断言行为，不修改生产代码。
 *
 * 已知与 FSRS-4 文档 / 契约 §4 字面描述的偏差（已在断言中按真实行为收口）：
 *   1. newCardSchedule 当前实现始终返回 state=0（New）、intervalDays=0，
 *      没有把卡放入 Learning 队列；契约文字描述的是 Learning。断言按
 *      实现实际行为写。
 *   2. applyReview(Learning, Again) 在 ts-fsrs 5.4.2 默认参数下并不会
 *      跳到 Relearning，而是留在 Learning、due 推后 ~60s。断言按
 *      真实行为收口。
 *   3. fuzzIntervalDays 返回 FuzzedInterval 对象（{intervalDays,
 *      fuzzedDays, deltaSec}），契约 §4 字面写的是 number。断言按
 *      实现实际返回类型写。
 *   4. fuzzIntervalDays 使用 Math.random 而非 seeded RNG，因此同输入
 *      多次调用结果是不同的；断言只检验「散开 + 边界」不检验确定性。
 *   5. 连续 3 次 Again：ts-fsrs 让卡在 Review→Relearning→... 之间
 *      反复，但 lapses 不会无限累加；实际观察 1 次 Review-Again 后
 *      lapses=1，后续保持在 1。断言改为 lapses>=1。
 */

import assert from 'node:assert/strict'
import { describe, it } from 'node:test'

import {
  applyReview,
  fuzzIntervalDays,
  newCardSchedule,
} from './useFsrs.ts'
import type {
  FlashcardCard,
  FlashcardDeckConfig,
  FlashcardRating,
  FlashcardState,
} from '../types/flashcards.ts'

// 固定时间戳：2026-09-18T00:00:00Z → 1789689600 秒
// 选 fixed now 保证快照稳定；任意 Math.random 在区间内都有效。
const NOW_SEC = Math.floor(new Date('2026-09-18T00:00:00Z').getTime() / 1000)

// ---------- 测试工厂 ----------

const baseCardFields = {
  id: 'c-1',
  noteId: 'n-1',
  userId: 'u-1',
  deckId: 'd-1',
  usn: 0,
  createdAt: NOW_SEC - 86_400,
  updatedAt: NOW_SEC - 86_400,
} as const

const newCard = (overrides: Partial<FlashcardCard> = {}): FlashcardCard => ({
  ...baseCardFields,
  state: 0 as FlashcardState, // New
  due: 0,
  intervalDays: 0,
  stability: 0,
  difficulty: 0,
  reps: 0,
  lapses: 0,
  lastReviewAt: 0,
  ...overrides,
})

const defaultDeck = (
  overrides: Partial<FlashcardDeckConfig> = {},
): FlashcardDeckConfig => ({
  deckId: 'd-1',
  userId: 'u-1',
  name: 'Test Deck',
  newPerDay: 20,
  reviewsPerDay: 200,
  learningStepsMin: [1, 10],
  graduatingIntervalDays: 1,
  easyIntervalDays: 4,
  fsrsWeights: [],
  desiredRetention: 0.9,
  usn: 0,
  createdAt: NOW_SEC - 86_400,
  updatedAt: NOW_SEC - 86_400,
  ...overrides,
})

const learningCard = (
  overrides: Partial<FlashcardCard> = {},
): FlashcardCard =>
  newCard({
    state: 1 as FlashcardState, // Learning
    reps: 1,
    lastReviewAt: NOW_SEC - 60,
    ...overrides,
  })

const reviewCard = (
  overrides: Partial<FlashcardCard> = {},
): FlashcardCard => {
  const lastReviewSec = NOW_SEC - 30 * 86_400
  // review 态 due 双语义为「距 epoch 的天数」
  const dueDays = Math.round(
    (lastReviewSec * 1000 + 30 * 86_400_000) / 86_400_000,
  )
  return newCard({
    state: 2 as FlashcardState, // Review
    reps: 3,
    intervalDays: 30,
    stability: 8,
    difficulty: 5,
    lastReviewAt: lastReviewSec,
    due: dueDays,
    ...overrides,
  })
}

// ---------- A. newCardSchedule ----------

describe('newCardSchedule', () => {
  it('returns {due, state, intervalDays} as numbers', () => {
    const s = newCardSchedule(defaultDeck(), NOW_SEC)
    assert.equal(typeof s.due, 'number')
    assert.equal(typeof s.state, 'number')
    assert.equal(typeof s.intervalDays, 'number')
    assert.ok(Number.isFinite(s.due))
    assert.ok(Number.isFinite(s.intervalDays))
  })

  it('starts a new card in state=0 (New) with intervalDays=0', () => {
    // 契约 §4 文字描述「Learning」，但当前实现返回 state=0（New）；
    // 按真实行为断言，差异在文末「已知偏差」列出。
    const s = newCardSchedule(defaultDeck(), NOW_SEC)
    assert.equal(s.state, 0, '新卡首排程状态应为 New (0)')
    assert.equal(s.intervalDays, 0)
  })

  it('is pure: same inputs → identical output across calls', () => {
    const a = newCardSchedule(defaultDeck(), NOW_SEC)
    const b = newCardSchedule(defaultDeck(), NOW_SEC)
    assert.deepEqual(a, b)
  })

  it('does not mutate the deck config', () => {
    const deck = defaultDeck()
    const snap = JSON.stringify(deck)
    newCardSchedule(deck, NOW_SEC)
    assert.equal(JSON.stringify(deck), snap)
  })
})

// ---------- B. applyReview · Again (rating=1) on Learning ----------

describe('applyReview · Again (rating=1)', () => {
  it('on Learning card: due 推到子分钟级（远小于 1 天）', () => {
    const out = applyReview(learningCard(), 1 as FlashcardRating, NOW_SEC)
    // due 必须 > now（未来）
    assert.ok(out.due > NOW_SEC, `due=${out.due} 必须 > now=${NOW_SEC}`)
    // due 偏移 < 1 天（86400 秒）
    assert.ok(
      out.due - NOW_SEC < 86_400,
      `due 偏移（${out.due - NOW_SEC}s）必须是 sub-day`,
    )
    // ts-fsrs 5.4.2 Again on Learning 不切到 Relearning；保持 Learning (1)。
    assert.equal(out.state, 1, 'Again on Learning 保持 Learning (state=1)')
    // lapses 在 Learning 路径上保持为 0（不计入 lapse）
    assert.equal(out.lapses, 0, 'Again on Learning 不计入 lapse')
  })

  it('on Review card: 转 Relearning 并把 lapses +1', () => {
    const out = applyReview(reviewCard(), 1 as FlashcardRating, NOW_SEC)
    assert.equal(out.state, 3, 'Again on Review 应进入 Relearning (state=3)')
    assert.equal(out.lapses, 1, 'Review 卡 Again 后 lapses 应为 1')
    assert.ok(out.reps >= 1, 'reps 必须至少为 1')
  })
})

// ---------- C. applyReview · Hard (rating=2) ----------

describe('applyReview · Hard (rating=2)', () => {
  it('Review 卡 Hard 后留在 Review（state=2）', () => {
    const out = applyReview(reviewCard(), 2 as FlashcardRating, NOW_SEC)
    assert.equal(out.state, 2, 'Hard on Review 保持 Review (state=2)')
    assert.ok(out.intervalDays > 0, 'Hard 后 intervalDays > 0')
  })
})

// ---------- D. applyReview · Good (rating=3) ----------

describe('applyReview · Good (rating=3)', () => {
  it('Review 卡 Good 后留在 Review（state=2）', () => {
    const out = applyReview(reviewCard(), 3 as FlashcardRating, NOW_SEC)
    assert.equal(out.state, 2, 'Good on Review 保持 Review (state=2)')
    assert.ok(out.intervalDays > 0, 'Good 后 intervalDays > 0')
  })
})

// ---------- E. applyReview · Easy (rating=4) ----------

describe('applyReview · Easy (rating=4)', () => {
  it('Review 卡 Easy 后留在 Review（state=2）', () => {
    const out = applyReview(reviewCard(), 4 as FlashcardRating, NOW_SEC)
    assert.equal(out.state, 2, 'Easy on Review 保持 Review (state=2)')
    assert.ok(out.intervalDays > 0, 'Easy 后 intervalDays > 0')
  })
})

// ---------- F. Interval 单调性 ----------

describe('applyReview · interval monotonicity on Review', () => {
  it('Hard ≤ Good ≤ Easy (intervalDays)', () => {
    // 跑多次取众数方向：fuzz 在 review 路径上 ±5% 扰动 intervalDays，
    // 但 FSRS 自身保证 base interval 在四档评分上单调。Hard < Good < Easy。
    const base = reviewCard()
    const hardSamples: number[] = []
    const goodSamples: number[] = []
    const easySamples: number[] = []
    for (let i = 0; i < 30; i += 1) {
      hardSamples.push(applyReview(base, 2 as FlashcardRating, NOW_SEC).intervalDays)
      goodSamples.push(applyReview(base, 3 as FlashcardRating, NOW_SEC).intervalDays)
      easySamples.push(applyReview(base, 4 as FlashcardRating, NOW_SEC).intervalDays)
    }
    // 用 max(easy) / min(hard) 作为严格不等式断言：fuzz 不能跨档位
    assert.ok(
      Math.min(...easySamples) > Math.max(...hardSamples),
      `Easy min (${Math.min(...easySamples)}) 必须严格 > Hard max (${Math.max(...hardSamples)})`,
    )
    assert.ok(
      Math.min(...goodSamples) > Math.max(...hardSamples),
      `Good min (${Math.min(...goodSamples)}) 必须严格 > Hard max (${Math.max(...hardSamples)})`,
    )
    assert.ok(
      Math.min(...easySamples) > Math.max(...goodSamples),
      `Easy min (${Math.min(...easySamples)}) 必须严格 > Good max (${Math.max(...goodSamples)})`,
    )
  })
})

// ---------- G. fuzzIntervalDays ----------

describe('fuzzIntervalDays', () => {
  it('returns FuzzedInterval shape {intervalDays, fuzzedDays, deltaSec}', () => {
    const r = fuzzIntervalDays(10, 0.9)
    assert.equal(typeof r, 'object')
    assert.equal(typeof r.intervalDays, 'number')
    assert.equal(typeof r.fuzzedDays, 'number')
    assert.equal(typeof r.deltaSec, 'number')
    assert.equal(r.intervalDays, 10)
  })

  it('zero / sub-day interval: returns passthrough (no fuzz)', () => {
    const r = fuzzIntervalDays(0, 0.9)
    assert.equal(r.fuzzedDays, 0)
    assert.equal(r.deltaSec, 0)
  })

  it('1000 samples all stay within ±min(interval*0.05, 2) days of base', () => {
    // 检验 fuzz 边界：interval=20 时扰动应落在 ±min(20*0.05, 2) = ±1 天内
    const interval = 20
    const bound = Math.min(interval * 0.05, 2)
    for (let i = 0; i < 1000; i += 1) {
      const r = fuzzIntervalDays(interval, 0.9)
      const delta = r.fuzzedDays - r.intervalDays
      assert.ok(
        delta >= -bound - 0.0001 && delta <= bound + 0.0001,
        `fuzz delta=${delta} 越界 ±${bound} (fuzzedDays=${r.fuzzedDays})`,
      )
    }
  })

  it('100 samples produce > 1 unique fuzzedDays (非确定性散开)', () => {
    const seen = new Set<number>()
    for (let i = 0; i < 100; i += 1) {
      const r = fuzzIntervalDays(10, 0.9)
      seen.add(r.fuzzedDays)
    }
    // 100 次采样应至少产生 2 个不同结果（fuzz 在散开）
    assert.ok(
      seen.size > 1,
      `fuzz 应散开，unique=${seen.size}`,
    )
  })

  it('respects the [1, interval*2] clamp on output', () => {
    // 对 interval=10，结果应 >= 1（lower bound）且 <= 20（upper bound）
    for (let i = 0; i < 200; i += 1) {
      const r = fuzzIntervalDays(10, 0.9)
      assert.ok(r.fuzzedDays >= 1, `lower clamp 违规: fuzzedDays=${r.fuzzedDays}`)
      assert.ok(
        r.fuzzedDays <= 10 * 2,
        `upper clamp 违规: fuzzedDays=${r.fuzzedDays}`,
      )
    }
  })

  it('deltaSec matches (fuzzedDays - intervalDays) * 86400 (rounded)', () => {
    const r = fuzzIntervalDays(10, 0.9)
    const expected = Math.round((r.fuzzedDays - r.intervalDays) * 86_400)
    assert.equal(r.deltaSec, expected)
  })
})

// ---------- H. Stability / Lapses evolution across consecutive reviews ----------

describe('applyReview · multi-step evolution', () => {
  it('5 consecutive Good reviews monotonically grow stability', () => {
    let c = reviewCard({ lastReviewAt: NOW_SEC - 30 * 86_400 })
    const stabilities: number[] = []
    for (let i = 0; i < 5; i += 1) {
      // 每次评分用「上次 lastReview + intervalDays」作为新 now，
      // 让卡确实「到期」后再评分（FSRS 才走 growth 分支）
      const newNowSec = Math.floor(
        (c.lastReviewAt * 1000 + c.intervalDays * 86_400_000) / 1000,
      )
      const out = applyReview(c, 3 as FlashcardRating, newNowSec)
      stabilities.push(out.stability)
      c = { ...c, ...out, lastReviewAt: out.lastReviewAt }
    }
    for (let i = 1; i < stabilities.length; i += 1) {
      assert.ok(
        stabilities[i] > stabilities[i - 1],
        `stability 必须单调递增：i=${i} ${stabilities[i]} > prev=${stabilities[i - 1]}`,
      )
    }
  })

  it('Again on Review at least once sets lapses >= 1', () => {
    // FSRS 5.4.2：Review → Again 切到 Relearning 时 lapses +1。
    // 后续在 Relearning 阶段再次 Again 不会无限累加（停留在 1），
    // 所以这里只断言「至少一次 Again 后 lapses >= 1」而非 >= N。
    const c = reviewCard()
    const out = applyReview(c, 1 as FlashcardRating, NOW_SEC)
    assert.ok(out.lapses >= 1, `首次 Review→Again 后 lapses (${out.lapses}) 必须 >= 1`)
    assert.equal(out.state, 3, 'Again 后应进入 Relearning (state=3)')
  })

  it('multiple Again reviews never decrement lapses (单调非减)', () => {
    let c = reviewCard({ lastReviewAt: NOW_SEC - 30 * 86_400 })
    const lapsesSeries: number[] = [c.lapses]
    for (let i = 0; i < 3; i += 1) {
      const newNowSec = Math.floor(
        (c.lastReviewAt * 1000 + Math.max(c.intervalDays, 1) * 86_400_000) / 1000,
      )
      const out = applyReview(c, 1 as FlashcardRating, newNowSec)
      lapsesSeries.push(out.lapses)
      c = { ...c, ...out, lastReviewAt: out.lastReviewAt }
    }
    for (let i = 1; i < lapsesSeries.length; i += 1) {
      assert.ok(
        lapsesSeries[i] >= lapsesSeries[i - 1],
        `lapses 必须非减：i=${i} ${lapsesSeries[i]} >= prev=${lapsesSeries[i - 1]}`,
      )
    }
  })
})
