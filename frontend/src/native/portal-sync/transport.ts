import { assertAuthSession, captureAuthSession, useAuthStore } from '../../stores/auth'
import { readCachedModuleMode } from '../../stores/syncPolicyStore'
import { fetchPortalWorkspaceBinding, WorkspaceBindingRequestError, type WorkspaceBinding } from '../../api/workspaceBinding'
import { fetchWithTimeout } from '../../api/fetch-timeout'
import { forceReauth } from '../../api/http'
import { REDCLAW_OIDC_AUTH_METHOD } from '../../api/authentication-mode'
import { readPortalErrorCode } from '../../api/portal-error'
import { resolveRedclawPortalBase } from '../../config/redclaw-portal'
import type { SyncDb, PortalAck } from './contract.ts'
import { PortalCursorExpired, portalChangePageSize } from './snapshot-recovery.ts'
import type { PortalTransport } from './engine.ts'
import type { PortalSyncModule } from '../../api/syncPolicyContract.ts'

export async function portalTransport(db: SyncDb, options: { cloud?: boolean; admit?: () => void; module?: PortalSyncModule } = {}) {
  const module = options.module ?? 'notes'
  const auth = useAuthStore()
  const session = captureAuthSession(auth)
  const owner = db.owner
  const base = resolveRedclawPortalBase()
  const assertOwner = () => {
    db.assertCurrent()
    assertAuthSession(auth, session)
    options.admit?.()
    if (!owner || !auth.token || auth.userId !== owner.userId || auth.workspaceId !== owner.workspaceId || resolveRedclawPortalBase() !== base || (options.cloud !== false && readCachedModuleMode(module) !== 'cloud')) {
      throw Object.assign(new Error('Portal notes sync admission changed'), { name: 'AbortError' })
    }
  }
  assertOwner()
  if (auth.authMethod === REDCLAW_OIDC_AUTH_METHOD) await auth.maybeRefresh()
  assertOwner()
  const token = auth.token
  const assertCurrent = () => {
    assertOwner()
    if (auth.token !== token) {
      throw Object.assign(new Error('Portal sync credential changed'), { name: 'AbortError' })
    }
  }
  const reauth = async (status: number, code: string) => {
    assertCurrent()
    if (status === 401 && code === 'parent_session_invalid') await forceReauth(session, token)
  }
  let binding: WorkspaceBinding
  try {
    binding = await fetchPortalWorkspaceBinding(owner!.workspaceId, { token, base, assertCurrent })
  } catch (error) {
    if (error instanceof WorkspaceBindingRequestError) await reauth(error.status, error.code)
    throw error
  }
  assertCurrent()
  if (binding.userId !== owner!.userId || binding.workspaceId !== owner!.workspaceId || !binding.tenantId || binding.expiresAt * 1000 <= Date.now()) {
    throw new Error('Workspace authority did not confirm the notes owner')
  }
  const request = async (path: string, init: RequestInit = {}) => {
    assertCurrent()
    if (binding.expiresAt * 1000 <= Date.now()) throw new Error('Workspace binding expired')
    const response = await fetchWithTimeout(`${base}/api/v1/portal/sync/v2/${path}`, {
      ...init, redirect: 'error', credentials: 'omit',
      headers: { Authorization: `Bearer ${token}`, 'X-Workspace-Binding': binding.binding, 'Content-Type': 'application/json' },
    })
    assertCurrent()
    if (!response.ok) {
      if (response.status === 401) await reauth(response.status, await readPortalErrorCode(response))
      if (response.status === 410 && path.startsWith('changes?') && await readPortalErrorCode(response) === 'reset_required') {
        assertCurrent()
        throw new PortalCursorExpired()
      }
      throw new Error(`Portal notes sync request failed (${response.status})`)
    }
    const body = await response.json()
    assertCurrent()
    return body
  }
  const workspace = encodeURIComponent(owner!.workspaceId)
  const transport: PortalTransport = {
    changes: cursor => request(`changes?workspace_id=${workspace}&module=notes&limit=${portalChangePageSize}&cursor=${encodeURIComponent(cursor)}`),
    push: items => request(`push?workspace_id=${workspace}`, { method: 'POST', body: JSON.stringify({ items }) }) as Promise<{ results: PortalAck[] }>,
  }
  return { assertCurrent, transport, request, purge: (modules: string[], resetId: string) => request(`purge?workspace_id=${workspace}`, {
    method: 'POST', body: JSON.stringify({ modules, reset_id: resetId }),
  }) }
}
