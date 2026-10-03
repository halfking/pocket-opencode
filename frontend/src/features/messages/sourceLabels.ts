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
 * ## 修法：白名单，只显示已知来源的本地化文案，未知一律留空
 *
 * 为什么不直接 `subtitle: ''`？因为这行是从通知队列来的，说清楚它来自邮件
 * 对用户有意义（`messagesHub.filter.email` 已经有这条文案）。
 * 为什么未知要留空而不是原样透出？因为**留空是安全的一侧，泄漏不是** ——
 * 出现一个没见过的 kind 时，宁可少一行副标题，也不要把 `scheduledtask.xyz`
 * 这种键甩到用户脸上。这与 email 摘要那条「宁可空串也不转储 MIME」是同一个取向。
 *
 * ## 这里只有**一层**防护，就是下面这张白名单（2026-10-03 审计订正）
 *
 * 原先这里还有一道 `if (INTERNAL_KEY_RE.test(raw)) return ''` 的兜底，
 * 注释写「万一有人把内部键塞进 source 字段，也别让它漏到界面上」。
 * **那道兜底永远不会触发**，删掉它 6 条单测照样全绿（负控实测，见
 * __tests__/sourceLabels.test.mjs 里「负控」那段）。原因是纯结构性的：
 *
 *   1. 能走到兜底的前提是 `NOTIFICATION_SOURCE_KEYS[raw]` 取到了值；
 *   2. 整张表的键是 `email` / `rss` / `task` —— **一个都不含点**；
 *   3. 而 `INTERNAL_KEY_RE` 要求至少一个点。
 *
 *   ⇒ 含点的 raw 早在白名单查表那一步就 return '' 了，兜底是死代码。
 *
 * 「含点的 source 会被拦住」这件事**本来就已经由白名单保证了**，
 * 兜底没有提供任何额外保护，却让读者以为有两层。
 *
 * 那「万一有人往白名单里加一个带点的键」怎么办？不在运行时加正则，
 * 而是在测试里钉死这条不变量（白名单里不许出现带点的键）——
 * 违规会在 CI 上响亮报红，而不是在真机上静默地把一行副标题变空。
 */
/** 已知来源 → 已有 i18n key。不在表里的一律视为未知。 */
export const NOTIFICATION_SOURCE_KEYS: Record<string, string> = {
  email: 'messagesHub.filter.email',
  rss: 'messagesHub.filter.rss',
  task: 'messagesHub.filter.task',
}

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
  // 只有白名单里的来源才出文案。含点的内部键（`email.important`）
  // 同样落在这里被拦下——不需要第二道正则，见文件头「只有一层防护」。
  if (!key) return ''
  return t(key)
}
