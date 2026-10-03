/**
 * Learning Core 的前端类型（docs/学习muse/04-数据模型与API契约.md §2.2）。
 *
 * 字段名与后端 internal/learning 的 JSON tag 一一对应；任何一侧改名，
 * `src/services/__tests__/learning.contract.test.ts` 与后端路由测试会先打红。
 *
 * 关键约束（不要在前端"发明"这些字段）：
 *  - userId / workspaceId 由服务端从 JWT 推导，请求体里的同名字段会被忽略；
 *  - stage / sourceKind / kind / ruleKind 都是闭合枚举，非法值服务端 400；
 *  - dueSummary 是「今天该做什么」的唯一口径，通知文案与界面共用它，
 *    所以两者永远不会各说各话。
 */

/** 学习条目的来源域。正文不在这里，只存引用（ADR-003）。 */
export type LearningSourceKind = 'note' | 'email' | 'rss' | 'meeting' | 'chat' | 'manual'

/** 学习漏斗阶段：已收集 → 学习中 → 复习中 → 已掌握 / 归档。 */
export type LearningStage = 'inbox' | 'learning' | 'review' | 'mastered' | 'archived'

/** 提醒触发类别。 */
export type LearningReminderKind = 'daily_digest' | 'spaced_review' | 'deadline' | 'streak'

/** 提醒重复规则：每日固定时刻 / 固定间隔 / 一次性。 */
export type LearningRuleKind = 'daily' | 'interval' | 'once'

/** 提醒状态。acked 是终态（用户表示"我知道了"），snoozed 只是推迟。 */
export type LearningReminderState = 'pending' | 'sent' | 'acked' | 'snoozed' | 'done'

/** 卡片状态，与 flashcards 的 0..3 对齐（后端调度器复用同一编码）。 */
export type LearningCardState = 0 | 1 | 2 | 3

/** 复习评分，与 flashcard_revlog.rating 对齐。 */
export type LearningRating = 1 | 2 | 3 | 4

export interface LearningItem {
  id: string
  workspaceId: string
  userId: string
  sourceKind: LearningSourceKind
  sourceId: string
  title: string
  summary?: string
  deckId?: string
  stage: LearningStage
  /** 1–5，影响回顾排序。 */
  importance: number
  /** 继承来源的标签（笔记标签 / 邮件分类 / RSS 分类），便于按同一套词汇检索。 */
  tags?: string[]
  /** Unix 秒。 */
  capturedAt: number
  updatedAt: number
}

/**
 * POST /api/learning/items 的请求体。
 *
 * `title` 可省略：服务端会用 sourceKind + sourceId 解析出来
 * （backend/internal/learning/sources），这正是"一键加入学习"只需要一个 id
 * 的原因。只有 manual 来源才必须自己带标题。
 */
export interface LearningCaptureInput {
  sourceKind: LearningSourceKind
  sourceId: string
  title?: string
  summary?: string
  deckId?: string
  importance?: number
  stage?: LearningStage
  tags?: string[]
}

export interface LearningReminder {
  id: string
  workspaceId: string
  userId: string
  kind: LearningReminderKind
  itemId?: string
  cardId?: string
  ruleKind: LearningRuleKind
  /** daily 规则为 "HH:MM"；interval 为分钟数；once 为 unix 秒。 */
  ruleValue?: string
  /** Unix 秒；提醒中枢的真相。 */
  nextDueAt: number
  state: LearningReminderState
  lastSentAt?: number
  snoozedUntil?: number
  createdAt: number
  updatedAt: number
}

/** POST /api/learning/reminders 的请求体。 */
export interface LearningReminderInput {
  kind: LearningReminderKind
  itemId?: string
  cardId?: string
  ruleKind: LearningRuleKind
  ruleValue?: string
  nextDueAt: number
}

/**
 * 今日概览。四项全为 0 时服务端不会推送通知（学习提醒的"无事不打扰"策略），
 * 所以界面上也应当展示为"今天没有待办"，而不是显示四个 0。
 */
export interface LearningDueSummary {
  userId?: string
  dueCards: number
  inbox: number
  reviewItems: number
  dueTasks: number
  nextDueAt?: number
}

/** POST /api/learning/schedule 的入参（服务端权威调度）。 */
export interface LearningScheduleInput {
  state: LearningCardState
  stability: number
  difficulty: number
  rating: LearningRating
  elapsedDays: number
  learningStepMin?: number
  desiredRetention?: number
  /** Unix 秒；省略则用服务端当前时间。 */
  now?: number
  learningSteps?: number[]
  graduatingIntervalDays?: number
  easyIntervalDays?: number
  reps?: number
  lapses?: number
}

/** POST /api/learning/schedule 的返回。 */
export interface LearningScheduleOutput {
  state: LearningCardState
  stability: number
  difficulty: number
  intervalDays: number
  /** Unix 秒。 */
  due: number
  relearning: boolean
  reps: number
  lapses: number
}

/** 列表接口的外层信封。 */
export interface LearningItemsResponse {
  items: LearningItem[]
}

export interface LearningRemindersResponse {
  reminders: LearningReminder[]
}

/** 连续学习天数（由后端派生，前端不计数）。 */
export interface LearningStreak {
  current: number
  longest: number
  /** 最近一次有学习的「天序号」，0 = 从来没有。 */
  lastActiveDay: number
  /** 今天是否已有学习行为——决定 UI 说「保持住」还是「从今天开始」。 */
  activeToday: boolean
}

export interface LearningStreakView {
  streak: LearningStreak
  /** 已达到的最高里程碑，未达到为 0。 */
  milestone: number
  /** 下一个目标里程碑，里程碑走完为 0。 */
  next: number
  /** 后端计算时使用的天序号，便于客户端发现时钟不一致。 */
  today: number
}
