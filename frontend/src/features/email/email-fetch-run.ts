import { emailApi } from '../../api/email'
import { PRODUCTION_API_BASE, resolveApiBase } from '../../config/api-base'
import { useAuthStore } from '../../stores/auth'
import { formatFetchHint, resolveFetchApiBase } from './email-fetch-plan'
import { configureNativeEmailFetch, runNativeEmailFetch } from './email-fetch-native'
import { pullInboxFromServer, syncInboxFromServer } from './email-inbox-page'

export { formatFetchHint, shouldRunBackgroundFetch } from './email-fetch-plan'

async function prepareNative(): Promise<boolean> {
  const auth = useAuthStore()
  if (!auth.token) return false
  return configureNativeEmailFetch(resolveFetchApiBase(resolveApiBase(), PRODUCTION_API_BASE), auth.token)
}

export async function runDelegatedEmailFetch(opts?: { classify?: boolean }): Promise<{ hint: string; classified: number }> {
  if (await prepareNative()) {
    const native = await runNativeEmailFetch()
    if (native.used) {
      await pullInboxFromServer()
      const hint = formatFetchHint(
        `已同步 ${native.synced} 个账户，新邮件 ${native.newCount}`,
        native.classified,
      )
      return { hint, classified: native.classified }
    }
  }
  const syncHint = await syncInboxFromServer()
  let classified = 0
  if (opts?.classify !== false) {
    try {
      const report = await emailApi.classifyInbox(20)
      classified = report.classified ?? 0
    } catch {
      /* 收信已完成；归类可稍后由导航栏或后台再跑 */
    }
  }
  return { hint: formatFetchHint(syncHint, classified), classified }
}
