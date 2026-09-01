/** Build the authenticated WebSocket endpoint from the configured API base. */
export function buildWebSocketUrl(apiBase: string, token?: string | null): string | null {
  const value = apiBase.trim()
  if (!value) return null

  let url: URL
  try {
    url = new URL(value)
  } catch {
    return null
  }

  if (!['http:', 'https:'].includes(url.protocol)) return null

  url.protocol = url.protocol === 'https:' ? 'wss:' : 'ws:'
  url.pathname = `${url.pathname.replace(/\/+$/, '')}/ws`
  url.search = ''
  url.hash = ''
  if (token) url.searchParams.set('token', token)

  return url.toString()
}
