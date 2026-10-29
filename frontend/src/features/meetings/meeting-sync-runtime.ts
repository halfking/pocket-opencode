import { createRecordRuntime, recordRuntimes } from '../../native/portal-sync/record-runtime.ts'
import { meetingSyncAdapter } from './meeting-sync-adapter.ts'

export const meetingSyncRuntime = createRecordRuntime(meetingSyncAdapter)
recordRuntimes.set('meetings', meetingSyncRuntime)
export const startPortalMeetingsSync = meetingSyncRuntime.start
export const syncPortalMeetingsNow = meetingSyncRuntime.syncNow
