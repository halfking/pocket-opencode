/**
 * relative-time.ts — 列表行的相对时间格式化（i18n 感知）。
 *
 * 为什么不直接用 toLocaleString()：
 *  - 列表里每行都渲染一次，Intl.DateTimeFormat 每次构造开销明显（会话列表实测
 *    一屏 20 行就是 20 次 formatter 构造），所以这里用 `cache` 复用。
 *  - `toLocaleDateString()` 输出「2026/10/3」这类长串，在 360dp 窄屏的行尾
 *    会把标题挤掉一行。列表场景要的是「3 分钟前」这种**宽度可控**的短标签。
 *
 * 为什么不用 `Intl.RelativeTimeFormat`：
 *  它的输出语法（"3 minutes ago" / "vor 3 Minuten"）由 locale 决定，我们无法保证
 *  它在 9 个语言包里都短到能放进行尾；而且它不认我们自有的 i18n key 体系——
 *  翻译走 vue-i18n 才能被 check:i18n 卡口覆盖。所以这里自己拼，翻译查 key。
 *
 * 7 天以上退回「月/日」：再长的相对时间（"43 天前"）信息量趋零，且宽度不可控。
 */

export type Translate = (key: string, named?: Record<string, unknown>) => string

const MINUTE = 60
const HOUR = 60 * MINUTE
const DAY = 24 * HOUR

/**
 * 毫秒/秒混合阈值。
 *
 * 与 `features/email/cleanup-filter.ts` 的 `emailDateToMs` 保持同一条规则
 * （`value < 1e12` 视为秒），免得仓库里出现两套量纲判据。
 * 1e12 秒 ≈ 公元 33658 年，1e12 毫秒 ≈ 2001 年——真实数据必然落在两侧，
 * 不会踩到边界。
 */
const MS_THRESHOLD = 1e12

/**
 * 把「可能是毫秒也可能是秒」的时间戳归一成 Unix **秒**。
 *
 * ## 为什么需要它
 *
 * 本地库的 *_at 列**单位不统一**，且这个不一致贯穿整个仓库：
 *   - LocalNote.updatedAt / LocalMeeting.startedAt / PkmNote.updatedAt
 *     ← 写入时用 `Date.now()`，**毫秒**
 *   - LocalEmail.date ← `emailDateToMs()`，**毫秒**
 *   - Notification.created_at ← `Math.floor(Date.now() / 1000)`，**秒**
 *   - RSS 的 publishedAt/fetchedAt 是 ISO 串，解析出来**毫秒**
 *
 * 只要有一个混进来，`formatRelative` 与「按时间倒序」就会一起错：
 * 毫秒被当秒时 diff 是大负数 → 永远显示「刚刚」；排序时毫秒项恒大于秒项 →
 * 「时间线」变成按来源分组的假时间线。**而且这两种错都不报错，只是看起来不对**，
 * 是最难被验收抓到的一类。
 *
 * 所以归一必须发生在**行构造处**（数据进组件的第一道），而不是让
 * formatRelative 去猜——通用工具猜量纲，出了错会静默影响所有调用方。
 */
export function toEpochSeconds(value: number): number {
  if (!Number.isFinite(value) || value <= 0) return 0
  return value < MS_THRESHOLD ? Math.floor(value) : Math.floor(value / 1000)
}

/**
 * @param ts       Unix 秒（与 localDB 的 *_at 列同单位）
 * @param t        vue-i18n 的 t
 * @param nowMs    注入当前毫秒，便于测试
 */
export function formatRelative(
  ts: number,
  t: Translate,
  nowMs: number = Date.now(),
): string {
  // 显式判有限性，而不是 `if (!ts)`。
  // `!ts` 恰好能挡住 0 和 NaN（两者都是 falsy），但**挡不住负数**：
  // -1 会一路走到 new Date(-1000)，渲染出一个看起来很正常的「1/1」，
  // 让缺失/损坏的时间戳伪装成一条真实记录。
  // 导入路径上的 createdAt 确实见过 0 与异常值，所以这里不赌 truthy。
  if (!Number.isFinite(ts) || ts <= 0) return ''
  const diffSec = Math.floor(nowMs / 1000) - ts
  // 未来时间（服务器时钟超前 / 定时任务的 nextDueAt）不显示「-3 分钟前」，
  // 按「刚刚」处理——负数的相对时间对用户没有意义。
  if (diffSec < MINUTE) return t('timefmt.justNow')
  if (diffSec < HOUR) return t('timefmt.minutesAgo', { count: Math.floor(diffSec / MINUTE) })
  if (diffSec < DAY) return t('timefmt.hoursAgo', { count: Math.floor(diffSec / HOUR) })
  if (diffSec < 7 * DAY) return t('timefmt.daysAgo', { count: Math.floor(diffSec / DAY) })
  const d = new Date(ts * 1000)
  return `${d.getMonth() + 1}/${d.getDate()}`
}

/** 秒 → 「M:SS」/「H:MM:SS」，用于会议时长。0 / 负数 / 非有限值返回空串。 */
export function formatDuration(ms: number): string {
  if (!Number.isFinite(ms) || ms <= 0) return ''
  const total = Math.round(ms / 1000)
  const h = Math.floor(total / 3600)
  const m = Math.floor((total % 3600) / 60)
  const s = total % 60
  const pad = (n: number) => String(n).padStart(2, '0')
  return h > 0 ? `${h}:${pad(m)}:${pad(s)}` : `${m}:${pad(s)}`
}
