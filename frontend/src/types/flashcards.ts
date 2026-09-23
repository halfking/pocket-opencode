/**
 * Flashcards v1 — 共享类型（对齐 docs/flashcards-contract.md §3）。
 *
 * 注意：本文件必须与契约保持一致。字段名固定 snake_case，命名直接
 * 来自契约 JSON 形状（前端收到的是后端已经 snake_case 化的 payload）。
 */
export type FlashcardRating = 1 | 2 | 3 | 4 // 1=Again 2=Hard 3=Good 4=Easy
export type FlashcardState = 0 | 1 | 2 | 3 // 0=new 1=learning 2=review 3=relearning

/**
 * Card template (Phase 3 of Anki feature injection).
 *
 * - `basic`:           front / back 正面反面
 * - `basic_reversed`:  正反互换（Phase 3.5 再实现 cardTemplates 展开）
 * - `cloze`:           Anki {{c1::answer}} 挖空语法；front/back 仍写入
 *                      `front` 字段（同样的 cloze 文本），模板由 UI 决定渲染。
 *
 * 旧数据没有 template 字段，按 basic 处理（前端 store hydrate 时回填）。
 */
export type FlashcardTemplate = 'basic' | 'basic_reversed' | 'cloze'

export interface FlashcardNote {
  id: string
  userId: string
  deckId: string
  front: string
  back: string
  /** Phase 3：卡模板。缺省视为 'basic'（向后兼容旧 note）。 */
  template?: FlashcardTemplate
  /** Cloze 专用字段：与 front 同义，便于 store 层强制从 cloze 模板渲染。
   *  旧 cloze note 的 cloze 文本存在 front 中（前后端协议 §2）；本字段为
   *  客户端冗余，便于在 editor / review UI 里不必从 front 重新解析。 */
  clozeText?: string
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
  /** Phase 4：父牌组（嵌套牌组树）。null/undefined = 顶层 deck。 */
  parentDeckId?: string | null
  /** Phase 4：FSRS 调度扩展。最大间隔天数（默认 36500 = 不限制）。 */
  maximumIntervalDays?: number
  /** Phase 4：Easy 按钮额外加成（FSRS 5.x 推荐 1.3，Anki 2024 起暴露）。 */
  easyBonus?: number
  /** Phase 4：Hard 按钮额外难度（FSRS 5.x 推荐 1.2）。 */
  hardInterval?: number
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
  /** Phase 3：模板类型。缺省 'basic'。 */
  template?: FlashcardTemplate
  /** Cloze 专用：与 front 同义（前端冗余）。 */
  clozeText?: string
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
