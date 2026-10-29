/**
 * next-event-honesty.test.ts — §229.1：`next_meeting` → 日程链的**五种结局**必须分得开。
 *
 * ── 这道门守的缺口 ──
 *
 * `ensureNextMeetingEvent` 此前只返回 `number`（0/1），而 **0 同时是四种
 * 完全不同的结果**：
 *
 *   ① `next_meeting` 为空   → 会议里确实没定下次，说空话**是对的**；
 *   ② 有原文但解不出时刻    → **用户说了时间点，是我们没听懂**；
 *   ③ 查重命中            → 日程**已经在日历里**（这是成功）；
 *   ④ feed/create 抛异常   → **我们没建成**（离线 / 日历未配置 / 被拒）。
 *
 * 而 `buildIngestNotice` 只有 `eventsCreated > 0` 一句 ⇒ ②④ 一律被报成
 * 「什么也没发生」。这与 §66.7 给 `action_items[].due` 补 `unresolved`
 * 是**同一个洞**：当时只补了待办那一侧，日历这一侧漏了。
 *
 * 打在用户需求「将一些时间点自动加入计划日程」上，且**不报任何错**。
 *
 * ── 为什么大部分是行为断言 ──
 *
 * `classifyNextMeeting` / `buildIngestNotice` 都是**零 I/O 的纯函数**，
 * 所以 ①②⑤ 可以真的调用并比对返回值/文案，而不是扫源码里有没有某个字
 * （§51「实现对 ≠ 被用上」；源码扫描分不清代码与注释里引述的同一句）。
 * 只有「I/O 那三种结局」与「值流」两处必须落到源码断言 —— 因为
 * `meeting-ingest.ts` import 了 localDB / calendarApi，node 里跑不起来。
 */

import assert from 'node:assert/strict'
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, it } from 'node:test'
import { classifyNextMeeting, buildNextMeetingEvent } from '../meeting-next-event.ts'
import { buildIngestNotice, type IngestNoticeInput } from '../meeting-ingest-notice.ts'

const HERE = path.dirname(fileURLToPath(import.meta.url))
const SRC = path.resolve(HERE, '..', '..', '..') // → frontend/src
const read = (rel: string) => fs.readFileSync(path.resolve(SRC, rel), 'utf8')

/** 2026-10-07（周三）10:00 本地。 */
const NOW = new Date(2026, 9, 7, 10, 0, 0).getTime()

/** 全部为零的基线（与 reminder-outcome-honesty.test.ts 同形状）。 */
const BASE: IngestNoticeInput = {
  todosCreated: 0, reminderPlanned: 0, reminders: 0, dueUnresolved: 0,
  eventsCreated: 0, eventUnresolved: 0, eventFailed: false, ingestFailed: false,
}

describe('§229.1 A · 纯判定：「没有」与「没听懂」必须是两个态', () => {
  it('① 空 next_meeting ⇒ none（沉默是对的）', () => {
    for (const blank of [null, undefined, '', '   ']) {
      assert.equal(classifyNextMeeting(blank as string | null, NOW), 'none',
        `空输入 ${JSON.stringify(blank)} 不该被判成「没听懂」—— 那是给用户一条假警报`)
    }
  })

  it('① 阳性对照：真网关原文能解析 ⇒ ready（不是 unresolved）', () => {
    // ⚠ 必须钉真网关实测出来的原文（§54 探针 glm-5.2 真实输出），不是我自拟的。
    assert.equal(classifyNextMeeting('下周三下午三点', NOW), 'ready')
    assert.equal(classifyNextMeeting('下周三下午三点客户评审会', NOW), 'ready')
  })

  it('② 有原文但解不出时刻 ⇒ unresolved（这是本节要守的那一格）', () => {
    // ⚠ 这三条**必须**仍然是 null —— 「不猜」是硬口径
    //   （meeting-next-event.test.ts:103 已经钉着），修 §229.1 不能顺手把它们解出来。
    for (const phrase of ['下周再对一次', '过完这个月', '尽快', '过两天']) {
      assert.equal(classifyNextMeeting(phrase, NOW), 'unresolved',
        `${phrase} 解不出时刻时应报「没听懂」，让用户知道有东西没被处理`)
      assert.equal(buildNextMeetingEvent({ title: 'X' }, phrase, NOW), null,
        `${phrase} 仍不该建日程 —— 「没听懂」是信号，不是「可以猜一个时间」`)
    }
  })

  it('负控：把 unresolved 当成 none 会让这条门转红（判据不是恒真）', () => {
    // ⚠ 「判据的绿只覆盖它量到的那件事」：断言两个不同的字符串落在
    //   **不同**的态上，才能排除「函数恒返回一个值」这种假货。
    const notEmpty = classifyNextMeeting('下次再聊', NOW)
    const empty = classifyNextMeeting('', NOW)
    assert.notEqual(notEmpty, empty, '「有内容但解不出」与「压根没有」被判成同一个态 ⇒ ④ 号缺口还在')
  })

  it('★ unresolved 与 buildNextMeetingEvent 的 null 必须同源（不得各判各的）', () => {
    // ⚠ 分类器和建日程器**各自**判一次「解不出」就会漂移：哪天有人在其中
    //   一边放宽了正则，另一边还返回 null ⇒ 状态说「没听懂」但其实建了日程。
    const samples = [
      '下周再对一次', '过完这个月', '那之前', '下次再聊', '尽快', '过两天', '年中', '',
      '下周三下午三点', '月底前', '季度末', '下个月底', '明天上午十点', '周五',
    ]
    for (const s of samples) {
      const gate = classifyNextMeeting(s, NOW)
      const built = buildNextMeetingEvent({ title: '周会' }, s, NOW) !== null
      if (gate === 'ready') assert.ok(built, `${s} 判成 ready 却建不出日程 —— 两边判据漂移了`)
      else assert.equal(built, false, `${s} 判成 ${gate} 却建出了日程 —— 两边判据漂移了`)
    }
  })
})

describe('§229.1 B · 行为文案：三种非零结局各有一句，互斥', () => {
  it('建成了 ⇒ 说「已加入日程」', () => {
    const s = buildIngestNotice({ ...BASE, eventsCreated: 1 })
    assert.match(s, /下次会议已加入日程/, s)
  })

  it('★ 没听懂 ⇒ 必须说出来（旧实现这里说空话）', () => {
    const s = buildIngestNotice({ ...BASE, eventUnresolved: 1 })
    assert.match(s, /没听清/, `「用户说了下次会议时间点但我们没听懂」必须说出来，得到：${s}`)
    assert.ok(!/已加入日程/.test(s), `解不出时刻却报「已加入日程」是撒谎：${s}`)
  })

  it('★ 没建成 ⇒ 必须说出来（旧实现这里说空话）', () => {
    const s = buildIngestNotice({ ...BASE, eventFailed: true })
    assert.match(s, /未能创建/, `日程建失败必须说出来，得到：${s}`)
    assert.ok(!/已加入日程/.test(s), `建失败却报「已加入日程」是撒谎：${s}`)
  })

  it('真的没有下次会议 ⇒ 仍然说空话（不能给「没定下次会议」也报一条）', () => {
    assert.equal(buildIngestNotice(BASE), '')
  })

  it('★ 互斥：三态不会同时说两句', () => {
    // ⚠ else if 写对了就只出一句；写成三个独立 if 会在将来某次改动后
    //   同时出现「已加入日程」和「未能创建」这种自相矛盾的文案。
    const both = buildIngestNotice({ ...BASE, eventsCreated: 1, eventFailed: true })
    assert.ok(!/未能创建/.test(both), `建成与失败同时说，逻辑互斥被破坏了：${both}`)
    const both2 = buildIngestNotice({ ...BASE, eventsCreated: 1, eventUnresolved: 1 })
    assert.ok(!/没听清/.test(both2), `建成与没听懂同时说，逻辑互斥被破坏了：${both2}`)
  })

  it('入库整体失败仍然覆盖一切（含新两格）', () => {
    const s = buildIngestNotice({
      todosCreated: 3, eventsCreated: 1, eventUnresolved: 1, eventFailed: true, ingestFailed: true,
    })
    assert.equal(s, '行动项未能入库')
  })

  it('待办侧的三格与日历侧的两格互不干扰（分句计数，各说各的）', () => {
    const s = buildIngestNotice({
      ...BASE, todosCreated: 2, reminderPlanned: 2, reminders: 1, dueUnresolved: 1, eventUnresolved: 1,
    })
    assert.match(s, /2 项行动已加入待办/, s)
    assert.match(s, /1 条日程提醒已建，1 条未建成/, s)
    assert.match(s, /1 条期限没听清/, s)
    assert.match(s, /下次会议的时间没听清/, s)
    assert.ok(!/待办与日程/.test(s), `不许把待办与日程捆在一起：${s}`)
  })
})

/**
 * 接线门：`ingestMeetingArtifacts` 真的把 I/O 的三种结局分出来。
 *
 * ⚠ 源码断言，能力有限（`meeting-ingest.ts` import localDB/calendarApi，
 *   node 里跑不起来）。它能抓住「状态被抹平成 0 / 异常被重新吞掉」，
 *   抓不住「跑起来真的对不对」—— 那部分要等真机（当前 §229 无实跑）。
 */
describe('§229.1 C · 接线（源码断言，局限见注）', () => {
  const ingest = read('features/meetings/meeting-ingest.ts')

  it('分类必须发生在**发请求之前**：none/unresolved 不该产生任何 I/O', () => {
    const fn = ingest.slice(ingest.indexOf('async function ensureNextMeetingEvent'))
    assert.ok(/const gate = classifyNextMeeting\(/.test(fn),
      'ensureNextMeetingEvent 没有走纯判定 classifyNextMeeting')
    const gateAt = fn.indexOf('const gate = classifyNextMeeting(')
    const feedAt = fn.indexOf('calendarApi.feed(')
    assert.ok(gateAt > 0 && feedAt > gateAt,
      '「没有/没听懂」必须在查重与建日程**之前**判掉，否则解不出时刻也照样打日历接口')
  })

  it('★ I/O 失败必须落成 failed，不得重新变成静默降级', () => {
    // ⚠ 这是 §229.1 那个洞的原样：旧写法是调用点 `catch {}`，eventsCreated
    //   停在 0 且不留痕迹。变异 S2 把 `return { state: 'failed' }` 换成
    //   `return { state: 'none' }`，下面这条必须转红。
    // ⚠ 2026-10-29：catch 允许**带参数**（生产侧是 `catch (error) {`），
    //   而且 catch 体里有别的语句（`if (isAbortError...) throw` 与
    //   `operation.assertLocal()`）⇒ `[^}]*` 会在第一个 `}` 处断掉，
    //   永远看不到后面的 `return { state: 'failed' }` ⇒ 这道门恒红。
    //   ⇒ 判据放宽成「catch 体里出现 failed 态」，但**必须仍是 failed**：
    //     把它换成 'none' 依然要转红（变异 S2 的牙不许掉）。
    assert.ok(/catch\s*(\([^)]*\))?\s*\{[\s\S]{0,400}?return\s*\{\s*state:\s*'failed'\s*\}/s.test(ingest),
      '日程建失败没有落成 state=\'failed\' ⇒ 离线时用户永远不知道自己要的日程没建成')
  })

  it('★ 查重命中落成 duplicate（成功态，不是失败态）', () => {
    assert.ok(/if \(isDuplicateNextMeetingEvent\([^)]*\)\) return \{ state: 'duplicate' \}/.test(ingest),
      '查重命中必须报 duplicate —— 它是「已经在日历里」的成功态，混进 failed 会让用户以为日程没建成')
  })

  it('★ 两个新计数真的从结局映射出来（不是常量 0）', () => {
    for (const [state, line] of [
      ['created', "eventsCreated = eventOutcome.state === 'created' ? 1 : 0"],
      ['unresolved', "eventUnresolved = eventOutcome.state === 'unresolved' ? 1 : 0"],
      ['failed', "eventFailed = eventOutcome.state === 'failed'"],
    ] as const) {
      assert.ok(ingest.includes(line),
        `ingest 里没有「${line}」⇒ state='${state}' 到计数这一跳断了`)
    }
  })

  it('★ 返回值带上两格（漏一个就永远是 undefined）', () => {
    assert.match(ingest, /eventsCreated,\s*eventUnresolved,\s*eventFailed,\s*cloudSynced/,
      'ingestMeetingArtifacts 的返回值没带 eventUnresolved/eventFailed')
  })
})
