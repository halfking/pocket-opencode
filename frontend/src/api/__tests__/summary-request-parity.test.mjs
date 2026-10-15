/**
 * summary-request-parity.test.mjs — §92 前端半边：**真跑一遍**，抓网关实际收到的请求体。
 *
 * ── 它守的是什么 ──
 *
 * 摘要端点的请求字段此前一根门都没有：
 *   - §66.4 只钉「两份摘要提示词不许漂」（提示词**内容**）
 *   - §85 / §90 只给**精校**端点钉了 meta 与 segments / target_langs
 *
 * 而 §89 已经证过这个形状一次：交接点上没门的字段，改个名字/删掉，两边都不报错，
 * 网关把值静默收成零值。而摘要链是「录音时即时总结」的主链路（用户原话第一条需求）。
 *
 * ── 为什么是**行为门**而不是源码 grep ──
 *
 * 源码里写着 `JSON.stringify({ segments, prev_summary, meta })`，
 * 只能证明「代码写在那」，证不了「**发出去的是那一份」**（§51「实现对 ≠ 被用上」）。
 * 本门真的调 `meetingsApi.summarize()` / `refine()`，抓 fetch 的请求体来断言。
 *
 * ⚠ 「抓请求体」必须用**同一个**判定函数做正反两面（A 组与 D 组共用），
 *   否则负控验的是「负控自己另写一遍的判断」，与 A 实际用的判据无关。
 *
 * ⚠ 抓的是 **JSON.parse 后的对象**，不是原始字符串 ——
 *   断言原始字符串等于在断言序列化格式，而格式不是这里要守的东西。
 *
 * ── 与 Go 半边的分工 ──
 *
 *   · 本文件（前端）：请求体**真的长这样**（值也对了）。
 *   · backend/internal/server/summary_request_wire_test.go（Go）：
 *     Go 的 json tag 与前端**发出的键**逐条对账（读前端源码，不抄清单）。
 *   两半合起来才闭环；少任何一半，另一半照样全绿。
 */
import assert from 'node:assert/strict'
import path from 'node:path'
import { afterEach, describe, it } from 'node:test'

import {
  FRONTEND_ROOT, activatePinia, assertHit, captureRequests, loadBundle, restoreGlobals, stubBrowser,
} from './_wire-helpers.mjs'

const MEETINGS_API = path.join(FRONTEND_ROOT, 'src', 'api', 'meetings.ts')
const SUMMARY_PATH = '/api/meetings/m1/summary'
const REFINE_PATH = '/api/meetings/m1/refine'

afterEach(restoreGlobals)

const SEGMENTS = [{
  id: 's1', meetingId: 'm1', speakerLabel: '张伟', lang: 'zh',
  text: '下周三下午三点跟王总开客户评审会。',
  startMs: 1200, endMs: 18000,
}]

const META = {
  title: '悬界芯片客户评审会',
  participants: ['张伟', '林岚'],
  location: '深圳办公室',
}

// 两个端点都必须成功，否则会走回落链，请求体里就混入 /api/llm/chat 的噪声。
const OK_ROUTES = {
  [SUMMARY_PATH]: { summary: '对齐了评审会时间', key_points: ['评审会定在下周三'] },
  [REFINE_PATH]: { refined_transcript: '[张伟] 下周三下午三点跟王总开客户评审会。' },
}

/**
 * A/D 两组共用的判据。**只判请求体**，不判返回值。
 *
 * @param what   用于报错信息
 * @param body   实际抓到的请求体
 * @param top    期望的顶层键（顺序即断言的顺序）
 * @param metaOk 是否要求 meta 三个字段都带值
 */
function assertMeetingRequest(what, body, top, metaOk = true) {
  assert.ok(body && typeof body === 'object', `${what}：请求体不是一个对象`)

  // ① 顶层键**逐条**列出，而不是 Object.keys 排序后比数组 ——
  //    少一个键、多一个键、键改名，三种都在这条里报出来。
  assert.deepEqual(Object.keys(body).sort(), [...top].sort(),
    `${what}：顶层字段与约定不符（实际 ${JSON.stringify(Object.keys(body))}）`)

  // ② segments：五个字段，且**值**要真的对。
  //    ⚠ 只断言键在是「形态对」；§84 教训是数据流缺陷长得和形态一样 ——
  //      start_ms 传成 undefined 时键仍然在，网关照样收成 0。
  assert.equal(body.segments.length, 1, `${what}：segments 长度不对`)
  const seg = body.segments[0]
  assert.deepEqual(
    Object.keys(seg).sort(),
    ['end_ms', 'lang', 'speaker', 'start_ms', 'text'],
    `${what}：segment 字段与约定不符（实际 ${JSON.stringify(Object.keys(seg))}）`,
  )
  assert.equal(seg.speaker, '张伟', `${what}：speaker 没带上说话人标签`)
  assert.equal(seg.text, SEGMENTS[0].text, `${what}：text 与传入的不一致`)
  assert.equal(seg.lang, 'zh', `${what}：lang 没传`)
  assert.equal(seg.start_ms, 1200, `${what}：start_ms 不是毫秒数（形变/丢字段都会变成 0 或 undefined）`)
  assert.equal(seg.end_ms, 18000, `${what}：end_ms 不是毫秒数`)

  // ③ meta 三个字段，且**值**要真的对（术语表靠它，§64/§80/§81 实测 12/12）。
  if (metaOk) {
    assert.ok(body.meta && typeof body.meta === 'object', `${what}：meta 没有发出去`)
    assert.deepEqual(Object.keys(body.meta).sort(), ['location', 'participants', 'title'],
      `${what}：meta 字段与约定不符（实际 ${JSON.stringify(Object.keys(body.meta))}）`)
    assert.equal(body.meta.title, META.title, `${what}：meta.title 的值不对 ⇒ 术语表会静默失效`)
    assert.deepEqual(body.meta.participants, META.participants, `${what}：meta.participants 的值不对`)
    assert.equal(body.meta.location, META.location, `${what}：meta.location 的值不对`)
  }
}

describe('§92：会议两个端点的请求字段（真跑一遍抓请求体）', () => {
  it('A. summarize() 发出去的请求体，字段与值都对', async () => {
    stubBrowser()
    await activatePinia()
    const seen = captureRequests(OK_ROUTES)
    const mod = await loadBundle(MEETINGS_API, 'meetings-api-sum')

    await mod.meetingsApi.summarize('m1', SEGMENTS, '上一轮已对齐接口联调进度。', META)

    const hit = assertHit(seen, SUMMARY_PATH)
    assertMeetingRequest('摘要端点', hit.body, ['segments', 'prev_summary', 'meta'])
    // prev_summary 是滚动摘要的**唯一**载体：丢了它每一轮都从零开始
    // （§66.2 真网关实测：第二轮起 action_items 归零、第三轮 summary 为空）。
    assert.equal(hit.body.prev_summary, '上一轮已对齐接口联调进度。',
      '摘要端点没带上已有摘要 ⇒ 滚动摘要退化成首轮')

    // ⚠ 不许顺手多打一个 /api/llm/chat：那就是回落，说明上面成功返回没生效，
    //   抓到的 body 会是回落链的噪声而不是真实链路。
    assert.equal(seen.filter((s) => s.path === '/api/llm/chat').length, 0,
      '走了回落链（多打了一次 /api/llm/chat）—— 说明 A 组验的不是主链路')
  })

  it('B. refine() 发出去的请求体同样对，且不与摘要端点串味', async () => {
    stubBrowser()
    await activatePinia()
    const seen = captureRequests(OK_ROUTES)
    const mod = await loadBundle(MEETINGS_API, 'meetings-api-ref')

    // ⚠ 先打一次摘要端点，好让下面「不串味」那条断言**有前提可查**。
    //   第一版只调 refine，然后去 seen 里找摘要请求 —— 根本不存在，
    //   `in undefined` 当场抛出来。断言的形状是对的，前提没建立。
    await mod.meetingsApi.summarize('m1', SEGMENTS, undefined, META)
    await mod.meetingsApi.refine('m1', SEGMENTS, ['en', 'ja'], META)

    const hit = assertHit(seen, REFINE_PATH)
    assertMeetingRequest('精校端点', hit.body, ['segments', 'target_langs', 'meta'])
    assert.deepEqual(hit.body.target_langs, ['en', 'ja'],
      'target_langs 不是原样传下去 ⇒ 翻译目标语言这条需求整条断掉')

    // ★ 两个 body 必须不同：它们字段几乎一样，最容易在复制粘贴里串味，
    //   而串味之后两条断言会同时绿。
    const sum = assertHit(seen, SUMMARY_PATH).body
    assert.equal('target_langs' in sum, false, '摘要端点混进了 target_langs')
    assert.equal('prev_summary' in hit.body, false, '精校端点混进了 prev_summary')
  })

  it('C. meta 省略时也不许发成字符串 "undefined"', async () => {
    stubBrowser()
    await activatePinia()
    const seen = captureRequests(OK_ROUTES)
    const mod = await loadBundle(MEETINGS_API, 'meetings-api-nometa')

    await mod.meetingsApi.summarize('m1', SEGMENTS)

    const hit = assertHit(seen, SUMMARY_PATH)
    assert.equal(typeof hit.body.prev_summary, 'undefined', '省略 prevSummary 不该发出去')
    // JSON.stringify 会直接**丢掉** undefined 值 ⇒ meta 整个键消失，
    // 这是正确行为（Go 侧零值）。这条守的是「别有人改成字符串拼接」。
    assert.equal('meta' in hit.body, false, '未传 meta 时不该出现 meta 键')
    assert.equal(typeof hit.body.segments[0].start_ms, 'number',
      'start_ms 不能是 undefined（JSON 序列化后会整个键消失）')
  })

  it('D. 负控：判据真的有牙（把违规请求体喂给 A 组同一个函数）', async () => {
    const TOP = ['segments', 'prev_summary', 'meta']

    // ① 顶层键改名：prev_summary → prevSummary
    assert.throws(() => assertMeetingRequest('负控', {
      segments: [{ speaker: '张伟', text: SEGMENTS[0].text, lang: 'zh', start_ms: 1200, end_ms: 18000 }],
      prevSummary: 'x', meta: META,
    }, TOP), /顶层字段与约定不符/)

    // ② 少一个顶层字段（meta 整个消失 —— 这正是 §91.3 那个缺口的「若上游补上传参」形态）
    assert.throws(() => assertMeetingRequest('负控', {
      segments: [{ speaker: '张伟', text: SEGMENTS[0].text, lang: 'zh', start_ms: 1200, end_ms: 18000 }],
      prev_summary: 'x',
    }, TOP), /顶层字段与约定不符/)

    // ③ ★ **键全在、值是 undefined**：这是最难抓的一类 ——
    //    形态完全正确，但网关会按「键存在但无值」处理成零值。
    //    只断言键在的判据在这一条上会**全绿**。
    assert.throws(() => assertMeetingRequest('负控', {
      segments: [{ speaker: '张伟', text: SEGMENTS[0].text, lang: 'zh', start_ms: undefined, end_ms: 18000 }],
      prev_summary: 'x', meta: META,
    }, TOP), /start_ms 不是毫秒数/)

    // ④ meta 键在、值为空 ⇒ 术语表静默失效（§64 的原始缺陷形态）
    assert.throws(() => assertMeetingRequest('负控', {
      segments: [{ speaker: '张伟', text: SEGMENTS[0].text, lang: 'zh', start_ms: 1200, end_ms: 18000 }],
      prev_summary: 'x', meta: { title: '', participants: [], location: '' },
    }, TOP), /meta.title 的值不对/)

    // ⑤ segment 字段改名
    assert.throws(() => assertMeetingRequest('负控', {
      segments: [{ speaker: '张伟', text: SEGMENTS[0].text, language: 'zh', start_ms: 1200, end_ms: 18000 }],
      prev_summary: 'x', meta: META,
    }, TOP), /segment 字段与约定不符/)
  })
})