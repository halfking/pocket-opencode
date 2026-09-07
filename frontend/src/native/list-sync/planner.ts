/**
 * 用户数据列表 LWW 规划器：比较 local/remote 的 updatedAt。
 * 较新的一侧获胜；相等则不动；dirty 本地即使时间戳相等也上行。
 */
export interface ListStamp {
  id: string
  updatedAt: number
  dirty?: boolean
}

export interface ListSyncPlan {
  pullIds: string[]
  pushIds: string[]
}

export function isLocalOnlyId(id: string): boolean {
  return id.startsWith('local-')
}

export function newLocalId(kind: string, nowMs = Date.now()): string {
  const rand = Math.random().toString(36).slice(2, 8)
  return `local-${kind}-${nowMs}-${rand}`
}

export function planListSync(
  local: ListStamp[],
  remote: ListStamp[],
  opts: { pushLocalOnly?: boolean } = {},
): ListSyncPlan {
  const pushLocalOnly = opts.pushLocalOnly !== false
  const localById = new Map(local.map((s) => [s.id, s]))
  const remoteById = new Map(remote.map((s) => [s.id, s]))
  const pullIds: string[] = []
  const pushIds: string[] = []

  for (const r of remote) {
    const l = localById.get(r.id)
    if (!l || r.updatedAt > l.updatedAt) pullIds.push(r.id)
  }
  for (const l of local) {
    const r = remoteById.get(l.id)
    if (!r) {
      if (pushLocalOnly) pushIds.push(l.id)
      continue
    }
    if (l.updatedAt > r.updatedAt || (l.dirty && l.updatedAt >= r.updatedAt)) {
      pushIds.push(l.id)
    }
  }
  return { pullIds, pushIds }
}
