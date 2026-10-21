// recording-duration-consistency.test.ts — §803/§804 的**行为**门。
//
// §785 记的是「62 秒时间进度只保存 57.36 秒 PCM，差 4.64 秒」，而运行时**没有任何
// 提示**。§803 追到根因：录音链路里每个时长数字都是 `Date.now()` 的差，产物真实长度
// 从未被测量 ⇒ 系统里**没有任何量能与产物长度矛盾**。不是没人发现，是没有可发现的东西。
//
// ⚠ 本文件刻意**不含**「拿分段 endMs 对照 elapsedMs」这类用例：§803.2 已证明那是恒等式，
// 对「实际抓到多少音频」零信息量。写成测试只会给人「这条被覆盖了」的错觉。
//
// ★ 夹具按**生产形状**：
//   · WAV 字节 = 44 字节头 + samples*2、16kHz/16-bit/单声道（wav-encode.ts:54-57）
//   · §785 的 62s vs 57.36s 是真实读数，不是编出来的比例
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import {
  assessDurationDrift,
  createMeasuringToWav,
  finalizeFailureNotice,
  carriedStartTime,
  resolveWallClockDurationMs,
  resolveDurationGuardInput,
  nativeCaptureWindowAfterPart,
  wavDurationSec,
} from '../recording-duration-consistency.ts'
import {
  createNativePartTimelineState,
  describeNativeTimelineDegradation,
  readNativeEpoch,
  rebaseNativePartTimeline,
} from '../native-part-timeline.ts'
import { dedupeStep, likelyAudioOverlap } from '../meeting-dedup.ts'
import { stripVueComments as stripComments } from '../../../__tests__/source-scan.mjs'
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

/** 读仓内源码（路径相对**本文件**所在目录）。 */
const HERE = dirname(fileURLToPath(import.meta.url))
const read = (rel: string) => readFileSync(join(HERE, rel), 'utf8')

/**
 * 按**生产形状**造一段 WAV。
 *
 * ⚠ 第一版的夹具是 `new ArrayBuffer(44 + n*2)` —— 那**根本不是 WAV**，
 * 只是恰好让「减 44」那条朴素公式成立。真实产物上就露馅了（§806）：
 * `acoustic-speaker-mic.wav` 的 chunk 布局是 `fmt `(16) → `LIST`(26) → `data`，
 * `data` 偏移 78。⇒ 夹具必须自己拼真 RIFF，否则测试在保护一个错误的假设。
 */
function wavOf(seconds: number, opts: { extraChunk?: string; sampleRate?: number } = {}): ArrayBuffer {
  const sr = opts.sampleRate ?? 16000
  const frames = Math.round(seconds * sr)
  const dataBytes = frames * 2
  const extra = opts.extraChunk
    ? 8 + Buffer.byteLength(opts.extraChunk, 'utf8') + ((Buffer.byteLength(opts.extraChunk, 'utf8')) & 1)
    : 0
  const buf = Buffer.alloc(12 + 24 + extra + 8 + dataBytes) // RIFF+fmt( +LIST) +data
  let o = 0
  buf.write('RIFF', o, 'ascii'); o += 4
  buf.writeUInt32LE(buf.length - 8, o); o += 4
  buf.write('WAVE', o, 'ascii'); o += 4
  buf.write('fmt ', o, 'ascii'); o += 4
  buf.writeUInt32LE(16, o); o += 4
  buf.writeUInt16LE(1, o); o += 2      // PCM
  buf.writeUInt16LE(1, o); o += 2      // mono
  buf.writeUInt32LE(sr, o); o += 4
  buf.writeUInt32LE(sr * 2, o); o += 4 // byteRate
  buf.writeUInt16LE(2, o); o += 2      // blockAlign
  buf.writeUInt16LE(16, o); o += 2     // bitsPerSample
  if (extra) {
    const body = opts.extraChunk!
    const n = Buffer.byteLength(body, 'utf8')
    buf.write('LIST', o, 'ascii'); o += 4
    buf.writeUInt32LE(n, o); o += 4
    buf.write(body, o, 'utf8'); o += n + (n & 1)
  }
  buf.write('data', o, 'ascii'); o += 4
  buf.writeUInt32LE(dataBytes, o); o += 4
  return buf.buffer.slice(buf.byteOffset, buf.byteOffset + buf.length)
}

describe('wavDurationSec —— 从字节算真实时长（不依赖浏览器）', () => {
  it('⭐ §806：带 LIST chunk 的真实布局（data 偏移 78）也能读对', () => {
    // acoustic-speaker-mic.wav 的真实 chunk 布局：fmt (16) → LIST(26) → data
    const wav = wavOf(57.36, { extraChunk: 'INFOhello world.....' }) // 26 字节 LIST 体
    assert.equal(wavDurationSec(wav), 57.36)
    // ⚠ 朴素公式（减 44）在这个布局上读成 57.36106 —— 差 1.06ms（=26B/2/16000）
  })

  it('无 LIST 的紧排布局（data 偏移 44）同样读对', () => {
    assert.equal(wavDurationSec(wavOf(57.36)), 57.36)
  })

  it('真实素材的两条读数：57.36s 与 752.292s', () => {
    assert.equal(wavDurationSec(wavOf(57.36, { extraChunk: 'INFO1234567890ab' })), 57.36)
    assert.equal(wavDurationSec(wavOf(752.292)), 752.292)
  })

  it('⚠ 变异的产物：奇数长度的 chunk 必须按补齐字节跳过', () => {
    // 本条的由来：变异「不按奇数补齐字节推进 chunk」最初**没被抓住**，
    // 因为夹具里的 LIST 长度全是偶数。⇒ 补一条奇数长度的。
    // 'INFOabc' = 7 字节（奇数）⇒ 下一个 chunk 从 body+7+1 开始。
    const odd = wavOf(3, { extraChunk: 'INFOabc' })
    assert.equal(wavDurationSec(odd), 3)
  })

  it('⛔ 不再对非 WAV 字节返回「假时长」', () => {
    // 第一版的夹具正是这种形状：不是 WAV，却能读出时长。现在必须 null。
    assert.equal(wavDurationSec(new ArrayBuffer(44 + 16000 * 2)), null)
  })

  it('整秒往返：1s / 12.5s / 752.292s 都读得回来', () => {
    for (const sec of [1, 12.5, 752.292]) {
      const got = wavDurationSec(wavOf(sec))
      assert.ok(got !== null, `${sec}s 应读得出`)
      assert.ok(Math.abs(got! - sec) < 0.001, `${sec}s 读成 ${got}`)
    }
  })

  it('接受 Uint8Array（ArrayBuffer 的视图）', () => {
    const buf = wavOf(3)
    assert.equal(wavDurationSec(new Uint8Array(buf)), 3)
  })

  // ⚠ 量不到必须返回 null，**不能返回 0**。返回 0 会被读成「录了 0 秒」，
  // 那正是 §803.1 同型的假读数：没有那个量，不是那个量为 0。
  it('量不到时返回 null 而不是 0（空/太短/非 RIFF/无 data chunk）', () => {
    assert.equal(wavDurationSec(new ArrayBuffer(0)), null)
    assert.equal(wavDurationSec(new ArrayBuffer(40)), null, '连头都不完整 ⇒ 量不到')
    const notRiff = new ArrayBuffer(44 + 32000)
    assert.equal(wavDurationSec(notRiff), null, '不是 RIFF ⇒ 量不到')
    // 只有 fmt 没有 data
    const head = wavOf(1).slice(0, 36)
    assert.equal(wavDurationSec(head), null, '没有 data chunk ⇒ 量不到')
    // ⚠ 这条的由来：变异「解析失败时返回 0」最初**没被抓住** —— 因为上面每个
    // 「量不到」用例都在早期 guard（长度 <44 / 非 RIFF）就返回了，**从没走到**
    // 末尾那个组合判断（fmt 有但 data 没有）。⇒ 必须有一条能真正走到它的用例。
    const riffNoData = new Uint8Array(48)
    const dv = new DataView(riffNoData.buffer)
    const ascii = new TextEncoder()
    riffNoData.set(ascii.encode('RIFF'), 0)
    dv.setUint32(4, 40, true)
    riffNoData.set(ascii.encode('WAVE'), 8)
    riffNoData.set(ascii.encode('fmt '), 12)
    dv.setUint32(16, 16, true)
    dv.setUint16(20, 1, true)   // PCM
    dv.setUint16(22, 1, true)   // mono
    dv.setUint32(24, 16000, true)
    dv.setUint32(28, 32000, true)
    dv.setUint16(32, 2, true)
    dv.setUint16(34, 16, true)
    assert.equal(wavDurationSec(riffNoData), null, '有 fmt 无 data ⇒ 必须 null 而非 0')
  })

  it('⛔ 不许把「量不到」折成 0 秒', () => {
    for (const bad of [new ArrayBuffer(0), new ArrayBuffer(40), new ArrayBuffer(44 + 32000)]) {
      assert.notEqual(wavDurationSec(bad), 0)
    }
  })

  it('data 声明长度超出实际字节时按实际算（流式封装常见）', () => {
    const wav = wavOf(10)
    const b = new Uint8Array(wav)
    // 把 data 的声明长度改成很大
    const dv = new DataView(wav)
    const dataAt = new TextDecoder().decode(b.subarray(0, 44)).indexOf('data')
    dv.setUint32(dataAt + 4, 0x7fffffff, true)
    const got = wavDurationSec(wav)
    assert.ok(got !== null && Math.abs(got - 10) < 0.001, `应为 ~10s，实际 ${got}`)
  })
})

describe('assessDurationDrift —— 墙钟 vs 真实音频', () => {
  it('⭐ §785 那个真实缺口：62s vs 57.36s 判为 audio-shorter 且 ok=false', () => {
    const d = assessDurationDrift(62_000, 57.36)
    assert.equal(d.ok, false)
    assert.equal(d.reason, 'audio-shorter')
    assert.equal(d.driftMs, 4640) // 4.64 秒，与 §785 记录一致
    assert.ok(Math.abs(d.ratio! - 57.36 / 62) < 1e-9)
  })

  it('一致时判 ok 且 reason=within-tolerance', () => {
    const d = assessDurationDrift(60_000, 60)
    assert.equal(d.ok, true)
    assert.equal(d.reason, 'within-tolerance')
    assert.equal(d.driftMs, 0)
  })

  it('短录音的丢失按绝对下限判，不被相对份额吞掉', () => {
    // 2s 录音只剩 1.0s（丢一半）：容差 = max(500, 2%*2000=40) = 500 ⇒ 1000 > 500 必报
    const d = assessDurationDrift(2_000, 1.0)
    assert.equal(d.toleranceMs, 500)
    assert.equal(d.ok, false)
    assert.equal(d.driftMs, 1_000)
  })

  it('长录音的丢失按相对份额判', () => {
    // 20 分钟录音缺 60s（5%）：容差 = max(500, 24000) = 24000 ⇒ 60000 > 24000 必报
    const d = assessDurationDrift(1_200_000, 1_140)
    assert.equal(d.toleranceMs, 24_000)
    assert.equal(d.ok, false)
    assert.equal(d.driftMs, 60_000)
  })

  // ⚠ 这条钉住一个**刻意的取舍**：相对份额 ⇒ 长录音里的小额绝对丢失会被放过。
  // 20 分钟丢 4s = 0.33% < 2% ⇒ 不报。按绝对值报警会让长会议几乎必然刷屏。
  // 要改口径就必须同时改这条用例。
  it('⚠ 取舍钉住：20 分钟录音丢 4s（0.33%）判 ok —— 这是刻意放过，不是漏测', () => {
    const d = assessDurationDrift(1_200_000, 1_196)
    assert.equal(d.ok, true)
    assert.equal(d.driftMs, 4_000)
    assert.equal(d.reason, 'within-tolerance')
  })

  it('音频比墙钟**长**也能抓到（反方向不许漏）', () => {
    const d = assessDurationDrift(10_000, 40)
    assert.equal(d.ok, false)
    assert.equal(d.reason, 'audio-longer')
    assert.equal(d.driftMs, -30_000)
  })

  it('显式覆盖容差参数（供将来调口径，不必改本模块）', () => {
    const d = assessDurationDrift(60_000, 58, { toleranceMs: 1_000 })
    assert.equal(d.ok, false)
    assert.equal(d.toleranceMs, 1_000)
  })

  // ⚠ 量不到真值 ⇒ 判 ok 且 reason=no-ground-truth。这是 §803 那个静默缺口
  // 的**诚实写法**：不能拿「量不到」当成「已排除」。
  it('真值量不到时判 ok 且 reason=no-ground-truth（不是 within-tolerance）', () => {
    for (const bad of [null, Number.NaN, Number.POSITIVE_INFINITY]) {
      const d = assessDurationDrift(62_000, bad)
      assert.equal(d.ok, true)
      assert.equal(d.reason, 'no-ground-truth')
      assert.equal(d.driftMs, null)
      assert.equal(d.ratio, null)
    }
  })

  it('elapsedMs 非法（0/负）时同样退到 no-ground-truth，不做除法', () => {
    assert.equal(assessDurationDrift(0, 10).reason, 'no-ground-truth')
    assert.equal(assessDurationDrift(-1, 10).reason, 'no-ground-truth')
  })

  it('边界：差值恰好等于容差判 ok（闭区间）', () => {
    const d = assessDurationDrift(60_000, 55, { toleranceMs: 5_000 })
    assert.equal(d.ok, true)
    assert.equal(d.driftMs, 5_000)
  })
})

describe('createMeasuringToWav —— 测量 + 透传（这层胶水本来是测不到的）', () => {
  it('转码成功：量到真实时长，且原样返回同一份字节', async () => {
    const bytes = wavOf(12.5)
    const measured: (number | null)[] = []
    const out = await createMeasuringToWav(async () => bytes, (s) => measured.push(s))(
      new Blob([]),
    )
    assert.equal(out, bytes, '必须透传同一份字节，不能另造一份')
    assert.equal(measured.length, 1)
    assert.ok(Math.abs(measured[0]! - 12.5) < 0.001)
  })

  // ⚠ 返回 null（不需转码）是**正常路径**，不是错误。此时真值量不到，
  // 必须上报 null 让上层落到 no-ground-truth，而不是静默不回调。
  it('返回 null（不需转码）：上报 null，不静默跳过', async () => {
    const measured: (number | null)[] = []
    const out = await createMeasuringToWav(async () => null, (s) => measured.push(s))(new Blob([]))
    assert.equal(out, null)
    assert.deepEqual(measured, [null])
  })

  it('字节不是合法 WAV：上报 null 而不是 0', async () => {
    const measured: (number | null)[] = []
    await createMeasuringToWav(async () => new ArrayBuffer(7), (s) => measured.push(s))(new Blob([]))
    assert.deepEqual(measured, [null])
  })

  // ⚠ 透传失败必须**照旧抛出**：吞异常会让调用方以为转码成功了
  //   （meeting-recording-finalize.ts:105-106 对 defaultToWav 的注释就是这么定的）。
  it('⛔ 底层抛错时原样抛出，不吞不改', async () => {
    await assert.rejects(
      () => createMeasuringToWav(async () => { throw new Error('NO_AUDIO_CONTEXT') }, () => {})(
        new Blob([]),
      ),
      /NO_AUDIO_CONTEXT/,
    )
  })
})

// ⚠ 本组的由来：§806 只支持 PCM 却**没检查**编码类型，于是 Opus-in-WAV 与 ADPCM
// 都会被套进 PCM 公式、算出「10s」这种**看起来合理、实则完全错误**的数字。
// 真实缺陷，且只有造非 PCM 容器才照得出来。
function wavWithFormat(
  audioFormat: number,
  opts: { subFormat?: number; bits?: number; sr?: number; ch?: number; seconds?: number } = {},
): Uint8Array {
  const bits = opts.bits ?? 16
  const sr = opts.sr ?? 16000
  const ch = opts.ch ?? 1
  const ext = audioFormat === 0xfffe
  const fmtSize = ext ? 40 : 16
  const dataBytes = Math.round((opts.seconds ?? 10) * sr * ch * bits / 8)
  const b = new Uint8Array(12 + 8 + fmtSize + 8 + dataBytes)
  const dv = new DataView(b.buffer)
  const a = new TextEncoder()
  b.set(a.encode('RIFF'), 0); dv.setUint32(4, b.length - 8, true); b.set(a.encode('WAVE'), 8)
  b.set(a.encode('fmt '), 12); dv.setUint32(16, fmtSize, true)
  dv.setUint16(20, audioFormat, true); dv.setUint16(22, ch, true)
  dv.setUint32(24, sr, true); dv.setUint32(28, sr * ch * bits / 8, true)
  dv.setUint16(32, ch * bits / 8, true); dv.setUint16(34, bits, true)
  if (ext) {
    dv.setUint16(36, 22, true)                                   // cbSize
    dv.setUint16(38, bits, true)                                  // ValidBitsPerSample
    dv.setUint32(40, 0x3f, true)                                  // ChannelMask
    dv.setUint16(44, opts.subFormat ?? 1, true)                   // SubFormat GUID 头两字节
    dv.setUint16(46, 0, true)
  }
  const dataAt = 12 + 8 + fmtSize
  b.set(a.encode('data'), dataAt); dv.setUint32(dataAt + 4, dataBytes, true)
  return b
}

describe('⛔ 只认「帧数线性于 dataSize」的编码，否则返回 null（§807）', () => {
  it('PCM(1) 与 IEEE float(3)：定长编码，算得出', () => {
    assert.equal(wavDurationSec(wavWithFormat(1)), 10)
    assert.equal(wavDurationSec(wavWithFormat(3)), 10)
  })

  it('⭐ Opus-in-WAV(0x704F) 返回 null —— 它的 bitsPerSample 是占位值 16', () => {
    assert.equal(wavDurationSec(wavWithFormat(0x704f)), null)
  })

  it('ADPCM(2) / μ-law(7) / MP3(0x55)：压缩块与采样数无线性关系 ⇒ null', () => {
    for (const af of [2, 7, 0x55]) {
      assert.equal(wavDurationSec(wavWithFormat(af)), null, `format ${af} 应返回 null`)
    }
  })

  it('EXTENSIBLE(0xFFFE) 要读 SubFormat：包 PCM 算得出，包 Opus 返回 null', () => {
    assert.equal(wavDurationSec(wavWithFormat(0xfffe, { subFormat: 1 })), 10)
    assert.equal(wavDurationSec(wavWithFormat(0xfffe, { subFormat: 0x704f })), null)
  })

  it('EXTENSIBLE 但 fmt 太短读不到 SubFormat ⇒ null（认不出就不是 PCM）', () => {
    const b = wavWithFormat(0xfffe)
    assert.equal(wavDurationSec(b.slice(0, 12 + 8 + 16 + 8)), null)
  })

  it('⛔ 不许对非 PCM 返回「猜出来的秒数」', () => {
    for (const af of [0x704f, 2, 7, 0x55]) {
      assert.notEqual(wavDurationSec(wavWithFormat(af)), 10)
    }
  })
})

describe('§812 墙钟必须在写入时刻重算，不读 interval 的陈旧缓存', () => {
  it('正常：在写入时刻重算 now - startTime', () => {
    assert.equal(resolveWallClockDurationMs(62_000, 2_000, 999, false), 60_000)
  })

  // ⭐ 这条是整组的由来：后台节流会让缓存值过期，而录音**继续**（onHidden 只弹 toast）。
  //   旧代码 `const durationMs = this.elapsedMs.value` 会把 200 写进库。
  it('⭐ 缓存过期时必须用重算值，而不是缓存值', () => {
    const now = 62_000, startTime = 2_000, staleCache = 200   // 缓存停在 200ms 那一刻
    assert.equal(resolveWallClockDurationMs(now, startTime, staleCache, false), 60_000)
    assert.notEqual(resolveWallClockDurationMs(now, startTime, staleCache, false), staleCache)
  })

  it('没开始录（startTime=0）⇒ 退回缓存，不算出负数或垃圾值', () => {
    assert.equal(resolveWallClockDurationMs(62_000, 0, 200, false), 200)
  })

  it('暂停态 ⇒ 退回缓存（墙钟本就不该继续走）', () => {
    assert.equal(resolveWallClockDurationMs(62_000, 2_000, 30_000, true), 30_000)
  })

  it('now 非法（NaN/Infinity）⇒ 退回缓存', () => {
    assert.equal(resolveWallClockDurationMs(Number.NaN, 2_000, 200, false), 200)
    assert.equal(resolveWallClockDurationMs(Number.POSITIVE_INFINITY, 2_000, 200, false), 200)
  })

  it('结果不为负', () => {
    assert.equal(resolveWallClockDurationMs(1_000, 5_000, 200, false), 0)
  })
})

describe('§814 换设备后计时起点要前移，否则累计时长丢失', () => {
  // ⭐ 旧代码只补 `elapsedMs.value = keptElapsed`，而下一次 interval tick
  //   （`Date.now() - startTime`，startTime 已被 start() 重设为 now）会立刻覆盖它。
  it('⭐ 挪起点后，重算值从 keptElapsed 自然续上', () => {
    const now = 100_000, kept = 45_000
    const start = carriedStartTime(now, kept)
    // 下一 tick（+200ms）算出来的值应当是 kept + 200，而不是 ~200
    assert.equal(resolveWallClockDurationMs(now + 200, start, 0, false), kept + 200)
  })

  it('挪起点后不会因为时间倒流而算出负数', () => {
    assert.ok(carriedStartTime(100_000, 45_000) > 0)
  })

  it('keptElapsed 非法/0 ⇒ 起点就是 now（等价于「从头开始」）', () => {
    assert.equal(carriedStartTime(100_000, 0), 100_000)
    assert.equal(carriedStartTime(100_000, -5), 100_000)
    assert.equal(carriedStartTime(100_000, Number.NaN), 100_000)
  })

  it('连续两次换设备不会丢时长（累加而非覆盖）', () => {
    const t0 = 100_000                                   // 开始录音
    const firstKept = resolveWallClockDurationMs(150_000, t0, 0, false)   // 录了 50s
    assert.equal(firstKept, 50_000)

    const start1 = carriedStartTime(150_000, firstKept)   // 第一次换设备，续上 50s
    const secondKept = resolveWallClockDurationMs(170_000, start1, 0, false)
    assert.equal(secondKept, 70_000, '第二次换设备前应累计到 70s，而不是回到 20s')

    const start2 = carriedStartTime(170_000, secondKept) // 第二次换设备
    assert.equal(resolveWallClockDurationMs(175_000, start2, 0, false), 75_000)
  })
})

// ─────────────────────────────────────────────────────────────────────────
// §818 · `finalizeFailureNotice` —— 「转码失败必须可见」的**决策**在这里，
//   放这里是因为 `finalizeRecording` 在 node 里跑不到（§52）。
//   第一版把判断写在调用点、判据对着 console.warn 匹配 ⇒ 变异 B 全绿、零牙。
describe('finalizeFailureNotice —— §818 的决策点（真行为断言）', () => {
  it('一切正常时必须返回 null（否则每次录音都刷噪声）', () => {
    assert.equal(finalizeFailureNotice({ applied: true }), null)
  })

  it('★ 转码失败 ⇒ 必须出现根因文案', () => {
    const n = finalizeFailureNotice({ applied: false, reason: 'empty-transcript', transcodeFailed: true, transcodeError: 'decodeAudioData failed' })
    assert.ok(n, '转码失败却没有文案 —— §818 的洞没修')
    assert.match(String(n), /转码失败/, '缺根因表述')
    assert.match(String(n), /decodeAudioData failed/, '原始异常信息丢了，无法定位')
    // 根因与症状都要在：只报症状的话，排查的人会去查网关，而真问题在转码。
    assert.match(String(n), /empty-transcript/, '下游症状也该一并透出')
  })

  it('★ 转码失败但侥幸 applied:true ⇒ 仍要出声（成功会掩盖下次静默劣化）', () => {
    const n = finalizeFailureNotice({ applied: true, transcodeFailed: true, transcodeError: 'X' })
    assert.match(String(n), /转码失败/, 'applied:true 被当成了「一切正常」')
  })

  it('未传 toWav 导致 applied:false（正常降级）⇒ 仍要出声，但不是「转码失败」', () => {
    const n = finalizeFailureNotice({ applied: false, reason: 'audio-not-found' })
    assert.ok(n)
    assert.doesNotMatch(String(n), /转码失败/, '「没传 toWav」被误报成转码失败')
  })

  it('★ applied:false 且没有 reason 时仍要有文案（不能因为缺字段就沉默）', () => {
    const n = finalizeFailureNotice({ applied: false })
    assert.ok(n, 'applied:false 却没有 reason ⇒ 判据把失败又藏回去了')
  })

  it('负控：有 reason 但 applied:true ⇒ 不该报「整段重转未生效」', () => {
    const n = finalizeFailureNotice({ applied: true, reason: 'empty-transcript' })
    assert.doesNotMatch(String(n), /整段重转未生效/, 'applied:true 却报了未生效')
  })

  it('★ 变异防线：撤掉「转码失败」分支必须被抓（不是只测有文案）', () => {
    const n = finalizeFailureNotice({ applied: false, reason: 'empty-transcript', transcodeFailed: true })
    assert.match(String(n), /转码失败/, '去掉 transcodeFailed 分支后仍报 ⇒ 判据没咬到那个分支')
  })
})

// ─────────────────────────────────────────────────────────────────────────
// §955 · `resolveDurationGuardInput` —— 护栏拿**哪个墙钟**去比。
//
// ★ 为什么它必须是**纯函数**（§52 纪律）：`finalizeRecording` 在 node 里跑不到
//   ⇒ 决策写在它体内 ⇒ 一行行为门都没有 ⇒ 变异打不红。
//
// ⚠⚠ 这一组存在的理由是 §954 的一个**假声称**：
//   §954 的文档写着「§805 的时长护栏在原生链上复活」，而那句话是**假的**
//   —— `durationSec` 在生产者与消费者之间被丢掉 ⇒ 护栏恒判 `no-ground-truth`。
//   而更根本的是：**就算接上了，比的也是两个不同相位的钟**
//   （JS 的 `start()` 比原生服务开采**晚**）⇒ 短录音会假警报。
//   ⇒ 护栏在原生链上必须用**与盘上音频同源**的原生采集窗口。
describe('§955 · 护栏的墙钟必须与盘上音频同源', () => {
  it('★ Web / iOS 链（没有原生窗口）⇒ 墙钟就是 JS 墙钟，行为一字未变', () => {
    const g = resolveDurationGuardInput({ elapsedMs: 62_000, measuredAudioSec: 57.36 })
    assert.equal(g.source, 'js-wall-clock')
    assert.equal(g.wallClockMs, 62_000, 'Web 链的墙钟被换掉了 ⇒ §805 以来行为变了')
    assert.equal(g.audioSec, 57.36)
    // ⭐ 端到端：Web 链上 §785 那个真实缺口仍然判得出来
    const d = assessDurationDrift(g.wallClockMs, g.audioSec)
    assert.equal(d.ok, false)
    assert.equal(d.reason, 'audio-shorter')
  })

  it('★★ 原生链且窗口可信 ⇒ 墙钟必须换成**原生采集窗口**（不是 JS 墙钟）', () => {
    const g = resolveDurationGuardInput({
      elapsedMs: 62_000,
      nativeCapture: { used: true, windowMs: 60_000 },
      measuredAudioSec: 57.36,
    })
    assert.equal(g.source, 'native-window')
    assert.equal(g.wallClockMs, 60_000, '原生链仍然拿 JS 墙钟比 ⇒ 比的是系统偏移，不是「音频丢了多少」')
    // 同源比较：窗口 60s、音频 57.36s ⇒ 差 2.64s（这才是真的缺口）
    const d = assessDurationDrift(g.wallClockMs, g.audioSec)
    assert.equal(d.ok, false)
    assert.equal(d.reason, 'audio-shorter')
    assert.equal(d.driftMs, 2640)
  })

  // ⚠⚠ 这一条是本组**最关键**的一条：不可信时**不许**退回 JS 墙钟。
  it('★★ 原生链但窗口不可信（切过设备）⇒ 判「量不到」，**不许**拿 JS 墙钟顶替', () => {
    const g = resolveDurationGuardInput({
      elapsedMs: 62_000,
      nativeCapture: { used: true, windowMs: null },
      measuredAudioSec: 60,
    })
    assert.equal(g.source, 'native-untrusted')
    assert.equal(g.wallClockMs, 0, '拿 JS 墙钟顶替 ⇒ 差的是系统偏移 ⇒ 短录音会假警报')
    const d = assessDurationDrift(g.wallClockMs, g.audioSec)
    assert.equal(d.ok, true)
    assert.equal(d.reason, 'no-ground-truth', '不可信时必须判「量不到」，而不是判成「正常」')
  })

  it('★ 负控：拿 JS 墙钟当原生窗口 ⇒ 差值会变成「系统偏移」而不是真缺口', () => {
    // 真实场景的量级：原生服务比 JS 的 start() 晚 ~1.5s 开始 ⇒ 音频 60s、窗口 61.5s。
    // 若错用 JS 墙钟 62s ⇒ 报「差 2s」；用同源窗口 61.5s ⇒ 报「差 1.5s」—— 后者才是真的。
    const wrong = assessDurationDrift(62_000, 60)
    const right = assessDurationDrift(
      resolveDurationGuardInput({ elapsedMs: 62_000, nativeCapture: { used: true, windowMs: 61_500 }, measuredAudioSec: 60 }).wallClockMs,
      60,
    )
    assert.equal(wrong.driftMs, 2000)
    assert.equal(right.driftMs, 1500)
    assert.notEqual(wrong.driftMs, right.driftMs, '两个钟算出来的差值必须不同，否则这条判据恒真')
  })

  it('★ 音频时长量不到时，护栏输入里的 audioSec 必须是 null（不是 0）', () => {
    for (const bad of [undefined, null, 0, Number.NaN, -5]) {
      const g = resolveDurationGuardInput({
        elapsedMs: 60_000,
        nativeCapture: { used: true, windowMs: 60_000 },
        measuredAudioSec: bad as number | null | undefined,
      })
      assert.equal(g.audioSec, null, `${String(bad)} 被折成了 ${g.audioSec} ⇒ 「量不到」与「量为 0」混了`)
      assert.equal(assessDurationDrift(g.wallClockMs, g.audioSec).reason, 'no-ground-truth')
    }
  })

  it('★ 原生窗口为 0（还没有分片到）⇒ 判「量不到」，不许当成 0ms 的墙钟去比', () => {
    const g = resolveDurationGuardInput({
      elapsedMs: 60_000,
      nativeCapture: { used: true, windowMs: 0 },
      measuredAudioSec: 60,
    })
    assert.equal(g.source, 'native-untrusted')
    assert.equal(assessDurationDrift(g.wallClockMs, g.audioSec).reason, 'no-ground-truth')
  })

  it('★ nativeCapture 缺省 / used=false ⇒ 一律走 JS 墙钟（原生链没启用时）', () => {
    assert.equal(resolveDurationGuardInput({ elapsedMs: 1_000, measuredAudioSec: 1 }).source, 'js-wall-clock')
    assert.equal(
      resolveDurationGuardInput({ elapsedMs: 1_000, nativeCapture: { used: false, windowMs: null }, measuredAudioSec: 1 }).source,
      'js-wall-clock',
    )
  })
})

describe('§955 · 原生采集窗口的累积规则（纯函数）', () => {
  it('★ 随分片单调增长', () => {
    assert.equal(nativeCaptureWindowAfterPart(0, 8_000), 8_000)
    assert.equal(nativeCaptureWindowAfterPart(8_000, 16_000), 16_000)
    assert.equal(nativeCaptureWindowAfterPart(16_000, 24_000), 24_000)
  })

  // ⚠ 中途切过设备时原生时间轴**回卷**（新一轮从 0 开始）⇒ 这时不能把窗口变小，
  //   否则护栏会拿一个「倒退」的墙钟去比 ⇒ 读数毫无意义。
  it('★ 乱序/回卷的读数不得让窗口变小', () => {
    assert.equal(nativeCaptureWindowAfterPart(16_000, 4_000), 16_000)
  })

  it('★ 非法读数（NaN / Infinity / 负数）不得污染窗口', () => {
    for (const bad of [Number.NaN, Number.POSITIVE_INFINITY, -1]) {
      assert.equal(nativeCaptureWindowAfterPart(16_000, bad), 16_000,
        `${String(bad)} 污染了窗口`)
    }
  })

  it('★ 起点为 0 时也要能长起来（不是恒 0）', () => {
    assert.notEqual(nativeCaptureWindowAfterPart(0, 8_000), 0)
  })
})

// ══════════════════════════════════════════════════════════════════════════
// §956 · 原生分片的**会话时间轴**
//
// ── 缺陷链（每一步都在生产代码里可核对）──
//   1. `MeetingRecordService.loop()` 的 `captureStart` **每轮重取**
//      ⇒ 原生服务每重启一轮，轮内相对量就**从 0 重新开始**。
//   2. 中途切设备 ⇒ 原生服务重启一轮（`switchDevice` → `start(..., {resume:true})`
//      → `ACTION_START` → `startCapture()` 先 `stopCapture()`）。
//   3. `recordingRuntime` 把 `part.startMs/part.endMs` 直接当**分段时间范围**
//      ⇒ **回卷的时间戳进了库** ⇒ 三处下游受损：
//        ① 分段按 `startMs` 有序插入 ⇒ 后半场被排到**最前面**；
//        ② 相邻段**重叠** ⇒ 去重的**精确裁剪档**（§634/§683 认定生产不可达、
//           能吃掉正文的那一档）被打开；
//        ③ 整场重转的回执按时间重叠分桶 ⇒ 后半场的话挂到前半场说话人名下。
//
// ── 修法 ──
//   原生每片多报一个 `epochMs`（本轮采集原点的绝对墙钟）⇒ JS 侧据此把各轮
//   **精确**拼回同一根会话时间轴；拿不到（旧原生包）时**钳位保单调**：
//   时间轴是近似的，但**永不倒退** ⇒ ② 那条损伤不成立。
// ══════════════════════════════════════════════════════════════════════════

describe('§956 · 会话时间轴：换轮次必须精确重基', () => {
  /** 第一轮采集原点的绝对墙钟（毫秒）。 */
  const EPOCH_1 = 1_700_000_000_000
  /** 第二轮采集原点：真实空隙 800ms（切设备那一下的墙钟）。 */
  const EPOCH_2 = EPOCH_1 + 24_000 + 800

  /** 一轮的 3 片（生产形状：每片 8s，轮内相对量）。 */
  const roundOf = (epochMs: number, n = 3) =>
    Array.from({ length: n }, (_, i) => ({
      startMs: i * 8_000, endMs: (i + 1) * 8_000, epochMs,
    }))

  /** 把一串分片跑过重基，返回逐片结果。 */
  const runTimeline = (parts: Array<{ startMs: number; endMs: number; epochMs?: number }>) => {
    let st = createNativePartTimelineState()
    return parts.map((p) => {
      const r = rebaseNativePartTimeline(st, p)
      st = r.state
      return r
    })
  }

  it('★ 同一轮：原样透传（单轮原生链的行为一字未变）', () => {
    const out = runTimeline(roundOf(EPOCH_1))
    out.forEach((r, i) => {
      assert.equal(r.startMs, i * 8_000)
      assert.equal(r.endMs, (i + 1) * 8_000)
      assert.equal(r.clamped, false, '同一轮不该被钳位')
      assert.equal(r.state.degraded, false, '同一轮不该退化')
    })
  })

  it('★ 换轮次（带 epoch）：精确重基 ⇒ 真实空隙如实保留', () => {
    const out = runTimeline([...roundOf(EPOCH_1), ...roundOf(EPOCH_2)])
    assert.equal(out[2].endMs, 24_000, '第一轮结束于 24s')
    // 第二轮第一片：轮内 0 ⇒ 会话坐标 = (EPOCH_2 - EPOCH_1) = 24_800
    assert.equal(out[3].startMs, 24_800,
      '换轮次必须精确重基（保留 800ms 真实空隙），不能从 0 重新开始')
    assert.equal(out[3].rebased, true)
    assert.equal(out[3].state.degraded, false, '精确重基不算退化')
  })

  it('★ 回卷（拿不到 epoch，旧原生包）：钳位保单调 + 标记退化', () => {
    const out = runTimeline([
      { startMs: 0, endMs: 8_000 },
      { startMs: 8_000, endMs: 16_000 },
      { startMs: 0, endMs: 8_000 },   // 第二轮：轮内相对量从 0 重新开始 ⇒ 回卷
    ])
    assert.equal(out[2].rolledBack, true, '必须识别出时间轴倒退')
    assert.equal(out[2].clamped, true, '倒退必须被钳位')
    assert.equal(out[2].startMs, 16_000, '倒退的部分必须被拉回不与上一片重叠')
    assert.equal(out[2].state.degraded, true, '近似时间轴必须留痕')
  })

  it('★ 陈旧 epoch（比基准还早）：钳位 + 退化（不许算出天文数字偏移）', () => {
    let st = createNativePartTimelineState()
    st = rebaseNativePartTimeline(st, { startMs: 0, endMs: 8_000, epochMs: EPOCH_1 }).state
    const r = rebaseNativePartTimeline(st, { startMs: 0, endMs: 8_000, epochMs: EPOCH_1 - 60_000 })
    assert.ok(Number.isFinite(r.startMs) && r.startMs >= 0, '偏移必须有限且非负')
    assert.equal(r.startMs, 8_000, '陈旧 epoch 必须被钳位，不得倒退')
    assert.equal(r.state.degraded, true)
  })

  it('★ 非法 epoch（0 / NaN / 负数 / 缺失）不得产生荒谬偏移（0 = 1970 年，不是原点）', () => {
    for (const bad of [0, Number.NaN, -1, undefined]) {
      const r = rebaseNativePartTimeline(createNativePartTimelineState(),
        { startMs: 0, endMs: 8_000, epochMs: bad })
      assert.equal(r.startMs, 0, `${String(bad)} 被当成了原点`)
      assert.ok(Math.abs(r.startMs) < 1e6, `${String(bad)} 产生了荒谬偏移`)
    }
  })

  it('★ epoch=0 绝不能被当成原点（否则下一片的偏移是天文数字）', () => {
    let st = createNativePartTimelineState()
    st = rebaseNativePartTimeline(st, { startMs: 0, endMs: 8_000, epochMs: 0 }).state
    const r = rebaseNativePartTimeline(st, { startMs: 0, endMs: 8_000, epochMs: 1_700_000_000_000 })
    assert.ok(r.startMs < 1e6, `偏移爆炸了：${r.startMs}`)
  })

  it('★ 载荷里的 epoch 解析：缺失/非法一律 undefined（绝不当成 0）', () => {
    for (const bad of [undefined, null, 0, Number.NaN, -1, '', 'abc', true, {}]) {
      assert.equal(readNativeEpoch(bad), undefined,
        `${JSON.stringify(bad)} 被折成了一个合法 epoch`)
    }
    assert.equal(readNativeEpoch(1_700_000_000_000), 1_700_000_000_000)
    assert.equal(readNativeEpoch('1700000000000'), 1_700_000_000_000, '数字串也算数（桥可能给串）')
  })

  it('★ 不变式：任意序列输出恒单调、相邻不重叠（这是本模块存在的全部理由）', () => {
    const shapes = [
      [{ startMs: 0, endMs: 8_000 }, { startMs: 8_000, endMs: 16_000 }, { startMs: 0, endMs: 8_000 }],
      [{ startMs: 0, endMs: 8_000, epochMs: EPOCH_1 }, { startMs: 0, endMs: 8_000, epochMs: EPOCH_2 }],
      [{ startMs: 0, endMs: 8_000, epochMs: EPOCH_1 }, { startMs: 0, endMs: 8_000, epochMs: EPOCH_1 - 5_000 }],
      [{ startMs: 16_000, endMs: 24_000 }, { startMs: Number.NaN, endMs: Number.NaN }, { startMs: -100, endMs: -50 }],
    ]
    for (const shape of shapes) {
      let prevEnd = 0
      for (const r of runTimeline(shape)) {
        assert.ok(Number.isFinite(r.startMs) && Number.isFinite(r.endMs), '产生了非有限时间')
        assert.ok(r.startMs >= prevEnd, `时间轴倒退了：${r.startMs} < ${prevEnd}`)
        assert.ok(r.endMs >= r.startMs, 'endMs 必须 ≥ startMs')
        prevEnd = r.endMs
      }
    }
  })

  it('★ 退化留痕文案必须说清「近似」与「不会被裁掉正文」', () => {
    const r = rebaseNativePartTimeline(
      { baseEpochMs: null, lastEndMs: 8_000, degraded: false },
      { startMs: 0, endMs: 8_000 },
    )
    const msg = describeNativeTimelineDegradation(r, { startMs: 0, endMs: 8_000 })
    assert.match(msg, /退化/)
    assert.match(msg, /钳位/)
    assert.match(msg, /不重叠/)
  })
})

describe('§956 · 端到端：回卷会打开「能吃掉正文」的精确裁剪档，重基后必须不可达', () => {
  it('★ 负控：回卷的原始时间轴确实让相邻段重叠（§634 的不变量被打破）', () => {
    // 第一轮第二片结束于 16_000；第二轮第一片轮内从 0 开始 ⇒ 落回第一轮里面。
    assert.equal(likelyAudioOverlap({ prevEndMs: 16_000, nextStartMs: 0 }), true,
      '负控：回卷的原始时间轴确实重叠 —— 否则下面那条判据恒真')
  })

  it('★ 重基后相邻段不重叠 ⇒ 精确裁剪档不可达（§634/§683 的结论继续成立）', () => {
    let st = createNativePartTimelineState()
    const parts = [
      { startMs: 0, endMs: 8_000, epochMs: 1_700_000_000_000 },
      { startMs: 8_000, endMs: 16_000, epochMs: 1_700_000_000_000 },
      { startMs: 0, endMs: 8_000, epochMs: 1_700_000_000_000 + 16_800 },
    ]
    const segs = parts.map((p) => {
      const r = rebaseNativePartTimeline(st, p)
      st = r.state
      return { startMs: r.startMs, endMs: r.endMs }
    })
    for (let i = 1; i < segs.length; i++) {
      assert.equal(
        likelyAudioOverlap({ prevEndMs: segs[i - 1].endMs, nextStartMs: segs[i].startMs }),
        false,
        `第 ${i} 对相邻段重叠了 ⇒ 去重的精确裁剪档（§634：能吃掉正文）被打开`,
      )
    }
  })

  it('★ 正文不许被吃掉：用户真的一句重复（「好的」+「好的，我继续」）必须原样保留', () => {
    const prev = '好的'
    const next = '好的，我继续'
    // 时间轴判为重叠 ⇒ 走**精确**档 ⇒ 前缀被裁（用户真说的话被吃掉）
    const rolled = dedupeStep({ prev, raw: next, prevEndMs: 16_000, startMs: 0 })
    assert.equal(rolled.text, '，我继续', '负控：重叠档确实把「好的」吃掉了')
    // 不重叠 ⇒ 走**保守**档 ⇒ 原样保留
    const ok = dedupeStep({ prev, raw: next, prevEndMs: 16_000, startMs: 16_000 })
    assert.equal(ok.text, '好的，我继续', '不重叠时不得吃掉用户真说的话')
  })
})

describe('§956 · 接线层（源码扫描，⚠ 零牙已如实标注）', () => {
  const runtime = () => stripComments(read('../../../native/recordingRuntime.ts'))

  it('★ 生产者必须走那个**纯函数**（否则真件与被测的本体又分家了）', () => {
    const s = runtime()
    assert.match(s, /from '\.\.\/features\/meetings\/native-part-timeline'/,
      'runtime 没 import 那个纯模块')
    assert.equal((s.match(/rebaseNativePartTimeline\(/g) ?? []).length, 1,
      '必须恰好调用一次那个纯函数（多处调用 ⇒ 形状会漂）')
  })

  it('★ 喂给 processSegment 的必须是**重基后**的时间，不是原生的轮内相对量', () => {
    const s = runtime()
    const m = /processSegment\(\s*nativePartToBlob\(part\)\s*,\s*(\w+)\.startMs\s*,\s*(\w+)\.endMs\s*\)/.exec(s)
    assert.ok(m, '找不到原生分片喂给 processSegment 的调用点')
    assert.notEqual(m[1], 'part', '必须用重基结果的时间，不能直接用 part.startMs（那正是回卷的源头）')
    assert.notEqual(m[2], 'part', '同上')
  })

  it('★ 新的一场必须重置时间轴；切设备（resume）绝不能清', () => {
    const s = runtime()
    const fi = s.indexOf('if (freshSession) {')
    const ti = s.indexOf('this.nativeTimeline = createNativePartTimelineState()')
    assert.ok(fi > 0, '找不到 freshSession 分支')
    assert.ok(ti > fi, '时间轴的重置必须在 freshSession 分支里（新的一场才有新的原点）')
    assert.equal((s.match(/this\.nativeTimeline = createNativePartTimelineState\(\)/g) ?? []).length, 1,
      '时间轴只许在 freshSession 里重置一次（切设备走 resume ⇒ 清了就等于回卷）')
  })
})

