export interface IdRemap {
  localId: string
  serverId: string
}

/** 把内存列表里的临时 id 换成服务端 id；目标 id 已存在则丢掉本地副本。 */
export function applyIdRemap<T extends { id: string }>(rows: T[], remap: IdRemap): T[] {
  if (!remap.localId || !remap.serverId || remap.localId === remap.serverId) return rows
  const hasServer = rows.some((r) => r.id === remap.serverId)
  const next: T[] = []
  for (const row of rows) {
    if (row.id === remap.localId) {
      if (hasServer) continue
      next.push({ ...row, id: remap.serverId })
      continue
    }
    next.push(row)
  }
  return next
}

export function applyIdRemaps<T extends { id: string }>(rows: T[], remaps: IdRemap[]): T[] {
  return remaps.reduce((acc, remap) => applyIdRemap(acc, remap), rows)
}

/** 缩略图 / 选中态等以 id 为 key 的字典一并改名。 */
export function remapRecordMap<T>(
  map: Record<string, T>,
  remap: IdRemap,
): Record<string, T> {
  if (!remap.localId || !remap.serverId || remap.localId === remap.serverId) return map
  if (!(remap.localId in map)) return map
  const next = { ...map }
  if (!(remap.serverId in next)) next[remap.serverId] = next[remap.localId]
  delete next[remap.localId]
  return next
}
