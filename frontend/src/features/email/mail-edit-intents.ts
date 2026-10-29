type Owner = { userId: string; workspaceId: string } | null
const versions = new Map<string, number>()
const pending = new Map<string, number>()
let nextVersion = 0
const key = (owner: Owner, id: string) => JSON.stringify([owner?.userId ?? '', owner?.workspaceId ?? '', id])

/** A source mirror lookup may not overtake a local edit already requested. */
export function beginMailAccountEdit(owner: Owner, id: string): number {
  const version = ++nextVersion
  versions.set(key(owner, id), version)
  pending.set(key(owner, id), version)
  return version
}
export function finishMailAccountEdit(owner: Owner, id: string, version: number): void {
  if (pending.get(key(owner, id)) === version) pending.delete(key(owner, id))
}
export function hasPendingMailAccountEdit(owner: Owner, id: string): boolean {
  return pending.has(key(owner, id))
}
export function mailAccountEditVersion(owner: Owner, id: string): number {
  return versions.get(key(owner, id)) ?? 0
}
