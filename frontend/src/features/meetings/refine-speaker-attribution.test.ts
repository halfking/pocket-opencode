/**
 * refine-speaker-attribution.test.ts — 收尾精校**不能**把说话人标签压没。
 *
 * ── 这条护栏守的是一个静默的信息丢失 ──
 *   录音期间，每段都由本地声纹聚类（SpeakerDiarizer）标上说话人，用户还能
 *   亲手改名（labelSpeaker → updateSegmentSpeaker 落库）。但 stop() 走整段
 *   重转时，旧实现把整场文本压成**一条** `speakerLabel: null` 的分段再交给
 *   精校 ⇒ 所有说话人归属在停止那一刻一起消失，且：
 *     - 服务端提示词第 6 条要求「保留每行开头的 [说话人] 标记」，
 *       而它的输入整场会议只有一个 [说话人] ⇒ 这条要求恒真地满足；
 *     - 精校文本进笔记，用户读到的是一段无归属的长文。
 *
 *   而**恢复归属所需的数据本来就在响应里**：后端 stt/full.go 的
 *   `FullResult.Segments` 带 `startSec/endSec`，`/api/stt/transcribe-full`
 *   原样透出（server_stt_stream.go:98），前端 `SttFullResult.segments`
 *   早有类型 —— 只是旧注入签名把它结构化地丢在了接缝处。
 *
 * 判据一律落在**行为**上（挂完之后标签还在不在、文本是不是高精度那份），
 * 不扫字段名；同时保留 A 组负控，钉住「旧实现确实会丢」。
 */
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import {
  attributeFullTranscript,
  buildRefineBaseSegments,
  type FullSegmentReceipt,
} from './meeting-final-transcript.ts'
import { buildUtteranceSegment } from './meeting-utterance.ts'

const HERE = dirname(fileURLToPath(import.meta.url))
const read = (f: string) => readFileSync(join(HERE, f), 'utf8')

type Seg = {
  id: string
  meetingId: string
  speakerLabel: string | null
  lang: string
  confidence: number
  startMs: number
  endMs: number
  text: string
}

/** 实时分段的形态：VAD 切片 5~8 秒一条，标签来自声纹聚类（可能已被用户改名）。 */
const realtime = (): Seg[] => [
  { id: 's1', meetingId: 'm1', speakerLabel: '张伟', lang: 'zh', confidence: 0.9, startMs: 0, endMs: 6000, text: '这个季度的续费方案basically已经定了' },
  { id: 's2', meetingId: 'm1', speakerLabel: '李娜', lang: 'zh', confidence: 0.9, startMs: 6000, endMs: 13000, text: '定了吗我这边还有两个客户没签' },
  { id: 's3', meetingId: 'm1', speakerLabel: '张伟', lang: 'zh', confidence: 0.9, startMs: 13000, endMs: 21000, text: '没签的那两个走线下就是王总那边' },
  { id: 's4', meetingId: 'm1', speakerLabel: '李娜', lang: 'zh', confidence: 0.9, startMs: 21000, endMs: 28000, text: '评审会我准备一下对比表' },
]

/** 整段重转的形态：按静音切，每段带时间范围；这里是**纠过错**的那一份。 */
const fullReceipts = (): FullSegmentReceipt[] => [
  { index: 0, startSec: 0, endSec: 11.5, text: '这个季度的续费方案基本上已经定了' },
  { index: 1, startSec: 11.5, endSec: 22, text: '没签的那两个走线下，就是王总和林岚那边' },
  { index: 2, startSec: 22, endSec: 28, text: '评审会我准备一下对比表' },
]

/**
 * 复刻 api/meetings.ts 的 `toApiSegments`（精校请求体里 speaker 的真实口径）。
 * 按本项目一贯纪律，判据要**照抄消费者的真实公式**，不能照抄字段名。
 */
const toApiSegments = (segments: Seg[]) =>
  segments.map((s) => ({ speaker: s.speakerLabel ?? '说话人', text: s.text }))

/** 复刻服务端 segmentsToText 的行渲染（server_meeting.go:745）。 */
const serverPromptTranscript = (segments: Seg[]) =>
  toApiSegments(segments).map((s) => `[${s.speaker}] ${s.text}`).join('\n')

/** 旧实现：把整场压成一条无标签分段。 */
const legacyCollapse = (segs: Seg[], fullText: string, elapsedMs: number): Seg[] => [
  { id: 'm1-full', meetingId: 'm1', speakerLabel: null, lang: 'zh', confidence: 1, startMs: 0, endMs: elapsedMs, text: fullText },
]

describe('A · 负控：旧实现确实会静默丢说话人', () => {
  it('压成一条后，服务端提示词里只剩一个 [说话人]', () => {
    const segs = realtime()
    const full = '这个季度的续费方案基本上已经定了没签的那两个走线下就是王总和林岚那边评审会我准备一下对比表'
    const legacy = legacyCollapse(segs, full, 28000)
    const prompt = serverPromptTranscript(legacy)

    // 这是缺陷的可观测形状：**整场会议只有一行**。
    assert.equal(prompt.split('\n').length, 1, '旧实现把整场压成了一行')
    assert.match(prompt, /^\[说话人\]/, '且没有任何真实说话人归属')
    // 用户亲手改过的名字在旧实现里一个都不剩。
    for (const name of ['张伟', '李娜']) {
      assert.ok(!prompt.includes(name), `旧实现的提示词里不该出现 ${name}`)
    }
  })

  it('★ 同一份输入，新实现必须产出多个说话人（否则这条护栏是恒真的）', () => {
    const segs = realtime()
    const out = attributeFullTranscript(segs, fullReceipts())
    assert.ok(out, '有逐段回执 + 有标签时不该返回 null')
    const prompt = serverPromptTranscript(out)
    assert.ok(prompt.split('\n').length >= 2, '仍只有一行 ⇒ 新实现没起作用')
    assert.match(prompt, /\[张伟\]/)
    assert.match(prompt, /\[李娜\]/)
  })
})

describe('B · 行为：标签保留 + 文本换成高精度那份', () => {
  it('分段数与说话人归属与实时分段一致', () => {
    const segs = realtime()
    const out = attributeFullTranscript(segs, fullReceipts())
    assert.ok(out)
    assert.equal(out.length, segs.length, '分段数不该变')
    assert.deepEqual(out.map((s) => s.speakerLabel), ['张伟', '李娜', '张伟', '李娜'])
  })

  it('★ 正文取的是纠过错的高精度文本，不是原分段文本', () => {
    const out = attributeFullTranscript(realtime(), fullReceipts())
    assert.ok(out)
    const joined = out.map((s) => s.text).join('\n')
    // 原分段里那个同音字错误必须消失（它正是整段重转要修的东西）。
    assert.ok(!joined.includes('basically'), `高精度文本不该含原 ASR 错误：${joined}`)
    assert.ok(joined.includes('基本上已经定了'), `应含纠错后的文本：${joined}`)
  })

  it('★ 高精度文本不丢：逐段回执的正文全部出现，且只出现一次', () => {
    const out = attributeFullTranscript(realtime(), fullReceipts())
    assert.ok(out)
    const joined = out.map((s) => s.text).join('\n')
    for (const r of fullReceipts()) {
      const body = (r.text ?? '').trim()
      const hits = joined.split(body).length - 1
      assert.equal(hits, 1, `「${body}」应恰好出现 1 次，实际 ${hits} 次（重复=内容膨胀）`)
    }
  })

  it('每段正文挂到时间上：第 1 段拿到 0–11.5s 的回执，不越界拿第 2 段的', () => {
    const out = attributeFullTranscript(realtime(), fullReceipts())
    assert.ok(out)
    assert.equal(out[0].text, '这个季度的续费方案基本上已经定了')
    assert.ok(!out[0].text.includes('王总'), '第 1 段不该含第 2 条回执的正文')
  })

  it('回执乱序也能落回正确位置（按时间而非按数组下标归属）', () => {
    const out = attributeFullTranscript(realtime(), [
      { index: 2, startSec: 22, endSec: 28, text: '评审会我准备一下对比表' },
      { index: 0, startSec: 0, endSec: 11.5, text: '这个季度的续费方案基本上已经定了' },
      { index: 1, startSec: 11.5, endSec: 22, text: '没签的那两个走线下，就是王总和林岚那边' },
    ])
    assert.ok(out)
    assert.equal(out[0].text, '这个季度的续费方案基本上已经定了')
    assert.equal(out[3].text, '评审会我准备一下对比表')
  })
})

describe('C · 守不住时必须返回 null（且不得凭空编归属）', () => {
  it('只有一条回执 ⇒ null（一条表达不了轮次，硬分派会让内容出现两次）', () => {
    const out = attributeFullTranscript(realtime(), [
      { index: 0, startSec: 0, endSec: 28, text: '整场文本' },
    ])
    assert.equal(out, null, 'webm 不可切分时后端只回一条整段，必须退回旧行为')
  })

  it('原分段一个标签都没有 ⇒ null（没有可挂的东西）', () => {
    const unlabeled = realtime().map((s) => ({ ...s, speakerLabel: null }))
    assert.equal(attributeFullTranscript(unlabeled, fullReceipts()), null)
    assert.equal(
      attributeFullTranscript(realtime().map((s) => ({ ...s, speakerLabel: '   ' })), fullReceipts()),
      null,
      '空白标签等同于没有标签',
    )
  })

  it('没有回执 / null / 空数组 ⇒ null', () => {
    assert.equal(attributeFullTranscript(realtime(), null), null)
    assert.equal(attributeFullTranscript(realtime(), undefined), null)
    assert.equal(attributeFullTranscript(realtime(), []), null)
  })

  it('纯静音段（无正文无错误）不参与，也不产生空段', () => {
    const out = attributeFullTranscript(realtime(), [
      ...fullReceipts(),
      { index: 3, startSec: 28, endSec: 29.5, text: '', error: '' },
    ])
    assert.ok(out)
    assert.equal(out.length, 4, '静音回执不该造出第五段')
  })
})

describe('D · 内容保全：失败段与未覆盖段都不能静默消失', () => {
  it('失败段留占位符，不静默丢掉那一段', () => {
    const out = attributeFullTranscript(realtime(), [
      { index: 0, startSec: 0, endSec: 6, text: '这个季度的续费方案基本上已经定了' },
      { index: 1, startSec: 6, endSec: 13, text: '', error: 'upstream 503' },
      { index: 2, startSec: 13, endSec: 28, text: '没签的那两个走线下，就是王总和林岚那边评审会我准备一下对比表' },
    ])
    assert.ok(out)
    const joined = out.map((s) => s.text).join('\n')
    assert.match(joined, /第 2 段转写失败/, `失败段应留占位符：${joined}`)
  })

  it('整段重转完全没覆盖到的段，保留它自己的原文本（宁可重复也不丢内容）', () => {
    const segs = realtime()
    const out = attributeFullTranscript(segs, [
      { index: 0, startSec: 0, endSec: 6, text: '这个季度的续费方案基本上已经定了' },
      { index: 1, startSec: 6, endSec: 13, text: '定了吗我这边还有两个客户没签' },
    ])
    assert.ok(out)
    assert.equal(out[2].text, segs[2].text, '没被覆盖的第 3 段应保留自己的文本')
    assert.equal(out[3].text, segs[3].text)
  })

  it('★ 异常时间必须被收敛后**仍按时间归属**，而不是被数组下标决定', () => {
    // 第一版这条只断言「返回了 null / 每段 text 还是字符串」，
    // 结果 M7（去掉 isFinite 守卫）打不红 —— 因为 Math.max(x, NaN)=NaN 让
    // 所有候选得分变成并列，而并列时恰好被「第一个候选」兜住，正文没丢、
    // 断言也就没红。**守恒的是结果，不是判据。**
    //
    // 这里改钉真正的不变式：时间异常的那条回执，应当与「起点收敛为 0」的
    // 等价回执落在**同一段**上。哪天有人把守卫拆了，两者的落点就会分叉。
    const normal = attributeFullTranscript(realtime(), [
      { index: 0, startSec: 0, endSec: 11.5, text: '这个季度的续费方案基本上已经定了' },
      { index: 1, startSec: 11.5, endSec: 28, text: '没签的那两个走线下，就是王总和林岚那边' },
    ])
    const nanStart = attributeFullTranscript(realtime(), [
      { index: 0, startSec: 0, endSec: 11.5, text: '这个季度的续费方案基本上已经定了' },
      { index: 1, startSec: Number.NaN, endSec: 28, text: '没签的那两个走线下，就是王总和林岚那边' },
    ])
    assert.ok(normal && nanStart)
    assert.equal(
      nanStart.findIndex((s) => s.text.includes('王总')),
      normal.findIndex((s) => s.text.includes('王总')),
      '异常时间的回执落点与等价回执不一致 ⇒ 归属已被数组下标/并列决定',
    )
    // 顺带钉住内容没丢（这是「丢内容用户一眼看得出」的那一类）。
    assert.ok(nanStart.map((s) => s.text).join('\n').includes('王总'), '正文不能因时间异常而消失')
  })
})

describe('E · baseSegments 决策：三条路径都必须真的走到', () => {
  const input = (over: Partial<Parameters<typeof buildRefineBaseSegments>[0]> = {}) => ({
    meetingId: 'm1',
    original: realtime(),
    fullText: '这个季度的续费方案基本上已经定了没签的那两个走线下，就是王总和林岚那边评审会我准备一下对比表',
    fullSegments: fullReceipts(),
    elapsedMs: 28000,
    ...over,
  })

  it('路径 2：有回执 + 有标签 ⇒ 保留说话人，且段数与实时分段一致', () => {
    const out = buildRefineBaseSegments(input())
    assert.equal(out.length, 4)
    assert.deepEqual(out.map((s) => s.speakerLabel), ['张伟', '李娜', '张伟', '李娜'])
  })

  it('★ 路径 3：挂不回去时必须退回「压成一条」，而不是变成「没有分段」', () => {
    // 这是上一版判据量不到的地方：当时只扫源码里的 `??`，把
    // `const attributed = segs`（永不为 null）塞进去照样全绿。
    for (const [name, over] of [
      ['无回执', { fullSegments: null }],
      ['仅一条回执', { fullSegments: [{ index: 0, startSec: 0, endSec: 28, text: '整场' }] }],
      ['原分段无标签', { original: realtime().map((s) => ({ ...s, speakerLabel: null })) }],
    ] as const) {
      const out = buildRefineBaseSegments(input(over as never))
      assert.equal(out.length, 1, `${name}：应退回压成一条`)
      assert.equal(out[0].speakerLabel, null, `${name}：兜底分段本就没有说话人`)
      assert.ok(out[0].text.length > 0, `${name}：兜底分段必须带着重转文本，不能是空的`)
      assert.equal(out[0].endMs, 28000, `${name}：兜底区间应覆盖整场录音`)
    }
  })

  it('路径 1：不采纳重转时不在本函数内发生（由调用方的 adopt 分支负责）', () => {
    // 本函数只回答「采纳之后用什么」；不采纳时调用方根本不进来。
    // 这里钉住这个契约：喂进来的 original 必须原样可用，不被本函数改写。
    const segs = realtime()
    const out = buildRefineBaseSegments(input({ fullSegments: [], original: segs }))
    assert.equal(out.length, 1)
    assert.equal(out[0].meetingId, 'm1')
    assert.equal(out[0].id, 'm1-full')
  })

  it('不采纳时调用方原样用分段：源码里 adopt 为假时必须走 : segments', () => {
    // ⚠ §52：baseSegments 的决策已移进纯决策层 meeting-refine-plan.ts
    const s = read('./meeting-refine-plan.ts')
    assert.match(
      s,
      /buildRefineBaseSegments\([\s\S]*?\)\s*\n\s*:\s*segments/,
      '不采纳重转时必须原样用分段',
    )
  })
})

describe('F · 接线：调用方必须真的用上（不然这是一条没人走的路）', () => {
  const src = () => read('./meeting-recording-finalize.ts')
  // ⚠ §52：归因决策在纯决策层，收尾层只负责把结果喂给精校。
  const planSrc = () => read('./meeting-refine-plan.ts')

  it('transcribeFull 的回执必须带 segments 上来', () => {
    assert.match(
      src(),
      /segments:\s*r\.segments/,
      '注入时没带 segments ⇒ 后端逐段回执在接缝处被丢掉，归属无从恢复',
    )
  })

  it('必须走 buildRefineBaseSegments 这条纯函数路径', () => {
    assert.match(planSrc(), /buildRefineBaseSegments\(/, '归属恢复决策没被接上')
  })
})
// ─────────────────────────────────────────────────────────────────────────────
// §554：两条「原来没人守」的读数。
//
// ① 那条 fallback（`if (best < 0) best = pickBucket(..., false)`）是**活代码**，
//    但**变异 M3 删掉它，全仓 335 个测试文件仍然全绿** ⇒ 零覆盖。
//    生产可达：实时分段来自 VAD（silenceMs 1500），收尾回执来自 SplitWAV
//    （minSilenceCutSec=5）—— **两套切分准则不同，回执数可以多于实时段数**。
//    此时每段都已有桶 ⇒ `preferEmpty` 版 `pickBucket` 恒返回 -1
//    ⇒ 没有 fallback，第 2~N 条回执被**静默丢弃**（实测 6 条丢 5 条）。
//    与 §491「分片静默丢失、读数里彻底不可见」是同一族。
//
// ② 字幕段（`origin:'manual'`，Web Speech 产出）**拿不到桶**，因而保留自己的文本。
//    ⚠ 这是 D 组那条「宁可重复也不丢内容」的**同一个 `|| o.text`**，
//    所以它不是疏漏而是**被有意守住的取舍** —— 名字里就写着。
//    这里钉住它，是为了让将来修 #42 的人**看见自己正在推翻哪一条**。
// ─────────────────────────────────────────────────────────────────────────────

describe('G · §554：两条原来没人守的读数', () => {
  it('★ 回执数多于段数时，多出来的回执不得被静默丢弃（M3：删掉 fallback 全仓仍全绿）', () => {
    // 形态刻意取「1 条实时段 × 6 条回执」：这是 fallback 唯一会触发的条件
    //（所有段都已有桶 + 还有回执没地方放）。仓库既有夹具是 4 段 3 回执，
    // 永远触不到它 —— 这正是它零覆盖的原因。
    const one: Seg[] = [{
      id: 's0', meetingId: 'm1', speakerLabel: '张伟', lang: 'zh', confidence: 0.9,
      startMs: 0, endMs: 30000, text: '整场就这一段实时文本',
    }]
    const bodies = ['第一句', '第二句', '第三句', '第四句', '第五句', '第六句']
    const out = attributeFullTranscript(one, bodies.map((text, i) => ({
      index: i, startSec: i * 5, endSec: (i + 1) * 5, text,
    })))
    assert.ok(out, '有回执有标签时不该返回 null')
    const joined = out.map((s) => s.text).join('\n')
    for (const b of bodies) {
      assert.ok(joined.includes(b), `「${b}」被静默丢弃了 —— fallback 那行没了？结果：${joined}`)
    }
    // 反向钉一下「不是恒真」：段数不变（不因回执多而造出新段），
    // 且实时段原文**确实被覆盖**（否则这条断言可能被「什么都没做」满足）。
    assert.equal(out.length, 1, '回执多不构成分段增长')
    assert.ok(!joined.includes('整场就这一段实时文本'), '唯一那段应被高精度正文覆盖')
  })

  it('⚠ 当前取舍：字幕段的归属**由「回执数 / 总段数」这个比值决定**，与它自称的时间轴无关', () => {
    // §554 的实测（扫 3×6 = 18 格）：开关在比值 **1.0** 处跳变 ——
    //   回执/总段数 ≤ 0.75 ⇒ 字幕段拿到回执 **0** 条（保留自己的文本）
    //   回执/总段数 ≥ 1.00 ⇒ 字幕段拿到回执 **100%**（时间错位实测达 10.5 秒）
    // 机制：`pickBucket(preferEmpty=true)` 在「所有段都已有桶」时返回 -1，
    //   于是 fallback 把剩下的回执塞进**最后一个残桶** —— 不看时间戳。
    // ⇒ ⭐ 这解释了 §553.2 那个「0/19」为什么是**语料比例造出来的**：
    //   它用 6 ASR + 6 字幕配 3 回执，比值 0.25，结构上轮不到字幕段当残桶。
    //
    // ⚠ 两条都钉住，因为生产落在哪一侧取决于真实音频的静音分布（⛔ 未测）。
    const asr = realtime()
    const mkCap = (startMs: number, text: string) => {
      const d = buildUtteranceSegment({ meetingId: 'm1', text, startMs })
      assert.ok(d)
      return { id: `c${startMs}`, ...d } as Seg
    }
    const cap = mkCap(6000, '另一台引擎的同一段内容')
    assert.equal(cap.origin, 'manual', '字幕段当前被标成 manual —— 这正是它豁免去重的原因')

    const recs = [
      { index: 0, startSec: 0, endSec: 11.5, text: '这个季度的续费方案基本上已经定了' },
      { index: 1, startSec: 11.5, endSec: 22, text: '没签的那两个走线下，就是王总和林岚那边' },
      { index: 2, startSec: 22, endSec: 28, text: '评审会我准备一下对比表' },
    ]

    // ① 3 段 2 回执 = 比值 0.67 < 1：字幕段抢不到残桶
    //    ⇒ 保留自己的文本（= 整段重转漏掉它，重复因此永久）。
    // ⚠ 注意回执**不能少于 2 条**：`attributeFullTranscript:299` 有 `bodies.length < 2 ⇒ null`。
    const few = attributeFullTranscript([asr[0], cap, asr[2]], recs.slice(0, 2))
    assert.ok(few)
    assert.equal(
      few.find((s) => s.id === cap.id)!.text,
      cap.text,
      '回执少于段数时字幕段应保留自己的文本（修 #42 前请先读这条）',
    )

    // ② 比值 = 1：段数刚好用尽，最后一个残桶是字幕段 ⇒ ⭐ 它**拿到**了 22~28s 的正文，
    //    而它自称的时间轴是 6.0~6.4s ⇒ **错位 16 秒量级**。这不是 bug，是残桶兜底的设计后果。
    const many = attributeFullTranscript([asr[0], cap, asr[2]], [
      ...recs,
      { index: 3, startSec: 28, endSec: 34, text: '会后我把纪要发群里' },
      { index: 4, startSec: 34, endSec: 40, text: '好嘞' },
    ])
    assert.ok(many)
    assert.equal(
      many.find((s) => s.id === cap.id)!.text,
      '评审会我准备一下对比表',
      '比值 ≥1 时字幕段会拿到残桶回执 —— 钉住它，免得哪天有人以为字幕段永远拿不到',
    )
  })

})
