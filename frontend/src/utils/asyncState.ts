/**
 * Typed async state primitives (PR3 of optimization v4).
 *
 * Implements the state machine frozen in
 *   docs/优化v4/15-PR1-契约冻结与发布前置.md §3.1
 *
 *   idle ──> loading ──> ready
 *                    ├─> empty
 *                    └─> error(retryable | terminal)
 *   ready/empty ──> refreshing ──> ready | error(保留旧数据)
 *   任何状态 ──> offline（保留本地数据与待发送队列）
 *
 * Rules enforced by the helpers:
 * - Each request is tagged with a `requestId`; an older request must not
 *   overwrite a newer one.
 * - `refreshing` keeps the previous data so the UI does not flicker.
 * - `error.retryable` only affects how the UI suggests recovery; the data
 *   payload of the previous successful load is preserved.
 * - `cancel()` is not an error; the caller can swallow it without
 *   surfacing a failure state.
 *
 * This module only provides helpers. PR3 deliberately does NOT touch
 * any feature store or page to keep the diff narrow and reversible.
 */

export type AsyncPhase =
  | 'idle'
  | 'loading'
  | 'ready'
  | 'empty'
  | 'error'
  | 'refreshing'
  | 'offline'

export interface AsyncError {
  /** Stable error code; see PR1 §10 for the canonical list. */
  code: string
  /** Human-readable message suitable for showing in UI. */
  message: string
  /** Whether the user can retry safely. */
  retryable: boolean
  /** HTTP/network status if available. */
  status?: number
  /** Request id that produced the error. */
  requestId?: string
}

export interface AsyncState<T> {
  phase: AsyncPhase
  data: T | null
  /** Last successfully loaded data, preserved across error/refreshing. */
  previous: T | null
  error: AsyncError | null
  /** Monotonic id of the latest issued request. */
  requestId: string | null
  /** Last successful load timestamp (ms epoch). */
  updatedAt: number | null
}

/** Compare helper: which state should "win" when interleaved responses arrive. */
export function isNewerRequest(
  incoming: string,
  current: string | null | undefined,
): boolean {
  if (!current) return true
  // Lexical compare works for ULID/UUIDv7/KSUID style ids.
  return incoming > current
}

/** Initial state for a brand-new async resource. */
export function idleAsyncState<T>(): AsyncState<T> {
  return {
    phase: 'idle',
    data: null,
    previous: null,
    error: null,
    requestId: null,
    updatedAt: null,
  }
}

/** Mark a request as started. */
export function startLoading<T>(
  prev: AsyncState<T>,
  requestId: string,
  isRefresh: boolean,
): AsyncState<T> {
  // If a newer request already owns the state, ignore this one. Equal
  // ids are accepted so a single request flow can transition loading ->
  // succeed/fail without producing a new id at every step.
  if (!isNewerRequest(requestId, prev.requestId) && prev.requestId !== null && prev.requestId !== requestId) {
    return prev
  }
  return {
    ...prev,
    phase: isRefresh ? 'refreshing' : 'loading',
    error: null,
    requestId,
  }
}

/** Mark a request as successful; preserve previous when the data is empty. */
export function succeedAsync<T>(
  prev: AsyncState<T>,
  requestId: string,
  data: T,
  isEmpty: (value: T) => boolean,
): AsyncState<T> {
  if (prev.requestId !== null && prev.requestId !== requestId && !isNewerRequest(requestId, prev.requestId)) {
    return prev
  }
  return {
    ...prev,
    phase: isEmpty(data) ? 'empty' : 'ready',
    data,
    previous: prev.data ?? prev.previous,
    error: null,
    requestId,
    updatedAt: Date.now(),
  }
}

/** Mark a request as failed; preserve previous data unless explicitly cleared. */
export function failAsync<T>(
  prev: AsyncState<T>,
  requestId: string,
  err: AsyncError,
): AsyncState<T> {
  if (prev.requestId !== null && prev.requestId !== requestId && !isNewerRequest(requestId, prev.requestId)) {
    return prev
  }
  return {
    ...prev,
    phase: 'error',
    error: { ...err, requestId },
    requestId,
    // Keep `data` so the UI can render stale data with a banner; the
    // previous successful value lives on `previous`.
    data: prev.data,
  }
}

/** Toggle to offline mode without losing local data. */
export function goOffline<T>(prev: AsyncState<T>): AsyncState<T> {
  if (prev.phase === 'offline') return prev
  return { ...prev, phase: 'offline' }
}

/** Toggle back to online; keep the current phase/data. */
export function goOnline<T>(prev: AsyncState<T>): AsyncState<T> {
  if (prev.phase !== 'offline') return prev
  // Pick the most descriptive non-offline phase we had.
  const next: AsyncPhase = prev.error ? 'error' : prev.data ? 'ready' : 'idle'
  return { ...prev, phase: next }
}

/** Cancel in-flight helper: not an error. Caller decides whether to keep state. */
export function cancelledAsync<T>(prev: AsyncState<T>): AsyncState<T> {
  // Cancellation keeps the current visible state and only clears the
  // requestId so the next request starts fresh.
  return { ...prev, requestId: null }
}

/** Build a fresh request id. */
export function newRequestId(): string {
  // Time-prefixed + random suffix so lexical sort tracks chronology.
  const t = Date.now().toString(36)
  const r = Math.random().toString(36).slice(2, 10)
  return `${t}-${r}`
}

/** Coerce an unknown thrown value into a typed AsyncError. */
export function toAsyncError(err: unknown): AsyncError {
  if (err && typeof err === 'object' && 'code' in err) {
    const e = err as Partial<AsyncError> & { message?: unknown; status?: unknown }
    return {
      code: typeof e.code === 'string' ? e.code : 'unknown_error',
      message:
        typeof e.message === 'string'
          ? e.message
          : '未知错误，请稍后重试。',
      retryable:
        typeof e.retryable === 'boolean'
          ? e.retryable
          : isRetryableStatus(e.status),
      status: typeof e.status === 'number' ? e.status : undefined,
    }
  }
  if (err instanceof Error) {
    return {
      code: 'unknown_error',
      message: err.message || '未知错误，请稍后重试。',
      retryable: true,
    }
  }
  return {
    code: 'unknown_error',
    message: '未知错误，请稍后重试。',
    retryable: true,
  }
}

function isRetryableStatus(status: unknown): boolean {
  if (typeof status !== 'number') return true
  if (status === 0) return true // network/timeout
  if (status === 408 || status === 425 || status === 429) return true
  if (status >= 500) return true
  return false
}

/** Human-friendly state label for status banners. */
export function describePhase<T>(state: AsyncState<T>): string {
  switch (state.phase) {
    case 'idle':
      return ''
    case 'loading':
      return '正在加载…'
    case 'ready':
      return ''
    case 'empty':
      return '当前没有内容'
    case 'error':
      return state.error?.message || '加载失败'
    case 'refreshing':
      return '正在刷新…'
    case 'offline':
      return '离线，仅本地内容可见'
  }
}
