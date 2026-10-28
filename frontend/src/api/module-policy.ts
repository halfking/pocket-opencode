import type { SyncMode, SyncModule } from './syncPolicyContract.ts'

export class ModulePolicyLocalError extends Error {
  readonly status = 409
  readonly body: { error: 'module_policy_local'; module: SyncModule }
  constructor(module: SyncModule) {
    super('module_policy_local')
    this.name = 'ModulePolicyLocalError'
    this.body = { error: 'module_policy_local', module }
  }
}

export function isModulePolicyLocalError(error: unknown): boolean {
  return !!error && typeof error === 'object' &&
    (error as { body?: { error?: unknown } }).body?.error === 'module_policy_local'
}

/** Legacy pocketd 写路径也受模块策略约束；删除仍允许清理云端副本。 */
export function blockedModuleWrite(
  path: string,
  method: string | undefined,
  mode: (module: SyncModule) => SyncMode,
): SyncModule | null {
  if (['GET', 'HEAD', 'OPTIONS', 'DELETE'].includes((method ?? 'GET').toUpperCase())) return null
  const resource = path.split('?')[0].split('/')[2]
  const owners: Record<string, SyncModule | undefined> = { rss: 'rss', email: 'email', emails: 'email', notes: 'notes', meetings: 'meetings' }
  const module = owners[resource]
  return module && mode(module) === 'local' ? module : null
}
