/** An unreadable rejection cannot establish that the parent session is invalid. */
export async function readPortalErrorCode(response: Response): Promise<string> {
  try {
    const body = await response.json()
    if (typeof body?.error === 'string') return body.error
    if (typeof body?.error?.code === 'string') return body.error.code
  } catch {
    return ''
  }
  return ''
}
