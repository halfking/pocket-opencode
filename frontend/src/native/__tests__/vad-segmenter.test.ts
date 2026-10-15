/**
 * vad-segmenter.test.ts — 会议链 VAD 切分的回归门（§536 / §537）
 *
 * 守卫的是一条**已经修过的严重缺陷**：§536 实测发现，单段语音一旦超过
 * `sliceBuffer` 的 60s 保留窗口（而那个窗口同时是段的音频来源），
 * 段就会**静默丢掉前面的音频**，`startMs/endMs` 却仍按真实起止自报。
 * 两条可达路径都已实测：① 背景噪声高于 0.0118（−38.6 dBFS）时段数阶跃塌陷成 1 段；
 * ② 页面切后台时 `tick()` 挂在 `requestAnimationFrame` 上停摆，
 * 而 `MediaRecorder` 不受影响地继续录制。
 *
 * ★ 为什么驱动真实类而不是复刻：`VadSegmenter` 的能量判定、
 *   取片窗口、预卷、保留窗口都是**互相咬合**的，
 *   任何一处复刻偏差都会让这个门守住一个假的形状。
 *   这里只替身三样外部依赖：Web Audio / MediaRecorder / 时钟。
 *
 * ★ 计量口径（读数全靠它，别改）：每片切片 Blob 的 size 恒为 1
 *   ⇒ 段 blob.size **就是切片数** ⇒ 段实际携带的音频时长 = size × sliceMs。
 *   没有这一步就只能数「出段几次」，看不见**每段带了多少音频**——
 *   而 §536 的缺陷恰恰是「段数看着正常、只是带的音频少了一半」。
 */
import assert from 'node:assert/strict'
import { test, after } from 'node:test'
import { VadSegmenter } from '../vad-segmenter.ts'

const SLICE_MS = 250
const BASE = 1_700_000_000_000
const STEP = 16

let VT = 0
let CURRENT_RMS = 0
let rafCb: (() => void) | null = null
let recorder: FakeMediaRecorder | null = null
const warnings: string[] = []

function makeAnalyser() {
  const a = {
    fftSize: 2048,
    get frequencyBinCount() {
      return a.fftSize / 2
    },
    // 与浏览器同构：字节时间域数据 = 128 + rms×128，四舍五入到整数
    // （8 位量化会让有效阈值从 0.012 挪到 0.0118，§536.4 扫出来的那条阶跃）
    getByteTimeDomainData(data: Uint8Array) {
      const v = Math.max(0, Math.min(255, Math.round(128 + CURRENT_RMS * 128)))
      data.fill(v)
    },
  }
  return a
}

class FakeAudioContext {
  state = 'running'
  constructor(_o?: unknown) {}
  createMediaStreamSource() {
    return { connect() {} }
  }
  createChannelSplitter() {
    return { connect() {} }
  }
  createAnalyser() {
    return makeAnalyser()
  }
  close() {
    this.state = 'closed'
  }
}

class FakeMediaRecorder {
  state = 'inactive'
  ondataavailable: ((e: { data: Blob }) => void) | null = null
  constructor(_s?: unknown) {
    recorder = this
  }
  start(_ms?: number) {
    this.state = 'recording'
  }
  stop() {
    this.state = 'inactive'
  }
  emit() {
    this.ondataavailable?.({ data: new Blob([new Uint8Array(1)]) })
  }
}

globalThis.AudioContext = FakeAudioContext as never
globalThis.MediaRecorder = FakeMediaRecorder as never
globalThis.requestAnimationFrame = ((cb: () => void) => {
  rafCb = cb
  return 1
}) as never
globalThis.cancelAnimationFrame = (() => {
  rafCb = null
}) as never
const realNow = Date.now
const realWarn = console.warn
Date.now = () => BASE + VT
console.warn = (...a: unknown[]) => {
  warnings.push(a.join(' '))
}

type Span = { start: number; end: number; rms: number }
type Seg = { startMs: number; endMs: number; coveredMs: number; spanMs: number }

/** 跑一遍虚拟录音。`freeze` 模拟页面切后台：录音继续出片，但 rAF 不再回调。 */
async function run(
  opts: Record<string, number>,
  spans: Span[],
  totalMs: number,
  freeze: [number, number] | null = null,
): Promise<Seg[]> {
  VT = 0
  CURRENT_RMS = 0
  rafCb = null
  recorder = null
  warnings.length = 0
  const out: Seg[] = []
  const seg = new VadSegmenter({
    ...opts,
    onSegment: (s) =>
      out.push({
        startMs: s.startMs,
        endMs: s.endMs,
        coveredMs: s.blob.size * SLICE_MS,
        spanMs: s.endMs - s.startMs,
      }),
  })
  await seg.start({} as never)
  let nextSliceAt = 0
  for (VT = 0; VT <= totalMs; VT += STEP) {
    CURRENT_RMS = rmsAt(spans, VT)
    if (VT >= nextSliceAt) {
      recorder?.emit()
      nextSliceAt += SLICE_MS
    }
    if (freeze && VT >= freeze[0] && VT < freeze[1]) continue
    rafCb?.()
  }
  seg.stop()
  return out
}

function rmsAt(spans: Span[], t: number): number {
  for (const s of spans) if (t >= s.start && t < s.end) return s.rms
  return 0
}

const totalCovered = (segs: Seg[]) => segs.reduce((a, s) => a + s.coveredMs, 0)
const maxSpan = (segs: Seg[]) => segs.reduce((a, s) => Math.max(a, s.spanMs), 0)

/**
 * ⚠ §549：区分「丢音频」与「强制切分」是**两种失效**，必须分开断言。
 *
 * 这两条断言原先都写成 `warnings.length === 0`，而它们的说明文字其实说的是
 * 「不该再报**段音频不完整**」/「音频没丢，不该告警」—— 也就是说**本意就是前者**。
 * ⭐ 写成「零告警」会把「强制切分告警」一并禁掉，而强制切分在连续讲话里
 *   是**正确行为**（本就没有停顿可切），禁掉它等于把一个有用的诊断信号删了。
 * ⇒ 这里收窄到具体消息：原意一分不丢，判据还更精确（不会再被无关告警误伤）。
 */
const lossWarnings = () => warnings.filter((w) => w.includes('段音频不完整'))
const forcedCutWarnings = () => warnings.filter((w) => w.includes('强制切分'))

// 生产参数见 recordingRuntime.ts 的 VadSegmenter 构造处。
const PROD = { silenceMs: 1500, minSpeechMs: 400, energyThreshold: 0.012, maxSpeechMs: 20_000 }

test('正常形态：按静音边界出段，每段都带全整句', async () => {
  const spans: Span[] = []
  for (let i = 0; i < 6; i++) spans.push({ start: i * 4800, end: i * 4800 + 3000, rms: 0.3 })
  const segs = await run(PROD, spans, 30000)
  assert.equal(segs.length, 6, '6 段 3s 语音 + 1.8s 间隔应产出 6 段')
  for (const s of segs) assert.ok(s.coveredMs >= 3000, `每段必须覆盖整句语音，实测 ${s.coveredMs}ms`)
  assert.equal(warnings.length, 0, '正常形态不该有「段音频不完整」告警')
})

test('连续语音超过保留窗口时不再丢音频，且段长被 maxSpeechMs 封住', async () => {
  for (const dur of [70, 90, 120]) {
    const segs = await run(PROD, [{ start: 0, end: dur * 1000, rms: 0.3 }], dur * 1000 + 2000)
    assert.ok(segs.length >= Math.floor(dur / 20) - 1, `${dur}s 连续语音应按 ~20s 分段，实测 ${segs.length} 段`)
    // 上界 = 封顶 + 静音尾（末段按静音收尾，天然多带一个 silenceMs）
    assert.ok(maxSpan(segs) <= 22_000, `${dur}s 档段长应被封住，实测 ${maxSpan(segs)}ms`)
    // §536 的读数：90s 丢 31.2s、120s 丢 61.2s。修好后丢失必须归零。
    // 容差按「每段至多一片」的取整量级给，不给随时长增长的余地。
    const cov = totalCovered(segs)
    assert.ok(
      cov >= dur * 1000 - segs.length * 2 * SLICE_MS,
      `${dur}s 档仍有音频丢失：覆盖 ${cov}ms / 语音 ${dur * 1000}ms`,
    )
    // ⚠ §549：断言「段音频不完整」这一条，不是「零告警」——
    //   连续讲话本来就会被 maxSpeechMs 强制切段（那是对的），它有自己那条告警。
    assert.equal(lossWarnings().length, 0, `${dur}s 档不该再报「段音频不完整」（这是修复前唯一的症状）`)
  }
})

test('背景噪声高于能量阈值时：段数塌陷，但不再丢音频（丙未做，封顶兜住）', async () => {
  const spans: Span[] = []
  for (let i = 0; i < 8; i++) spans.push({ start: i * 5000, end: i * 5000 + 2000, rms: 0.3 })
  for (let i = 0; i < 8; i++) spans.push({ start: i * 5000 + 2000, end: i * 5000 + 5000, rms: 0.02 })
  const segs = await run(PROD, spans, 42000)
  // 修前这里是 1 段（塌陷）。丙（底噪自适应）未做，所以塌陷本身仍在，
  // 但 maxSpeechMs 会把它切开 ⇒ 段数 > 1 且段长有界。
  assert.ok(segs.length >= 2, `塌陷形态应被 maxSpeechMs 切开，实测仍只有 ${segs.length} 段`)
  assert.ok(maxSpan(segs) <= 22_000, `段长应被封住，实测 ${maxSpan(segs)}ms`)
})

test('页面切后台（rAF 停摆）不足 60s：音频一片没丢', async () => {
  const spans: Span[] = [
    { start: 0, end: 8000, rms: 0.3 },
    { start: 8000, end: 9000, rms: 0 },
    { start: 9000, end: 90000, rms: 0.3 },
  ]
  const segs = await run(PROD, spans, 95000, [8000, 40000])
  assert.ok(maxSpan(segs) <= 22_000, `停摆回来后段长应被封住，实测 ${maxSpan(segs)}ms`)
  assert.equal(lossWarnings().length, 0, '停摆 32s < 60s 保留窗口 ⇒ 音频没丢，不该报丢音频')
})

test('页面切后台超过 60s 保留窗口：仍会丢，但必须告警且时间轴对齐', async () => {
  const spans: Span[] = [
    { start: 0, end: 8000, rms: 0.3 },
    { start: 8000, end: 9000, rms: 0 },
    { start: 9000, end: 200000, rms: 0.3 },
  ]
  const segs = await run(PROD, spans, 205000, [8000, 100000])
  // ⚠ 告警必须发在「起点回推」那一刻（事件源），不能只发在 finalizeSegment 的
  //   覆盖检查里 —— 起点一回推 span 就等于 covered，那条检查永远绿，
  //   丢音频这件事被对齐动作本身藏了起来（§537.3 坑一）。
  assert.ok(warnings.length > 0, '停摆超过保留窗口 ⇒ 真的丢了，必须告警，否则这次丢失彻底不可见')
  assert.ok(maxSpan(segs) <= 22_000, `段长应被封住，实测 ${maxSpan(segs)}ms`)
  // 时间轴必须从「还拿得到的最早位置」开始，否则整场又被算成一个说话人
  assert.ok((segs[0]?.startMs ?? 0) > 35_000, `首段起点应对齐到保留窗口边缘，实测 ${segs[0]?.startMs}ms`)
})

after(() => {
  Date.now = realNow
  console.warn = realWarn
})

test('强制切分必须被告知，且每场只报一次（§548.2 的「静默退化」）', async () => {
  // 连续讲话 70s ⇒ 必然触发 maxSpeechMs 封顶强制切分。
  // §548.2：音频一段不少（覆盖率 100%），但段边界不在停顿上
  // ⇒ 质量对的是「定长硬切」那一行而不是「静音感知切分」那一行，
  // 而**修复前这条路径完全沉默**。本用例钉住「必须发声」。
  const segs = await run(PROD, [{ start: 0, end: 70000, rms: 0.3 }], 72000)
  assert.ok(segs.length >= 3, `70s 连续讲话应被强制切段，实测 ${segs.length} 段`)
  assert.equal(lossWarnings().length, 0, '音频没丢，不该报丢音频')
  assert.equal(forcedCutWarnings().length, 1, `强制切分必须报，且每场只报一次，实测 ${forcedCutWarnings().length} 条`)
  // ⚠ 告警必须带上「此刻实测 RMS + 阈值」：判别「底噪压住了判定」与
  //   「讲话确实连续」只能靠它，而那个判断**不该由本层下**（两者的读数同形）。
  const w = forcedCutWarnings()[0]
  assert.match(w, /RMS=/, `告警必须带实测 RMS，实测：${w}`)
  assert.match(w, /energyThreshold=/, `告警必须带阈值，实测：${w}`)
})

test('正常形态（每句都切在停顿上）一次强制切分都不该有（§549）', async () => {
  const spans: Span[] = []
  for (let i = 0; i < 6; i++) spans.push({ start: i * 4800, end: i * 4800 + 3000, rms: 0.3 })
  await run(PROD, spans, 30000)
  assert.equal(warnings.length, 0, `一切正常时不该有任何告警，实测：${warnings.join(' | ')}`)
})
