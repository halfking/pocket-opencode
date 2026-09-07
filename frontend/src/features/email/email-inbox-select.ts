export function toggleSelect(current: Set<string>, id: string): Set<string> {
  const next = new Set(current)
  if (next.has(id)) next.delete(id)
  else next.add(id)
  return next
}

export function selectedIdList(selected: Set<string>): string[] {
  return [...selected]
}
