import { AccApiError, listAccSopRuns, ACC_TIMEOUT_MS, type AccSopRunListItem } from '../../api/accApi.ts'
import { fetchWithTimeout } from '../../api/fetch-timeout.ts'
import type { AccLogAuthority } from './acc-log-runtime.ts'

interface Snapshot { savedAt: string; items: AccSopRunListItem[] }
// Opaque process/session authority objects, never token strings or persisted keys.
const snapshots = new WeakMap<object, Snapshot>()

export function discardLegacyBoardSnapshot(): void {
  try { localStorage.removeItem('acc_board_last_runs') }
  catch { /* Storage may be unavailable; the legacy entry is never read. */ }
}

export function readBoardSnapshot(owner: object): Snapshot | null {
  const snapshot = snapshots.get(owner)
  return snapshot ? structuredClone(snapshot) : null
}

export function saveBoardSnapshot(owner: object, items: AccSopRunListItem[]): void {
  snapshots.set(owner, { savedAt: new Date().toISOString(), items: structuredClone(items) })
}

export function clearBoardSnapshot(owner: object): void { snapshots.delete(owner) }

export function boardOwnerCurrent(owner: object | null, current: object | null): boolean {
  if (!owner || owner !== current) return false
  try { (owner as AccLogAuthority).assertCurrent(); return true }
  catch { /* An expired session/configuration grants no cache or result admission. */ return false }
}

export async function loadBoardRuns(owner: object) {
  const authority = owner as AccLogAuthority
  authority.assertCurrent()
  const result = await listAccSopRuns(20)
  authority.assertCurrent()
  if (result.source === 'registry') {
    // The legacy list API swallows every HTTP error as a registry fallback.
    // Recheck that fallback's status without changing its shared normalizer/API:
    // auth failures must not become cache admission; outages must not erase it.
    const headers: Record<string, string> = { Accept: 'application/json' }
    if (authority.token) headers.Authorization = `Bearer ${authority.token}`
    const response = await fetchWithTimeout(`${authority.base}/api/v2/sop/runs`, { headers }, { timeoutMs: ACC_TIMEOUT_MS })
    authority.assertCurrent()
    await response.body?.cancel()
    authority.assertCurrent()
    if (!response.ok && response.status !== 404 && response.status !== 405) {
      throw new AccApiError(response.status, '任务列表暂不可用')
    }
  }
  return result
}
