/**
 * snapshot-store — 本地快照存储（list-sync 体系的通用持久化点）。
 *
 * 按 namespace 分组存 JSON 快照，支持 dirty 标记（离线先行、联网回推）。
 * 当前实现基于 localStorage（web / dev 环境可用）；原生壳后续可无缝
 * 替换为 SQLite 实现而不影响调用方接口。
 */

export interface SnapshotRow<T> {
  id: string
  updatedAt: number
  dirty: boolean
  payload: T
}

const PREFIX = 'pocket:snapshots:'

function storage(): Storage | null {
  try {
    if (typeof localStorage === 'undefined') return null
    return localStorage
  } catch {
    return null
  }
}

function loadNS<T>(ns: string): Record<string, SnapshotRow<T>> {
  const raw = storage()?.getItem(PREFIX + ns)
  if (!raw) return {}
  try {
    const parsed = JSON.parse(raw) as Record<string, SnapshotRow<T>>
    return parsed && typeof parsed === 'object' ? parsed : {}
  } catch {
    return {}
  }
}

function saveNS<T>(ns: string, rows: Record<string, SnapshotRow<T>>): void {
  try {
    storage()?.setItem(PREFIX + ns, JSON.stringify(rows))
  } catch { /* 存储满/不可写：快照属尽力而为的缓存，静默降级 */ }
}

export async function listSnapshots<T>(ns: string): Promise<Array<SnapshotRow<T>>> {
  return Object.values(loadNS<T>(ns))
}

export async function upsertSnapshots<T>(
  ns: string,
  rows: Array<SnapshotRow<T>>,
): Promise<void> {
  if (!rows.length) return
  const existing = loadNS<T>(ns)
  for (const row of rows) {
    if (!row?.id) continue
    existing[row.id] = row
  }
  saveNS(ns, existing)
}
