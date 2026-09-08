import { Capacitor } from '@capacitor/core'
import { emailApi } from '../../api/email'
import { PRODUCTION_API_BASE, resolveApiBase } from '../../config/api-base'
import { useAuthStore } from '../../stores/auth'
import { formatFetchHint, resolveFetchApiBase, sanitizeFetchHint } from './email-fetch-plan'
import { configureNativeEmailFetch, runNativeEmailFetch } from './email-fetch-native'
import { pullInboxFromServer, syncInboxFromServer } from './email-inbox-page'

export { formatFetchHint, sanitizeFetchHint, shouldRunBackgroundFetch } from './email-fetch-plan'

async function prepareNative(): Promise<boolean> {
  const auth = useAuthStore()
  if (!auth.token) return false
  return configureNativeEmailFetch(resolveFetchApiBase(resolveApiBase(), PRODUCTION_API_BASE), auth.token)
}

export async function runDelegatedEmailFetch(opts?: { classify?: boolean }): Promise<{ hint: string; classified: number }> {
  await pullInboxFromServer()
  if (await prepareNative()) {
    const native = await runNativeEmailFetch()
    if (native.used) {
      await pullInboxFromServer()
      return {
        hint: sanitizeFetchHint(formatFetchHint(
          `已同步 ${native.synced} 个账户，新邮件 ${native.newCount}`,
          native.classified,
        )),
        classified: native.classified,
      }
    }
  }
  // 真机不走 WebView IMAP（长请求会 Failed to fetch）；只保留已拉到的列表。
  if (Capacitor.isNativePlatform()) {
    return { hint: '', classified: 0 }
  }
  const syncHint = await syncInboxFromServer()
  let classified = 0
  if (opts?.classify !== false) {
    try {
      const report = await emailApi.classifyInbox(20)
      classified = report.classified ?? 0
    } catch {
      /* 列表已拉；归类交给导航栏 */
    }
  }
  return { hint: sanitizeFetchHint(formatFetchHint(syncHint, classified)), classified }
}
