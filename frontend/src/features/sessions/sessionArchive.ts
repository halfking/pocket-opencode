/**
 * sessionArchive.ts — 会话归档本地元数据（P2 E5-S4）。
 *
 * OpenCode 上游无 archive 写接口；归档是设备侧列表过滤，不改变服务端会话状态。
 * 非敏感的 session id 按 workspace + instance 分区存 localStorage。解析失败时
 * fail-open（不隐藏任何会话），避免损坏数据让条目消失。
 */

const PREFIX = 'pocket_session_archive_v1'

export interface ArchiveScope {
  workspaceId: string
  instanceId: string
}

export function archiveStorageKey(scope: ArchiveScope): string {
  const workspace = scope.workspaceId.trim() || 'default'
  const instance = scope.instanceId.trim() || 'all'
  return `${PREFIX}:${encodeURIComponent(workspace)}:${encodeURIComponent(instance)}`
}

export function parseArchivedIds(raw: string | null): Set<string> {
  if (!raw) return new Set()
  try {
    const value: unknown = JSON.parse(raw)
    if (!Array.isArray(value)) return new Set()
    return new Set(value.filter((id): id is string => typeof id === 'string' && id !== ''))
  } catch {
    return new Set()
  }
}

export function readArchivedIds(storage: Pick<Storage, 'getItem'>, scope: ArchiveScope): Set<string> {
  return parseArchivedIds(storage.getItem(archiveStorageKey(scope)))
}

export function writeArchivedIds(
  storage: Pick<Storage, 'setItem'>,
  scope: ArchiveScope,
  ids: Iterable<string>,
): void {
  const sorted = [...new Set(ids)].filter(Boolean).sort()
  storage.setItem(archiveStorageKey(scope), JSON.stringify(sorted))
}

export function setSessionArchived(
  storage: Pick<Storage, 'getItem' | 'setItem'>,
  scope: ArchiveScope,
  sessionId: string,
  archived: boolean,
): Set<string> {
  const ids = readArchivedIds(storage, scope)
  if (archived) ids.add(sessionId)
  else ids.delete(sessionId)
  writeArchivedIds(storage, scope, ids)
  return ids
}
