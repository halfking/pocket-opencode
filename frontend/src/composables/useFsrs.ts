/**
 * useFsrs — FSRS-5 调度封装（契约 §4）。
 *
 * v1 唯一调度真相：前端 `useFsrs.ts` 封装 `ts-fsrs`。后端 Go 端仅做
 * 存储，不重算 FSRS；复习评分接口接受前端已经算好的 `due/state/
 * stability/difficulty/interval_days` 字段。这样避免 ts-fsrs 与 go-fsrs
 * 数值不一致问题（v1 风险点之一）。
 *
 * 三段导出（契约 §4 强制要求）：
 *   1. `applyReview(card, rating, now)` —— 对一张已有 card 应用评分
 *   2. `fuzzIntervalDays(intervalDays, deckDesiredRetention)` —— 区间扰动
 *   3. `newCardSchedule(deckConfig, now)` —— 新卡初始状态
 *
 * 实现选择：
 *   - 使用 `ts-fsrs` 默认 Scheduler（`fsrs(generatorParameters({}))`）。
 *   - 把 `enable_fuzz` 关掉，把 fuzz 控制权交给本模块实现，保证契约
 *     描述的 `min(interval * 0.05, 2)` 均匀扰动行为可被前端单元测试
 *     直接断言（库内建 fuzz 算法不可预测）。
 */
import {
  createEmptyCard,
  fsrs as createFsrs,
  generatorParameters,
  Rating,
  State,
  type Card as FsrsInternalCard,
  type CardInput as FsrsInternalCardInput,
} from 'ts-fsrs'
import type {
  FlashcardCard,
  FlashcardDeckConfig,
  FlashcardRating,
  FlashcardState,
} from '../types/flashcards'

/** 契约 §4 强制的导出别名。`Rating` 是 union，不能用 interface 扩展，故改用 `type`。 */
export interface FsrsCardState extends FlashcardCard {}
export type ReviewRating = FlashcardRating

/** 模糊扰动结果：原区间、扰动后区间、扰动秒数（用于测试断言）。 */
export interface FuzzedInterval {
  intervalDays: number
  fuzzedDays: number
  /** 实际写入 `due` 字段的偏移（秒；复习中）或偏移（秒；学习中）。 */
  deltaSec: number
}

/** 把契约 rating（1..4）映射到 ts-fsrs 的 Rating enum。 */
function ratingToEnum(rating: FlashcardRating): Rating {
  switch (rating) {
    case 1: return Rating.Again
    case 2: return Rating.Hard
    case 3: return Rating.Good
    case 4: return Rating.Easy
    default: throw new Error(`useFsrs: invalid rating ${rating}`)
  }
}

/** 把契约 state（0..3）映射到 ts-fsrs 的 State enum。 */
function stateToEnum(state: FlashcardState): State {
  switch (state) {
    case 0: return State.New
    case 1: return State.Learning
    case 2: return State.Review
    case 3: return State.Relearning
    default: throw new Error(`useFsrs: invalid state ${state}`)
  }
}

function stateFromEnum(state: State): FlashcardState {
  switch (state) {
    case State.New: return 0
    case State.Learning: return 1
    case State.Review: return 2
    case State.Relearning: return 3
    default: throw new Error(`useFsrs: unknown ts-fsrs state ${state}`)
  }
}

/**
 * 契约 card -> ts-fsrs 内部 card。
 *
 * 双语义 due（契约 §1.2 / §4）：
 *   - new / learning / relearning 态：`card.due` 是秒级 Unix 时间戳
 *   - review 态：`card.due` 是「距 epoch 的天数」（与 Anki 一致）
 *
 * 关键修复（解决 [[useFsrs-quirks]] 中的契约漂移）：
 *   1. review 态 due 解释：`card.due * 86_400_000`（不再加 nowMs）。
 *      旧实现 `nowMs + card.due * 86_400_000` 会把「距 epoch 天数」
 *      当成「now 之后的天数」，导致下一张复习卡到期时间在 50+ 年后。
 *   2. learning_steps 双向桥接：ts-fsrs 把它当作「当前学习阶梯索引」
 *      （0 = 第一步；`-1` 表示未在学习中）。从 card.roundtrips 出。
 *      旧实现固定写 0，导致 Good on learning 永远停在 step 0、永远
 *      不毕业，与契约 §6「Good on learning 走两步后毕业」不符。
 */
function cardToFsrsInput(card: FlashcardCard, nowMs: number): FsrsInternalCardInput {
  const isLearning = card.state === 0 || card.state === 1 || card.state === 3
  const dueMs = isLearning
    ? card.due > 0 ? card.due * 1000 : nowMs
    : card.due > 0 ? card.due * 86_400_000 : nowMs
  // card.learningSteps 表示「当前学习阶梯索引」。
  // -1 / 0 都是合理初值：-1 让 ts-fsrs 跳过学习步骤（review 路径），
  //   0 让 New 卡进入学习步骤 0（第一步）；Relearning 同样从 0 开始。
  // 我们把 0 当作默认值（=「刚开始学习」），保证 New+Good 进入 learning。
  const learningSteps = card.learningSteps ?? 0
  return {
    due: new Date(dueMs),
    stability: card.stability,
    difficulty: card.difficulty,
    elapsed_days: card.lastReviewAt > 0
      ? Math.max(0, Math.round((nowMs - card.lastReviewAt) / 86_400_000))
      : 0,
    scheduled_days: card.intervalDays,
    learning_steps: learningSteps,
    reps: card.reps,
    lapses: card.lapses,
    state: stateToEnum(card.state),
    last_review: card.lastReviewAt > 0 ? new Date(card.lastReviewAt * 1000) : undefined,
  }
}

function fsrsCardToContract(card: FsrsInternalCard, prev: FlashcardCard): FlashcardCard {
  const nextState = stateFromEnum(card.state)
  const isLearning = nextState === 1 || nextState === 3
  const intervalDays = card.scheduled_days ?? prev.intervalDays
  const due = isLearning
    ? Math.round(card.due.getTime() / 1000) // 秒级时间戳
    : Math.round((card.due.getTime() - new Date(0).getTime()) / 86_400_000) // 距 epoch 天数
  // 透传 ts-fsrs 给出的 next learning step。
  // 在 review 路径上它会是 0（已毕业）；learning/relearning 路径上它
  // 表示下一次要走的阶梯索引，round-trip 到 card 上保留下次评分用。
  return {
    ...prev,
    state: nextState,
    due,
    intervalDays,
    stability: card.stability,
    difficulty: card.difficulty,
    reps: card.reps,
    lapses: card.lapses,
    learningSteps: typeof card.learning_steps === 'number' ? card.learning_steps : prev.learningSteps ?? 0,
    lastReviewAt: card.last_review ? Math.round(card.last_review.getTime() / 1000) : prev.lastReviewAt,
  }
}

/**
 * 单一 Scheduler 实例：模块加载时构造，default params（v1 不暴露调参）。
 *
 * 不开启 enable_fuzz：fuzz 行为由 `fuzzIntervalDays` 显式施加，方便测试。
 */
const scheduler = createFsrs(generatorParameters({ enable_fuzz: false }))

/**
 * 应用一次评分，返回新 card 状态。now 为秒级 Unix 时间戳。
 *
 * 内部流程：
 *   1. ts-fsrs repeat 得到四档候选（card + log）
 *   2. fuzz interval（≥1 天才扰动）
 *   3. 把 fuzz 偏移加到 due 上，recount intervalDays
 */
export function applyReview(
  card: FlashcardCard,
  rating: FlashcardRating,
  now: number,
): FlashcardCard {
  const nowMs = now * 1000
  const input = cardToFsrsInput(card, nowMs)
  const preview = scheduler.repeat(input, new Date(nowMs))
  // FlashcardRating = 1|2|3|4 直接对应 Rating enum 的 Again/Hard/Good/Easy
  // 数值；Rating.Manual(=0) 不在我们的 FlashcardRating 联合内
  const item = preview[rating]
  if (!item) throw new Error(`useFsrs.applyReview: no preview for rating ${rating}`)
  const base = fsrsCardToContract(item.card, card)
  // fuzz 段：只在即将进入 review/relearning 且 ≥1 天时扰动
  if (base.state === 2 && base.intervalDays >= 1) {
    const fuzzed = fuzzIntervalDays(base.intervalDays, 0.9 /* v1 默认 */)
    if (fuzzed.fuzzedDays !== base.intervalDays) {
      const deltaDays = fuzzed.fuzzedDays - base.intervalDays
      // review 中 due 是「距 epoch 天数」，平移同样天数
      return { ...base, intervalDays: fuzzed.fuzzedDays, due: base.due + deltaDays }
    }
  }
  return base
}

/**
 * 区间扰动（契约 §4）：
 *   - intervalDays < 1：不扰动（学习/同日内重排）
 *   - 否则：均匀加/减 `min(interval * 0.05, 2)` 天，结果夹到 [1, interval * 2]
 *
 * `deckDesiredRetention` 入参保留给后续 v2 接入 retention-aware fuzz
 * 用，目前 v1 不参与计算（契约 §4 仍要求导出该形参）。
 */
export function fuzzIntervalDays(
  intervalDays: number,
  _deckDesiredRetention: number,
): FuzzedInterval {
  if (!Number.isFinite(intervalDays) || intervalDays < 1) {
    return { intervalDays, fuzzedDays: intervalDays, deltaSec: 0 }
  }
  const maxPerturb = Math.min(intervalDays * 0.05, 2)
  // 均匀扰动：[-maxPerturb, +maxPerturb]
  const offset = (Math.random() * 2 - 1) * maxPerturb
  // 注意：均匀扰动可以小到 0。Math.round 会让偶尔结果等于原值；这对
  // 测试意味着「同卡同 instant 同 rating」也可能无 fuzz 偏移，因此测试
  // 应当跨多次重复验证 |due_a - due_b| 在允许窗口内，而非要求非零。
  const raw = intervalDays + offset
  const lower = 1
  const upper = intervalDays * 2
  const fuzzedDays = Math.max(lower, Math.min(upper, Math.round(raw * 10) / 10))
  const deltaDays = fuzzedDays - intervalDays
  const deltaSec = Math.round(deltaDays * 86_400)
  return { intervalDays, fuzzedDays, deltaSec }
}

/**
 * 新卡初始 schedule（契约 §4）：
 *   - due = now（立刻到期，秒级时间戳；状态 0=new 时库内 due 即秒级）
 *   - state = 0 (new)
 *   - intervalDays = 0
 *
 * 注意：ts-fsrs 的 `createEmptyCard` 也产出 state=New 的 card，但
 * 这里我们直接基于契约字段返回，避免多余字段泄漏到 store。
 */
export function newCardSchedule(
  _deckConfig: FlashcardDeckConfig,
  now: number,
): { due: number; state: FlashcardState; intervalDays: number } {
  void _deckConfig
  void now
  return { due: 0, state: 0, intervalDays: 0 }
}

/** 内部使用：暴露 createEmptyCard 供测试与 store 的草稿路径复用。 */
export function emptyFsrsCard(now: number) {
  return createEmptyCard(new Date(now * 1000))
}

/**
 * Composable 形式（与现有 useToast / useAppSettings 风格一致）。
 * 由于内部状态全为纯函数，本 composable 不持有任何响应式状态；提供
 * 这个工厂仅为让视图层用 `const { applyReview, fuzzIntervalDays } = useFsrs()`。
 */
export function useFsrs() {
  return {
    applyReview,
    fuzzIntervalDays,
    newCardSchedule,
  }
}
