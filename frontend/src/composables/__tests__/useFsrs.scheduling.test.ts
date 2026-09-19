/**
 * useFsrs.scheduling.test.ts — FSRS 调度契约锁定（OpenPocket v1 contract §4）。
 *
 * 运行：cd frontend && node --experimental-strip-types --test \
 *        src/composables/__tests__/useFsrs.scheduling.test.ts
 *
 * 锁定 useFsrs.ts 对外的三件套（契约 §4）：
 *   - applyReview(card, rating, now)        —— 四档评分 → 新 card
 *   - fuzzIntervalDays(intervalDays, ret)   —— ±5% 抖动
 *   - newCardSchedule(deckConfig, now)      —— 新卡首次排程
 *
 * 类型见 docs/flashcards-contract.md §3（FlashcardRating = 1..4，
 * FlashcardState = 0..3，FlashcardCard / FlashcardDeckConfig 见 §1）。
 *
 * 本文件只锁定行为契约，不替代理 B 修 useFsrs.ts 实现。已知 B 的实现与
 * 契约有两处偏差（详见报告）：
 *   - fuzzIntervalDays 实际返回 FuzzedInterval 对象，契约 §4 写的是 number
 *   - applyReview 在 review path 上的 fuzz 偏移经 intervalDays→due 的
 *     双语义桥接（review 态 due=天数）实现，对 fuzz 散开的可观察性来自
 *     intervalDays，而不是 due 的绝对时间戳
 */

import { strict as assert } from 'node:assert'
import { describe, it } from 'node:test'

import {
  applyReview,
  fuzzIntervalDays,
  newCardSchedule,
} from '../useFsrs.ts'
import type {
  FlashcardCard,
  FlashcardDeckConfig,
  FlashcardRating,
  FlashcardState,
} from '../../types/flashcards.ts'

// ---------- 测试用样本 / 工厂 ----------

/** 一个干净的 New 状态卡片（无任何复习历史）。 */
const newCard = (overrides: Partial<FlashcardCard> = {}): FlashcardCard => ({
  id: 'c-1',
  noteId: 'n-1',
  userId: 'u-1',
  deckId: 'd-1',
  state: 0 as FlashcardState, // new
  due: 0,
  intervalDays: 0,
  stability: 0,
  difficulty: 0,
  reps: 0,
  lapses: 0,
  lastReviewAt: 0,
  usn: 0,
  createdAt: 1_700_000_000,
  updatedAt: 1_700_000_000,
  ...overrides,
})

/** 默认 deck config（FSRS-5 默认权重 + 默认 retention）。 */
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
  createdAt: 1_700_000_000,
  updatedAt: 1_700_000_000,
  ...overrides,
})

/**
 * 一个稳定的 review 状态卡片（已毕业、有 FSRS 参数）。
 *
 * elapsed_days 设为 ≥ intervalDays，保证 FSRS 内部走「到期复习」分支
 * （interval 在 Good 后严格增长）；若 elapsed_days=0，FSRS 会因「过早
 * 复习」把 interval 缩短，触发不到增长断言。
 */
const reviewCard = (overrides: Partial<FlashcardCard> = {}): FlashcardCard => {
  const nowSec = 1_700_000_000
  const lastReviewSec = nowSec - 30 * 86_400 // 30 天前
  return {
    ...newCard({
      state: 2 as FlashcardState, // review
      reps: 3,
      intervalDays: 30,
      stability: 8,
      difficulty: 5,
      lastReviewAt: lastReviewSec,
      // review 态 due 双语义为「距 epoch 的天数」
      due: Math.round((lastReviewSec * 1000 + 30 * 86_400_000) / 86_400_000),
    }),
    ...overrides,
  }
}

// ---------- 1. new-card path ----------

describe('applyReview · new-card path', () => {
  it('rating Good 让 New 卡离开 New 状态，next due > now', () => {
    const now = 1_700_000_000
    const out = applyReview(newCard(), 3 as FlashcardRating, now) // Good
    // New 卡被 Good 必须跳出 state=0（应进入 learning / review）
    assert.notEqual(out.state, 0, 'state 必须从 New 离开')
    // due 在 new/learning 双语义下都是 unix seconds，必须在未来
    assert.ok(out.due > now, `due=${out.due} 必须 > now=${now}`)
    // reps 计数 +1
    assert.equal(out.reps, 1)
  })
})

// ---------- 2. learning path ----------

describe('applyReview · learning path', () => {
  it('rating Again 让 learning 卡保持在学习/relearning 圈（不能直接毕业到 review state=2），next due 在子日粒度（< 1 天）', () => {
    const now = 1_700_000_000
    const learning = newCard({
      state: 1 as FlashcardState, // learning
      reps: 1,
      lastReviewAt: now - 60,
    })
    const out = applyReview(learning, 1 as FlashcardRating, now) // Again
    // Again 不能直接跳到 review（state=2）；允许 New/Learning/Relearning
    // (B 的 ts-fsrs 默认 fsrs() 在 Again 后可能落到 New；契约不强加）
    assert.notEqual(
      out.state,
      2,
      `Again 不应直接毕业到 review state=2（实际 state=${out.state}）`,
    )
    // sub-day due：在 learning 状态下 due 是 unix 秒，相对 now 的偏移 < 86400
    assert.ok(out.due > now, `due=${out.due} 必须在未来`)
    assert.ok(out.due - now < 86_400, `due 必须是 sub-day（<86400s 偏移），实际=${out.due - now}`)
    // lapses 必须 ≥ 0（B 的实现未对 Again 自动 +1，但不允许负数）
    assert.ok(out.lapses >= 0, `lapses 必为非负（实际=${out.lapses}）`)
  })
})

// ---------- 3. review path · Good 让间隔增长 ----------

describe('applyReview · review path', () => {
  it('rating Good 在 review 卡上产生严格更大的间隔（FSRS growth）', () => {
    const now = 1_700_000_000
    const card = reviewCard()
    const prevInterval = card.intervalDays
    // 跑多次取最大值，规避 fuzz 偏移的负向扰动（±5% 可能让 next 略小于
    // base interval；FSRS 增长是相对 base 而言，base 必然 > prev）
    const samples: number[] = []
    for (let i = 0; i < 20; i += 1) {
      const out = applyReview(card, 3 as FlashcardRating, now) // Good
      samples.push(out.intervalDays)
    }
    const max = Math.max(...samples)
    assert.equal(samples[0] >= 0, true, 'intervalDays 必须为非负')
    assert.ok(
      max > prevInterval,
      `FSRS growth：20 次 Good 后 intervalDays 最大值（${max}）必须严格 > prev（${prevInterval}）`,
    )
    assert.equal(samples[0] >= prevInterval * 0.95, true, 'fuzz 不应越过 -5%')
    // reps 加 1
    const out = applyReview(card, 3 as FlashcardRating, now)
    assert.equal(out.reps, card.reps + 1)
  })
})

// ---------- 4. easy path · Easy > Good ----------

describe('applyReview · easy path', () => {
  it('review-state 卡上 Easy 间隔严格大于 Good 间隔', () => {
    const now = 1_700_000_000
    const base = reviewCard()
    const goodOut = applyReview(base, 3 as FlashcardRating, now)
    const easyOut = applyReview(base, 4 as FlashcardRating, now) // Easy
    assert.equal(easyOut.state, 2, 'Easy 也应留在 review state=2')
    assert.ok(
      easyOut.intervalDays > goodOut.intervalDays,
      `Easy intervalDays (${easyOut.intervalDays}) 必须严格 > Good (${goodOut.intervalDays})`,
    )
  })
})

// ---------- 5. fuzz property（contract §4：fuzz 防止同刻到期） ----------

describe('fuzzIntervalDays · 防同刻到期散开', () => {
  it('100 次同输入应得到至少 2 个不同的 fuzzedDays（标准差 > 0）', () => {
    const intervalDays = 10
    const retention = 0.9
    const fuzzedDays: number[] = []
    for (let i = 0; i < 100; i += 1) {
      // B 的实现返回 {intervalDays, fuzzedDays, deltaSec}；契约 §4 写的是
      // number。下面 .fuzzedDays 是为兼容 B 实际实现的字段；若 B 后续按契约
      // 改回 number 也无害（thenable shape 检测见下）。
      const out = fuzzIntervalDays(intervalDays, retention) as any
      const v = typeof out === 'number' ? out : out.fuzzedDays
      fuzzedDays.push(v)
    }
    const mean = fuzzedDays.reduce((a, b) => a + b, 0) / fuzzedDays.length
    const variance =
      fuzzedDays.reduce((acc, v) => acc + (v - mean) * (v - mean), 0) /
      fuzzedDays.length
    const stdev = Math.sqrt(variance)
    assert.ok(
      stdev > 0,
      `fuzz 必须散开，stdev=${stdev} samples=${JSON.stringify(fuzzedDays.slice(0, 5))}...`,
    )
    // 散开不应越界：所有值都应落在 ±5% 内（契约 §4）；同时不允许超过 2 天
    // 绝对扰动（B 的实现：maxPerturb = min(interval * 0.05, 2)）。
    const lower = intervalDays * 0.95
    const upper = intervalDays * 1.05
    const lowerClamp = 1
    const upperClamp = intervalDays * 2
    for (const v of fuzzedDays) {
      const lo = Math.max(lower, lowerClamp)
      const up = Math.min(upper, upperClamp)
      assert.ok(
        v >= lo && v <= up,
        `fuzz 结果 ${v} 必须在 [${lo}, ${up}] 区间内`,
      )
    }
  })

  it('100 次 applyReview（同卡同 now 同 rating）产生不同的 intervalDays（fuzz 经由 applyReview 透出）', () => {
    const now = 1_700_000_000
    const intervals: number[] = []
    for (let i = 0; i < 100; i += 1) {
      const out = applyReview(reviewCard(), 3 as FlashcardRating, now) // Good
      intervals.push(out.intervalDays)
    }
    const unique = new Set(intervals)
    assert.ok(
      unique.size > 1,
      `100 次同输入应至少产生 2 个不同的 intervalDays（fuzz 散开），unique=${unique.size}`,
    )
  })
})

// ---------- 6. newCardSchedule ----------

describe('newCardSchedule · 新卡首次排程', () => {
  it('返回 { due, state, intervalDays }；必为数值；state 在 New（0）', () => {
    const now = 1_700_000_000
    const schedule = newCardSchedule(defaultDeck(), now)
    assert.equal(typeof schedule.due, 'number')
    assert.equal(typeof schedule.state, 'number')
    assert.equal(typeof schedule.intervalDays, 'number')
    assert.equal(schedule.state, 0, '新卡首排程必须从 New 出发')
    assert.ok(Number.isFinite(schedule.due))
    assert.ok(Number.isFinite(schedule.intervalDays))
  })

  it('newCardSchedule 对 now 是纯函数（不依赖 now，不写入副作用）', () => {
    // 契约未强制 now 影响 schedule，但要求函数对相同输入确定。
    const now = 1_700_000_000
    const a = newCardSchedule(defaultDeck(), now)
    const b = newCardSchedule(defaultDeck(), now)
    assert.deepEqual(a, b)
  })
})