/**
 * meeting-next-event.test.ts — §56：`next_meeting` → 日程。
 *
 * ── 这道门守的缺口 ──
 *
 * §54.2 真网关实测：`structured_minutes.next_meeting` 前端**有人读**
 * （`api/meetings.ts:250`），却长期是 `null` —— 因为：
 *   ① 提示词**从未要求**填它，范例还是 `null`；
 *   ② 即便模型填了，**没有任何写入方**（§48 建的是「行动项 → 待办」通路，
 *      会议时间点该落 `calendarApi` 的日程，两回事）。
 *
 * 而「下周三下午三点开个客户评审会」正是用户需求里最直观的
 * 「时间点自动加入计划日程」—— 缺了它，这条需求只覆盖了一半。
 *
 * ── 为什么是行为门 ──
 *
 * `meeting-next-event.ts` 被写成**零 import 的纯模块**（连 `meeting-due-reminder`
 * 都不能 import，它会拖进 `scheduledTasksApi` 的 I/O）。
 * 代价换来的是：门禁能直接 import 它并真的调用，
 * 而不是退化成「源码里有没有这段字」（§51「实现对 ≠ 被用上」）。
 *
 * ⚠ 真网关实测的输入是**原话**，不自拟：
 *   next_meeting = 「下周三下午三点」（§54 探针 glm-5.2 真实输出）
 */

import assert from 'node:assert/strict'
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, it } from 'node:test'
import {
  buildNextMeetingEvent,
  dedupeWindowSeconds,
  isDuplicateNextMeetingEvent,
  nextMeetingDueText,
  NEXT_MEETING_DURATION_MS,
} from '../meeting-next-event.ts'

const HERE = path.dirname(fileURLToPath(import.meta.url))
const SRC = path.resolve(HERE, '..', '..', '..')  // → frontend/src
const read = (rel: string) => fs.readFileSync(path.resolve(SRC, rel), 'utf8')

/** 2026-10-07（周三）10:00 本地时间。 */
const NOW = new Date(2026, 9, 7, 10, 0, 0).getTime()

/** 真网关实测返回的 next_meeting 原文。 */
const REAL_NEXT_MEETING = '下周三下午三点'

describe('§56 next_meeting → 日程（纯函数）', () => {
  it('真网关原文能解析成一条时刻正确的日程', () => {
    const input = buildNextMeetingEvent(
      { title: '客户评审会筹备', location: '三楼会议室' },
      REAL_NEXT_MEETING,
      NOW,
    )
    assert.ok(input, '真实 next_meeting 解析失败 —— 门的前提失效')

    // ★ 单位：日历 API 用 unix **秒**（calendar/types.ts），解析器给的是毫秒。
    //   漏掉这一步，日程会落在 1970 年 —— 而接口照样返回 200。
    const wantMs = new Date(2026, 9, 14, 15, 0, 0).getTime()
    assert.equal(input.startAt, Math.floor(wantMs / 1000),
      `startAt 不是「下周三下午三点」：得到 ${new Date(input.startAt * 1000).toString()}`)

    // ⚠ 必须断**绝对值**（3600 秒），不能写 `=== NEXT_MEETING_DURATION_MS / 1000`。
    //   后者是恒真判据：算出来的差值与被比较的常量同源，
    //   变异 N4 把常量改成 0 时两边都是 0，判据照样绿。
    assert.equal(input.endAt - input.startAt, 3600,
      '日程时长必须是约定的 60 分钟（3600 秒）；endAt===startAt 会在月视图里画成零长度条目')
    assert.equal(NEXT_MEETING_DURATION_MS, 60 * 60 * 1000,
      '默认时长常量被改了 —— 改它之前先想清楚 60 分钟这个约定还要不要成立')
    assert.equal(input.allDay, false)
    assert.equal(input.timezone, 'Asia/Shanghai')
    assert.equal(input.visibility, 'private')
    assert.equal(input.location, '三楼会议室')
    assert.ok(input.title.includes('客户评审会筹备'), `标题丢了会议名：${input.title}`)

    // 时刻必须真的是「下周三」，不能是「今天 + 若干天」。
    // ⚠ NOW 取的是 2026-10-07，它**本身就是周三**（getDay()=3），
    //   所以「下周三」= 10/14 仍是周三 3 —— 不是 0。
    //   这个断言第一版写成 0，红了；量具（我）错了，产品是对的。
    const d = new Date(input.startAt * 1000)
    assert.equal(d.getDay(), 3, `「下周三」应是周三（3），实得 ${d.getDay()}`)
    assert.equal(d.getDate(), 14)
    assert.equal(d.getHours(), 15)
  })

  it('没有会议标题时也能建（不能因为 title 空就整个放弃）', () => {
    const input = buildNextMeetingEvent({ title: '', location: '' }, REAL_NEXT_MEETING, NOW)
    assert.ok(input, '标题为空就不建日程 —— 那是把可选字段当必填')
    assert.equal(input.title, '下次会议')
    assert.equal(input.location, undefined, '空 location 不该写成空串')
  })

  it('空 / null / 解析不出来 ⇒ 不建，**不猜**', () => {
    // ⚠ 两个函数的职责不同，混着断言会得到假红（第一版就踩了）：
    //   nextMeetingDueText 只做「取文本 + 判空」
    //   buildNextMeetingEvent 才负责「解析不出时刻 ⇒ 不建」
    for (const blank of [null, undefined, '', '   ']) {
      assert.equal(nextMeetingDueText(blank as string | null), null,
        `空输入 ${JSON.stringify(blank)} 不该产出时间文本`)
      assert.equal(buildNextMeetingEvent({ title: 'X' }, blank as string | null, NOW), null)
    }
    for (const unparseable of ['下次再聊', '很快', '待定']) {
      assert.equal(buildNextMeetingEvent({ title: 'X' }, unparseable, NOW), null,
        `${unparseable} 解析不出时刻就不该建日程 —— 宁可少一条，不要一条时刻错掉的`)
    }
  })

  it('查重：同标题 + 同起始时刻视为已建过', () => {
    const input = buildNextMeetingEvent({ title: '周会' }, REAL_NEXT_MEETING, NOW)!
    assert.equal(isDuplicateNextMeetingEvent([{ title: input.title, startAt: input.startAt }], input), true,
      '重复精校会建出第二条同名同时刻的日程，日历里删不掉')
    assert.equal(isDuplicateNextMeetingEvent([{ title: '别的事', startAt: input.startAt }], input), false)
    assert.equal(isDuplicateNextMeetingEvent([{ title: input.title, startAt: input.startAt + 60 }], input), false,
      '不同时刻不该被误判成重复')
    assert.equal(isDuplicateNextMeetingEvent([], input), false)

    const w = dedupeWindowSeconds(input)
    assert.ok(w.from < input.startAt && w.to > input.endAt, '查重窗口必须包住日程本体')
  })
})

/**
 * 接线门：`ingestMeetingArtifacts` 真的调用它，并把结果**上报**。
 *
 * ⚠ 这是**源码断言**，能力有限：它能抓住「调用被删/被改短」，
 *   抓不住「调用了但返回值被丢掉」。那部分靠变异脚本 N1/N2
 *   （真改磁盘源码后必须转红）兜。
 *   写成源码断言的原因是 `meeting-ingest.ts` import 了 localDB /
 *   notes-store / calendarApi，node 里跑不起真实的入库链路 ——
 *   这里不假装能做行为断言。
 */
describe('§56 接线（源码断言，局限见注）', () => {
  const ingest = read('features/meetings/meeting-ingest.ts')
  const finalize = read('features/meetings/meeting-recording-finalize.ts')

  it('ingest 真的建日程，并把条数返回给调用方', () => {
    assert.ok(/buildNextMeetingEvent\s*\(/.test(ingest),
      'ingestMeetingArtifacts 没有调用 buildNextMeetingEvent ⇒ next_meeting 仍然没有写入方')
    assert.ok(/calendarApi\.create\s*\(/.test(ingest), '没有真的建日程')
    assert.ok(/isDuplicateNextMeetingEvent\s*\(/.test(ingest),
      '没有查重 ⇒ 重复精校会在日历里堆出一串删不掉的同名日程')
    assert.ok(/eventsCreated/.test(ingest), '建了几条日程没有上报（§48 教训：建了要自报）')
  })

  it('收尾编排把 eventsCreated 透出来给用户看', () => {
    // ⚠ 必须钉**赋值**而不是这个名字。
    //   第一版写的是 `/eventsCreated/.test(finalize)`，变异 N11 把
    //   `outcome.eventsCreated = ingested.eventsCreated` 整行删掉后**仍然全绿**
    //   —— 因为类型声明 `eventsCreated: number` 与初值 `eventsCreated: 0`
    //   里都有这个词。这就是「只看变量名/形状」量不到值流。
    assert.ok(/outcome\.eventsCreated\s*=\s*ingested\.eventsCreated/.test(finalize),
      '收尾编排不把 eventsCreated 赋进 outcome ⇒ 建了日程用户也不知道')
    // ⚠ 2026-10-07（§66.7）：这条判据的锚点被迫改过一次。
    //   原来判的是「finalize 源码里出现『下次会议已加入日程』」——
    //   而 §66.7 把录后精校的提示文案搬进了纯模块 `meeting-ingest-notice.ts`
    //   （否则它无法被行为断言，只能扫源码），于是这句话**不在 finalize 里了**，
    //   判据变成假红。
    //
    //   与 Go 侧 `llm_prompt_schema_gate_test.go` 记的是同一件事：
    //   **抽取函数会打破按 anchor 取源码的门**。修法不是把字符串搬回去，
    //   而是把它换成更强的两条：文案在**纯模块**里（可行为断言）+
    //   finalize 真的**把 eventsCreated 传进去**（可接线断言）。
    assert.ok(/下次会议已加入日程/.test(read('features/meetings/meeting-ingest-notice.ts')),
      '建了日程却没有对应的用户可见反馈 —— 文案不在纯模块里，无法被行为断言')
    assert.ok(/buildIngestNotice\s*\(/.test(finalize),
      '收尾编排没有走纯文案模块 buildIngestNotice')
    assert.ok(/eventsCreated:\s*outcome\.eventsCreated/.test(finalize),
      'buildIngestNotice 没有收到 outcome.eventsCreated ⇒ 建了日程仍然不会说出来')
  })
})

/**
 * 负控：把单位换算去掉，必须转红。
 *
 * ⚠ 这条判据的形态是「算出来的值」，不是「代码里有没有某个字」——
 *   所以它能抓住 §56 注释里写的那个具体风险（毫秒当秒 ⇒ 落在 1970 年），
 *   而 grep `'1000'` 抓不住（那行除法完全可能是对的）。
 */
describe('负控', () => {
  it('去掉毫秒→秒换算后，startAt 判据必须转红', () => {
    const input = buildNextMeetingEvent({ title: '周会' }, REAL_NEXT_MEETING, NOW)!
    const mutated = { ...input, startAt: new Date(2026, 9, 14, 15, 0, 0).getTime() }
    const wantMs = new Date(2026, 9, 14, 15, 0, 0).getTime()
    // 基线：换算正确时通过
    assert.equal(input.startAt, Math.floor(wantMs / 1000))
    // 变异：漏了换算（毫秒直接当秒）时必须不通过
    assert.notEqual(mutated.startAt, Math.floor(wantMs / 1000),
      '漏掉单位换算后判据仍然放行 —— 本门量不到 §56 最容易犯的那个错')
  })
})
