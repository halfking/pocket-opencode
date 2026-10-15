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
 *
 * ⚠⚠ **§566 勘误，上面最后这一条永远不会发生。**
 *   「若早于 now 超一天」那条保护建在 `at < now - DAY_MS` 上（见 `parseDueAt` 末段），
 *   而 `at` 由 `localTime(y, mo, d, …)` 构造、`y/mo/d` 取自 `new Date(now)`
 *   ⇒ **`at` 恒为「今天」**，与 `now` 同日 ⇒ 该不等式**恒假** ⇒ 158 万次穷举下 count=0。
 *   ⇒ 真实行为是：**裸钟点永不返回 null，一律「未来则今天、已过则明天」**。
 *   ⇒ 所以这段注释承诺的「宁可不要，也不建假日程」在裸钟点上是**空头支票**；
 *     `#34` 若拍板改成归 null，正好要让这行条件变可达 ⇒ 别先删它。
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
const MIN_RE = `(?:[点點]?(?:半|一刻|三刻|\\d{1,2}\\s*分)|${CN_NUM_CLASS}{1,3}\\s*分?)`
// ★§540：分钟组前允许**一个**骑缝「点」（ASR 切片边界落在字中间时会吐出
//   「2点点半」「2点点20分」「10点点一刻」）。
//   ⚠ 但**只给「半/一刻/三刻」和「数字+分」这两支**加：
//   若给「纯数字」也加，「2点3点」会被吃成「2点3」⇒ 静默变成 **02:03**。
//   而「点3点」这种相邻双钟点在真实文本里虽少见，代价却是「凭空错 3 分钟」。
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
  //
  // ★§540：分钟组前允许**一个**骑缝「点」。切片边界落在字中间时 ASR 会吐出
  //   「2点点半」「2点点20分」，此前它们被这条全锚定判 null
  //   ⇒ 调用方退化成只认整点（「2点」）⇒ **静默少 30 分钟**。
  //   只放宽一格：两个以上骑缝点仍然判 null，全锚定的原意不动。
  const pm = new RegExp(`^(${HOUR_RE})[点點時时](?:[点點]?(半|一刻|三刻)|[点點]?(\\d{1,2}|${CN_NUM_CLASS}{1,3})\\s*分?)?$`).exec(t)
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
//
// ★§540：时段词与钟点之间允许**一个**骑缝「点」。切片边界落在字中间时
//   ASR 会吐出「下午点两点半」；此前中间只允许空白（`\s*`），
//   可选时段组匹配不上 ⇒ 正则改从「两点半」起匹配 ⇒ **时段词被静默丢弃**
//   ⇒ `applyDayPeriod(undefined, 2)` = **02:30**（差整整 12 小时）。
//   只放宽一格；「下午3点」这类正常形态走的是 `[点點]?` 匹配空，仍取同一个钟点。
const PERIOD_CLOCK_RE = new RegExp(`(${PERIOD_SOURCE})?\\s*[点點]?\\s*(${CLOCK_SOURCE})`)

/**
 * 紧跟在钟点之后、形如「骑缝点… + 分钟」的残渣（见 §540）。
 *
 * ⚠ 必须允许**一个以上**的骑缝点：`MIN_RE` 只放行一个，但真实文本里
 *   连着两处骑缝完全可能（两次切片边界都落在同一句里）——
 *   「明天三点点点半」实测会让 CLOCK_SOURCE 只吃下「三点」，
 *   残渣是「点点半」。第一版守卫只认一个「点」⇒ 漏过它，
 *   于是「半」被静默丢掉（差 30 分钟），正是本文件要防的那种错。
 */
const SEAM_MINUTE_TAIL_RE = /^[点點時时\s]+(?:半|一刻|三刻|\d|[零一二两三四五六七八九十])/

/** 在文本里找「时段修饰词 + 紧邻的钟点片段」。 */
function findPeriodClock(text: string): PeriodClock | null {
  // ⚠ 用 g 标志逐个试，而不是 exec 一次就收：
  //   §540 只放行**一个**骑缝点，而骑缝多于一处时（下面 skip）这一处是坏的。
  //   若只看第一个匹配，后面的「四点开会」这类正常钟点会被连坐丢掉。
  const re = new RegExp(PERIOD_CLOCK_RE.source, 'g')
  for (let m = re.exec(text); m !== null; m = re.exec(text)) {
    const clock = m[2].replace(/\s+/g, '')
    if (!parseClockPart(clock)) continue
    const end = m.index + m[0].length
    // ★§540：钟点后面还紧跟着「点 + 分钟」⇒ 骑缝不止一处，
    //   而 CLOCK_SOURCE 只吃掉了整点 ⇒ 「半/分」会被**静默丢掉**（差 30 分钟）。
    //   本文件的立场是「宁可判 null，也不要半截钟点静默算错」⇒ 跳过这一处，
    //   继续在文本后面找下一个（不因为一处坏的就放弃整句）。
    if (SEAM_MINUTE_TAIL_RE.test(text.slice(end))) continue
    return { period: m[1], clock, idx: m.index, end }
  }
  return null
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
// ★ §356：前缀档位计数。
// 三族根因相同 —— 「下 / 下下 / 下下下…」的**档位**没被计数，只被识别了固定几档。
// 一处抽象 + 三处调用，避免同一份规则写成三份。
function countLead(text: string | undefined): number {
  return ((text || "").match(/下/g) || []).length
}

export function parseDueAt(text: string, now: number = Date.now()): ParsedDue | null {
  const t = (text || '').trim()
  if (!t) return null

  // 0) 机器格式透传：ISO / YYYY-MM-DD / YYYY-MM-DD HH:mm。
  //    Date.parse 已能吃 ISO，但「YYYY-MM-DD」按 UTC 解析会漂 8 小时，
  //    这里统一按本地时区构造。
  //
  // ⚠ §390：月/日必须是 **1–2 位**，不能要求补零。
  //   中文分支本来就能吃「10月9日」（单数位），而机器分支原先只认 `\d{2}` ——
  //   于是**同一个日期，机器格式要求补零、中文格式不要求**，
  //   `2026-10-9` 落到下面的「只有钟点」分支，日期被**静默丢弃**，
  //   答出「下一个 14:00」= **比正确日期早一天**（§390.2 实测）。
  const iso = /(\d{4})-(\d{1,2})-(\d{1,2})(?:[T\s](\d{1,2}):(\d{1,2}))?/.exec(t)
  if (iso) {
    const m1 = Number(iso[2])
    const d1 = Number(iso[3])
    if (m1 < 1 || m1 > 12 || d1 < 1 || d1 > 31) return null
    const h = iso[4] !== undefined ? Number(iso[4]) : 9
    const min = iso[5] !== undefined ? Number(iso[5]) : 0
    const at = localTime(Number(iso[1]), m1, d1, h, min)
    // ⚠ 逐月天数必须校验：`new Date(y, 1, 30)` 会静默溢出成 3-02，
    //   「2026-02-30」会答出一个完全错误的日子，且不报错。
    if (Number.isNaN(at) || new Date(at).getDate() !== d1) return null
    return { at, matchedText: iso[0] }
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
  // ★ §365：前缀一律吃「下/个」的整串，再交给 countLead 数档位 ——
  // 与月族（3d）、季/年族（3c）共用同一处抽象，不再手写「下下|下」两档。
  //
  // ⚠ 为什么不是 `(?:下个?)+`：它遇到「下下下个」会因末位「个」匹配不上而回退，
  //   从第二个「下」重新起 ⇒ 档位被少算一档（§354 在月族上踩过同一个坑）。
  //   吃整串 + 计数「下」的字数，两种写法都对。
  //
  // ⚠ 另一条：`这|本` 仍留在正则里，但它不参与计数（走裸周X那一支）。
  //   留着它是为了 `matchedText` 逐字不变 —— 去掉会让「这周一」的匹配串退化成「周一」。
  const week = /(?:(?<wlead>(?:下|个)+)|(?<wsame>[这本]))?\s*(?:周|星期|礼拜)(?<wwd>[一二三四五六日天])/.exec(t)
  if (week) {
    const gw = week.groups || {}
    const target = '一二三四五六日天'.indexOf(gw.wwd ?? '')
    if (target < 0) return null
    const cur = base.getDay() === 0 ? 6 : base.getDay() - 1
    const wLead = countLead(gw.wlead)
    let addDays: number
    if (wLead >= 1) addDays = (7 - cur) + target + 7 * (wLead - 1)
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
  // ★ §365：月表达式本身可以就是「第几个下」（`下个月15号` 里「月」前面没有数字），
  // 并补上**逐月天数校验**。
  //
  // ① 为什么加这一支：「下个月15号」原本根本进不了这一支 ⇒ 掉进 3b「只有日」分支
  //    ⇒ **答成本月 15 号**（差整整一个月，且不报错）。
  //    而「下个月十五号」更彻底：3b 只认 ASCII 数字 ⇒ 直接 null（漏报）。
  //    同一形态，阿拉伯数字给错值、中文数字给 null —— 两种不同的错。
  // ② 为什么补天数校验：原来只挡 `dd > 31`，而 `localTime(y, 9, 31, …)` 会被
  //    JS 静默归一成 10/1 ⇒「9月31日」实测得到 2027-10-01（还顺带滚了一年）。
  //    anchor 分支早就为月末天数踩过同一个坑，这里是它的月日版本。
  //
  // ⚠ 第一版把「下个」写成月的**修饰词**（独立前缀组），害得整支永远匹配不上；
  //   而对照组「12月20号」照样绿 ⇒ 是「对照仍绿」把那个错误结构放行的。
  const md = /(?<mmr>\d{1,2}|[一二两三四五六七八九十]+|(?:下个?)+)\s*月\s*(?<ddr>\d{1,2}|[一二两三四五六七八九十]+)\s*[日号]/.exec(t)
  if (md) {
    const gm = md.groups || {}
    const monthRaw = gm.mmr
    const dd = cnNumber(gm.ddr) ?? Number(gm.ddr)
    if (dd === null || Number.isNaN(dd) || dd < 1 || dd > 31) return null
    // 「下个」不是月的修饰词，它**就是**月的表达式 ⇒ 走 countLead 推档位。
    const mLead = countLead(monthRaw)
    let mmm: number
    let yy = y
    if (mLead > 0) {
      mmm = mo + mLead
      if (mmm > 12) { mmm -= 12; yy += 1 }
    } else {
      const mm = cnNumber(monthRaw) ?? Number(monthRaw)
      if (mm === null || Number.isNaN(mm) || mm < 1 || mm > 12) return null
      mmm = mm
      // 未带前缀且目标月已过 ⇒ 推到明年（10 月说「1 月底」多半指明年）。
      if (mm < mo) yy += 1
    }
    // ★ 逐月天数必须按**目标年月**算：11 月 31 天、2 月平年 28 天、闰年 29 天。
    if (dd > new Date(yy, mmm, 0).getDate()) return null
    const c = pickClock(t)
    return {
      at: dayAt(localTime(yy, mmm, dd, 0, 0), c),
      matchedText: md[0] + (c ? c.matched : ''),
    }
  }
  // 3d) ★ 2026-10-08 新增（§229.2）：「下个月底 / 下月初 / 下个月中」。
  //
  // 为什么必须排在下面的 anchor 分支**之前**，而不是并进去：
  // anchor 分支补上裸「月底/月初/月中」之后，正则从左往右扫到
  // 「下个月底」的**位置 2** 就会命中「月底」⇒ 答成**本月**底。
  // 而本文件自己的口径写着「宁可判 null，也不要匹配到前缀就当整段成立」
  // （见 parseClockPart 注释），`meeting-next-event.ts` 写着
  // 「建一条时刻错掉的日程比不建更糟」——
  // 把「下个月」答成「本月」正是那种错日程，比漏掉更坏。
  // ⇒ 必须在前一支把「下个」吃掉，再让 anchor 分支只看「本月」。
  const nextMonthAnchor = /((?:下个?)+)\s*月\s*(底|末|初|中)/.exec(t)
  if (nextMonthAnchor) {
    const lead = countLead(nextMonthAnchor[1])
    let mm = mo + lead
    let yy = y
    if (mm > 12) { mm -= 12; yy += 1 }
    const c2 = pickClock(t)
    // 月末天数必须按**目标月**算（下个月是 2 月时是 28 还是 29 天）。
    const dd = nextMonthAnchor[2] === '初' ? 1
      : nextMonthAnchor[2] === '中' ? 15
        : new Date(yy, mm, 0).getDate()
    return {
      at: dayAt(localTime(yy, mm, dd, 0, 0), c2),
      matchedText: nextMonthAnchor[0] + (c2 ? c2.matched : ''),
    }
  }

  // 3c) ★ 2026-10-07 新增：月度/季度锚点「十一月底」「11月初」「季末」「年底」。
  //
  // 为什么必须有：真网关实测（TestLiveGatewayRollingSummary，§37）
  // 里，**两个模型**对「续期合同要在十一月底之前签完」这一句
  // 都产出了 `due: "十一月底之前"` / `"十一月底前"` / `"11月底"` ——
  // 这是中文表达月度截止的**唯一自然说法**，模型不是乱填的。
  //
  // 而解析器原来只有 8 个分支（ISO / 相对日 / X天后 / 周X / 绝对日期 /
  // 只有日 / 只有钟点 / 冒号式），**没有这一类** ⇒
  // 硬期限解析不出来 ⇒ 提醒不建 ⇒ 用户的「时间点自动进日程」在这条上断掉，
  // 而且**不报错**：待办照样入库，只是没有 due_at。
  //
  // ⇒ 这是「合法但空」家族的又一次：不是失败，是静默地什么都没发生。
  // ⚠ ③ 组新增词时**必须**同步下面 `a3 ===` 的分支集合，否则新词会静默
  //   落进最后的 catch-all 按「月中」处理。判据
  //   `meeting-due-anchor-gap.test.ts` 用「正则枚举 == 分支枚举」钉住这一格。
  const anchor = /(?<qlead>(?:下个?)+)(?<qk>季末|季度末)|(?<ylead>(?:下个?)+)(?<yk>年底|年末)|(?<mon>\d{1,2}|[一二两三四五六七八九十]+)\s*月\s*(?<grain>底|末|初|中)|(?<anchorStr>季末|季度末|本季度末|年底|年末|本月底|本月末|月底|月末|本月初|月初|月中)/.exec(t)
  if (anchor) {
    const c = pickClock(t)
    // ⚠ anchor[3] 才是「季末/年底/…」那一组。写成 anchor[4] 时
    //   「十一月底」照样对（它走下面的月份分支、够不到这个索引），
    //   而「季末」「年底」「本月底」全部返回 Invalid Date ——
    //   典型「只测了主路径、边界全坏」。
    const gs = anchor.groups || {}
    const a3 = gs.qk || gs.yk || gs.anchorStr || anchor[3]
    //   一律走命名组：新增前缀捕获组会把位置索引整体右移，
    //   历史上正是这个坑让「十一月底」等形态返回 Invalid Date。
    const monRaw = gs.mon ?? anchor[1]
    const grain = gs.grain ?? anchor[2]
    if (a3) {
      // 季末 / 年底 / 本月底（无月份数字的那几种）
      let mm: number
      let dd: number
      // ★ §356：a3 已在上方按命名组统一取好（见该处注释）。
      let yy = y
      // ⚠ 季度末/年底的「最后一天」必须按**真实月末天数**算，不能写死 31：
      //   9 月与 6 月只有 30 天，`localTime(y, 9, 31, …)` 会被 JS 静默溢出成 10/1，
      //   实测 2026-09-30 说「季末」得到的是 10/1 —— 一个完全错误的日子。
      const lastDayOf = (yy2: number, mm2: number) => new Date(yy2, mm2, 0).getDate()
      // ★ §356：带前缀的档位（命名组）。裸串命中时两者都是 0 ⇒ 行为与原版逐字一致。
      const qLead = countLead(gs.qlead)
      const yLead = countLead(gs.ylead)
      if (a3 === '年底' || a3 === '年末') {
        if (localTime(yy, 12, lastDayOf(yy, 12), 0, 0) < base.getTime()) { yy += 1 }
        yy += yLead
        mm = 12; dd = lastDayOf(yy, 12)
      } else if (a3 === '季末' || a3 === '季度末' || a3 === '本季度末') {
        // 季末 = 当前季度的最后一天；已过则**滚一个季度**（滚成「明年同一天」是错的）。
        let qEnd = Math.ceil(mo / 3) * 3
        if (localTime(yy, qEnd, lastDayOf(yy, qEnd), 0, 0) < base.getTime()) {
          qEnd += 3
          if (qEnd > 12) { qEnd -= 12; yy += 1 }
        }
        qEnd += qLead * 3
        while (qEnd > 12) { qEnd -= 12; yy += 1 }
        mm = qEnd; dd = lastDayOf(yy, qEnd)
      } else if (a3 === '本月底' || a3 === '月底' || a3 === '本月末' || a3 === '月末') {
        // ⚠ 「本月底」明确指当月，**不滚年**：10 月 6 日说「本月初」
        //   已经过去，但滚到明年十月是荒谬的。
        //   裸「月底」与它同义 —— 口语里说「月底前」远比说「本月底前」常见，
        //   只认「本月底」就是让绝大多数月份表达落空（§229.2 实测）。
        // ★ §366：「月末」与「本月末」此前**漏报**（返回 null），
        //   而同族的「月底」「下个月末」都能解析 ——
        //   又一次「这条规则有几处调用点」只改了一部分。
        //   「月末」在商务口语里与「月底」完全同义（月末结算 / 月末汇报）。
        mm = mo; dd = new Date(y, mo, 0).getDate()
      } else if (a3 === '本月初' || a3 === '月初') {
        mm = mo; dd = 1
      } else if (a3 === '月中') {
        mm = mo; dd = 15
      } else {
        // ⚠§229.2：③ 组枚举到 '月中' 为止，所以正常走不到这里。
        //   **刻意不写 catch-all 默认值**：原来写成 `else { dd = 1 }` 的话，
        //   日后有人往正则里加了新词而忘了加分支，它会静默按「月初」算 ——
        //   没有任何门会红，而用户会拿到一条时刻错掉的日程。
        //   现在改成 fail-closed：返回 null ⇒ 走 §229.1 的「没听清」，
        //   那是用户能看见、能纠正的状态。
        return null
      }
      return { at: dayAt(localTime(yy, mm, dd, 0, 0), c), matchedText: anchor[0] + (c ? c.matched : '') }
    }
    const mm = cnNumber(monRaw) ?? Number(monRaw)
    if (mm === null || mm < 1 || mm > 12) return null
    // 目标月已过 ⇒ 推到明年（10 月说「1 月底」多半指明年）
    const yy = mm < mo ? y + 1 : y
    // 底/末 = 该月最后一天；初 = 1 号；中 = 15 号
    //
    // ⚠ 月末天数必须用**目标年**算：2 月在闰年是 29 天、平年是 28 天。
    //   用基准年算会让「明年 2 月底」在平年↔闰年之间差一天。
    const dd = grain === '初' ? 1 : grain === '中' ? 15 : new Date(yy, mm, 0).getDate()
    return {
      at: dayAt(localTime(yy, mm, dd, 0, 0), c),
      matchedText: anchor[0] + (c ? c.matched : ''),
    }
  }

  // 3b) 只有日：「20号」——本月已过则顺延下月。
  //
  // ⚠⚠ §524 实测到本分支的**误报**，就地留证（结论与修法见设计文档 §524.4，
  // 已登记为待拍板项，**本节不代拍口径**）：
  //
  //	「3号会议室」   → 2026-11-03 09:00   匹配「3号」
  //	「地点在3号会议室」 → 2026-11-03 09:00   匹配「3号」
  //	「5号楼」       → 2026-11-05 09:00
  //	「2号线」       → 2026-11-02 09:00
  //
  // ⇒ 正则不要求「号」后面是边界，**楼/室/线/车/院这些编号用法一律被答成日期**。
  // ⭐ 而这件事是 **ASR 的阿拉伯化喂出来的**：同一句写成中文数字的
  //　「三号会议室」→ null ✅ —— 因为本分支的正则只认 `\d{1,2}`，
  //　而 §520 实测「中文数字→阿拉伯数字」占全部 ASR 错误的 52%。
  //　⇒ **ASR 越准，这个假日程越容易被触发**（识别对了反而更糟）。
  //
  // ⛔ 方向相反的漏报同时存在（同一正则形态）：
  //　「二十号前提交」→ null、「三号前提交」→ null
  //　⇒ 真日期用法里**带中文数字的反而认不出**，误报与漏报并存。
  //
  // ⚠ 与本文件自己的两条口径直接冲突：
  //　① `meeting-next-event.ts`「建一条时刻错掉的日程比不建更糟」；
  //　② 上面 `:471` 注释「绝不能只拿钟点去答一个日期」。
  //　本分支正是在「只有一个阿拉伯数字 + 号」时**无条件**答一个日期。
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
    // ⭐ §390：**绝不能只拿钟点去答一个日期**。
    //   本文件自己的口径写着两句话：
    //     ·「宁可判 null，也不要匹配到前缀就当整段成立」
    //     ·（`meeting-next-event.ts`）「建一条时刻错掉的日程比不建更糟」
    //   而这一支原先只要文本里有钟点就返回 —— 于是「10/09 14:00」「2026-10-9 14:00」
    //   这类**认不出的日期**被**静默丢弃**，只剩钟点落到「下一个 14:00」
    //   ⇒ **答出比正确日期早一天的时间**。
    //   用户看到的是一个**看起来合理**的期限而不是空值 ⇒ 不会察觉 ⇒ 可能错过会议/交付。
    //
    //   修法按本文件既有哲学走 fail-closed：把钟点摘掉之后，
    //   **若还剩下日期样的碎片，就返回 null**（= 用户能看见、能纠正的「没听清」）。
    const rest = t.replace(c.matched, ' ')
    if (/\d{1,4}\s*[-/]\s*\d{1,2}|\d{1,2}\s*月\s*\d{1,2}\s*[日号]?/.test(rest)) return null
    let at = localTime(y, mo, d, c.h, c.m)
    // ⚠⚠ §566：**下面这行是死分支，而且文件头承诺的那条保护就建立在它上面。**
    //
    // 结构原因：`at` 由 `localTime(y, mo, d, …)` 构造，而 `y/mo/d` 取自
    // `new Date(now)`（见本函数 :256-259）⇒ **`at` 恒为「今天」**，`now` 也是今天
    // ⇒ `at − now ∈ (−24h, +24h)` 开区间 ⇒ `at < now − DAY_MS` **恒假**。
    //
    // 实测（§566.3）：158 万次穷举（366 天 × 每天 205 个基准时刻 × 21 个裸钟点）下，
    // 本行的 **`return null` 分支体 V8 覆盖率 count = 0**。
    //
    // ⚠️ **别把它当保护读**：文件头写着「若早于 now 超一天就明确指向过去，返回 null」，
    //   而这句在裸钟点这个输入类上**永远不会发生**。
    //   ⇒ 真实后果：`下午三点` 在 15:04 说出来 ⇒ **明天 15:00**（不是 null）
    //   ⇒ 调用方的 `unresolved` 计数**恒为 0** ⇒ 用户一句话都收不到（§565）。
    //
    // ⛔ **不删**（删死代码是范围口径，且 #34 若拍板「裸钟点归 null」
    //   正好要把这行的条件改成可达的 ⇒ 删了就白改）。
    //   ⇒ 正确处置：**留着、加注释、钉判据**（`meeting-due.test.ts` 末尾那条
    //     「裸钟点在一个整天里任何时刻都不会返回 null」），
    //     哪天真改了条件，那条判据会红并提示同步更新文件头与 §566。
    if (at < now - DAY_MS) return null // 明确指向昨天 ⇒ 放弃，不建假日程（⛔ §566：此分支不可达）
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
