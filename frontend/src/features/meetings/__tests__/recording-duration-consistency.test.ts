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
  wavDurationSec,
} from '../recording-duration-consistency.ts'

/** 按生产形状造一段 16k WAV：44 字节头 + n 帧 × 2 字节。 */
const wavOf = (seconds: number): ArrayBuffer => new ArrayBuffer(44 + Math.round(seconds * 16000) * 2)

describe('wavDurationSec —— 从字节算真实时长（不依赖浏览器）', () => {
  it('§785 的真实形态：62s 墙钟 / 57.36s 音频 ⇒ 读出 57.36s', () => {
    // 917760 帧 @16k = 57.36s，正是 §785 记下的帧数
    const wav = new ArrayBuffer(44 + 917760 * 2)
    assert.equal(wavDurationSec(wav), 57.36)
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
  it('量不到时返回 null 而不是 0（空/过短/字节数不对齐）', () => {
    assert.equal(wavDurationSec(new ArrayBuffer(0)), null)
    assert.equal(wavDurationSec(new ArrayBuffer(44)), null, '只有头没有数据 ⇒ 量不到')
    assert.equal(wavDurationSec(new ArrayBuffer(40)), null, '连头都不完整 ⇒ 量不到')
    assert.equal(wavDurationSec(new ArrayBuffer(44 + 3)), null, '数据字节数不对齐 2 的倍数')
  })

  it('⛔ 不许把「量不到」折成 0 秒', () => {
    // 显式钉住这条：它是本模块唯一容易写错、且错了最难发现的地方
    for (const bad of [new ArrayBuffer(0), new ArrayBuffer(44), new ArrayBuffer(44 + 3)]) {
      assert.notEqual(wavDurationSec(bad), 0)
    }
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
