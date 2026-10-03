/**
 * 消息 hub 里「通知」那一类行的副标题文案。
 *
 * ## 为什么要单独抽一个模块
 *
 * 2026-10-03 真机（Redmi 2411DRN47C）在「消息」tab 看到每条通知行下面都挂着
 * 一串 `email.importance` / `email.import`。用 CDP 量到 `/api/notifications`
 * 返回 50 条，`kind` **全部**是 `email.important`、`source` 全部是 `email` ——
 * 也就是说这不是个别脏数据，而是**每一行都在显示内部键**。
 *
 * 根因是 MessagesHubView 原来的
 *
 *     subtitle: n.kind || n.source || '',
 *
 * `kind` / `source` 是服务端的**内部分类键**，不是展示文案。原作者大概是想
 * 「有值就显示」，但没想过这个值长得像 `email.important`。
 *
 * ## 修法：只显示已知来源的本地化文案，未知一律留空
 *
 * 为什么不直接 `subtitle: ''`？因为这行是从通知队列来的，说清楚它来自邮件
 * 对用户有意义（`messagesHub.filter.email` 已经有这条文案）。
 * 为什么未知要留空而不是原样透出？因为**留空是安全的一侧，泄漏不是** ——
 * 出现一个没见过的 kind 时，宁可少一行副标题，也不要把 `scheduledtask.xyz`
 * 这种键甩到用户脸上。这与 email 摘要那条「宁可空串也不转储 MIME」是同一个取向。
 */
/** 已知来源 → 已有 i18n key。不在表里的一律视为未知。 */
export const NOTIFICATION_SOURCE_KEYS: Record<string, string> = {
  email: 'messagesHub.filter.email',
  rss: 'messagesHub.filter.rss',
  task: 'messagesHub.filter.task',
}

/** 长得像内部键的值（`email.important` / `scheduledtask.foo`）—— UI 层绝不该出现。 */
export const INTERNAL_KEY_RE = /^[a-z][a-z0-9]*(\.[a-z][a-z0-9]*)+$/

/**
 * 取通知行的副标题。
 *
 * @param source 通知的 source 字段（如 'email'）
 * @param kind   通知的 kind 字段（如 'email.important'）——**故意不用**
 * @param t      i18n 翻译函数
 * @returns 本地化文案；来源未知时返回空串
 */
export function notificationSourceLabel(
  source: string | undefined | null,
  kind: string | undefined | null,
  t: (key: string) => string,
): string {
  const raw = (source || '').trim()
  if (!raw) return ''
  const key = NOTIFICATION_SOURCE_KEYS[raw]
  if (!key) return ''
  // 兜底：万一有人把内部键塞进了 source 字段，也别让它漏到界面上。
  if (INTERNAL_KEY_RE.test(raw)) return ''
  return t(key)
}
