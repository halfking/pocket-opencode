import type { SyncDb } from './contract.ts'

type Owner = SyncDb['owner']
const known = new Map<string, number>()
const resetting = new Set<string>()
const key = (owner: NonNullable<Owner>) => JSON.stringify([owner.userId, owner.workspaceId])

export function validEpoch(value: unknown): value is number {
  return Number.isSafeInteger(value) && Number(value) >= 0
}

/** Capture synchronously at the business operation's entry, before any await. */
export function captureNotesEpoch(owner: Owner): number | null {
  if (!owner || resetting.has(key(owner))) return null
  return known.get(key(owner)) ?? null
}

export function rememberNotesEpoch(owner: Owner, epoch: number): void {
  if (!owner || !validEpoch(epoch)) throw new Error('Verified notes epoch required')
  known.set(key(owner), Math.max(epoch, known.get(key(owner)) ?? 0))
}

export function markNotesResetPending(owner: Owner, pending: boolean): void {
  if (!owner) return
  if (pending) resetting.add(key(owner))
  else resetting.delete(key(owner))
}
