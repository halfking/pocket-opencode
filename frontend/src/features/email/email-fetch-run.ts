import { emailApi } from '../../api/email'
import { formatFetchHint } from './email-fetch-plan'
import { syncInboxFromServer } from './email-inbox-page'

export { formatFetchHint, shouldRunBackgroundFetch } from './email-fetch-plan'

export async function runDelegatedEmailFetch(opts?: { classify?: boolean }): Promise<{ hint: string; classified: number }> {
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
