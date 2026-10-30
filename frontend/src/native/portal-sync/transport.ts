import { assertAuthSession, captureAuthSession, useAuthStore } from '../../stores/auth'
import { readCachedModuleMode } from '../../stores/syncPolicyStore'
import { fetchPortalWorkspaceBinding } from '../../api/workspaceBinding'
import { fetchWithTimeout } from '../../api/fetch-timeout'
import { resolveRedclawPortalBase } from '../../config/redclaw-portal'
import type { SyncDb, PortalAck } from './contract.ts'
import type { PortalTransport } from './engine.ts'
import type { PortalSyncModule } from '../../api/syncPolicyContract.ts'

export async function portalTransport(db: SyncDb, options: { cloud?: boolean; admit?: () => void; module?: PortalSyncModule } = {}) {
  const module = options.module ?? 'notes'
  const auth = useAuthStore()
  const session = captureAuthSession(auth)
  const owner = db.owner
  const token = auth.token
  const base = resolveRedclawPortalBase()
  const assertCurrent = () => {
    db.assertCurrent()
    assertAuthSession(auth, session)
    options.admit?.()
    if (!owner || !token || auth.userId !== owner.userId || auth.workspaceId !== owner.workspaceId || resolveRedclawPortalBase() !== base || (options.cloud !== false && readCachedModuleMode(module) !== 'cloud')) {
      throw Object.assign(new Error('Portal notes sync admission changed'), { name: 'AbortError' })
    }
  }
  assertCurrent()
  const binding = await fetchPortalWorkspaceBinding(owner!.workspaceId, { token, base, assertCurrent })
  assertCurrent()
  if (binding.userId !== owner!.userId || binding.workspaceId !== owner!.workspaceId || !binding.tenantId || binding.expiresAt * 1000 <= Date.now()) {
    throw new Error('Workspace authority did not confirm the notes owner')
  }
  const request = async (path: string, init: RequestInit = {}) => {
    assertCurrent()
    if (binding.expiresAt * 1000 <= Date.now()) throw new Error('Workspace binding expired')
    const response = await fetchWithTimeout(`${base}/api/v1/portal/sync/v2/${path}`, {
      ...init, headers: { Authorization: `Bearer ${token}`, 'X-Workspace-Binding': binding.binding, 'Content-Type': 'application/json' },
    })
    assertCurrent()
    if (!response.ok) throw new Error(`Portal notes sync request failed (${response.status})`)
    const body = await response.json()
    assertCurrent()
    return body
  }
  const workspace = encodeURIComponent(owner!.workspaceId)
  const transport: PortalTransport = {
    changes: cursor => request(`changes?workspace_id=${workspace}&module=notes&limit=50&cursor=${encodeURIComponent(cursor)}`),
    push: items => request(`push?workspace_id=${workspace}`, { method: 'POST', body: JSON.stringify({ items }) }) as Promise<{ results: PortalAck[] }>,
  }
  return { assertCurrent, transport, request, purge: (modules: string[], resetId: string) => request(`purge?workspace_id=${workspace}`, {
    method: 'POST', body: JSON.stringify({ modules, reset_id: resetId }),
  }) }
}
