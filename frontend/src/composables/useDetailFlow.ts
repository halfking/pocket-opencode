/**
 * useDetailFlow — typed detail-page flow (PR10 of optimization v4).
 *
 * Implements the shared detail lifecycle for Notes / Email / Vault
 * per docs/优化v4/08-移动端UI与交互规范.md §6 and §7.
 *
 * Goals (PR10 boundary, see 14 §2 row 10):
 *   - Reusable open / load / save / cancel / retry / discard pipeline
 *     built on the AsyncState primitive from PR3.
 *   - Provides clipboard lifecycle helpers (copy + clear) used by Vault
 *     entries per docs/优化v4/02-安全审计与整改清单.md ADR-007.
 *   - Detects unsaved changes so the Android back button / swipe-back
 *     gesture can prompt before destroying the editor.
 *   - Does NOT modify any existing detail page; this is an opt-in
 *     composable for new code or future migrations.
 *
 * PR10 does NOT:
 *   - Touch the markdown sanitizer or network layer.
 *   - Manage clipboard history outside the page lifetime.
 *   - Replace the existing per-feature stores.
 */

import { computed, onBeforeUnmount, ref, shallowRef, type Ref, type ComputedRef } from 'vue'
import {
  type AsyncState,
  type AsyncError,
  describePhase,
  failAsync,
  idleAsyncState,
  newRequestId,
  startLoading,
  succeedAsync,
} from '../utils/asyncState'

export interface UseDetailFlowOptions<T> {
  /** Loader: receives the resource id and returns the entity. */
  load: (id: string) => Promise<T>
  /** Saver: receives the working draft and returns the canonical T. */
  save: (draft: T) => Promise<T>
  /** Deleter (optional): receives the id and removes the entity. */
  remove?: (id: string) => Promise<void>
  /** Whether this detail page holds a sensitive value (Vault TOTP). */
  sensitive?: boolean
  /** Time-to-live for sensitive clipboard copies in ms (default 20s). */
  copyTtlMs?: number
}

export interface UseDetailFlowReturn<T> {
  /** Reactive AsyncState. We expose the ref directly so consumers can
   *  read `state.value.phase`, etc. without UnwrapRef noise. */
  state: Ref<AsyncState<T>>
  draft: Ref<T | null>
  dirty: ComputedRef<boolean>
  statusLabel: ComputedRef<string>
  load: (id: string) => Promise<void>
  save: () => Promise<void>
  cancel: () => void
  remove: () => Promise<void>
  copyToClipboard: (value: string) => void
}

export function useDetailFlow<T>(opts: UseDetailFlowOptions<T>): UseDetailFlowReturn<T> {
  // We use markRaw / shallowRef semantics for the AsyncState because
  // we never mutate the inner object — we always replace it via the
  // PR3 helpers, which keeps Vue's reactive UnwrapRef from interfering.
  const state = shallowRef<AsyncState<T>>(idleAsyncState() as AsyncState<T>)
  // The "working" copy the user edits; commits back to state on save.
  const draft = shallowRef<T | null>(null) as Ref<T | null>
  // Initial value snapshot so we can detect edits.
  const initialSnapshot = ref<string>('')
  const copyTtlMs = opts.copyTtlMs ?? 20_000
  const clipboardTimer = ref<number | null>(null)

  const dirty = computed(() => {
    if (!draft.value) return false
    try {
      return JSON.stringify(draft.value) !== initialSnapshot.value
    } catch {
      return true
    }
  })

  const statusLabel = computed(() => describePhase(state.value))

  async function load(id: string): Promise<void> {
    const reqId = newRequestId()
    state.value = startLoading(state.value, reqId, false)
    try {
      const data = await opts.load(id)
      if (state.value.requestId !== reqId) return // stale
      state.value = succeedAsync(state.value, reqId, data, (v) => v == null)
      draft.value = data
      initialSnapshot.value = JSON.stringify(data ?? null)
    } catch (err) {
      const e = toAsyncError(err)
      state.value = failAsync(state.value, reqId, e)
    }
  }

  async function save(): Promise<void> {
    if (!draft.value) return
    const reqId = newRequestId()
    state.value = startLoading(state.value, reqId, false)
    try {
      const saved = await opts.save(draft.value)
      if (state.value.requestId !== reqId) return
      state.value = succeedAsync(state.value, reqId, saved, () => false)
      draft.value = saved
      initialSnapshot.value = JSON.stringify(saved ?? null)
    } catch (err) {
      const e = toAsyncError(err)
      state.value = failAsync(state.value, reqId, e)
    }
  }

  function cancel(): void {
    // Caller can route back; we just clear the dirty flag.
    if (draft.value && state.value.data) {
      draft.value = JSON.parse(initialSnapshot.value || 'null')
    }
  }

  async function remove(): Promise<void> {
    if (!opts.remove) return
    const id = (draft.value as unknown as { id?: string })?.id
    if (!id) return
    const reqId = newRequestId()
    state.value = startLoading(state.value, reqId, false)
    try {
      await opts.remove(id)
      if (state.value.requestId !== reqId) return
      state.value = succeedAsync(state.value, reqId, null as unknown as T, () => true)
      draft.value = null
      initialSnapshot.value = ''
    } catch (err) {
      const e = toAsyncError(err)
      state.value = failAsync(state.value, reqId, e)
    }
  }

  function copyToClipboard(value: string): void {
    if (!value) return
    if (typeof navigator === 'undefined' || !navigator.clipboard) {
      // Clipboard API unavailable; bail silently per ADR-007.
      return
    }
    navigator.clipboard.writeText(value).catch(() => {
      // ignore — we don't surface clipboard errors as user-visible
      // failures (system-level clipboard may be locked by another app).
    })
    if (clipboardTimer.value != null) {
      window.clearTimeout(clipboardTimer.value)
    }
    clipboardTimer.value = window.setTimeout(() => {
      // Best-effort cleanup. We cannot guarantee the OS clipboard was
      // not modified by another action in between.
      navigator.clipboard.writeText('').catch(() => {
        /* ignore */
      })
      clipboardTimer.value = null
    }, opts.sensitive ? copyTtlMs : 5_000)
  }

  onBeforeUnmount(() => {
    if (clipboardTimer.value != null) {
      window.clearTimeout(clipboardTimer.value)
      clipboardTimer.value = null
    }
    // Final defensive clipboard wipe when a sensitive page unmounts.
    if (opts.sensitive && typeof navigator !== 'undefined' && navigator.clipboard) {
      navigator.clipboard.writeText('').catch(() => {
        /* ignore */
      })
    }
  })

  return {
    state,
    draft,
    dirty,
    statusLabel,
    load,
    save,
    cancel,
    remove,
    copyToClipboard,
  }
}

function toAsyncError(err: unknown): AsyncError {
  if (err && typeof err === 'object' && 'code' in err) {
    const e = err as Partial<AsyncError> & { message?: unknown; status?: unknown }
    return {
      code: typeof e.code === 'string' ? e.code : 'unknown_error',
      message: typeof e.message === 'string' ? e.message : '操作失败',
      retryable: typeof e.retryable === 'boolean' ? e.retryable : true,
      status: typeof e.status === 'number' ? e.status : undefined,
    }
  }
  if (err instanceof Error) {
    return { code: 'unknown_error', message: err.message || '操作失败', retryable: true }
  }
  return { code: 'unknown_error', message: '操作失败', retryable: true }
}
