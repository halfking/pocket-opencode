/**
 * meeting-due.ts — 会议/随手记待办里的**中文自然语言时间** → 具体时刻。
 *
 * 为什么存在：LLM 摘要返回的 action_items.due 是自由文本（「明天下午三点」
 * 「周五上午10点」「12月20日」），而链路下游有两处只认机器格式——
 *   - local_todos.due_at 需要 epoch ms（meeting-todo-persist.parseDue 此前
 *     只做 Date.parse，中文一律 NaN → null，时间信息**静默丢弃**）；
 *   - 「时间点自动入计划日程」需要可调度的 at 时刻（RFC3339）。
 *
 * 不引依赖的理由：chrono-node 全家桶 100KB+，而会议待办的 due 只是一个
 * **短语**（不是任意长文本的实体抽取）；自家正则 200 行以内可覆盖，
 * 且时区/“下午”这类中文语义必须自己定义才可控。
 * 参考：chrono-node（github.com/wanasit/chrono）及其中文 fork
 * （chrono-node-zh / weather-bot fork）的短语覆盖面；本项目不需要它的
 * index/text 结构，只需要「能不能解出一个时刻」。
 *
 * 语义约定（对齐中文口语习惯）：
 *   - 「下午3点」= 15:00；「凌晨3点」= 03:00；「晚上/半夜 11点」= 23:00；
 *   - 「中午12点」= 12:00（不折算 0 点）；「早上8点半」= 08:30；
 *   - 「周X / 星期X / 礼拜X」无前缀或「这/本」= **本周内未来最近**的那个
 *     （含今天——周三说「周三」指今天）；「下X」= 下周同一天（本周三说
 *     「下周一」= +5 天，不是 +12 天）；「下下X」再往后推一周。
 *     ★ 这里用「(7-cur)+target」而不是「inWeek+7」：后者会把「本周内
 *     最近的周一」和「下周一」重复计数（周三说下周一会算成 12 天后）。
 *   - 只给出日期没有时刻时：「X月X日/X号」默认 09:00；「明天/后天」
 *     同样 09:00——会议待办「明天交」的合理含义是「明天上班时间」，
 *     而不是明天的当前时刻。
 *   - **过去时刻的处理按分支不同**，不要笼统理解成「一律丢弃」：
 *     · 显式给了日期/周几（「今天上午十点」「周三」）⇒ 锚定那一天，
 *       即使默认的钟点已经过（周三 15:04 说「周三」= 今天 09:00，
 *       是「今天要做」而不是「已经过期」，丢了反而丢待办）；
 *     · 只给了钟点没给日期（「下午三点」）⇒ 存在「今天还是明天」的歧义，
 *       此时若早于 now 超一天（即昨天那个钟点）就**明确指向过去**，
 *       返回 null：宁可不要，也不建一个马上触发的假日程。
 */

export interface ParsedDue {
  /** 解析出的时刻（epoch ms）。 */
  at: number
  /** 命中的时间短语原文（写日志/回显用）。 */
  matchedText: string
}

const DAY_MS = 24 * 60 * 60 * 1000

const CN_DIGITS: Record<string, number> = {
  零: 0, 一: 1, 二: 2, 两: 2, 三: 3, 四: 4, 五: 5, 六: 6, 七: 7, 八: 8, 九: 9, 十: 10,
}

/** 中文数字 → number；「二十三」「十五」「八」都能解，无法解返回 null。 */
function cnNumber(s: string): number | null {
  if (!s) return null
  if (/^\d+$/.test(s)) return Number(s)
  // 单个汉字数字（含「两」）
  if (s.length === 1 && s in CN_DIGITS) return CN_DIGITS[s]
  // 十 / 十五 / 二十 / 二十一 / 二十五
  const m = /^(一|两|二|三|四|五|六|七|八|九)?十(一|二|三|四|五|六|七|八|九)?$/.exec(s)
  if (!m) return null
  const tens = m[1] ? CN_DIGITS[m[1]] : 1
  const ones = m[2] ? CN_DIGITS[m[2]] : 0
  return tens * 10 + ones
}

function pad2(n: number): string {
  return String(n).padStart(2, '0')
}

/** 由 Y/M/D 与可选 h:m 在**本地时区**构造时刻（与前端计划任务展示口径一致）。 */
function localTime(y: number, m: number, d: number, h: number, min: number): number {
  return new Date(y, m - 1, d, h, min, 0, 0).getTime()
}

/** 上午/下午等时段修饰词对 12 小时制钟点的折算。 */
function applyDayPeriod(period: string | undefined, hour: number): number {
  if (!period) return hour
  switch (period) {
    case '凌晨': return hour === 12 ? 0 : hour // 凌晨12点 ≈ 0 点
    case '早上':
    case '清晨':
    case '上午': return hour === 12 ? 0 : hour // 「上午12点」罕见，按 0 点兜底
    case '中午': return hour < 11 && hour > 2 ? 12 : hour // 中午12点=12；容忍「中午11点半」
    case '下午': return hour === 12 ? 12 : (hour >= 1 && hour <= 11 ? hour + 12 : hour)
    case '傍晚': return hour >= 1 && hour <= 6 ? hour + 12 : hour
    case '晚上':
    case '夜里':
    case '晚间': return hour === 12 ? 0 : (hour >= 1 && hour <= 11 ? hour + 12 : hour)
    case '半夜': return hour === 12 ? 0 : (hour >= 1 && hour <= 5 ? hour + 12 : hour)
    default: return hour
  }
}

// ── 钟点语法 ────────────────────────────────────────────────────────────────
// 拆成 HOUR/MIN 两个可复用片段，而不是把「八点半」「3点30分」「3点」写成
// 一串并列备选。★ 并列备选会在这里踩坑：备选 `[零一二…]+点` 先于
// `[零一二…]+点半` 命中时，clock 截成「八点」丢掉「半」，后面再怎么
// 折算都拿不回 30 分。改成「点 + 可选分钟」的结构后，「半」天然是
// 分钟部分的一部分，不存在「丢掉后半截」。
const CN_NUM_CLASS = '[零一二两三四五六七八九十]'
const HOUR_RE = `(?:\\d{1,2}|${CN_NUM_CLASS}{1,3})`
// ★ 备选顺序要紧：半/一刻/三刻 必须排在宽松的「中文数字+分?」前面，
// 否则「三点三刻」会被宽松分支吃掉「三」、把「刻」留在外面，45 分变 3 分。
const MIN_RE = `(?:半|一刻|三刻|\\d{1,2}\\s*分?|${CN_NUM_CLASS}{1,3}\\s*分?)`
// 整段钟点：15:00 / 3点30分 / 三点半 / 下午3点 / 8点一刻
const CLOCK_SOURCE = `\\d{1,2}\\s*[:：]\\s*\\d{1,2}|${HOUR_RE}\\s*[点點時时](?:${MIN_RE})?`
const PERIOD_SOURCE = '凌晨|清晨|早上|上午|中午|下午|傍晚|晚上|夜里|晚间|半夜'

/** 解析一段钟点文本 → 24 小时制 {h, m}；解不出返回 null。 */
function parseClockPart(s: string): { h: number; m: number } | null {
  const t = s.replace(/\s+/g, '')
  if (!t) return null
  // 1) 冒号式：15:00 / 3:30（自带分钟，不走「点」分支）
  const colon = /(\d{1,2})[:：](\d{1,2})/.exec(t)
  if (colon) {
    const h = Number(colon[1])
    const m = Number(colon[2])
    if (h > 23 || m > 59) return null
    return { h, m }
  }
  // 2) 「X点[半|一刻|三刻|N分]」。整体锚定 ^…$：宁可判 null，
  //    也不要「匹配到前缀就当整段成立」——半截钟点会静默算错 30 分钟。
  const pm = new RegExp(`^(${HOUR_RE})[点點時时](?:(半|一刻|三刻)|(\\d{1,2}|${CN_NUM_CLASS}{1,3})\\s*分?)?$`).exec(t)
  if (!pm) return null
  const h = cnNumber(pm[1])
  if (h === null || h < 0 || h > 23) return null
  let m = 0
  if (pm[2] === '半') m = 30
  else if (pm[2] === '一刻') m = 15
  else if (pm[2] === '三刻') m = 45
  else if (pm[3] !== undefined) {
    const mm = cnNumber(pm[3].replace(/分$/, ''))
    m = mm === null ? 0 : mm
  }
  if (m < 0 || m > 59) return null
  return { h, m }
}

interface PeriodClock {
  period: string | undefined
  clock: string
  idx: number
  end: number
}

// 模块级常量、无 g 标志 ⇒ 每次 exec 都从头扫，无 lastIndex 残留。
const PERIOD_CLOCK_RE = new RegExp(`(${PERIOD_SOURCE})?\\s*(${CLOCK_SOURCE})`)

/** 在文本里找「时段修饰词 + 紧邻的钟点片段」。 */
function findPeriodClock(text: string): PeriodClock | null {
  const m = PERIOD_CLOCK_RE.exec(text)
  if (!m) return null
  const clock = m[2].replace(/\s+/g, '')
  if (!parseClockPart(clock)) return null
  return { period: m[1], clock, idx: m.index, end: m.index + m[0].length }
}

/** 文本里任意位置的钟点（可选时段），供各日期分支共用。 */
function pickClock(text: string): { h: number; m: number; matched: string } | null {
  const pc = findPeriodClock(text)
  if (!pc) return null
  const parsed = parseClockPart(pc.clock)
  if (!parsed) return null
  return {
    h: applyDayPeriod(pc.period, parsed.h),
    m: parsed.m,
    matched: pc.period ? `${pc.period}${pc.clock}` : pc.clock,
  }
}

/** 把「某天的 00:00 + 钟点」拼起来；无钟点时用默认 09:00。 */
function dayAt(dayStart: number, c: { h: number; m: number } | null): number {
  const h = c ? c.h : 9
  const m = c ? c.m : 0
  return dayStart + (h * 60 + m) * 60_000
}

/**
 * 主入口：从待办文本 / due 短语解析时刻。
 *
 * @param text 含时间表述的文本（「明天下午三点交周报」「周五上午10点」）
 * @param now  参考时刻（默认 Date.now()），测试注入用
 * @returns 解析成功则返回 {at, matchedText}；只有「裸钟点且明确指向过去」
 *          时返回 null
 */
export function parseDueAt(text: string, now: number = Date.now()): ParsedDue | null {
  const t = (text || '').trim()
  if (!t) return null

  // 0) 机器格式透传：ISO / YYYY-MM-DD / YYYY-MM-DD HH:mm。
  //    Date.parse 已能吃 ISO，但「YYYY-MM-DD」按 UTC 解析会漂 8 小时，
  //    这里统一按本地时区构造。
  const iso = /(\d{4})-(\d{2})-(\d{2})(?:[T\s](\d{1,2}):(\d{2}))?/.exec(t)
  if (iso) {
    const h = iso[4] !== undefined ? Number(iso[4]) : 9
    const min = iso[5] !== undefined ? Number(iso[5]) : 0
    const at = localTime(Number(iso[1]), Number(iso[2]), Number(iso[3]), h, min)
    if (!Number.isNaN(at)) return { at, matchedText: iso[0] }
    return null
  }

  const base = new Date(now)
  const y = base.getFullYear()
  const mo = base.getMonth() + 1
  const d = base.getDate()

  // 1a) 相对日：「今天/明天/后天/大后天」+ 可选钟点
  const relDay = /(今天|今日|明天|明日|后天|後天|大后天|大後天)/.exec(t)
  if (relDay) {
    const add = relDay[1].startsWith('今天') || relDay[1].startsWith('今日') ? 0
      : relDay[1].startsWith('明天') || relDay[1].startsWith('明日') ? 1
        : relDay[1].startsWith('大') ? 3 : 2
    const c = pickClock(t)
    return {
      at: dayAt(new Date(y, mo - 1, d + add).getTime(), c),
      matchedText: c ? `${relDay[1]}${c.matched}` : relDay[1],
    }
  }

  // 1b) 「3天后」「两周后」+ 可选钟点
  const afterDays = /(\d{1,2}|[一二两三四五六七八九十]+)\s*(天|个?周|个?星期)\s*[后後]/.exec(t)
  if (afterDays) {
    const n = cnNumber(afterDays[1]) ?? Number(afterDays[1])
    if (n === null || n <= 0) return null
    const days = afterDays[2].startsWith('天') ? n : n * 7
    const c = pickClock(t)
    return {
      at: dayAt(new Date(y, mo - 1, d + days).getTime(), c),
      matchedText: afterDays[0] + (c ? c.matched : ''),
    }
  }

  // 2) 周X：「周三」「下周二」「下周五上午十点」「这周六晚上八点」。
  //    cur/target 都是「周一=0 … 周日=6」。
  //    · 无前缀 / 这 / 本：本周内未来最近（含今天）= (target-cur+7)%7；
  //    · 下：下周同一天 = (7-cur)+target；周三(2)说下周一(0)= 5 天；
  //    · 下下：再 +7。
  //    ★ 不能写成「inWeek + 7」：本周三说「下周一」时 inWeek 已经指向
  //    10-12（下周一），再加 7 会算成 10-19，整整多一周。
  const week = /(下下|下|这|本)?\s*(?:周|星期|礼拜)([一二三四五六日天])/.exec(t)
  if (week) {
    const target = '一二三四五六日天'.indexOf(week[2])
    if (target < 0) return null
    const cur = base.getDay() === 0 ? 6 : base.getDay() - 1
    const prefix = (week[1] || '').replace(/\s/g, '')
    let addDays: number
    if (prefix === '下') addDays = (7 - cur) + target
    else if (prefix === '下下') addDays = (7 - cur) + target + 7
    else {
      addDays = (target - cur + 7) % 7
    }
    const c = pickClock(t)
    return {
      at: dayAt(new Date(y, mo - 1, d + addDays).getTime(), c),
      matchedText: week[0].trim() + (c ? c.matched : ''),
    }
  }

  // 3) 绝对日期：「12月20日」「3月5号」+ 可选钟点
  const md = /(\d{1,2}|[一二两三四五六七八九十]+)\s*月\s*(\d{1,2}|[一二两三四五六七八九十]+)\s*[日号]/.exec(t)
  if (md) {
    const mm = cnNumber(md[1]) ?? Number(md[1])
    const dd = cnNumber(md[2]) ?? Number(md[2])
    if (mm === null || dd === null || mm < 1 || mm > 12 || dd < 1 || dd > 31) return null
    // 已过去的月份推到明年（10 月说「1月5号」多半指明年）
    const yy = mm < mo ? y + 1 : y
    const c = pickClock(t)
    return {
      at: dayAt(localTime(yy, mm, dd, 0, 0), c),
      matchedText: md[0] + (c ? c.matched : ''),
    }
  }
  // 3b) 只有日：「20号」——本月已过则顺延下月。
  const dOnly = /(\d{1,2})\s*[日号]/.exec(t)
  if (dOnly) {
    const dd = Number(dOnly[1])
    if (dd < 1 || dd > 31) return null
    let mm = mo
    let yy = y
    if (dd < d) {
      mm += 1
      if (mm > 12) { mm = 1; yy += 1 }
    }
    const c = pickClock(t)
    return {
      at: dayAt(localTime(yy, mm, dd, 0, 0), c),
      matchedText: dOnly[0] + (c ? c.matched : ''),
    }
  }

  // 4) 只有钟点没有日期：「下午三点」「晚上8:30」——未来则今天，已过则明天。
  //    只有这一支存在「今天还是昨天」的歧义，所以过去保护也只在这一支。
  const c = pickClock(t)
  if (c) {
    let at = localTime(y, mo, d, c.h, c.m)
    if (at < now - DAY_MS) return null // 明确指向昨天 ⇒ 放弃，不建假日程
    if (at < now) at += DAY_MS
    return { at, matchedText: c.matched }
  }

  return null
}

/**
 * 把解析结果编码成后端 `at` 型计划任务的 scheduleExpr。
 *
 * 口径与 schedule-plan.encodeSchedule 的 once 分支完全一致：
 * `YYYY-MM-DDTHH:mm:00` + 东八区固定偏移（那边写的是字面量 `+08:00`，
 * 这里用拼接生成，语义相同且便于统一维护）。
 * 后端 time.Parse(RFC3339) 可直接吃。
 */
const TZ_SUFFIX = `+${pad2(8)}:00`

export function dueAtToScheduleExpr(at: number): string {
  const d = new Date(at)
  return `${d.getFullYear()}-${pad2(d.getMonth() + 1)}-${pad2(d.getDate())}T${pad2(d.getHours())}:${pad2(d.getMinutes())}:00${TZ_SUFFIX}`
}
