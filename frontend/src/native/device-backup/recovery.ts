import { Directory, Encoding, Filesystem } from '@capacitor/filesystem'
import { settleMeetingAudioRestore } from '../meeting-audio'
import { isWebFallbackRuntime, runtimePlatform } from '../runtime-platform'
import { backupCacheRoot, backupStagePath, deviceDatabaseBackup, type DatabaseStage } from './native-database'
import { backupStat, cacheMediaPath, mediaRoots, readBackupText, removeBackupPath, type MediaRoot } from './files'

const JOURNAL = 'pocket-device-restore/pending.json'
const journalDirectory = () => runtimePlatform() === 'ios' ? Directory.LibraryNoCloud : Directory.Data
const MARKER = '.pocket-device-restore'
const UUID = /^[0-9a-f]{8}(-[0-9a-f]{4}){3}-[0-9a-f]{12}$/
const DIGEST = /^[0-9a-f]{64}$/
const markerPath = (root: string) => `${root}/${MARKER}`

export interface PendingBackupRestore extends DatabaseStage {
  digest: string
}

let pending: Promise<unknown> = Promise.resolve()

/** The production unlock path and restore entry share this queue. */
export function serializeBackupRestore<T>(operation: () => Promise<T>): Promise<T> {
  const result = pending.then(operation)
  pending = result.catch(() => {})
  return result
}

/** Run inside the backup queue before allocating a new stage. */
export async function recoverAbandonedBackup(): Promise<void> {
  if (isWebFallbackRuntime()) return
  await settlePendingBackupRestore()
  // Cache can be evicted at any time. A cleanup error must not block local unlock.
  await removeBackupPath(backupCacheRoot, Directory.Cache, true).catch(() => {
    console.warn('[backup] Abandoned staging files could not be removed')
  })
}

export async function recordPendingBackup(restore: PendingBackupRestore): Promise<void> {
  const path = `${JOURNAL}-${restore.stageId}`
  const directory = journalDirectory()
  await Filesystem.writeFile({
    path, directory, encoding: Encoding.UTF8, data: JSON.stringify(restore), recursive: true,
  })
  await Filesystem.rename({ from: path, to: JOURNAL, directory })
}

export async function ensureEmptyMedia(): Promise<void> {
  for (const root of mediaRoots) {
    if (await backupStat(root, Directory.Data)) {
      throw Object.assign(new Error('The local backup media slot is not empty'), { code: 'BACKUP_TARGET' })
    }
  }
}

export async function installBackupMedia(restore: PendingBackupRestore, roots: Set<MediaRoot>, assertCurrent: () => void) {
  for (const root of roots) {
    const from = cacheMediaPath(restore, root)
    await Filesystem.writeFile({
      path: markerPath(from), directory: Directory.Cache, encoding: Encoding.UTF8, data: restore.stageId,
    })
    assertCurrent()
    if (await backupStat(root, Directory.Data)) throw Object.assign(new Error('The media target changed'), { code: 'BACKUP_TARGET' })
    await Filesystem.rename({ from, to: root, directory: Directory.Cache, toDirectory: Directory.Data })
  }
}

/**
 * The database is the final commit. A matching digest keeps the already
 * installed media. An empty database removes only effects tagged by this
 * journal. An unrelated database preserves all data and blocks initialization.
 */
export async function settlePendingBackupRestore(): Promise<void> {
  if (isWebFallbackRuntime()) return
  const directory = journalDirectory()
  const value = await readBackupText(JOURNAL, directory)
  if (value === null) return
  const restore = JSON.parse(value) as PendingBackupRestore | null
  if (!restore || typeof restore.stageId !== 'string' || !UUID.test(restore.stageId) ||
      typeof restore.digest !== 'string' || !DIGEST.test(restore.digest)) {
    throw Object.assign(new Error('The local backup journal is invalid'), { code: 'BACKUP_STATE' })
  }
  const { status } = await deviceDatabaseBackup.databaseRestoreStatus({ digest: restore.digest })
  if (status !== 'empty' && status !== 'published') {
    throw Object.assign(new Error('The local database differs from the recorded restore'), { code: 'BACKUP_STATE' })
  }

  const keep = status === 'published'
  for (const root of mediaRoots) {
    const marker = markerPath(root)
    if (await readBackupText(marker, Directory.Data) !== restore.stageId) continue
    if (keep) await removeBackupPath(marker, Directory.Data)
    else await removeBackupPath(root, Directory.Data, true)
  }
  await settleMeetingAudioRestore(restore.stageId, keep)
  await removeBackupPath(backupStagePath(restore), Directory.Cache, true)
  await removeBackupPath(JOURNAL, directory)
}
