/**
 * flashcards.ts — Pinia store for OpenPocket v1 flashcards (契约 §2 + §4).
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
      lastSyncedAt: typeof parsed.lastSyncedAt === 'number' ? parsed.lastSyncedAt : 0,
    }
  } catch {
    return emptyCache()
  }
}

function emptyCache(): CachedState {
  return { notes: [], cards: [], deckConfigs: [], lastSyncedAt: 0 }
}

function writeCache(state: CachedState) {
  try {
    if (typeof localStorage === 'undefined') return
    localStorage.setItem(CACHE_KEY, JSON.stringify(state))
  } catch {
    // 隐私模式 / 配额耗尽：缓存失败不应阻塞视图
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

function writeOutbox(items: OutboxItem[]) {
  try {
    if (typeof localStorage === 'undefined') return
    localStorage.setItem(OUTBOX_KEY, JSON.stringify(items))
  } catch {
    /* ignore */
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

function nowSec(): number {
  return Math.floor(Date.now() / 1000)
}

export const useFlashcardsStore = defineStore('flashcards', () => {
  const notes = ref<FlashcardNote[]>([])
  const cards = ref<FlashcardCard[]>([])
  const deckConfigs = ref<FlashcardDeckConfig[]>([])
  const outbox = ref<OutboxItem[]>([])
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
    const now = nowSec()
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
    const now = nowSec()
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
    const now = nowSec()
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

  function persistCache() {
    writeCache({
      notes: notes.value,
      cards: cards.value,
      deckConfigs: deckConfigs.value,
      lastSyncedAt: lastSyncedAt.value,
    })
  }

  function persistOutbox() {
    writeOutbox(outbox.value)
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
      if (typeof envelope.serverTimeMs === 'number') {
        lastSyncedAt.value = Math.floor(envelope.serverTimeMs / 1000)
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
    outbox.value = [...outbox.value, { ...item, seq, enqueuedAt: nowSec() }]
    persistOutbox()
    if (online.value) void flushOutbox()
  }

  /** 应用评分：本地先算 FSRS，再 patch card 状态，最后入队 outbox。 */
  function applyReviewLocally(cardId: string, rating: FlashcardRating): FlashcardCard | null {
    const idx = cards.value.findIndex((c) => c.id === cardId)
    if (idx < 0) return null
    const prev = cards.value[idx]
    const updated = useFsrs().applyReview(prev, rating, nowSec())
    cards.value = [
      ...cards.value.slice(0, idx),
      { ...updated, updatedAt: nowSec() },
      ...cards.value.slice(idx + 1),
    ]
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
      reviewedAt: nowSec(),
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
      return await fetchDueCountSvc(deckIdArg, nowSec())
    } catch {
      return dueByDeck.value.get(deckIdArg) ?? 0
    }
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
    const updated = { ...next, updatedAt: nowSec() }
    deckConfigs.value.splice(idx, 1, updated)
    persistCache()
  }

  return {
    notes,
    cards,
    deckConfigs,
    outbox,
    lastSyncedAt,
    loading,
    flushing,
    error,
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
    saveDeckConfig,
  }
})

// 把内部 helpers 暴露给测试（不污染默认导出）
export const __test__ = { mergeById, stripServerFieldsIn: (x: unknown) => x }