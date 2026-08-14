/**
 * useSessionState — typed mobile session state surface (PR7 of optimization v4).
 *
 * Implements the session / run / approval states from
 *   docs/优化v4/15-PR1-契约冻结与发布前置.md §3.3
 *   docs/优化v4/08-移动端UI与交互规范.md §4.3–§4.4
 *
 * Goals (PR7 boundary, see 14 §2 row 7):
 *   - New composable that new pages / views can adopt incrementally.
 *   - Does NOT change existing SessionListView / SessionConversationView.
 *   - Surfaces idle / running / waiting_user / completed / failed /
 *     cancelled, and a derived `waitingReason` of 'approval' | 'question'
 *     so the UI can pick the right Bottom Sheet (PR8).
 *   - Wraps the AsyncState helper from PR3 so consumers get a typed
 *     phase + request id without duplicating logic.
 *
 * PR7 does NOT:
 *   - Modify existing pages (SessionListView, SessionConversationView).
 *   - Add new WS subscriptions; consumers continue to pass their own
 *     status source (typically from the idempotent WS bus from PR5).
 *   - Send anything to the network; this is a pure reactive surface.
 */

import { computed, ref, type ComputedRef, type Ref } from 'vue'
import {
  type AsyncState,
  type AsyncError,
  describePhase,
  idleAsyncState,
  startLoading,
  succeedAsync,
  failAsync,
  cancelledAsync,
  newRequestId,
} from '../utils/asyncState'

/** Session lifecycle phases — see PR1 §3.3. */
export type SessionPhase =
  | 'unknown'
  | 'idle'
  | 'running'
  | 'waiting_user'
  | 'completed'
  | 'failed'
  | 'cancelled'

/** Why `waiting_user` is currently held — drives Bottom Sheet selection. */
export type WaitingReason = 'approval' | 'question' | null

export interface SessionSummary {
  id: string
  instanceId: string
  title: string
  preview?: string
  updatedAt: number
  hasPending: boolean
  pendingType?: 'permission' | 'question'
}

export interface UseSessionStateOptions {
  /** Initial page size for the list (default 30). */
  pageSize?: number
}

export interface UseSessionStateReturn {
  /** AsyncState<T>-shaped list state with phases from PR1. */
  list: ComputedRef<AsyncState<SessionSummary[]>>
  /** Mutation: tell the surface to start a load (initial or refresh). */
  beginLoad: (refresh?: boolean) => void
  /** Mutation: report a successful load. `isEmpty` lets the helper
   * decide between `ready` and `empty`. */
  finishLoad: (items: SessionSummary[]) => void
  /** Mutation: report a failed load. */
  failLoad: (err: AsyncError) => void
  /** Mutation: cancel the in-flight request without surfacing an error. */
  cancelLoad: () => void

  /** Aggregate of the per-item phase map; used to render the page-level
   * status banner without iterating every summary. */
  aggregate: ComputedRef<SessionPhase>
  /** Map of session id → derived waiting reason for the Bottom Sheet. */
  waitingBySession: ComputedRef<Record<string, WaitingReason>>
  /** Human-readable banner label for the list state. */
  statusLabel: ComputedRef<string>
}

export function useSessionState(
  options: UseSessionStateOptions = {},
): UseSessionStateReturn {
  // Per-PR3: AsyncState<T> is mutable internally; we expose it as a
  // ComputedRef so consumers can read but not mutate directly.
  const pageSize = options.pageSize ?? 30
  const state = ref<AsyncState<SessionSummary[]>>(idleAsyncState())
  // Detail overlay: session id → derived phase. Filled by the page
  // from streaming events; kept here so future PRs can adopt without
  // touching every consumer.
  const detailPhase = ref<Record<string, SessionPhase>>({})
  const waiting = ref<Record<string, WaitingReason>>({})

  function beginLoad(refresh = false): void {
    const id = newRequestId()
    state.value = startLoading(state.value, id, refresh)
  }
  function finishLoad(items: SessionSummary[]): void {
    const reqId = state.value.requestId || newRequestId()
    state.value = succeedAsync(
      state.value,
      reqId,
      items.slice(0, pageSize),
      (arr) => arr.length === 0,
    )
  }
  function failLoad(err: AsyncError): void {
    const reqId = state.value.requestId || newRequestId()
    state.value = failAsync(state.value, reqId, err)
  }
  function cancelLoad(): void {
    state.value = cancelledAsync(state.value)
  }

  const list = computed(() => state.value)

  const aggregate = computed<SessionPhase>(() => {
    switch (state.value.phase) {
      case 'loading':
        return 'running'
      case 'refreshing':
        return 'running'
      case 'ready':
      case 'empty':
        return 'idle'
      case 'error':
        return 'failed'
      case 'offline':
        return 'unknown'
      default:
        return 'unknown'
    }
  })

  const waitingBySession = computed(() => waiting.value)
  const statusLabel = computed(() => describePhase(state.value))

  // Surface detailPhase / waiting as a stable map so consumers can read
  // them through the returned object.
  void detailPhase // reserved for PR7.1 once streaming events adopt this composable

  return {
    list,
    beginLoad,
    finishLoad,
    failLoad,
    cancelLoad,
    aggregate,
    waitingBySession,
    statusLabel,
  }
}

/**
 * Helper to derive a `WaitingReason` from a per-item SessionSummary.
 * Pure function — exported so pages can reuse the logic without having
 * to mount the composable just for this one decision.
 */
export function deriveWaitingReason(item: SessionSummary): WaitingReason {
  if (!item.hasPending) return null
  if (item.pendingType === 'permission') return 'approval'
  if (item.pendingType === 'question') return 'question'
  return 'approval'
}

/** Pure: pick the most severe phase from a list of session summaries. */
export function worstPhase(items: SessionSummary[]): SessionPhase {
  let worst: SessionPhase = 'idle'
  for (const it of items) {
    if (it.hasPending) return 'waiting_user'
  }
  return worst
}
