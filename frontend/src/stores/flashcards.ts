/**
 * flashcards.ts — Pinia store for AI Work Mess v1 flashcards (契约 §2 + §4).
 *
 * Local-first：
 *   - 状态（notes / cards / deckConfigs）缓存到 localStorage `flashcards:v1`。
 *   - 同步：syncFromServer() 用 lastSyncedAt 作为 since 拉增量，写回 lastSyncedAt。
 *   - 写操作：enqueueReview / enqueueNoteCreate / enqueueNoteDelete 推到 outbox，
 *     flushOutbox() 在恢复在线后顺序回放。
 *   - FSRS 调度：applyReviewLocally() 调用 useFsrs.applyReview，写回 card 后
 *     enqueue 一个 review 事件到 outbox。
 *
 * 不在视图层手动写 Pinia：始终走 actions（保持单一入口，便于测试）。
 */
import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import {
  createDeck as createDeckSvc,
  createNote,
  deleteNote,
  dueCount as fetchDueCountSvc,
  patchCard,
  pullAll,
  pullCards,
  pullNotes,
  recordReview,
} from '../services/flashcards'
import { useFsrs } from '../composables/useFsrs'
import { dueNowSec, liveNowSec, startDueClock } from './flashcardDueClock'
import type {
  FlashcardCard,
  FlashcardDeckConfig,
  FlashcardDeckSummary,
  FlashcardNote,
  FlashcardNoteInput,
  FlashcardRating,
  FlashcardReviewLog,
} from '../types/flashcards'

interface CachedState {
  notes: FlashcardNote[]
  cards: FlashcardCard[]
  deckConfigs: FlashcardDeckConfig[]
  /** Phase 5：本地复习日志（仅本地累计；后端契约暂未返回 reviewLog 列表）。 */
  reviewLogs: FlashcardReviewLog[]
  lastSyncedAt: number
}

interface OutboxItemBase {
  /** 自增序号，用于按插入顺序回放。 */
  seq: number
  /** 入队时本地秒级时间戳，便于排查卡顿。 */
  enqueuedAt: number
}

export type OutboxItem =
  | (OutboxItemBase & {
      kind: 'review'
      cardId: string
      rating: FlashcardRating
      elapsedMs: number
      reviewedAt: number
      fsrs: {
        due: number
        state: FlashcardCard['state']
        stability: number
        difficulty: number
        intervalDays: number
      }
    })
  | (OutboxItemBase & {
      kind: 'createNote'
      input: FlashcardNoteInput
    })
  | (OutboxItemBase & {
      kind: 'patchNote'
      noteId: string
      patch: Partial<Pick<FlashcardNote, 'front' | 'back' | 'tags'>>
    })
  | (OutboxItemBase & {
      kind: 'deleteNote'
      noteId: string
    })
  | (OutboxItemBase & {
      kind: 'patchCard'
      cardId: string
      patch: { due?: number; state?: FlashcardCard['state'] }
    })

const CACHE_KEY = 'flashcards:v1'
const OUTBOX_KEY = 'flashcards:v1:outbox'

function readCache(): CachedState {
  try {
    if (typeof localStorage === 'undefined') return emptyCache()
    const raw = localStorage.getItem(CACHE_KEY)
    if (!raw) return emptyCache()
    const parsed = JSON.parse(raw) as Partial<CachedState>
    return {
      notes: Array.isArray(parsed.notes) ? parsed.notes : [],
      cards: Array.isArray(parsed.cards) ? parsed.cards : [],
      deckConfigs: Array.isArray(parsed.deckConfigs) ? parsed.deckConfigs : [],
      reviewLogs: Array.isArray(parsed.reviewLogs) ? parsed.reviewLogs : [],
      lastSyncedAt: typeof parsed.lastSyncedAt === 'number' ? parsed.lastSyncedAt : 0,
    }
  } catch {
    return emptyCache()
  }
}

function emptyCache(): CachedState {
  return { notes: [], cards: [], deckConfigs: [], reviewLogs: [], lastSyncedAt: 0 }
}

function writeCache(state: CachedState): boolean {
  try {
    if (typeof localStorage === 'undefined') return true
    localStorage.setItem(CACHE_KEY, JSON.stringify(state))
    return true
  } catch (err) {
    // ⚠⚠§868/D36：原来这里只有一句「缓存失败不应阻塞视图」的注释。
    // 对**缓存**本身这句话成立（服务端才是权威），但它把同一函数
    // `writeOutbox`（见下）的失败也一起盖住了 —— 而那不是缓存。
    console.warn('[flashcards] 本地缓存未能写入（服务端仍为权威，但离线期间的新改动只活在内存里）：', err)
    return false
  }
}

function readOutbox(): OutboxItem[] {
  try {
    if (typeof localStorage === 'undefined') return []
    const raw = localStorage.getItem(OUTBOX_KEY)
    if (!raw) return []
    const parsed = JSON.parse(raw) as OutboxItem[]
    return Array.isArray(parsed) ? parsed : []
  } catch {
    return []
  }
}

function writeOutbox(items: OutboxItem[]): boolean {
  try {
    if (typeof localStorage === 'undefined') return true
    localStorage.setItem(OUTBOX_KEY, JSON.stringify(items))
    return true
  } catch (err) {
    // ⚠⚠⚠§868/D36：这一处原来写着 `/* ignore */`，是本节定性最重的一条。
    //
    // 失败链：enqueue() 先把事件塞进**内存** outbox，再 persistOutbox()。
    // 写不进去时内存里那条还在、`if (online) flushOutbox()` 也不会报错
    // ⇒ 用户看到「已入队」。**但离线时**（正是 outbox 存在的唯一理由）
    // 它只活在内存 ⇒ 下次 `loadFromCache` 读回的是**旧队列**
    // ⇒ 这条复习事件**永久丢失**，服务端再也不会知道，用户的复习进度
    // 与服务器静默分叉。而 `applyReviewLocally` 已经把 FSRS 状态改进内存/缓存了。
    //
    // 按 §866 的族规律：丢的是**用户正在产出的数据** ⇒ 必须说话，
    // 不能与「缓存写不进去」共用一句「不应阻塞视图」的理由。
    console.warn('[flashcards] outbox 未能写入本地存储（离线时排队的复习事件会在退出后丢失）：', err)
    return false
  }
}

function mergeById<T extends { id: string; updatedAt?: number } | { deckId: string; updatedAt?: number }>(
  prev: T[],
  next: T[],
): T[] {
  const map = new Map<string, T>()
  for (const item of prev) map.set((item as { id?: string; deckId?: string }).id ?? (item as { deckId?: string }).deckId ?? '', item)
  for (const item of next) {
    const key = (item as { id?: string; deckId?: string }).id ?? (item as { deckId?: string }).deckId ?? ''
    const existing = map.get(key)
    if (!existing || (item.updatedAt ?? 0) >= (existing.updatedAt ?? 0)) {
      map.set(key, item)
    }
  }
  return [...map.values()]
}

/**
 * BUG-AS（handoff §4.55）：这里原本是个直接读 `Date.now()` 的 `liveNowSec()`。
 * 三个到期判据 computed 依赖它，而 `Date.now()` 不是响应式依赖 ⇒
 * 卡片在页面打开期间跨过到期时刻，computed 不重算，到期数与「开始复习」都卡住。
 *
 * 现在拆成两个来源，写之前先想清楚要哪一个：
 *   - `dueNowSec()`：响应式，专门给**到期判据**用（computed 依赖它才会重算）
 *   - `liveNowSec()`：真实时间，给**记录时间戳**用（enqueuedAt / reviewedAt 等），
 *     免得被 tick 间隔拖成最多 30 秒的旧值
 */
export const useFlashcardsStore = defineStore('flashcards', () => {
  // 到期数要随时间自己走（BUG-AS）：时钟是模块级单例，这里启动一次即可
  startDueClock()
  const notes = ref<FlashcardNote[]>([])
  const cards = ref<FlashcardCard[]>([])
  const deckConfigs = ref<FlashcardDeckConfig[]>([])
  const outbox = ref<OutboxItem[]>([])
  /** Phase 5：本地复习日志（仅本地累计，stats 视图消费）。 */
  const reviewLogs = ref<FlashcardReviewLog[]>([])
  const lastSyncedAt = ref(0)
  const loading = ref(false)
  const flushing = ref(false)
  const error = ref('')
  const online = ref(typeof navigator === 'undefined' ? true : navigator.onLine)

  if (typeof window !== 'undefined') {
    window.addEventListener('online', () => {
      online.value = true
      void flushOutbox()
    })
    window.addEventListener('offline', () => {
      online.value = false
    })
  }

  // ---------- computed ----------
  const dueByDeck = computed(() => {
    // 响应式时间：卡片在页面打开期间跨过到期时刻时，本 computed 才会重算（BUG-AS）
    const now = dueNowSec()
    const map = new Map<string, number>()
    for (const card of cards.value) {
      if (card.deletedAt && card.deletedAt > 0) continue
      const due = card.due
      const isLearning = card.state === 0 || card.state === 1 || card.state === 3
      const isDue = isLearning ? due <= now : true
      if (!isDue) continue
      map.set(card.deckId, (map.get(card.deckId) ?? 0) + 1)
    }
    return map
  })

  const deckSummaries = computed<FlashcardDeckSummary[]>(() => {
    const summaries = new Map<string, FlashcardDeckSummary>()
    for (const cfg of deckConfigs.value) {
      summaries.set(cfg.deckId, {
        deckId: cfg.deckId,
        name: cfg.name,
        newCount: 0,
        dueCount: 0,
        totalCards: 0,
        updatedAt: cfg.updatedAt,
      })
    }
    const now = dueNowSec()
    for (const card of cards.value) {
      if (card.deletedAt && card.deletedAt > 0) continue
      let summary = summaries.get(card.deckId)
      if (!summary) {
        // 没有 deck config 但有 card —— 用 deckId 当兜底名
        summary = {
          deckId: card.deckId,
          name: card.deckId,
          newCount: 0,
          dueCount: 0,
          totalCards: 0,
          updatedAt: card.updatedAt,
        }
        summaries.set(card.deckId, summary)
      }
      summary.totalCards += 1
      if (card.state === 0) summary.newCount += 1
      const isLearning = card.state === 1 || card.state === 3
      if (isLearning ? card.due <= now : card.due >= 0) {
        if (card.state !== 0) summary.dueCount += 1
      }
    }
    return [...summaries.values()].sort((a, b) => b.updatedAt - a.updatedAt)
  })

  const dueCardsForDeck = computed(() => (deckId: string) => {
    const now = dueNowSec()
    return cards.value
      .filter((c) => c.deckId === deckId && !(c.deletedAt && c.deletedAt > 0))
      .filter((c) => {
        if (c.state === 0) return true
        const isLearning = c.state === 1 || c.state === 3
        return isLearning ? c.due <= now : true
      })
      .sort((a, b) => a.due - b.due)
  })

  const cardsByNote = computed(() => (noteId: string) =>
    cards.value.filter((c) => c.noteId === noteId && !(c.deletedAt && c.deletedAt > 0)),
  )

  const noteById = computed(() => (id: string) => notes.value.find((n) => n.id === id) ?? null)
  const deckById = computed(() => (id: string) =>
    deckConfigs.value.find((d) => d.deckId === id) ?? null,
  )

  // ---------- actions ----------
  function loadFromCache() {
    const cached = readCache()
    notes.value = cached.notes
    cards.value = cached.cards
    deckConfigs.value = cached.deckConfigs
    lastSyncedAt.value = cached.lastSyncedAt
    outbox.value = readOutbox()
  }

  /** ⚠§868/D36：写入失败必须可诊断（见 writeOutbox 的失败链分析）。 */
  const cachePersistFailed = ref(false)
  const outboxPersistFailed = ref(false)
  /** 任一落盘失败都为真 ⇒ 视图据此说人话（写在所有空态分支之外）。 */
  const persistFailed = computed(() => cachePersistFailed.value || outboxPersistFailed.value)

  function persistCache() {
    if (writeCache({
      notes: notes.value,
      cards: cards.value,
      deckConfigs: deckConfigs.value,
      reviewLogs: reviewLogs.value,
      lastSyncedAt: lastSyncedAt.value,
    })) cachePersistFailed.value = false
    else cachePersistFailed.value = true
  }

  function persistOutbox() {
    if (writeOutbox(outbox.value)) outboxPersistFailed.value = false
    else outboxPersistFailed.value = true
  }

  async function syncFromServer(): Promise<void> {
    loading.value = true
    error.value = ''
    try {
      const envelope = await pullAll(lastSyncedAt.value)
      notes.value = mergeById(notes.value, envelope.notes ?? [])
      cards.value = mergeById(cards.value, envelope.cards ?? [])
      deckConfigs.value = mergeById(deckConfigs.value, envelope.decks ?? [])
      const deletedSet = new Set(envelope.deletedIds ?? [])
      if (deletedSet.size > 0) {
        notes.value = notes.value.filter((n) => !deletedSet.has(n.id))
        cards.value = cards.value.filter((c) => !deletedSet.has(c.id))
      }
      // BUG-O（2026-09-30 真机验收）：水位线必须取**本次实际收到的最大
      // updatedAt**，不能取 serverTimeMs。
      //
      // 后端过滤是 `updated_at > $since`（严格大于，flashcards/store.go:361）。
      // 原实现把 lastSyncedAt 设成 floor(serverTimeMs/1000)，也就是"服务器
      // 此刻的时间"。凡是在「写入完成」与「这次拉取」之间发生的变更，其
      // updated_at 小于这个水位线，下一次 since 更大，于是**永远拉不回来**。
      //
      // 真机上的具体表现：UI 新建卡片 -> POST /api/flashcards/notes 201，
      // 服务端同时生成 note 和 card（card 的 id 由服务端生成，客户端本地
      // 根本没有这条记录，只能靠 sync 拉）。但随后一次 sync 把 lastSyncedAt
      // 推到服务端当前时间，card 的 updated_at 落在水位线之前 —— 卡组详情页
      // 「开始复习」恒 disabled，卡片永远不出现，而 PG 里数据明明在、宿主侧
      // 直接调 since=0 也能拿到。
      //
      // 没有数据时保持原水位线不动：空结果不代表"服务端此刻之前都已同步"。
      const maxUpdated = [
        ...(envelope.notes ?? []),
        ...(envelope.cards ?? []),
        ...(envelope.decks ?? []),
      ].reduce((acc, item) => {
        const ts = typeof item?.updatedAt === 'number' ? item.updatedAt : 0
        return ts > acc ? ts : acc
      }, 0)
      if (maxUpdated > 0) {
        lastSyncedAt.value = maxUpdated
      }
      persistCache()
    } catch (e: any) {
      error.value = e?.message || '同步失败'
      throw e
    } finally {
      loading.value = false
    }
  }

  /** 入队一个新 outbox 事件（按 seq 自增；online 时立即 flush）。 */
  function enqueue(item: OutboxItem) {
    const seq = (outbox.value[outbox.value.length - 1]?.seq ?? 0) + 1
    outbox.value = [...outbox.value, { ...item, seq, enqueuedAt: liveNowSec() }]
    persistOutbox()
    if (online.value) void flushOutbox()
  }

  /** 应用评分：本地先算 FSRS，再 patch card 状态，最后入队 outbox。 */
  function applyReviewLocally(cardId: string, rating: FlashcardRating): FlashcardCard | null {
    const idx = cards.value.findIndex((c) => c.id === cardId)
    if (idx < 0) return null
    const prev = cards.value[idx]
    const now = liveNowSec()
    const updated = useFsrs().applyReview(prev, rating, now)
    cards.value = [
      ...cards.value.slice(0, idx),
      { ...updated, updatedAt: now },
      ...cards.value.slice(idx + 1),
    ]
    // Phase 5：写一条本地 review log（Stats 视图消费）。
    // - prevInterval/nextInterval 与 store 字段同名，但卡 .due 在学习中用秒、
    //   复习中用「距 epoch 的天数」——为统一，这里都按天数记录。
    const prevIntervalDays = prev.intervalDays ?? 0
    const nextIntervalDays = updated.intervalDays ?? 0
    reviewLogs.value = [
      ...reviewLogs.value,
      {
        id: `${cardId}-${now}`,
        cardId,
        userId: prev.userId,
        reviewedAt: now,
        rating,
        prevState: prev.state,
        nextState: updated.state,
        prevInterval: prevIntervalDays,
        nextInterval: nextIntervalDays,
        elapsedDays: prevIntervalDays,
      },
    ]
    // 防止无限增长，保留最近 1000 条
    if (reviewLogs.value.length > 1000) {
      reviewLogs.value = reviewLogs.value.slice(-1000)
    }
    persistCache()
    return cards.value[idx]
  }

  function enqueueReview(cardId: string, rating: FlashcardRating, elapsedMs = 0) {
    const card = cards.value.find((c) => c.id === cardId)
    if (!card) return
    enqueue({
      seq: 0,
      enqueuedAt: 0,
      kind: 'review',
      cardId,
      rating,
      elapsedMs,
      reviewedAt: liveNowSec(),
      fsrs: {
        due: card.due,
        state: card.state,
        stability: card.stability,
        difficulty: card.difficulty,
        intervalDays: card.intervalDays,
      },
    })
  }

  function enqueueCreateNote(input: FlashcardNoteInput) {
    enqueue({ seq: 0, enqueuedAt: 0, kind: 'createNote', input })
  }

  function enqueuePatchNote(
    noteId: string,
    patch: Partial<Pick<FlashcardNote, 'front' | 'back' | 'tags'>>,
  ) {
    enqueue({ seq: 0, enqueuedAt: 0, kind: 'patchNote', noteId, patch })
  }

  function enqueueDeleteNote(noteId: string) {
    enqueue({ seq: 0, enqueuedAt: 0, kind: 'deleteNote', noteId })
  }

  function enqueuePatchCard(cardId: string, patch: { due?: number; state?: FlashcardCard['state'] }) {
    enqueue({ seq: 0, enqueuedAt: 0, kind: 'patchCard', cardId, patch })
  }

  async function flushOutbox(): Promise<void> {
    if (flushing.value) return
    if (!online.value) return
    if (outbox.value.length === 0) return
    flushing.value = true
    try {
      // 复制待处理列表，按 seq 顺序处理；失败的留在原位供下次重试。
      const queue = outbox.value.slice()
      const drained: OutboxItem[] = []
      for (const item of queue) {
        try {
          await dispatchOutboxItem(item)
          drained.push(item)
        } catch {
          // 网络/服务端错误：保留在 outbox 中，等下次 flush。
          break
        }
      }
      if (drained.length > 0) {
        const drainedSet = new Set(drained.map((d) => d.seq))
        outbox.value = outbox.value.filter((i) => !drainedSet.has(i.seq))
        persistOutbox()
      }
    } finally {
      flushing.value = false
    }
  }

  async function dispatchOutboxItem(item: OutboxItem): Promise<FlashcardReviewLog | void> {
    switch (item.kind) {
      case 'review':
        await recordReview(item.cardId, item.rating, {
          reviewedAt: item.reviewedAt,
          ...item.fsrs,
        })
        return
      case 'createNote':
        await createNote(item.input)
        return
      case 'patchNote':
        await patchNoteCompat(item.noteId, item.patch)
        return
      case 'deleteNote':
        await deleteNote(item.noteId)
        return
      case 'patchCard':
        await patchCard(item.cardId, item.patch)
        return
    }
  }

  /** 兼容旧 store 内已有 patchNote 字段命名；调用 services.patchNote。 */
  async function patchNoteCompat(
    id: string,
    patch: Partial<Pick<FlashcardNote, 'front' | 'back' | 'tags'>>,
  ) {
    const { patchNote } = await import('../services/flashcards')
    await patchNote(id, patch)
  }

  /** UI 调用的"立即拉一次"+ "flush outbox"组合。 */
  async function refresh() {
    await syncFromServer()
    await flushOutbox()
  }

  /** 视图层辅助：按 deckId 拉一次服务端 due 数，失败时退回本地计算。 */
  async function fetchDueCount(deckIdArg: string): Promise<number> {
    try {
      return await fetchDueCountSvc(deckIdArg, liveNowSec())
    } catch {
      return dueByDeck.value.get(deckIdArg) ?? 0
    }
  }

  /**
   * 创建卡组（BUG-K，2026-09-30 真机验收）。
   *
   * 此前**没有任何创建卡组的路径**：后端无 POST /api/flashcards/decks，
   * 前端列表页「新建卡组」又直接跳 /flashcards/new（新建卡片页）。
   * 结果 decks=0 → selectedDeckId 为空 → 保存恒 disabled，闪卡从零状态不可用。
   *
   * 走服务端建（后端会填 FSRS 默认值），成功后合并进本地缓存并返回。
   */
  async function createDeck(name: string): Promise<FlashcardDeckConfig> {
    const created = await createDeckSvc(name.trim())
    const idx = deckConfigs.value.findIndex((d) => d.deckId === created.deckId)
    if (idx >= 0) deckConfigs.value.splice(idx, 1, created)
    else deckConfigs.value.push(created)
    persistCache()
    return created
  }

  /**
   * Phase 4：保存 deck config（local-first）。
   *
   * 行为：替换式写整个 deckConfig；持久化到 localStorage；不入 outbox
   * （后端契约暂未提供 PATCH /decks/:id/config，Phase 4.1 增量）。
   *
   * 视图层用 `store.deckById(deckId)` 读最新；如保存前后差异需要做 diff，
   * 在调用方保存 hasChanges 状态。
   */
  function saveDeckConfig(next: FlashcardDeckConfig) {
    const idx = deckConfigs.value.findIndex((d) => d.deckId === next.deckId)
    if (idx < 0) return
    const updated = { ...next, updatedAt: liveNowSec() }
    deckConfigs.value.splice(idx, 1, updated)
    persistCache()
  }

  /* Phase 6：导入用批量 setter（JSON 导入替换式覆盖）。 */
  function replaceAllNotes(next: FlashcardNote[]) {
    notes.value = next
    persistCache()
  }
  function replaceAllCards(next: FlashcardCard[]) {
    cards.value = next
    persistCache()
  }
  function replaceAllDeckConfigs(next: FlashcardDeckConfig[]) {
    deckConfigs.value = next
    persistCache()
  }
  function replaceAllReviewLogs(next: FlashcardReviewLog[]) {
    reviewLogs.value = next.length > 1000 ? next.slice(-1000) : next
    persistCache()
  }

  return {
    notes,
    cards,
    deckConfigs,
    outbox,
    reviewLogs,
    lastSyncedAt,
    loading,
    flushing,
    error,
    persistFailed,
    online,
    dueByDeck,
    deckSummaries,
    dueCardsForDeck,
    cardsByNote,
    noteById,
    deckById,
    loadFromCache,
    syncFromServer,
    refresh,
    flushOutbox,
    enqueueReview,
    enqueueCreateNote,
    enqueuePatchNote,
    enqueueDeleteNote,
    enqueuePatchCard,
    applyReviewLocally,
    fetchDueCount,
    createDeck,
    saveDeckConfig,
    replaceAllNotes,
    replaceAllCards,
    replaceAllDeckConfigs,
    replaceAllReviewLogs,
  }
})

// 把内部 helpers 暴露给测试（不污染默认导出）
export const __test__ = { mergeById, stripServerFieldsIn: (x: unknown) => x }