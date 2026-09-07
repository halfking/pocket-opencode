export const DEFAULT_LIST_PAGE_SIZE = 30

/** 分页追加：按 id 去重后交给调用方排序（或保持追加顺序）。 */
export function mergeListPages<T extends { id: string }>(
  existing: T[],
  incoming: T[],
  sort?: (a: T, b: T) => number,
): T[] {
  const seen = new Set(existing.map((i) => i.id))
  const added = incoming.filter((i) => i.id && !seen.has(i.id))
  const merged = [...existing, ...added]
  return sort ? merged.sort(sort) : merged
}

export function pageHasMore(pageLen: number, pageSize = DEFAULT_LIST_PAGE_SIZE): boolean {
  return pageLen >= pageSize
}
