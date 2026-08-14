/**
 * Persistent outbox primitives (PR13 of optimization v4).
 *
 * Implements the outbox model from
 *   docs/优化v4/02-安全审计与整改清单.md §3 SEC-06
 *   docs/优化v4/05-数据模型与本地云同步.md
 *
 * Goals (PR13 boundary, see 14 §2 row 13):
 *   - Typed record + state machine for queued actions.
 *   - Pure helpers for enqueue / claim / succeed / fail / dead-letter,
 *     backed by an injectable storage interface so the storage layer
 *     (LocalDB / SQLite / in-memory) is decided by the host.
 *   - Per-record idempotency key + server cursor so retries do not
 *     produce duplicate side-effects.
 *
 * PR13 does NOT:
 *   - Replace the in-memory WebSocket-hub offline queue; that queue is
 *     for transient UI events, not for durable actions.
 *   - Auto-start a background loop. The host decides when to drain
 *     (e.g. on app resume, on network change).
 *   - Persist to LocalDB. The OutboxStorage interface lets the caller
 *     supply the actual backend; this file ships the logic only.
 *
 * Per the docs/优化v4/04 §3 'default deny egress' and docs/优化v4/06
 * 'data classification', only 'retriable_action' payloads are accepted.
 * UI notifications and analytics events MUST NOT be queued here.
 */

import { newRequestId } from './asyncState'

export type OutboxKind = 'retriable_action'

export type OutboxState =
  | 'queued'
  | 'inflight'
  | 'succeeded'
  | 'failed'
  | 'dead_letter'

export interface OutboxRecord<T = unknown> {
  /** ULID-ish monotonic id; doubles as the UI-side correlation id. */
  id: string
  /** Caller-supplied key; the server uses this to dedupe replays. */
  idempotencyKey: string
  /** Workspace scope; mismatched records are dropped on workspace switch. */
  workspaceId: string
  /** Action type, e.g. 'note.create' or 'email.send'. */
  action: string
  /** Payload. May be encrypted by the host before enqueue. */
  payload: T
  /** Monotonic creation time (ms epoch). */
  createdAt: number
  /** When the next attempt is allowed (ms epoch). */
  nextAttemptAt: number
  /** Number of attempts so far. */
  attempts: number
  /** Server cursor returned on success (used for resume). */
  cursor?: string
  /** Last error code/message for diagnostics. */
  lastError?: string
  /** Lifecycle state. */
  state: OutboxState
  /** Time-to-live in ms; after this, the record is dead-lettered. */
  ttlMs: number
}

/** Storage abstraction. Hosts back this with SQLite / LocalDB / in-memory. */
export interface OutboxStorage {
  put(record: OutboxRecord): Promise<void>
  get(id: string): Promise<OutboxRecord | null>
  delete(id: string): Promise<void>
  /** Return all queued records with nextAttemptAt <= now, ordered by createdAt. */
  listReady(now: number, limit: number): Promise<OutboxRecord[]>
  /** Return the count of records in the given states. */
  countByState(states: OutboxState[]): Promise<number>
}

export interface EnqueueOptions<T> {
  workspaceId: string
  action: string
  payload: T
  idempotencyKey: string
  ttlMs?: number
  /** Delay before the first attempt (default 0). */
  delayMs?: number
}

export function newIdempotencyIdempotencyKey(seed?: string): string {
  // Caller-supplied key wins; otherwise build a deterministic shape so
  // retries within the same app session collapse.
  if (seed) return seed
  return newRequestId()
}

/** Pure helper: build a fresh queued record. */
export function enqueue<T>(opts: EnqueueOptions<T>, now: number = Date.now()): OutboxRecord<T> {
  const ttl = opts.ttlMs ?? 24 * 60 * 60 * 1000 // 24h default per SEC-06
  return {
    id: newRequestId(),
    idempotencyKey: opts.idempotencyKey,
    workspaceId: opts.workspaceId,
    action: opts.action,
    payload: opts.payload,
    createdAt: now,
    nextAttemptAt: now + (opts.delayMs ?? 0),
    attempts: 0,
    state: 'queued',
    ttlMs: ttl,
  }
}

/** Pure helper: mark a record as in-flight. */
export function claim(record: OutboxRecord, now: number = Date.now()): OutboxRecord {
  if (record.state === 'inflight') return record
  return { ...record, state: 'inflight', attempts: record.attempts + 1 }
}

/** Pure helper: terminal success. Caller removes the record after. */
export function succeed(record: OutboxRecord, cursor?: string): OutboxRecord {
  return {
    ...record,
    state: 'succeeded',
    cursor: cursor ?? record.cursor,
    lastError: undefined,
  }
}

/** Pure helper: compute exponential backoff for the next attempt. */
export function backoffMs(record: OutboxRecord, attempt: number, base = 1_000, cap = 60_000): number {
  // Exponential with full jitter; capped so a misconfigured host cannot
  // sleep for hours.
  const exp = Math.min(cap, base * 2 ** Math.max(0, attempt - 1))
  return Math.floor(Math.random() * exp)
}

/** Pure helper: should this record be dead-lettered? */
export function shouldDeadLetter(record: OutboxRecord, now: number, maxAttempts = 8): boolean {
  if (record.attempts >= maxAttempts) return true
  if (now - record.createdAt > record.ttlMs) return true
  return false
}

/** Pure helper: mark for retry with backoff. */
export function failForRetry(record: OutboxRecord, errorCode: string, now: number = Date.now()): OutboxRecord {
  const next = backoffMs(record, record.attempts)
  return {
    ...record,
    state: 'queued',
    lastError: errorCode,
    nextAttemptAt: now + next,
  }
}

/** Pure helper: move to dead-letter after exceeding limits. */
export function deadLetter(record: OutboxRecord, errorCode: string): OutboxRecord {
  return {
    ...record,
    state: 'dead_letter',
    lastError: errorCode,
  }
}

/**
 * Decide whether a queued record still belongs to the active workspace.
 * Returns true when the record should remain; false means the caller
 * should drop it (SEC-06: '切换 workspace 不发送旧 workspace 队列').
 */
export function matchesWorkspace(record: OutboxRecord, workspaceId: string): boolean {
  return record.workspaceId === workspaceId
}
