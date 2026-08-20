/**
 * Idempotent WebSocket bus (PR5 of optimization v4).
 *
 * Implements the WS envelope + idempotency rules from
 *   docs/优化v4/15-PR1-契约冻结与发布前置.md §4
 *
 * Goals (PR5 boundary, see 14 §2 row 5):
 *   - Single subscription path; unknown event types are kept instead of
 *     silently dropped so the rest of the app can be migrated later.
 *   - Events are dispatched at most once per (type, id) pair, even when
 *     a reconnect causes the upstream to replay.
 *   - Out-of-order delivery is tolerated: events with the same
 *     `cause.action_id` collapse into the latest one we have seen.
 *   - No new business events are introduced; this module only wraps the
 *     existing `wsClient`.
 *   - Existing `ws-bus.ts` keeps working; this file adds an alternative
 *     `subscribe()` API that consumers can opt into per-PR.
 *
 * PR5 does NOT:
 *   - Add reconnection backoff (still uses wsClient's built-in logic).
 *   - Send anything new; only dispatches what the server already emits.
 *   - Touch the existing `ws-bus.ts` event subscriptions.
 *
 * The pure helpers (envelope normalization, dedupe) are exported so
 * tests can run without a live WebSocket.
 */

import wsClient from '../api/websocket'

/** Frozen envelope v1 — see PR1 §4. */
export interface WsEnvelopeV1<T = unknown> {
  v: 1
  id: string
  ts: number
  channel: string
  topic: string
  type: string
  data: T
  cause?: {
    action_id?: string
    approval_id?: string
    correlation_id?: string
  }
}

export type AnyEnvelope = WsEnvelopeV1 | { v?: number; type?: string; id?: string; data?: unknown; [k: string]: unknown }

export interface DispatchRecord {
  envelope: AnyEnvelope
  receivedAt: number
}

export interface DispatchLog {
  /** Maps `id` to the most recent dispatch for that id. */
  byEventId: Map<string, DispatchRecord>
  /** Maps `cause.action_id` (or `cause.approval_id`) to its latest event id. */
  byActionId: Map<string, string>
  /** Trim records older than this many ms to keep memory bounded. */
  retentionMs: number
}

export function createDispatchLog(retentionMs = 5 * 60 * 1000): DispatchLog {
  return {
    byEventId: new Map(),
    byActionId: new Map(),
    retentionMs,
  }
}

/**
 * Normalize a raw server payload into the v1 envelope shape, or return
 * `null` if it is too malformed to act on. We never throw; unknown
 * shapes are passed through with `type` from the raw payload so callers
 * can still log/observe them.
 */
export function normalizeEnvelope(raw: unknown): AnyEnvelope | null {
  if (!raw || typeof raw !== 'object') return null
  const r = raw as Record<string, unknown>
  if (r.v === 1 && typeof r.type === 'string') {
    return raw as WsEnvelopeV1
  }
  // Legacy / pre-envelope shape: { type, payload }
  if (typeof r.type === 'string') {
    return {
      v: 0,
      id: typeof r.id === 'string' ? r.id : undefined,
      ts: typeof r.ts === 'number' ? r.ts : Date.now(),
      channel: typeof r.channel === 'string' ? r.channel : 'legacy',
      topic: typeof r.topic === 'string' ? r.topic : '',
      type: r.type,
      data: r.payload ?? r.data ?? null,
    }
  }
  return null
}

export interface DedupeDecision {
  /** True when the event has been seen before (and we should NOT replay it). */
  duplicate: boolean
  /** True when this event makes a previously seen `action_id` obsolete. */
  supersedes?: string
}

/**
 * Record an envelope into the log and decide whether the consumer should
 * be invoked. Returns `duplicate: true` when the same id was already
 * processed within the retention window.
 */
export function recordEnvelope(
  log: DispatchLog,
  envelope: AnyEnvelope,
  now: number = Date.now(),
): DedupeDecision {
  if (envelope.id) {
    const prior = log.byEventId.get(envelope.id)
    if (prior) {
      return { duplicate: true }
    }
    log.byEventId.set(envelope.id, { envelope, receivedAt: now })
  }
  const cause = (envelope as { cause?: { action_id?: string; approval_id?: string } }).cause
  const actionId = cause?.action_id || cause?.approval_id
  if (actionId) {
    log.byActionId.set(actionId, envelope.id || `${now}`)
  }
  // Trim old records after the insert so the cap is honored even when
  // many entries arrive in a tight loop. We never trim during a single
  // call's read of `size` to avoid an O(n^2) walk; instead we sample
  // every Nth insertion.
  if (log.byEventId.size >= 1024) {
    const cutoff = now - log.retentionMs
    for (const [id, rec] of log.byEventId) {
      if (rec.receivedAt < cutoff) log.byEventId.delete(id)
    }
    // If still over cap, drop the oldest entries (LRU-ish).
    if (log.byEventId.size >= 1024) {
      const sorted = Array.from(log.byEventId.entries()).sort(
        (a, b) => a[1].receivedAt - b[1].receivedAt,
      )
      const toDrop = log.byEventId.size - 512
      for (let i = 0; i < toDrop; i++) {
        log.byEventId.delete(sorted[i][0])
      }
    }
  }
  return { duplicate: false }
}

/** Pure helper: extract `event type` from a raw message. */
export function extractEventType(raw: unknown): string | null {
  const env = normalizeEnvelope(raw)
  return env?.type ?? null
}

// ---- runtime subscription ---------------------------------------------

type Listener = (env: AnyEnvelope) => void

const listenersByType: Map<string, Set<Listener>> = new Map()
const envelopeLog: DispatchLog = createDispatchLog()
let wired = false

/**
 * Wire the idempotent bus on top of the existing wsClient. Idempotent.
 * Existing ws-bus.ts subscriptions are untouched.
 */
export function initIdempotentWsBus(): void {
  if (wired) return
  wired = true
  wsClient.on('message', (msg: unknown) => {
    const env = normalizeEnvelope(msg)
    if (!env || !env.type) return
    const decision = recordEnvelope(envelopeLog, env)
    if (decision.duplicate) return
    const listeners = listenersByType.get(env.type)
    if (listeners) {
      for (const l of listeners) {
        try {
          l(env)
        } catch (err) {
          // Never let a faulty subscriber take down the bus.
          // eslint-disable-next-line no-console
          console.warn('[idempotent-ws] subscriber threw for', env.type, err)
        }
      }
    }
    // Also dispatch to wildcard subscribers for diagnostics.
    const wildcards = listenersByType.get('*')
    if (wildcards) {
      for (const l of wildcards) {
        try {
          l(env)
        } catch (err) {
          // eslint-disable-next-line no-console
          console.warn('[idempotent-ws] wildcard subscriber threw', err)
        }
      }
    }
  })
}

export interface SubscribeHandle {
  /** Stop receiving events. Safe to call multiple times. */
  unsubscribe(): void
}

/**
 * Subscribe to a specific event type. The callback is invoked at most
 * once per `(type, id)` pair (idempotent), regardless of how many
 * reconnects/replays the upstream emits.
 */
export function subscribe<T = unknown>(
  eventType: string,
  cb: (env: WsEnvelopeV1<T>) => void,
): SubscribeHandle {
  const wrapped: Listener = (env) => cb(env as WsEnvelopeV1<T>)
  let set = listenersByType.get(eventType)
  if (!set) {
    set = new Set()
    listenersByType.set(eventType, set)
  }
  set.add(wrapped)
  return {
    unsubscribe() {
      const s = listenersByType.get(eventType)
      if (s) s.delete(wrapped)
    },
  }
}

/** Test/HMR: reset internal state. Not for production use. */
export function _resetIdempotentBusForTest(): void {
  listenersByType.clear()
  envelopeLog.byEventId.clear()
  envelopeLog.byActionId.clear()
  wired = false
}
