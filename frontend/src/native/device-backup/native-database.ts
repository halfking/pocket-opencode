import { registerPlugin } from '@capacitor/core'

export interface DatabaseStage {
  stageId: string
}

interface KeyedDatabaseStage extends DatabaseStage {
  password: string
}

interface DeviceDatabaseBackupPlugin {
  snapshotDatabase(stage: DatabaseStage): Promise<void>
  validateDatabase(stage: KeyedDatabaseStage): Promise<void>
  ensureEmptyDatabase(): Promise<void>
  publishDatabase(stage: KeyedDatabaseStage): Promise<void>
}

export const deviceDatabaseBackup = registerPlugin<DeviceDatabaseBackupPlugin>('DeviceBackup')

/** Path under Capacitor Filesystem Directory.Cache. */
export function backupDatabasePath(stage: DatabaseStage): string {
  return `pocket-device-backup/${stage.stageId}/database.sqlite`
}
