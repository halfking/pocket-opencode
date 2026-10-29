import { createRecordRuntime, recordRuntimes } from '../../native/portal-sync/record-runtime.ts'
import { rssSyncAdapter } from './rss-sync-adapter'

export const rssSyncRuntime = createRecordRuntime(rssSyncAdapter)
export function startRssPortalSync(): void { recordRuntimes.set('rss',rssSyncRuntime);rssSyncRuntime.start() }
