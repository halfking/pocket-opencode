// refine-plan.test.ts — §52：`planRefine` 的**行为**门。
//
// 收尾链的决策部分此前全在 `finalizeRecording` 函数体里，而那个函数依赖
// localDB + HTTP、在 node 里跑不到 ⇒ 这些决策**一行行为门都没有**。
// 而 §45/§48/§49/§50 四轮的 bug **没有一个在 I/O 里**：
//   §45 名单恒空、§48 consumers 没人调、§49 分段恒空、§50 该转码没转。
// ⇒ 本文件把决策提成纯函数后逐条钉住。
//
// ★ 夹具用的是**生产形状**：
//   · speakerLabel 取自真网关 ASR 输出里真实出现的名字（§50）
//   · 占位标签形态取自 speaker-diarization.ts:57 的 `说话人 ${nextId}`
//   · 「整段重转只回 1 条回执」是 §50 对真 webm 的实测形态
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { planRefine } from '../meeting-refine-plan.ts'
import type { MeetingSegment } from '../meetings-store.ts'

const seg = (over: Partial<MeetingSegment> & { id: string; text: string }): MeetingSegment => ({
  meetingId: 'm1', speakerLabel: null, lang: 'zh', confidence: 0.9,
  startMs: 0, endMs: 1000, ...over,
} as MeetingSegment)

/** 真网关 ASR 的真实输出（2026-10-07 实测），含「林兰」这个真同音错。 */
const REAL_LINE = '下周三下午三点，跟王总还有林兰开个客户评审会。'

/** §50 实测形态：webm 上传 ⇒ 网关 400 ⇒ 整段重转失败。 */
const FULL_FAILED = { text: null, applied: false, segments: null }
/** 转码后：wav 可切 ⇒ 后端回 2 条逐段回执。 */
const FULL_OK = {
  text: REAL_LINE,
  applied: true,
  segments: [
    { index: 0, startSec: 0, endSec: 9, text: REAL_LINE },
    { index: 1, startSec: 9, endSec: 18, text: '要准备对比表。' },
  ],
}
/** ★ 只有 1 条回执 ⇒ 归因必然失效（§50 对真 webm 的实测就是这条形态）。 */
const FULL_ONE_RECEIPT = { text: REAL_LINE, applied: true, segments: [{ index: 0, startSec: 0, endSec: 9, text: REAL_LINE }] }

const LABELED = [
  seg({ id: 's1', speakerLabel: '张伟', text: REAL_LINE }),
  seg({ id: 's2', speakerLabel: '林岚', text: '要准备对比表。' }),
]

describe('planRefine · 整段重转失败时（真机 webm 的常态，§50）', () => {
  const plan = planRefine({ meetingId: 'm1', segments: LABELED, full: FULL_FAILED, participants: [], elapsedMs: 18000 })

  it('★ 不采纳、沿用原始分段（用户仍拿得到内容）', () => {
    assert.equal(plan.adopted, false)
    // 断言**内容**原样沿用，而不是引用相等。
    // 2026-10-06：baseSegments 现在会过一道 dedupeSegments（切片重叠产生的
    // 「今天今天下午三点」会顺着持久层读回的分段进 refine prompt，被 LLM
    // 当正常文本润色并固化）。dedupeSegments 返回新数组 ⇒ 引用必然不同。
    // 但本用例的 LABELED 两条本就无重叠 ⇒ 内容必须逐条一致。
    // 若哪天 baseSegments 的回落路径真的丢了内容，这条会红。
    assert.deepEqual(plan.baseSegments, LABELED, '整段重转失败时必须原样沿用持久层分段的内容')
  })

  it('★★ 名单仍能从分段说话人标签推出来（这条是最容易被漏掉的）', () => {
    // 走「压成一条」兜底时，baseSegments 的 speakerLabel 是 null。
    // 若名单只从 baseSegments 取 ⇒ 名单为空 ⇒ 同音纠正静默不跑（§45 的老坑）。
    assert.deepEqual(plan.roster, ['张伟', '林岚'], `实测 ${JSON.stringify(plan.roster)}`)
  })

  it('★ 同音错在交给精校前就被改掉', () => {
    assert.equal(plan.rosterFixes, 1, `应恰好 1 条修正，实测 ${plan.rosterFixes}`)
    assert.ok(plan.refineSegments[0].text.includes('林岚'), '「林兰」没被改')
  })
})

describe('planRefine · 转码后拿到 ≥2 条回执时（§50 修复的目标形态）', () => {
  const plan = planRefine({ meetingId: 'm1', segments: LABELED, full: FULL_OK, participants: [], elapsedMs: 18000 })

  it('★ 采纳整段文本，且说话人归属按时间挂回去', () => {
    assert.equal(plan.adopted, true)
    assert.deepEqual(plan.baseSegments.map((s) => s.speakerLabel), ['张伟', '林岚'],
      '说话人标签在归因后丢了 —— §40 修的就是这个')
  })

  it('★ 采纳后仍能拿到名单并纠正', () => {
    assert.deepEqual(plan.roster, ['张伟', '林岚'])
    assert.equal(plan.rosterFixes, 1)
  })

  it('★ 用户手填名单与说话人标签取并集', () => {
    const p = planRefine({
      meetingId: 'm1', segments: LABELED, full: FULL_OK,
      participants: ['王总', '张伟'], elapsedMs: 18000,
    })
    assert.deepEqual(p.roster, ['王总', '张伟', '林岚'], `实测 ${JSON.stringify(p.roster)}`)
  })
})

describe('planRefine · 只有 1 条回执时（归因必然失效的那条形态）', () => {
  const plan = planRefine({ meetingId: 'm1', segments: LABELED, full: FULL_ONE_RECEIPT, participants: [], elapsedMs: 18000 })

  it('★ 采纳但走「压成一条」兜底，标签随之丢失', () => {
    assert.equal(plan.adopted, true)
    assert.equal(plan.baseSegments.length, 1)
    assert.equal(plan.baseSegments[0].speakerLabel, null, '归因失效时应当是单条无标签分段')
  })

  it('★★ 即便如此，名单也不能跟着丢 —— 原始分段里的真名仍然可用', () => {
    // 这是本文件最关键的一条：baseSegments 丢标签 ≠ 名单该跟着丢。
    // 名单是**权威信息**，原始分段里还在。
    assert.deepEqual(plan.roster, ['张伟', '林岚'],
      `名单跟着 baseSegments 一起丢了 ⇒ 同音纠正静默不跑。实测 ${JSON.stringify(plan.roster)}`)
  })
})

describe('planRefine · 名单为空与质量拒收，都必须安全失效', () => {
  it('★ 只有占位标签 ⇒ 名单为空 ⇒ 一个字都不改', () => {
    const placeholders = [
      seg({ id: 's1', speakerLabel: '说话人 1', text: REAL_LINE }),
      seg({ id: 's2', speakerLabel: '说话人 2', text: '要准备对比表。' }),
    ]
    const plan = planRefine({ meetingId: 'm1', segments: placeholders, full: FULL_FAILED, elapsedMs: 18000 })
    assert.deepEqual(plan.roster, [])
    assert.equal(plan.rosterFixes, 0)
    assert.equal(plan.refineSegments[0].text, REAL_LINE, '没有权威名单却改动了文本')
  })

  it('★ 整段文本短一半以上 ⇒ 拒采纳（宁可保持原样也不要内容变少）', () => {
    const long = '跟王总还有林兰开个客户评审会，要准备对比表，另外还有预算确认的事，续期合同也要签。'
    const plan = planRefine({
      meetingId: 'm1',
      segments: [seg({ id: 's1', speakerLabel: '张伟', text: long })],
      full: { text: '太短了', applied: true, segments: FULL_OK.segments },
      elapsedMs: 18000,
    })
    assert.equal(plan.adopted, false, '短文本被采纳了 —— 用户会看到内容凭空变少')
    assert.ok(plan.adoptReason, '必须给出拒采纳的原因，否则排障时分不清是转写失败还是质量不够')
  })
})

// ─────────────────────────────────────────────────────────────────────────────
// §558：上面那条判据**声称**要保证「排障时分不清是转写失败还是质量不够」，
// 但它只断言了 `adoptReason` **非空** ⇒ 把五个 reason 全改成同一个非空串，
// 它照样绿。⇒ 本组补的是**可区分性**本身。
//
// 为什么这一条重要：`adoptReason` 目前**零消费者**（§557 实测），
// 所以它是**唯一**承载「五种处境各自发生了什么」的地方。
// 一旦这几个值不再可区分，没有任何判据会喊，而排障时会读到同一个串。
//
// ⚠ 这里钉的是**值**而不是「非空」——五个值的具体来源：
//   ok / not-applied / full-empty / full-too-short(N) / segments-empty
//   前两个来自 planRefine 自己的三元（meeting-refine-plan.ts:85），
//   后三个来自 shouldAdoptFullTranscript。
//   ⚠ §557 的探针直接调 shouldAdoptFullTranscript，**漏掉了 not-applied** ——
//   而那一条正是 §50 对真机 webm 的实测形态（网关 400，高精度那遍压根没跑）。
// ─────────────────────────────────────────────────────────────────────────────
describe('planRefine · 五种「整段重转」处境必须互相可区分（§558）', () => {
  const LONG_LINE = '跟王总还有林兰开个客户评审会，要准备对比表，另外还有预算确认的事，续期合同也要签。'
  const SEGS = [
    seg({ id: 's1', speakerLabel: '张伟', text: LONG_LINE }),
    seg({ id: 's2', speakerLabel: '林岚', text: '要准备对比表。' }),
  ]
  const REC = [
    { index: 0, startSec: 0, endSec: 9, text: LONG_LINE },
    { index: 1, startSec: 9, endSec: 18, text: '要准备对比表。' },
  ]
  const planFor = (full: unknown, segs = SEGS) =>
    planRefine({ meetingId: 'm1', segments: segs, full: full as never, elapsedMs: 18000 })

  it('★ 五个 reason 互不相同，且各自对应一个真实处境', () => {
    const got = {
      ok: planFor({ text: LONG_LINE + '要准备对比表。', applied: true, segments: REC }).adoptReason,
      // ⚠ §50 对真机 webm 的实测形态：网关 400 ⇒ applied=false ⇒ 高精度那遍压根没跑
      notApplied: planFor({ text: null, applied: false, segments: null }).adoptReason,
      fullEmpty: planFor({ text: '', applied: true, segments: [] }).adoptReason,
      tooShort: planFor({ text: '太短了', applied: true, segments: [{ index: 0, startSec: 0, endSec: 18, text: '太短了' }] }).adoptReason,
      segmentsEmpty: planFor({ text: LONG_LINE, applied: true, segments: REC }, []).adoptReason,
    }
    assert.equal(got.ok, 'ok', '正常采纳的 reason 变了')
    assert.equal(got.notApplied, 'not-applied', '网关失败的 reason 变了 —— 这是真机最常见的那一种')
    assert.equal(got.fullEmpty, 'full-empty', 'applied 但正文为空的 reason 变了')
    assert.match(got.tooShort, /^full-too-short\(/, `质量拒收的 reason 变了：${got.tooShort}`)
    assert.equal(got.segmentsEmpty, 'segments-empty', '实时分段全空的 reason 变了')

    const uniq = new Set(Object.values(got))
    assert.equal(
      uniq.size, 5,
      `五种处境只给出 ${uniq.size} 个不同的 reason —— 排障时分不出来了：${JSON.stringify(got)}`,
    )
  })

  it('★ 「网关失败」与「质量不够」必须给出不同的 reason（这正是原判据声称要保证的）', () => {
    const gwFail = planFor({ text: null, applied: false, segments: null }).adoptReason
    const quality = planFor({ text: '太短了', applied: true, segments: [{ index: 0, startSec: 0, endSec: 18, text: '太短了' }] }).adoptReason
    assert.notEqual(
      gwFail, quality,
      '「转写压根没跑」与「跑了但质量不够」撞成同一个 reason —— 用户与排障都会看到「精翻完成」而无从知道差在哪',
    )
  })
})
describe('planRefine · 喂给 LLM 的分段必须去重（2026-10-06）', () => {
  // ★ 第 6 个出口。finalizeRecording 的调用方（两条录音入口）都注明
  //   「读持久层而不是 recorder.runtime.segments」——那个数组由 DB 重建，
  //   **不经过** ingestSpeechBlob / recordingRuntime / useLiveSummary /
  //   api 降级 / MeetingDetailView 那五处已接上去重的出口。
  //   ⇒ 切片重叠产生的「今天今天下午三点」会顺着它进 refine prompt，
  //     被 LLM 当正常文本润色并固化。
  const OVERLAPPED = [
    seg({ id: 'o1', speakerLabel: '张伟', text: '今天下午三点开项目评审会' }),
    seg({ id: 'o2', speakerLabel: '张伟', text: '今天下午三点开项目评审，请准备材料' }),
  ]

  it('归因失败回落时也要去重（不得把重复喂给 refine）', () => {
    const plan = planRefine({
      meetingId: 'm1', segments: OVERLAPPED, full: FULL_FAILED,
      participants: [], elapsedMs: 16000,
    })
    const joined = plan.refineSegments.map((s) => s.text).join('')
    assert.ok(!/今天今天/.test(joined), `refine 输入仍有重复：${joined}`)
    assert.equal(plan.refineSegments[1].text, '，请准备材料',
      `第二段净增应只剩「，请准备材料」，实际 ${JSON.stringify(plan.refineSegments[1].text)}`)
  })

  it('归因成功时也要去重', () => {
    const plan = planRefine({
      meetingId: 'm1', segments: OVERLAPPED, participants: [], elapsedMs: 16000,
      full: {
        text: '今天下午三点开项目评审会，请准备材料。',
        applied: true,
        segments: [
          { index: 0, startSec: 0, endSec: 9, text: '今天下午三点开项目评审会，请准备材料。' },
        ],
      },
    })
    const joined = plan.refineSegments.map((s) => s.text).join('')
    assert.ok(!/评审会评审会/.test(joined), `归因成功后重叠未消解：${joined}`)
    assert.ok(!/今天今天/.test(joined), `归因成功后仍有重复：${joined}`)
  })

  it('负控：未去重的输入确实含重复（证明上面两条判据能区分）', () => {
    assert.ok(
      /今天下午三点开项目评审会今天下午三点开项目评审/.test(
        OVERLAPPED.map((s) => s.text).join(''),
      ),
      '负控样本本身必须含重复，否则判据恒真',
    )
  })
})
