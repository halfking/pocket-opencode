/** WebView 不直连 IMAP；原生或 H5 只委托 pocketd。 */
export function formatFetchHint(syncHint: string, classified: number): string {
  if (classified > 0) return `${syncHint}，已归类 ${classified}`
  return syncHint
}

export function shouldRunBackgroundFetch(now: number, lastAt: number, minGapMs = 15 * 60_000): boolean {
  return lastAt <= 0 || now - lastAt >= minGapMs
}

export type EmailFetchStage = 'native-sync' | 'js-sync' | 'pull-list'

/** 真机走原生 HTTP；H5 走 JS fetch。两边都只打 pocketd，再拉列表。 */
export function emailFetchStages(nativeAvailable: boolean): EmailFetchStage[] {
  return nativeAvailable ? ['native-sync', 'pull-list'] : ['js-sync', 'pull-list']
}

/** Capacitor 上 resolveApiBase 可能被空覆盖成 ''；原生收信必须有绝对地址。 */
export function resolveFetchApiBase(resolved: string, nativeFallback: string): string {
  const base = (resolved || '').trim()
  if (base) return base.replace(/\/$/, '')
  return (nativeFallback || '').replace(/\/$/, '')
}
