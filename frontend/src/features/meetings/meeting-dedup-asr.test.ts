// meeting-dedup-asr.test.ts —— 用**真实 ASR 输出**守住去重行为。
//
// ══ 数据从哪来（重要）══
// MiniMax TTS 生成一段 7.6 秒的会议音频（两句），然后用 ffmpeg 按不同窗口
// 切分，**每一份都送真实 ASR 转写**。得到：
//
//   pre_c (0.0–2.4s) → 「这个季度的续费方案，基本」
//   win_a (0.0–4.2s) → 「这个季度的续费方案基本上已经定了。」
//   win_b (2.6–7.0s) → 「已经定了。没签的那两个客户，我们走线下」
//   下一句（无重叠） → 「评审会，我准备一下对比表。」
//
// ★ 关键点：win_a 与 win_b **覆盖同一段音频**（重叠 1.6 秒），但因为
//   上下文不同，ASR 在重叠区给出的字**不一样**（win_a 有「基本」，
//   win_b 从句中开始所以没有）。这才是真实的切分重叠形态。
//
// ══ 一次自我更正（2026-10-07）══
// 本文件第一版用的不是上面这批数据，而是**我在生成 TTS 时就写了两段不同文本**
// （「走线下处理」vs「手头先处理」），然后称之为「真实 ASR 的同音错误」。
// **那是错的**：那两句根本不同音（走 zǒu / 手 shǒu），而 ASR 两边都转对了。
// 用那批诱导数据算出的「真重叠覆盖率下界 0.4667 < 阈值 0.6 ⇒ 存在遗留缺陷」
// **不成立**。换成真实切分数据后覆盖率分别是 0.6471 / 0.9167，去重完全正常。
// 教训见 `meeting-dedup.ts` 的 ANCHOR_COVERAGE 注释。
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { dedupeSegments, renderTranscript } from './meeting-dedup.ts'

const PRE = '这个季度的续费方案，基本'
const WIN_A = '这个季度的续费方案基本上已经定了。'
const WIN_B = '已经定了。没签的那两个客户，我们走线下'
const NEXT = '评审会，我准备一下对比表。'

/** 真实切分形态：pre_c 与 win_a 同起点，win_b 与 win_a 在时间上重叠 1.6 秒。 */
const realChunkRun = [
  { text: PRE, startMs: 0, endMs: 2400 },
  { text: WIN_A, startMs: 0, endMs: 4200 },
  { text: WIN_B, startMs: 2600, endMs: 7000 },
  { text: NEXT, startMs: 7200, endMs: 10000 },
]

describe('meeting-dedup-asr · §1 真实切分重叠必须去干净', () => {
  it('重叠区只出现一次', () => {
    const joined = dedupeSegments(realChunkRun).map((s) => s.text).join('')
    assert.equal((joined.match(/已经定了/g) ?? []).length, 1, `重叠区重复了：${joined}`)
    assert.equal((joined.match(/这个季度的续费方案/g) ?? []).length, 1, `句首重复了：${joined}`)
    assert.equal((joined.match(/对比表/g) ?? []).length, 1, `末句重复了：${joined}`)
  })

  it('每句内容都在（去重不许丢内容）', () => {
    const joined = dedupeSegments(realChunkRun).map((s) => s.text).join('')
    for (const frag of ['续费方案', '已经定了', '走线下', '对比表']) {
      assert.ok(joined.includes(frag), `内容丢了：${frag} 不在「${joined}」里`)
    }
  })

  it('本段零净增时被整段丢弃（§A 的行为，真实数据也覆盖）', () => {
    // 用真实数据构造：把 win_b 的全文再作为一段传进来。
    // 它的每个字都已在累积正文里 ⇒ 必须整段丢弃，而不是原样残留。
    const input = [
      { text: WIN_A, startMs: 0, endMs: 4200 },
      { text: WIN_B, startMs: 2600, endMs: 7000 },
      { text: WIN_B, startMs: 3000, endMs: 7000 },
      { text: NEXT, startMs: 7200, endMs: 10000 },
    ]
    const out = dedupeSegments(input)
    const joined = out.map((s) => s.text).join('')
    assert.equal((joined.match(/走线下/g) ?? []).length, 1, `整段被包含的分段原样残留了：${joined}`)
    // ⚠ 这两条是被变异 D4 逼出来的：只断言拼接文本**抓不到**
    //   「把它标成『有新增』但文本为空」——因为空文本拼进去什么都不会变。
    //   而空段照样会在 `renderTranscript` 里渲染成一行「[说话人] 」。
    assert.equal(out.length, input.length - 1, `零净增段没有被丢弃，输出 ${out.length} 段`)
    assert.ok(out.every((s) => (s.text ?? '').length > 0), `残留了空文本段：${JSON.stringify(out)}`)
  })

  it('renderTranscript 不得为零净增段产出一行「[说话人] 」', () => {
    const out = renderTranscript([
      { speakerLabel: 'A', text: WIN_A, startMs: 0, endMs: 4200 },
      { speakerLabel: 'A', text: WIN_B, startMs: 2600, endMs: 7000 },
      { speakerLabel: 'A', text: WIN_B, startMs: 3000, endMs: 7000 },
      { speakerLabel: 'A', text: NEXT, startMs: 7200, endMs: 10000 },
    ])
    const lines = out.split('\n')
    assert.equal(lines.length, 3, `零净增段仍被渲染成了一行：${JSON.stringify(out)}`)
    assert.ok(lines.every((l) => l.replace(/^\[\S*\] /, '').length > 0), `有空行：${JSON.stringify(out)}`)
  })
})

describe('meeting-dedup-asr · §2 正常语流一字不丢', () => {
  it('三句全部保留且逐字不变', () => {
    const clean = [
      { text: PRE, startMs: 0, endMs: 2400 },
      { text: WIN_B, startMs: 3000, endMs: 7000 },
      { text: NEXT, startMs: 7200, endMs: 10000 },
    ]
    const out = dedupeSegments(clean)
    assert.equal(out.length, 3)
    assert.equal(out.map((s) => s.text).join(''), clean.map((s) => s.text).join(''))
  })

  it('renderTranscript 同样一字不丢', () => {
    const clean = [
      { text: PRE, startMs: 0, endMs: 2400 },
      { text: WIN_B, startMs: 3000, endMs: 7000 },
      { text: NEXT, startMs: 7200, endMs: 10000 },
    ]
    const out = renderTranscript(clean.map((s) => ({ speakerLabel: 'A', ...s })))
    assert.equal(out.split('\n').length, 3)
    for (const s of clean) assert.ok(out.includes(s.text), `缺：${s.text}`)
  })
})

describe('meeting-dedup-asr · §3 两个出口必须同一口径', () => {
  // ⚠ 本轮修的缺陷之所以能同时存在于两个出口，就是因为「净增」那段逻辑
  //   被**抄了两遍**。现在合并成 `dedupeStep`，这里钉住它们的一致性。
  it('重叠场景下两条出口给出同样的正文字符', () => {
    const fromSegments = dedupeSegments(realChunkRun).map((s) => s.text).join('')
    const fromRender = renderTranscript(
      realChunkRun.map((s) => ({ speakerLabel: 'A', ...s })),
    )
      .split('\n')
      .map((l) => l.replace(/^\[A\] /, ''))
      .join('')
    assert.equal(fromSegments, fromRender)
  })
})

describe('meeting-dedup-asr · §4 负控：不得为省事而丢真实内容', () => {
  it('没有时间戳时，短的真实发言不得被当成重复丢掉', () => {
    // ⚠ 这一条**咬住过我**：我第一版无条件丢「零净增」段，
    //   结果「李四单独说『可行』」这种真实短句被整段吃掉 —— 丢内容。
    //   现在只在**时间轴确认重叠**时才丢。
    const out = dedupeSegments([{ text: '这个方案我觉得可行' }, { text: '可行' }])
    assert.equal(out.length, 2, '缺时间戳时不得丢段')
    assert.equal(out[1].text, '可行')
  })

  it('有正常静音间隙时，短的真实发言也不得被丢', () => {
    const out = dedupeSegments([
      { text: '这个方案我觉得可行', startMs: 0, endMs: 3000 },
      { text: '可行', startMs: 4000, endMs: 5000 },
    ])
    assert.equal(out.length, 2, '有静音间隙 ⇒ 不是重叠 ⇒ 不得丢段')
    assert.equal(out[1].text, '可行')
  })

  it('用户真的把同一句说了两遍（有静音间隙）⇒ 保留两遍', () => {
    const out = dedupeSegments([
      { text: '这个方案可行', startMs: 0, endMs: 3000 },
      { text: '这个方案可行', startMs: 5000, endMs: 8000 },
    ])
    assert.equal(out.length, 2, '有静音间隙 ⇒ 是用户真的重复说 ⇒ 不得当重叠裁掉')
  })
})