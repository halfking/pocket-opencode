export interface BackupOwner {
  userId: string
  workspaceId: string
}

export const recordingStores = ['recordings', 'owned-recordings'] as const
export type RecordingStore = typeof recordingStores[number]

export interface BackupRecording {
  store: RecordingStore
  key: string
  mimeType: string
}

export type BackupEntry = (
  | { kind: 'database' }
  | { kind: 'file'; path: string }
  | ({ kind: 'recording' } & BackupRecording)
) & { size: number }

export interface BackupManifest {
  owner: BackupOwner
  entries: BackupEntry[]
}

export type BackupErrorCode = 'format' | 'authentication' | 'owner' | 'source'

export class BackupError extends Error {
  readonly code: BackupErrorCode

  constructor(code: BackupErrorCode) {
    super(`Local backup: ${code}`)
    this.name = 'BackupError'
    this.code = code
  }
}

function badFormat(): never {
  throw new BackupError('format')
}

function object(value: unknown): Record<string, unknown> {
  if (!value || typeof value !== 'object' || Array.isArray(value)) badFormat()
  return value as Record<string, unknown>
}

function id(value: unknown, maxLength = 1024): value is string {
  return typeof value === 'string' && value.length > 0 && value.length <= maxLength && !/[\u0000-\u001f\u007f]/.test(value)
}

function assetPath(value: unknown): string {
  if (typeof value !== 'string' || value.length > 1024) badFormat()
  const [root, ...parts] = value.split('/')
  if ((root !== 'notes' && root !== 'recordings') || parts.length < 2) badFormat()
  for (const part of parts) {
    if (!/^[A-Za-z0-9._-]{1,255}$/.test(part) || part === '.' || part === '..') badFormat()
  }
  return value
}

function ownedRecordingKey(key: string): boolean {
  let tuple: unknown
  try { tuple = JSON.parse(key) } catch { return false }
  if (!Array.isArray(tuple) || tuple.length !== 4 || tuple[0] !== 'owned-v1' || !id(tuple[3])) return false
  const [, userId, workspaceId] = tuple
  return ((id(userId) && id(workspaceId)) || (userId === '' && workspaceId === '')) && JSON.stringify(tuple) === key
}

export function validateBackupRecording(value: unknown): BackupRecording {
  const { store, key, mimeType } = object(value)
  if (store !== 'recordings' && store !== 'owned-recordings') badFormat()
  if (!id(key, 8192) || (store === 'owned-recordings' && !ownedRecordingKey(key))) badFormat()
  if (typeof mimeType !== 'string' || !/^[\x20-\x7e]{0,255}$/.test(mimeType)) badFormat()
  return { store, key, mimeType }
}

export function validateManifest(value: unknown): BackupManifest {
  const input = object(value)
  const { userId, workspaceId } = object(input.owner)
  if (!id(userId) || !id(workspaceId) || !Array.isArray(input.entries)) badFormat()

  const entries: BackupEntry[] = []
  const files = new Set<string>()
  const directories = new Map<string, string>()
  const recordings = new Set<string>()
  for (const item of input.entries) {
    const { kind, size, path } = object(item)
    if (typeof size !== 'number' || !Number.isSafeInteger(size) || size < 0) badFormat()
    switch (kind) {
      case 'database': {
        if (entries.length !== 0 || size === 0) badFormat()
        entries.push({ kind, size })
        break
      }
      case 'file': {
        const original = assetPath(path)
        const canonical = original.toLowerCase()
        if (files.has(canonical) || directories.has(canonical)) badFormat()
        let ancestor = original
        while (ancestor.includes('/')) {
          ancestor = ancestor.slice(0, ancestor.lastIndexOf('/'))
          const key = ancestor.toLowerCase()
          const previous = directories.get(key)
          if (files.has(key) || (previous !== undefined && previous !== ancestor)) badFormat()
          directories.set(key, ancestor)
        }
        files.add(canonical)
        entries.push({ kind, size, path: original })
        break
      }
      case 'recording': {
        const recording = validateBackupRecording(item)
        const identity = JSON.stringify([recording.store, recording.key])
        if (recordings.has(identity)) badFormat()
        recordings.add(identity)
        entries.push({ kind, size, ...recording })
        break
      }
      default:
        badFormat()
    }
  }
  if (entries[0]?.kind !== 'database') badFormat()
  return { owner: { userId, workspaceId }, entries }
}
