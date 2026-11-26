import { registerPlugin } from '@capacitor/core'

export interface DatabaseStage {
  stageId: string
}

interface KeyedDatabaseStage extends DatabaseStage {
  password: string
}

interface DeviceDatabaseBackupPlugin {
  snapshotDatabase(stage: DatabaseStage): Promise<void>
  validateDatabase(stage: KeyedDatabaseStage & { includeDigest?: boolean }): Promise<{ digest?: string }>
  databaseRestoreStatus(input: { digest: string }): Promise<{ status: 'empty' | 'published' | 'conflict' }>
  ensureEmptyDatabase(): Promise<void>
  publishDatabase(stage: KeyedDatabaseStage): Promise<void>
  pickArchive(stage: DatabaseStage): Promise<{ completed: boolean }>
  saveArchive(stage: DatabaseStage): Promise<{ completed: boolean }>
}

export const deviceDatabaseBackup = registerPlugin<DeviceDatabaseBackupPlugin>('DeviceBackup')
export const backupCacheRoot = 'pocket-device-backup'

/** Path under Capacitor Filesystem Directory.Cache. */
export function backupStagePath(stage: DatabaseStage): string {
  return `${backupCacheRoot}/${stage.stageId}`
}

export function backupDatabasePath(stage: DatabaseStage): string {
  return `${backupStagePath(stage)}/database.sqlite`
}

export function backupArchivePath(stage: DatabaseStage): string {
  return `${backupStagePath(stage)}/OpenPocket-backup.pocketbackup`
}
