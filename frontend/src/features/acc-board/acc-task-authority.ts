import { readAcc } from '../../api/accApi'
import { usesDirectPocketAuthority } from '../../api/authentication-mode'
import { fetchPocketWorkspaceBinding, fetchPortalWorkspaceBinding } from '../../api/workspaceBinding'
import { resolveRuntimeApiBase } from '../../config/api-base'
import { resolveRedclawPortalBase } from '../../config/redclaw-portal'
import { useAuthStore } from '../../stores/auth'
import type { AccLogAuthority } from './acc-log-runtime'

export async function verifyAccTaskAuthority(
  owner: AccLogAuthority, auth: ReturnType<typeof useAuthStore>, signal: AbortSignal,
) {
  const userId = auth.userId, workspaceId = auth.workspaceId, token = auth.token
  const direct = usesDirectPocketAuthority(auth.authMethod)
  const resolveBase = direct ? resolveRuntimeApiBase : resolveRedclawPortalBase
  const base = resolveBase()
  function assertCurrent() {
    owner.assertCurrent()
    if (signal.aborted || auth.activationStatus !== 'ready' || auth.userId !== userId || auth.workspaceId !== workspaceId
        || auth.token !== token || resolveBase() !== base) throw new DOMException('ACC task authority changed', 'AbortError')
  }
  assertCurrent()
  if (!owner.token || !token || !userId || !workspaceId || workspaceId !== owner.workspaceId) {
    throw new Error('请先登录并在 ACC 看板配置当前账号的凭据')
  }
  const getBinding = direct ? fetchPocketWorkspaceBinding : fetchPortalWorkspaceBinding
  const binding = await getBinding(workspaceId, { base, token, assertCurrent })
  assertCurrent()
  if (binding.userId !== userId || binding.workspaceId !== workspaceId) {
    throw new Error('当前登录未获工作区授权')
  }
  const verified = { base: owner.base, token: owner.token, assertCurrent() {
    assertCurrent()
    if (binding.expiresAt * 1000 <= Date.now()) throw new Error('工作区绑定已过期，请刷新')
  } }
  const account = await readAcc<{ authenticated?: boolean; user_id?: string; tenant_id?: string }>(
    verified, '/api/v2/auth/session', signal,
  )
  if (account?.authenticated !== true || account.user_id !== userId || account.tenant_id !== binding.tenantId) {
    throw new Error('ACC 凭据与当前账号或租户不一致')
  }
  return verified
}
