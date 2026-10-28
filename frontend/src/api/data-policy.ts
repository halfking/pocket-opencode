import { readCachedModuleMode, recordSyncReportEntry } from '../stores/syncPolicyStore.ts'
import type { SyncModule } from './syncPolicyContract.ts'
import { ModulePolicyLocalError } from './module-policy.ts'

/** 共享运算 API 必须由调用方传业务归属，不能按 /stt 或 /llm 路径猜。 */
export function assertCloudModule(module?: SyncModule): void {
  if (module && readCachedModuleMode(module) === 'local') {
    recordSyncReportEntry({ module, status: 'skipped' })
    throw new ModulePolicyLocalError(module)
  }
}
