/**
 * flashcards.ts — OpenPocket v1 flashcards HTTP service layer.
 *
 * 锁定 src/services/__tests__/flashcards.contract.test.ts：所有方法签名、
 * 请求 method/url/body 形状必须与契约一致。客户端绝对不能带上服务端
 * 权威字段（id / userId / usn / createdAt / updatedAt）—— `stripServerFields`
 * 在落库前剥离。
 *
 * 所有错误包络与契约 §1.5 一致：`{ error, code?, retryable?, request_id? }`。
 * 调用方拿到的是 ApiError，body 字段已被解析为对象供业务判断。
 *
 * 这里不缓存任何状态：状态在 stores/flashcards.ts 中（local-first + outbox）。
 */
import { http, ApiError } from '../api/http'
import type {
  FlashcardCard,
  FlashcardDeckConfig,
  FlashcardNote,
  FlashcardNoteInput,
  FlashcardRating,
  FlashcardReviewResponse,
  FlashcardsDueResponse,
  FlashcardsSyncEnvelope,
} from '../types/flashcards'

const BASE = '/api/flashcards'

/**
 * 服务端权威字段（契约 §1.5）。
 * 任何写入请求（POST/PATCH）都必须在序列化前 remove。
 */
const SERVER_FIELDS = ['id', 'userId', 'usn', 'createdAt', 'updatedAt', 'deletedAt'] as const

function stripServerFields<T extends Record<string, unknown>>(input: T): Record<string, unknown> {
  const out: Record<string, unknown> = {}
  for (const [k, v] of Object.entries(input)) {
    if ((SERVER_FIELDS as readonly string[]).includes(k)) continue
    out[k] = v
  }
  return out
}

interface IncrementalNotesResponse {
  notes: FlashcardNote[]
  serverTimeMs: number
  deletedIds?: string[]
}

interface IncrementalCardsResponse extends FlashcardsSyncEnvelope {
  serverTimeMs?: number
}

/** 增量拉取 cards + decks（契约 §2 GET /api/flashcards?since=&limit=）。 */
export async function pullCards(since = 0, limit = 200): Promise<IncrementalCardsResponse> {
  const params = new URLSearchParams()
  if (since > 0) params.set('since', String(since))
  if (limit > 0) params.set('limit', String(limit))
  const query = params.toString()
  const path = `${BASE}${query ? `?${query}` : ''}`
  const body = await http<Partial<IncrementalCardsResponse>>(path)
  return {
    cards: Array.isArray(body.cards) ? body.cards : [],
    decks: Array.isArray(body.decks) ? body.decks : [],
    notes: Array.isArray(body.notes) ? body.notes : [],
    serverTimeMs: typeof body.serverTimeMs === 'number' ? body.serverTimeMs : undefined,
    deletedIds: Array.isArray(body.deletedIds) ? body.deletedIds : [],
  }
}

/** 增量拉取 notes（契约 §2 GET /api/flashcards/notes?since=）。 */
export async function pullNotes(since = 0): Promise<IncrementalNotesResponse> {
  const params = new URLSearchParams()
  if (since > 0) params.set('since', String(since))
  const query = params.toString()
  const path = `${BASE}/notes${query ? `?${query}` : ''}`
  const body = await http<Partial<IncrementalNotesResponse>>(path)
  return {
    notes: Array.isArray(body.notes) ? body.notes : [],
    serverTimeMs: typeof body.serverTimeMs === 'number' ? body.serverTimeMs : 0,
    deletedIds: Array.isArray(body.deletedIds) ? body.deletedIds : [],
  }
}

/** 一次性增量拉取（cards + decks + notes + serverTimeMs + deletedIds）。 */
export async function pullAll(since = 0): Promise<IncrementalCardsResponse & IncrementalNotesResponse> {
  const [cardsEnv, notesEnv] = await Promise.all([pullCards(since), pullNotes(since)])
  const deletedIds = new Set<string>([...(cardsEnv.deletedIds ?? []), ...(notesEnv.deletedIds ?? [])])
  return {
    cards: cardsEnv.cards,
    decks: cardsEnv.decks,
    notes: notesEnv.notes,
    serverTimeMs:
      typeof cardsEnv.serverTimeMs === 'number' ? cardsEnv.serverTimeMs : notesEnv.serverTimeMs,
    deletedIds: Array.from(deletedIds),
  }
}

/** 创建 note（契约 §2 POST /api/flashcards/notes）。 */
export async function createNote(input: FlashcardNoteInput): Promise<FlashcardNote> {
  const body = stripServerFields({ ...input }) as unknown as FlashcardNoteInput
  return http<FlashcardNote>(`${BASE}/notes`, {
    method: 'POST',
    body: JSON.stringify(body),
  })
}

/** 修改 note（契约 §2 PATCH /api/flashcards/notes/:id）。 */
export async function patchNote(
  id: string,
  patch: Partial<Pick<FlashcardNote, 'front' | 'back' | 'tags'>>,
): Promise<FlashcardNote> {
  const body = stripServerFields({ ...patch }) as unknown as Partial<FlashcardNoteInput>
  return http<FlashcardNote>(`${BASE}/notes/${encodeURIComponent(id)}`, {
    method: 'PATCH',
    body: JSON.stringify(body),
  })
}

/** 幂等软删除 note（契约 §2 DELETE /api/flashcards/notes/:id）。 */
export async function deleteNote(id: string): Promise<{ ok: true }> {
  return http<{ ok: true }>(`${BASE}/notes/${encodeURIComponent(id)}`, { method: 'DELETE' })
}

/** 手动建一张 extra card（契约 §2 POST /api/flashcards/cards）。 */
export async function createCard(input: { noteId: string; due?: number }): Promise<FlashcardCard> {
  const body = stripServerFields({ ...input })
  return http<FlashcardCard>(`${BASE}/cards`, {
    method: 'POST',
    body: JSON.stringify(body),
  })
}

/** 修改 card（契约 §2 PATCH /api/flashcards/cards/:id，v1 仅 due / state）。 */
export async function patchCard(
  cardId: string,
  patch: { due?: number; state?: FlashcardCard['state']; queue?: number },
): Promise<FlashcardCard> {
  const body = stripServerFields({ ...patch })
  return http<FlashcardCard>(`${BASE}/cards/${encodeURIComponent(cardId)}`, {
    method: 'PATCH',
    body: JSON.stringify(body),
  })
}

/**
 * 提交一次评分（契约 §2 POST /api/flashcards/cards/:id/review）。
 * FSRS 字段由 useFsrs 在前端算好后落库；后端只做存储，不重算。
 */
export async function recordReview(
  cardId: string,
  rating: FlashcardRating,
  payload: {
    reviewedAt?: number
    due: number
    state: FlashcardCard['state']
    stability: number
    difficulty: number
    intervalDays: number
  },
): Promise<FlashcardReviewResponse> {
  const body = {
    rating,
    reviewedAt: payload.reviewedAt ?? Math.floor(Date.now() / 1000),
    due: payload.due,
    state: payload.state,
    stability: payload.stability,
    difficulty: payload.difficulty,
    intervalDays: payload.intervalDays,
  }
  return http<FlashcardReviewResponse>(`${BASE}/cards/${encodeURIComponent(cardId)}/review`, {
    method: 'POST',
    body: JSON.stringify(body),
  })
}

/** 该卡组此刻到期卡片数（契约 §2 GET /api/flashcards/decks/:id/due?now=）。 */
export async function dueCards(deckId: string, nowSec: number): Promise<FlashcardsDueResponse> {
  const path = `${BASE}/decks/${encodeURIComponent(deckId)}/due?now=${nowSec}`
  const body = await http<Partial<FlashcardsDueResponse>>(path)
  return {
    cards: Array.isArray(body.cards) ? body.cards : [],
    totalDue: typeof body.totalDue === 'number' ? body.totalDue : 0,
  }
}

/** 直接拿 totalDue 数字（契约 §2 GET /api/flashcards/decks/:id/due?now=）。 */
export async function dueCount(deckId: string, nowSec: number): Promise<number> {
  const res = await dueCards(deckId, nowSec)
  return res.totalDue
}

export { ApiError }
export type { IncrementalCardsResponse, IncrementalNotesResponse }
// 引用以满足 verbatimModuleSyntax：FlashcardDeckConfig 在 v1 视图层未被使用，
// 但下游 store 会再次 import 此处以保持单一来源。
export type { FlashcardDeckConfig }