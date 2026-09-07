/** WebView 只委托服务端 IMAP；本机不直连邮箱。 */
export function formatFetchHint(syncHint: string, classified: number): string {
  if (classified > 0) return `${syncHint}，已归类 ${classified}`
  return syncHint
}

export function shouldRunBackgroundFetch(now: number, lastAt: number, minGapMs = 15 * 60_000): boolean {
  return lastAt <= 0 || now - lastAt >= minGapMs
}
