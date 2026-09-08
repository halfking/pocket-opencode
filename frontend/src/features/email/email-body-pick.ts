/**
 * pickEmailDetailBody — 邮件详情正文择优：远端优先（最新），为空时回退本地缓存。
 */
export function pickEmailDetailBody(cached: string, remote: string): string {
  const r = (remote || '').trim()
  return r || (cached || '')
}
