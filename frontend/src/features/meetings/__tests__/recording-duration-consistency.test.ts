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
  resolveWallClockDurationMs,
  wavDurationSec,
} from '../recording-duration-consistency.ts'

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

  // ⚠ 量不到真值 ⇒ 判 ok 且 reason=no-ground-truth。这是 §803.4 那个静默缺口
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
