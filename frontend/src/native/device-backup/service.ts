import { Directory } from '@capacitor/filesystem'
import { assertAuthSession, captureAuthSession, useAuthStore } from '../../stores/auth'
import { localDB } from '../local-db'
import { restoreMeetingAudio, snapshotMeetingAudio, type MeetingAudioBackupEntry } from '../meeting-audio'
import { isWebFallbackRuntime } from '../runtime-platform'
import { backupBlobSource, restoreBackup, writeBackup } from './archive'
import { BackupError, type BackupEntry } from './manifest'
import { backupArchivePath, backupDatabasePath, backupStagePath, deviceDatabaseBackup, type DatabaseStage } from './native-database'
import { cacheMediaPath, cacheRecordingBlob, cacheSink, captureBackupMedia, openCacheSource, removeBackupPath, type MediaRoot } from './files'
import { ensureEmptyMedia, installBackupMedia, recordPendingBackup, recoverAbandonedBackup, serializeBackupRestore, settlePendingBackupRestore } from './recovery'

const stage = (): DatabaseStage => ({ stageId: crypto.randomUUID() })

export function canUseDeviceBackup(): boolean {
  const auth = useAuthStore()
  return !isWebFallbackRuntime() && auth.isAuthenticated && !auth.ownerVerificationFailed && Boolean(auth.userId && auth.workspaceId)
}

function captureBackupAccess() {
  if (!canUseDeviceBackup()) throw Object.assign(new Error('Device backup requires a verified native session'), { code: 'BACKUP_OWNER' })
  const auth = useAuthStore()
  const session = captureAuthSession(auth)
  const lease = localDB.captureOperation({ kind: 'local-edit' })
  const owner = lease.owner.principal
  if (!owner || owner.userId !== auth.userId || owner.workspaceId !== auth.workspaceId) {
    throw Object.assign(new Error('Device backup owner is not ready'), { code: 'BACKUP_OWNER' })
  }
  const assertCurrent = () => {
    assertAuthSession(auth, session)
    localDB.assertOperationCurrent(lease)
  }
  assertCurrent()
  return { owner, lease, assertCurrent }
}

async function archiveFile(entry: { kind: 'database' } | { kind: 'file'; path: string }, path: string) {
  const source = await openCacheSource(path)
  return { entry: { ...entry, size: source.size } as BackupEntry, read: source.read }
}

export async function exportDeviceBackup(password: string): Promise<boolean> {
  const access = captureBackupAccess()
  return serializeBackupRestore(() => saveDeviceBackup(access, password))
}

async function saveDeviceBackup({ owner, lease, assertCurrent }: ReturnType<typeof captureBackupAccess>, password: string): Promise<boolean> {
  assertCurrent()
  await recoverAbandonedBackup()
  assertCurrent()
  const candidate = stage()
  try {
    let files: string[] = []
    let recordings: MeetingAudioBackupEntry[] = []
    await localDB.snapshotBackupDatabase(candidate, lease, async () => {
      files = await captureBackupMedia(candidate, assertCurrent)
      recordings = await snapshotMeetingAudio(assertCurrent)
    })
    assertCurrent()
    await deviceDatabaseBackup.validateDatabase({ ...candidate, password })
    const entries = [await archiveFile({ kind: 'database' }, backupDatabasePath(candidate))]
    for (const path of files) entries.push(await archiveFile({ kind: 'file', path }, cacheMediaPath(candidate, path)))
    for (const { blob, ...metadata } of recordings) {
      const source = backupBlobSource(blob)
      entries.push({ entry: { kind: 'recording', size: source.size, ...metadata }, read: source.read })
    }
    const sink = cacheSink(backupArchivePath(candidate))
    await writeBackup(owner, entries, password, bytes => {
      assertCurrent()
      return sink.write(bytes)
    })
    assertCurrent()
    return (await deviceDatabaseBackup.saveArchive(candidate)).completed
  } finally {
    await removeBackupPath(backupStagePath(candidate), Directory.Cache, true)
  }
}

export async function restoreDeviceBackup(password: string): Promise<boolean> {
  const access = captureBackupAccess()
  return serializeBackupRestore(async () => {
    const { owner, lease, assertCurrent } = access
    await recoverAbandonedBackup()
    await deviceDatabaseBackup.ensureEmptyDatabase()
    await ensureEmptyMedia()
    if ((await snapshotMeetingAudio(assertCurrent)).length) {
      throw Object.assign(new Error('The local recording store is not empty'), { code: 'BACKUP_TARGET' })
    }
    assertCurrent()
    const candidate = stage()
    try {
      const picked = await deviceDatabaseBackup.pickArchive(candidate)
      if (!picked.completed) return false
      assertCurrent()
      const source = await openCacheSource(backupArchivePath(candidate))
      await restoreBackup(source, password, owner, async () => {
        const roots = new Set<MediaRoot>()
        const recordings: { entry: Extract<BackupEntry, { kind: 'recording' }>; path: string }[] = []
        return {
          async open(entry) {
            assertCurrent()
            if (entry.kind === 'database') return cacheSink(backupDatabasePath(candidate))
            if (entry.kind === 'file') {
              roots.add(entry.path.split('/')[0] as MediaRoot)
              return cacheSink(cacheMediaPath(candidate, entry.path))
            }
            const path = `${backupStagePath(candidate)}/audio/${recordings.length}.wav`
            recordings.push({ entry, path })
            return cacheSink(path)
          },
          async commit() {
            const { digest } = await deviceDatabaseBackup.validateDatabase({ ...candidate, password, includeDigest: true })
            if (!digest || !/^[0-9a-f]{64}$/.test(digest)) throw Object.assign(new Error('Backup database identity unavailable'), { code: 'BACKUP_STATE' })
            await deviceDatabaseBackup.ensureEmptyDatabase()
            await ensureEmptyMedia()
            assertCurrent()
            const pending = { ...candidate, digest }
            await recordPendingBackup(pending)
            try {
              await installBackupMedia(pending, roots, assertCurrent)
              const audio: MeetingAudioBackupEntry[] = []
              for (const { entry, path } of recordings) {
                assertCurrent()
                const blob = await cacheRecordingBlob(path, entry.mimeType, entry.size)
                audio.push({ store: entry.store, key: entry.key, mimeType: entry.mimeType, blob })
              }
              await restoreMeetingAudio(audio, assertCurrent, pending.stageId)
              assertCurrent()
              await localDB.publishBackupDatabase(candidate, password, lease)
            } finally {
              // Handles both rejected installations and errors after the native database rename.
              await settlePendingBackupRestore()
            }
          },
          abort: async () => {},
        }
      })
      assertCurrent()
      return true
    } finally {
      await removeBackupPath(backupStagePath(candidate), Directory.Cache, true)
    }
  })
}

export function describeDeviceBackupError(error: unknown, restoring: boolean): string {
  if (error instanceof BackupError) {
    if (error.code === 'owner') return 'settings.deviceBackupOwnerMismatch'
    if (error.code === 'authentication') return 'settings.deviceBackupAuthentication'
    return 'settings.deviceBackupFormat'
  }
  const code = (error as { code?: string })?.code
  if (code === 'BACKUP_TARGET') return 'settings.deviceBackupTarget'
  if (code === 'BACKUP_OWNER') return 'settings.deviceBackupOwner'
  if (code === 'BACKUP_DATABASE') return restoring ? 'settings.deviceBackupInvalidDatabase' : 'settings.deviceBackupAuthentication'
  if (code === 'BACKUP_STATE') return 'settings.deviceBackupRestoreState'
  return restoring ? 'settings.deviceBackupRestoreFailure' : 'settings.deviceBackupSaveFailure'
}
