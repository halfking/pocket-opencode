/**
 * Flashcards v1 — 共享类型（对齐 docs/flashcards-contract.md §3）。
 *
 * 注意：本文件必须与契约保持一致。字段名固定 snake_case，命名直接
 * 来自契约 JSON 形状（前端收到的是后端已经 snake_case 化的 payload）。
 */
export type FlashcardRating = 1 | 2 | 3 | 4 // 1=Again 2=Hard 3=Good 4=Easy
export type FlashcardState = 0 | 1 | 2 | 3 // 0=new 1=learning 2=review 3=relearning

export interface FlashcardNote {
  id: string
  userId: string
  deckId: string
  front: string
  back: string
  tags: string[]
  usn: number
  createdAt: number
  updatedAt: number
  deletedAt?: number
}

export interface FlashcardCard {
  id: string
  noteId: string
  userId: string
  deckId: string
  state: FlashcardState
  /** 学习中=秒级 Unix 时间戳；复习中=距 epoch 的天数（双语义，契约 §1.2）。 */
  due: number
  intervalDays: number
  stability: number
  difficulty: number
  reps: number
  lapses: number
  /**
   * 客户端自有字段：ts-fsrs 当前学习阶梯索引，round-trip 持久化。
   * 服务端忽略（不在 SERVER_FIELDS 中），仅随 envelope 透传。
   * 可选：旧卡/导入卡可能缺失，缺省按"已毕业"处理。
   */
  learningSteps?: number
  lastReviewAt: number
  usn: number
  createdAt: number
  updatedAt: number
  deletedAt?: number
}

export interface FlashcardDeckConfig {
  deckId: string
  userId: string
  name: string
  newPerDay: number
  reviewsPerDay: number
  learningStepsMin: number[]
  graduatingIntervalDays: number
  easyIntervalDays: number
  fsrsWeights: number[]
  desiredRetention: number
  usn: number
  createdAt: number
  updatedAt: number
}

export interface FlashcardReviewLog {
  id: string
  cardId: string
  userId: string
  reviewedAt: number
  rating: FlashcardRating
  prevState: FlashcardState
  nextState: FlashcardState
  prevInterval: number
  nextInterval: number
  elapsedDays: number
}

/** 视图层用的轻量卡片元数据（不带 front/back，列表渲染用）。 */
export interface FlashcardDeckSummary {
  deckId: string
  name: string
  newCount: number
  dueCount: number
  totalCards: number
  updatedAt: number
}

/** 新建 / 编辑 note 的入参（契约 §2 POST/PATCH /notes）。 */
export interface FlashcardNoteInput {
  deckId: string
  front: string
  back: string
  tags?: string[]
}

/** 服务端增量信封（契约 §2 GET /api/flashcards）。 */
export interface FlashcardsSyncEnvelope {
  cards: FlashcardCard[]
  decks: FlashcardDeckConfig[]
  notes?: FlashcardNote[]
  serverTimeMs?: number
  deletedIds?: string[]
}

/** /due 端点响应（契约 §2）。 */
export interface FlashcardsDueResponse {
  cards: FlashcardCard[]
  totalDue: number
}

/** 复习评分响应（契约 §2 POST /cards/:id/review）。 */
export interface FlashcardReviewResponse {
  card: FlashcardCard
  log: FlashcardReviewLog
}
