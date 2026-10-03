/**
 * 由 HTTP API 基址构造带鉴权的 WebSocket 端点。
 *
 * 合入自 feat/harmonyos-phase-b（该分支其余改动已被主线 BUG-D/BUG-F 取代）。
 *
 * 为什么要这个纯函数：原来 websocket.ts 里是字符串拼接
 * `base.replace(/^http/, 'ws') + '/ws'`，有三个逃不掉的问题：
 *   1. base 为空串时得到 '/ws'，`new WebSocket('/ws')` 抛 SyntaxError；
 *   2. base 是非 http(s) 协议（如 capacitor://）时 replace 不匹配，
 *      拼出 `capacitor://…/ws` 这种 WebSocket 根本不接受的 scheme；
 *   3. base 自带尾斜杠或路径前缀时会拼出 `//ws`。
 * 这三种都会走 catch -> scheduleReconnect，而重连没有次数上限，
 * 于是变成一条永远停不下来的重连循环（真机 logcat 里刷屏的那个）。
 *
 * 这里把「能不能构造出一个合法 ws URL」的判据收敛到一处，取值空间封闭。
 */

/**
 * @param apiBase HTTP API 基址，如 `https://host:8088`
 * @param token   JWT；为空表示未登录
 * @returns 合法的 ws(s) URL；基址不可用时返回 null（调用方应当跳过连接，而不是重试）
 */
export function buildWebSocketUrl(apiBase: string, token?: string | null): string | null {
  const value = (apiBase || '').trim()
  if (!value) return null

  let url: URL
  try {
    url = new URL(value)
  } catch {
    return null
  }

  if (url.protocol !== 'http:' && url.protocol !== 'https:') return null

  url.protocol = url.protocol === 'https:' ? 'wss:' : 'ws:'
  url.pathname = `${url.pathname.replace(/\/+$/, '')}/ws`
  url.search = ''
  url.hash = ''
  if (token) url.searchParams.set('token', token)

  return url.toString()
}
