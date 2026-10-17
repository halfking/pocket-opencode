// audio-time-base.test.ts — §825：保存的音频**不从会议开头开始**时，
// 整段重转的逐段回执与会议绝对时间**不同基**，按时间重叠分桶会把后半场的话
// 挂到前半场的说话人名下。
//
// ── 缺陷的完整因果链（每一步都可在 node 里验）──
//   1. 录音中途切设备，`switchDevice` 里那次 `vadSegmenter.stop()` 的返回值
//      被丢弃（§824），而 `start()` 会 `new VadSegmenter`（fullChunks 从空起）
//      ⇒ 保存的音频只覆盖切换**之后**。
//   2. 服务端逐段回执的 startSec/endSec 是**上传那段音频自己的局部坐标**。
//   3. `attributeFullTranscript` 拿它与分段的**绝对时间**比重叠（pickBucket）。
//   4. ⇒ 每一条回执都落到时间上恰好重合的**别的**分段上。
//
// ★ 为什么长度守卫拦不住（这是本节的关键读数）：
//   `shouldAdoptFullTranscript` 按**字数**比，错位按**时间**发生，两者是不同的量。
//   实测（`.artifacts/s824-probe/attrib2.mjs`，4 段会议 / 后半场 2 条回执）：
//     · 采纳判定只随后半场**字数**变（3 句 → true，2 句 → false），与切点在
//       10%/25%/40%/50%/60%/75% 无关 —— 自检通过，两侧确实解耦；
//     · 落桶下标只随切点的**时间**变。
//   而切设备恰恰常发生在会议后半程、后半场话说得比较密 ⇒ 字数过半很正常。
//
// ⚠ 修复口径：本文件只钉住「**不许把错基的回执挂上去**」。
//   真正该做的是把各段音频拼起来、带上精确偏移（§824 的存储形态方案），
//   属属主拍板范围；本轮不代拍。
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { readFileSync } from 'node:fs'
import { planRefine } from '../meeting-refine-plan.ts'
import { attributeFullTranscript, shouldAdoptFullTranscript } from '../meeting-final-transcript.ts'
import type { MeetingSegment } from '../meetings-store.ts'

// ⭐ 夹具的一条纪律：**各段文本互不重叠**。dedupeSegments 是相邻重叠算法，
// 若几段长得几乎一样，它会裁掉后一段的尾巴 —— 那会让「分段原样保留」这条
// 断言测到的是去重而不是归属（本轮第一版夹具就栽在这里）。
const S1 = '老李把上季度的对比表先发到群里大家各自先过一遍。'
const S2 = '小王确认预算已经批下来了下周就能走采购流程。'
const S3 = '客户那边希望把联调时间从月底提前到月中。'
const S4 = '风险主要在联调环境不稳定需要提前申请资源。'

const seg = (over: Partial<MeetingSegment> & { id: string; text: string }): MeetingSegment => ({
  meetingId: 'm1', speakerLabel: null, lang: 'zh', confidence: 0.9,
  startMs: 0, endMs: 10_000, origin: 'asr', ...over,
} as MeetingSegment)

/** 一场 40s、4 段、两个说话人的会议。 */
const SEGS: MeetingSegment[] = [
  seg({ id: 's1', speakerLabel: '张伟', startMs: 0, endMs: 10_000, text: S1 }),
  seg({ id: 's2', speakerLabel: '李娜', startMs: 10_000, endMs: 20_000, text: S2 }),
  seg({ id: 's3', speakerLabel: '张伟', startMs: 20_000, endMs: 30_000, text: S3 }),
  seg({ id: 's4', speakerLabel: '李娜', startMs: 30_000, endMs: 40_000, text: S4 }),
]

/**
 * 切设备后上传的那段音频（只覆盖后半场）转出来的回执。
 * ★ 坐标是**局部**的：0 对应会议的 20s，不是 0s —— 这就是全部问题所在。
 */
const POST_RECEIPTS = [
  // 长度必须**过半**，否则 shouldAdoptFullTranscript 会先一步拦下，
  // 整个场景就变成「被现有守卫保护着」的空转（第一版夹具就是这样，
  // 且判据自己红了出来 —— 那是判据在起作用，不是夹具在起作用）。
  { index: 0, startSec: 0, endSec: 10, text: '【后半·高精度】客户希望联调时间提前到月中并且要看到完整方案。' },
  { index: 1, startSec: 10, endSec: 20, text: '【后半·高精度】资源申请要提前走否则月中联调来不及完成验收。' },
]

const FULL = {
  text: POST_RECEIPTS.map((r) => r.text).join(''),
  applied: true,
  segments: POST_RECEIPTS,
}

describe('§825 · 音频时间基错位', () => {
  it('★ 长度守卫本就会放行 —— 否则下面所有判据都是空转', () => {
    // 负控：若这个前提不成立（守卫已经拦住），「不许错基归属」就没有意义。
    const r = shouldAdoptFullTranscript(FULL.text, SEGS)
    assert.equal(r.adopt, true, `前置读数变了：守卫现在会拦下（${r.reason}），本节的场景需要重估`)
  })

  it('★ 错基回执**确实**会挂到别人的分段上（证明这条判据守的是真东西）', () => {
    const out = attributeFullTranscript(SEGS, POST_RECEIPTS)
    assert.ok(out, '前置读数变了：归因没生效，本节场景需要重估')
    const touched = out.map((s, i) => ({ i, t: s.text ?? '' }))
      .filter((x) => x.t.includes('【后半·高精度】'))
      .map((x) => x.i)
    // 后半场的内容落在 s1/s2（**前半场**那两段）上 —— 那正是缺陷本身。
    assert.deepEqual(touched.sort(), [0, 1],
      '前置读数变了：错基回执不再落到前半场分段上，说明 §825 的成因需要重新取证')
  })

  it('★★ 有偏移时不得采纳重转：分段原样保留，说话人不被顶替', () => {
    const plan = planRefine({
      meetingId: 'm1', segments: SEGS, full: FULL, participants: [], elapsedMs: 40_000,
      audioStartsAtMs: 20_000,
    })
    assert.equal(plan.adopted, false)
    assert.match(plan.adoptReason, /audio-time-base-offset/, '必须说得出为什么不采纳')
    assert.ok(
      plan.baseSegments.every((s) => !((s.text ?? '').includes('【后半·高精度】'))),
      '后半场的正文不得出现在任何一段里 —— 它会顶掉前半场说话人的话',
    )
    assert.deepEqual(
      plan.baseSegments.map((s) => s.text),
      SEGS.map((s) => s.text),
      '分段文本必须原样沿用（内容一条不少、归属一条不乱）',
    )
  })

  it('★★ 偏移为 0 时行为不变（别把正常录音也拦了）', () => {
    const plan = planRefine({
      meetingId: 'm1', segments: SEGS, full: FULL, participants: [], elapsedMs: 40_000,
      audioStartsAtMs: 0,
    })
    assert.equal(plan.adopted, true, '没有丢过音频就必须照常采纳 —— 这是本节最容易犯的错')
    assert.equal(plan.adoptReason, 'ok')
    assert.ok(
      plan.baseSegments.some((s) => (s.text ?? '').includes('【后半·高精度】')),
      '同基时高精度正文应当挂上去，否则说明 §825 的修复把 §40 的归因一起关了',
    )
  })

  it('偏移缺失 / 非有限 / 负数一律当 0（不得因此拒绝采纳）', () => {
    for (const v of [undefined, Number.NaN, -1, Number.POSITIVE_INFINITY]) {
      const plan = planRefine({
        meetingId: 'm1', segments: SEGS, full: FULL, participants: [], elapsedMs: 40_000,
        audioStartsAtMs: v as number | undefined,
      })
      assert.equal(plan.adopted, true, `偏移 ${String(v)} 不该影响采纳判定`)
    }
  })

  it('★ 名单仍从原始分段兜底（错基不牵连 §52 的名单修复）', () => {
    const plan = planRefine({
      meetingId: 'm1', segments: SEGS, full: FULL, participants: [], elapsedMs: 40_000,
      audioStartsAtMs: 20_000,
    })
    assert.deepEqual([...plan.roster].sort(), ['张伟', '李娜'])
  })
})

// ── 接线层：只保证「证据真的从 runtime 一路传到了决策层」 ──────────────
// ⚠ 这一层是**源码扫描**，按 §818 的教训它对「信号变死代码」零牙
//   （它只看文本存在，看不到可达性）。真牙齿在上面那条行为用例里。
//   两层都要：行为用例保证决策正确，扫描保证决策没被搬回不可达处。
describe('§825 · 接线（扫描层，零牙已如实标注）', () => {
  const src = (p: string) => readFileSync(new URL(p, import.meta.url), 'utf8')

  it('runtime 在「切换确实丢音频」那一支记下偏移', () => {
    const body = src('../../../native/recordingRuntime.ts')
    assert.match(
      body,
      /if \(preSwitchBlob && preSwitchBlob\.size > 0 && keptElapsed > 0\) \{[\s\S]{0,400}?this\.audioStartsAtMs = keptElapsed/,
      '偏移必须在丢音频的那一支置位 —— 放在别处会让没丢音频的录音也被当成错基',
    )
  })

  it('新一场录音要清掉上一场的偏移，而 resume（切设备）不清', () => {
    const body = src('../../../native/recordingRuntime.ts')
    assert.match(body, /if \(freshSession\) \{[\s\S]{0,400}?this\.audioStartsAtMs = 0/,
      '偏移必须在 freshSession 里清零，否则换一场录音就会误判为错基')
  })

  it('stop() 把偏移交给调用方', () => {
    const body = src('../../../native/recordingRuntime.ts')
    assert.match(body, /return \{ audioPath, durationMs, audioStartsAtMs: this\.audioStartsAtMs \}/,
      '偏移必须随 stop() 的返回值出去 —— 收尾链没有别的渠道拿得到它')
  })

  it('finalizeRecording 接住并转交 planRefine', () => {
    const body = src('../meeting-recording-finalize.ts')
    assert.match(body, /recording\?: \{ audioStartsAtMs\?: number \}/, '入参没声明')
    assert.match(body, /audioStartsAtMs: recording\?\.audioStartsAtMs \?\? 0/, '没有转交给 planRefine')
  })

  it('★ 两条录音路径都必须传（漏一条 = 那个入口的修复不存在）', () => {
    for (const p of ['../meeting-recording-finalize.ts', '../../sessions/useSessionLiveRecord.ts']) {
      assert.match(src(p), /audioStartsAtMs/, `${p} 没提到这个字段`)
    }
    const session = src('../../sessions/useSessionLiveRecord.ts')
    const detail = src('../MeetingDetailView.vue')
    assert.match(session, /\{ audioStartsAtMs: stopped\?\.audioStartsAtMs \?\? 0 \}/,
      '随手记路径没把 stop() 的偏移传下去')
    assert.match(detail, /\{ audioStartsAtMs: stopped\?\.audioStartsAtMs \?\? 0 \}/,
      '会议详情页路径没把 stop() 的偏移传下去')
  })
})
