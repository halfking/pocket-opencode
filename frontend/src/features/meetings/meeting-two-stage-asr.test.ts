// meeting-two-stage-asr.test.ts —— 两阶段转写的**接缝**，全部用真实 ASR 输出。
//
// ══ 为什么要有这道门 ══
// 本会话在接缝上出过两次问题，两次形态相同：
//   ① 修好「整段被包含时原样残留」，暴露出上游 `attributeFullTranscript`
//      在分段时间范围相同时把回执全给第一段 ⇒ 第二位说话人归属消失；
//   ② 修好 ①，暴露出我把 wire 负控的「退化必须可见」搞没了。
// 单个函数的门禁都绿，**合起来才出问题** —— 所以要有一道跑整条接缝的门。
//
// ══ 这道门**不覆盖**什么（避免后人误判）══
// 变异验证实测：撤掉 `attributeFullTranscript` 的「并列时优先空桶」
// （`pickBucket(preferEmpty)`）在这道门里**仍全绿** ——
// 因为本夹具三个窗口的时间戳互不相同，回执与它们的得分本来就是唯一的，
// 优先空桶不改变任何分配。
// 那个行为由 `__tests__/refine-plan.test.ts` 守着（那里的夹具刻意让
// 两段时间范围完全相同，正是并列场景）。
// ⇒ 两道门守的是**不同的场景**，别因为其中一道绿就认为另一道多余。
//
// ══ 数据 ══
// 同一段 7.6 秒会议音频（MiniMax TTS 生成两句普通话），按三种窗口切分，
// 每一份都送**真实 ASR** 转写：
//   阶段一（VAD 窗口）  pre_c(0–2.4s) / win_a(0–4.2s) / win_b(2.6–7.0s)
//   阶段二（整段重转）  整段一次 ASR，服务端按静音切成 2 条回执
// win_a 与 win_b 覆盖同一段音频（重叠 1.6s），但上下文不同 ⇒ 重叠区给字不同。
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { buildRefineBaseSegments, shouldAdoptFullTranscript } from './meeting-final-transcript.ts'
import { dedupeSegments, renderTranscript } from './meeting-dedup.ts'

const FULL_TEXT = '这个季度的续费方案基本上已经定了。没签的那两个客户，我们走线下处理。'
const RECEIPTS = [
  { index: 0, startSec: 0, endSec: 3.6, text: '这个季度的续费方案基本上已经定了。' },
  { index: 1, startSec: 3.6, endSec: 7.6, text: '没签的那两个客户，我们走线下处理。' },
]
const WINDOWS = [
  { id: 'w0', meetingId: 'm1', speakerLabel: '张伟', lang: 'zh', confidence: 0.9,
    text: '这个季度的续费方案，基本', startMs: 0, endMs: 2400 },
  { id: 'w1', meetingId: 'm1', speakerLabel: '张伟', lang: 'zh', confidence: 0.9,
    text: '这个季度的续费方案基本上已经定了。', startMs: 0, endMs: 4200 },
  { id: 'w2', meetingId: 'm1', speakerLabel: '李娜', lang: 'zh', confidence: 0.9,
    text: '已经定了。没签的那两个客户，我们走线下', startMs: 2600, endMs: 7000 },
]

const attributed = buildRefineBaseSegments({
  meetingId: 'm1', original: WINDOWS, fullText: FULL_TEXT,
  fullSegments: RECEIPTS, elapsedMs: 7628,
})
const final = dedupeSegments(attributed)
const joined = final.map((s) => s.text).join('')

describe('meeting-two-stage-asr · 阶段二把高精度文本挂回说话人', () => {
  it('采纳判定通过（整段重转不比分段文本短一半）', () => {
    assert.equal(shouldAdoptFullTranscript(FULL_TEXT, WINDOWS).adopt, true)
  })

  it('两位说话人都保住（§40 修的就是这个）', () => {
    const labels = final.map((s) => s.speakerLabel)
    assert.ok(labels.includes('张伟'), `张伟丢了：${JSON.stringify(labels)}`)
    assert.ok(labels.includes('李娜'), `李娜丢了：${JSON.stringify(labels)}`)
  })

  it('第一条高精度正文落在时间上靠前的分段上', () => {
    const first = final.find((s) => (s.text ?? '').includes('这个季度的续费方案'))
    assert.equal(first?.speakerLabel, '张伟', '「续费方案」那句应归张伟')
  })

  it('第二条高精度正文落在时间上靠后的分段上', () => {
    const second = final.find((s) => (s.text ?? '').includes('没签的那两个客户'))
    assert.equal(second?.speakerLabel, '李娜', '「没签的客户」那句应归李娜')
  })
})

describe('meeting-two-stage-asr · 接缝处不得产生重复', () => {
  for (const frag of ['这个季度的续费方案', '已经定了', '没签的那两个客户', '走线下']) {
    it(`「${frag}」只出现一次`, () => {
      const n = (joined.match(new RegExp(frag, 'g')) ?? []).length
      assert.equal(n, 1, `重复 ${n} 次：${joined}`)
    })
  }

  it('不得残留空文本段（否则会渲染成一行「[说话人] 」）', () => {
    assert.ok(final.every((s) => (s.text ?? '').length > 0),
      `残留空段：${JSON.stringify(final)}`)
  })
})

describe('meeting-two-stage-asr · 收尾必须比实时更完整，而不是更差', () => {
  // 实时路径把「这个季度的续费方案基本上已经定了」在窗口边界上切成了
  // 「……，基本」+「上已经定了。」；整段重转拿回完整句子。
  // ⇒ 两阶段正文**必然**不同，而且收尾应当是那个更完整的。
  // 断言「两者相等」会把这一轮的修复判成回归 —— 那是错的。
  it('实时正文丢掉了句尾，而收尾把它补回来了', () => {
    const live = renderTranscript(WINDOWS).replace(/^\[\S+\] /gm, '').replace(/\n/g, '')
    assert.ok(!live.includes('走线下处理'), '实时文本本就不该有完整句尾，否则这道门没有意义')
    assert.ok(joined.includes('走线下处理'), `收尾应补回完整句尾，实际：${joined}`)
  })

  it('两阶段的说话人序列一致（收尾不重新分配说话人）', () => {
    const live = renderTranscript(WINDOWS)
    const liveLabels = live.split('\n').map((l) => l.match(/^\[([^\]]*)\]/)?.[1])
    assert.deepEqual(final.map((s) => s.speakerLabel), liveLabels)
  })
})
