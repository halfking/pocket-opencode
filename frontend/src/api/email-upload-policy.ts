/** 邮件凭据不离开设备：旧账号 API 与配置同步只接受结构化元数据。 */
export function isEmailMetadataWrite(path: string, body: unknown): boolean {
  const parts = path.split('?')[0].split('/').map((part) => {
    try { return decodeURIComponent(part) } catch { return part }
  })
  const account = parts[2] === 'email' && parts[3] === 'accounts'
  const setting = parts[2] === 'user-settings' && parts[3] === 'email_account'
  if (!account && !setting) return true
  let parsed: unknown
  try { parsed = typeof body === 'string' ? JSON.parse(body) : body } catch { return false }
  if (!record(parsed)) return false
  if (setting) {
    if (!Object.entries(parsed).every(([key, value]) =>
      key === 'payload' || (key === 'updatedAt' && typeof value === 'number') || (key === 'secret' && !value))) return false
    parsed = parsed.payload
  }
  if (!record(parsed)) return false
  const strings = new Set(['id', 'displayName', 'emailAddress', 'imapHost', 'smtpHost', 'authType'])
  const numbers = new Set(['imapPort', 'smtpPort', 'syncIntervalMin', 'updatedAt', 'createdAt', 'lastSyncedAt'])
  return Object.entries(parsed).every(([key, value]) => {
    if (strings.has(key)) return typeof value === 'string'
    if (numbers.has(key)) return typeof value === 'number' && Number.isFinite(value)
    if (key === 'enabled') return typeof value === 'boolean'
    if (key === 'rules' && record(value)) return Object.entries(value).every(([field, list]) =>
      ['whitelist', 'blacklist', 'keywords'].includes(field) && Array.isArray(list) && list.every((item) => typeof item === 'string'))
    return false
  })
}

function record(value: unknown): value is Record<string, unknown> {
  return !!value && typeof value === 'object' &&
    (Object.getPrototypeOf(value) === Object.prototype || Object.getPrototypeOf(value) === null)
}

export function isEmailStaleWrite(body: unknown): boolean {
  return record(body) && body.error === 'stale write: server copy is newer' &&
    typeof body.updatedAt === 'number' && Number.isFinite(body.updatedAt)
}
