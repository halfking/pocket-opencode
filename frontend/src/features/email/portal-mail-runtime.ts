import { createRecordRuntime } from '../../native/portal-sync/record-runtime.ts'
import { portalMailAdapter } from './portal-mail-adapter.ts'

export const portalEmailRuntime = createRecordRuntime(portalMailAdapter)
export const startPortalEmailSync = portalEmailRuntime.start
export const syncPortalMailNow = portalEmailRuntime.syncNow
