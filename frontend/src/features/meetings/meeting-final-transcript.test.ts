// meeting-final-transcript.test.ts — 门禁：收尾必须**先全量重转再精校**。
//
// ── 这道门守的是什么（2026-10-06 用户实测反馈「识别错误率高」）──
//
// 修复前：收尾直接把**分段累积的错文本**丢给 LLM 润色。
// 修复后：先从 IndexedDB 取回完整音频 → 整段重转（整段上下文 = 最高准确率）
//         → 再把高精度文本交给 LLM 做它擅长的事（分段/去口水词/标点）。
//
// ★ 顺序是本质，不能倒过来：LLM 修不了同音字/专有名词/数字这类**识别**错误，
//   因为它只能看到已经错了的字符。一旦精校先跑（或根本不重转），
//   错字就永久留在结果里，用户看到的还是「识别错误率高」。
//
// 分三层，每层都带负控：
//   A 采纳判定：重转结果明显更差时必须**拒绝**采用。
//   B 接线：stop() 必须真的调 refetchFullTranscript，且在 refine **之前**。
//   C 降级路径：没有录音/请求失败时必须**保留**分段文本，绝不清空。
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import { resolveUploadTarget, shouldAdoptFullTranscript, refetchFullTranscript } from './meeting-final-transcript.ts'
import {
  planWavChunks, sliceWavChunk, parseWavLayout, mergeChunkedOutcomes,
  FULL_TRANSCRIBE_CHUNK_SECONDS, FULL_TRANSCRIBE_CUT_SEARCH_SECONDS,
  MIN_CHUNK_SECONDS,
} from './meeting-full-transcribe-chunks.ts'

const HERE = dirname(fileURLToPath(import.meta.url))
const read = (f: string) => readFileSync(join(HERE, f), 'utf8')

/**
 * ★ 剥注释但**保持长度**，这样 indexOf 量到的仍是同一篇源码的相对位置。
 *
 * 为什么必须剥（§49 的又一次现实验证）：共享收尾模块
 * `meeting-recording-finalize.ts` 的**头注释里**为了说明两条录音路径，
 * 写了 `` `useSessionLiveRecord.stop()` → `meetingsApi.refine(...)` ``。
 * 于是 `src.indexOf('meetingsApi.refine(')` 命中的是**注释里那句说明**
 * （下标 170），而不是真调用点（下标 ~5000）⇒「重转必须先于精校」这条判据
 * **误报红**，报的是「顺序反了」。
 *
 * ★ 教训复述：源码扫描的判据若不剥注释，会把**说明文字**当成**代码**。
 *   注释里提到一个调用点，就足以让顺序类判据彻底失灵，而且失败形态
 *   与「产品真的写反了」一模一样。
 */
const blank = (m: string) => m.replace(/[^\n]/g, ' ')
const stripComments = (t: string) => t
  .replace(/\/\*[\s\S]*?\*\//g, blank)
  .split('\n').map((l) => { const i = l.indexOf('//'); return i < 0 ? l : l.slice(0, i) }).join('\n')

const seg = (text: string) => [{ text }] as Array<{ text: string }>

describe('A · 采纳判定：重转更差时必须拒绝', () => {
  it('正常情况采用（整段文本不短于分段的一半）', () => {
    const segs = seg('今天下午三点开项目评审会'.repeat(6))
    const full = segs[0].text + '另外还有两个待确认事项'
    const r = shouldAdoptFullTranscript(full, segs)
    assert.equal(r.adopt, true, `应采用，实际拒绝：${r.reason}`)
  })

  it('重转结果掉一半以上 ⇒ 拒绝（多半被上游截断）', () => {
    const segs = seg('这是一段很长的会议转写内容'.repeat(10))
    const truncated = '这是一段很长的会议转写内容' // 只有 1/10
    const r = shouldAdoptFullTranscript(truncated, segs)
    assert.equal(r.adopt, false, '明显变短的结果不应被采用')
    assert.match(r.reason, /too-short/)
  })

  it('负控：旧实现（无脑采用）必须被上面那条判红', () => {
    const segs = seg('内容'.repeat(100))
    const full = '内容'
    // 旧行为 = 只要非空就采用 ⇒ 分段 200 字变成 2 字，用户看到内容蒸发。
    const legacyWouldAdopt = full.trim().length > 0
    assert.equal(legacyWouldAdopt, true, '负控：旧实现会采用')
    assert.equal(shouldAdoptFullTranscript(full, segs).adopt, false, '新实现必须拒绝')
  })

  it('空文本拒绝；分段为空时直接采用', () => {
    assert.equal(shouldAdoptFullTranscript('', seg('有内容')).adopt, false)
    assert.equal(shouldAdoptFullTranscript('   ', seg('有内容')).adopt, false)
    assert.equal(shouldAdoptFullTranscript('整段内容', []).adopt, true, '分段为空时应采用')
  })
})

describe('B · 接线层：stop() 必须先重转再精校（防回退）', () => {
  const src = () => stripComments(read('./meeting-recording-finalize.ts'))

  it('stop() 必须调用 refetchFullTranscript', () => {
    assert.match(src(), /refetchFullTranscript\(/, 'stop() 未做全量重转 —— 识别错误率不会有任何改善')
  })

  // ★ 这三条是本轮补上的**关键断言**。第一版实现是「把 IndexedDB 里的 webm
  //   blob 直接 POST 给 /api/meetings/{id}/transcribe」，它有两条硬限制：
  //     1. 上游单次时长：智谱 30s / OpenRouter ~60s / MiniMax 500s
  //        （backend/internal/stt/target.go 的 MaxSeconds）
  //        ⇒ 超过 8 分 20 秒的会议**必然失败**，而长会议恰是最需要精校的；
  //     2. 格式：真机录的是 webm，网关只收 mp3/wav。
  //   而只断言「有调 refetchFullTranscript」的话，退回那个实现门禁照样全绿。
  it('★ 必须走 transcribeFull（自带静音边界切段 + 长录音支持），不能裸传 webm', () => {
    const s = src()
    assert.match(
      s, /sttSettingsApi\.transcribeFull\(/,
      '收尾重转未走 transcribeFull —— 长录音会因超上游单次时长上限而失败',
    )
    assert.ok(
      !/api\/meetings\/\$\{meetingId\}\/transcribe/.test(s),
      '仍在裸传音频给 /api/meetings/{id}/transcribe（单次请求、无切段，会超时长上限）',
    )
  })

  it('★ 必须透出「有 N 段没转出来」（transcribeFull 的 failed 计数）', () => {
    assert.match(
      read('./meeting-final-transcript.ts'), /failedSegments/,
      '未透出失败段数 —— 用户会以为记录是完整的',
    )
    assert.match(src(), /failedSegments/, '调用方未处理失败段数')
  })

  it('★ 调用顺序必须是 重转 在 精校 之前', () => {
    const s = src()
    const iFull = s.indexOf('refetchFullTranscript(')
    const iRefine = s.indexOf('meetingsApi.refine(')
    assert.ok(iFull > 0, '缺少 refetchFullTranscript 调用')
    assert.ok(iRefine > 0, '缺少 meetingsApi.refine 调用')
    assert.ok(
      iFull < iRefine,
      '顺序反了：必须先整段重转拿高精度文本，再交给 LLM 精校。' +
      '反过来 LLM 只能润色已经错了的字符，修不了同音字/数字错误。',
    )
  })

  it('★ 必须经过 shouldAdoptFullTranscript 判定才采用（不得无脑替换）', () => {
    // ⚠ §52：采纳判定已移进纯决策层 meeting-refine-plan.ts（收尾层只做 I/O）
    assert.match(
      read('./meeting-refine-plan.ts'),
      /shouldAdoptFullTranscript\(/,
      '未做采纳判定 —— 上游截断时用户会看到内容变少',
    )
  })

  it('负控：顺序写反时判据必须能报红（防止判据恒真）', () => {
    // 构造一个「精校在前、重转在后」的样本，确认 iFull < iRefine 这条会失败。
    const badSample = `
      const result = await meetingsApi.refine(id, segs)
      const finalText = await refetchFullTranscript(id, segs)
    `
    const iFull = badSample.indexOf('refetchFullTranscript(')
    const iRefine = badSample.indexOf('meetingsApi.refine(')
    assert.ok(iFull > iRefine, '负控：顺序写反的样本应让 iFull < iRefine 断言失败')
  })
})

describe('C · 降级路径：重转失败绝不能清空结果', () => {
  it('refetchFullTranscript 在无音频/失败时返回 applied:false 而非抛错', () => {
    const s = read('./meeting-final-transcript.ts')
    // 所有失败分支都必须是「返回降级结果」，不能 throw。
    assert.match(s, /applied: false, reason:/, '缺少降级返回')
    // 不得有裸 throw 把异常抛给 stop()（那会让整个收尾流程中断）。
    assert.ok(!/\bthrow new\b/.test(s), '重转函数不应抛异常——它只是增强，失败应降级')
  })

  it('降级时用回分段文本（不丢内容）', () => {
    const s = read('./meeting-recording-finalize.ts')
    // adopt 为假时 baseSegments 必须回落到 segs。
    assert.match(s, /: segs\b/, '降级路径必须回落到原始 segments')
  })

  it('必须释放 objectURL（录音几十 MB，泄漏会拖垮长录音）', () => {
    const s = read('./meeting-final-transcript.ts')
    assert.match(s, /revokeObjectURL/, '未释放 audioUrl，长录音会持续占用内存')
  })
})

describe('F · 行为层：转码决策（真断言，不是「文件里有这个词」）', () => {
  // ★ 这组替代了原先的文本断言。文本断言的失败形态是「字符串还在、逻辑没了」：
  //   把 `if (opts?.toWav)` 改成 `if (false)`，文件里仍有 "toWav" 字样，
  //   /toWav/ 照样匹配 ⇒ 门禁绿而转码功能没了（2026-10-06 实测）。
  //
  // resolveUploadTarget 之所以被抽成纯函数：refetchFullTranscript 第一步就是
  // loadMeetingAudio（浏览器 IndexedDB），node 里必然失败 ⇒ 整条链路在单测中
  // 只能验到「降级不抛」，转码分支永远测不到。

  it('有 wav 时必须转成 audio/wav 并改用 .wav 文件名', () => {
    const webm = new Blob([new Uint8Array([0x1a, 0x45, 0xdf, 0xa3])], { type: 'audio/webm' })
    const wavBytes = new ArrayBuffer(64)
    const t = resolveUploadTarget(webm, wavBytes)
    assert.equal(t.blob.type, 'audio/wav', '转码后必须是 audio/wav（网关只收 mp3/wav）')
    assert.equal(t.filename, 'meeting.wav', '后端按扩展名决定能否切分，必须用 .wav')
    assert.notEqual(t.blob, webm, '转码后不能还把原 webm 传上去')
  })

  it('无 wav（转码失败/未提供）时退回原 blob，但保持原扩展名', () => {
    const webm = new Blob([new Uint8Array([0x1a, 0x45, 0xdf, 0xa3])], { type: 'audio/webm' })
    const t = resolveUploadTarget(webm, null)
    assert.equal(t.filename, 'meeting.webm', '退回时应按真实 MIME 推导扩展名')
    assert.equal(t.blob.type, 'audio/webm')
  })

  it('负控：m4a 录音退回时扩展名是 .m4a（不能一律 webm）', () => {
    const m4a = new Blob([new Uint8Array([0, 0, 0, 0x20])], { type: 'audio/mp4' })
    assert.equal(resolveUploadTarget(m4a, null).filename, 'meeting.m4a')
  })
})

// ─────────────────────────────────────────────────────────────────────────
// §818 · 转码失败必须**留痕**：此前它是空 catch 块，后果在 `reason` 里只剩
//        `empty-transcript`，根因丢失；而唯一生产调用方不读 `reason`
//        ⇒ 整条失败链在生产里没有任何出口。
//
// 这组是**行为**断言（不是「文件里有这个词」）：`refetchFullTranscript` 有
// `loadAudio` 注入口，node 里能真跑起来，所以「抛错的 toWav 会不会被记下来」
// 是可执行的断言。
describe('G · §818 转码失败必须留痕（行为层）', () => {
  const webm = () => new Blob([new Uint8Array([0x1a, 0x45, 0xdf, 0xa3])], { type: 'audio/webm' })
  const loadAudio = async () => webm()

  it('toWav 抛错 ⇒ 必须带 transcodeFailed:true 与原始异常信息', async () => {
    const r = await refetchFullTranscript('m1', async () => ({ text: 'x', failed: 0 }), {
      loadAudio,
      toWav: async () => { throw new Error('ENCODING_FAILED') },
    })
    assert.equal(r.transcodeFailed, true, '转码失败没有被记下来 —— §818 的洞没修')
    assert.match(String(r.transcodeError), /ENCODING_FAILED/, '原始异常信息丢了，无法定位是解码失败还是缺 AudioContext')
  })

  it('★ 降级行为不变：转码失败仍退回原 blob 上传，且不抛错', async () => {
    let seenType = ''
    let seenName = ''
    const r = await refetchFullTranscript('m1', async (b, f) => {
      seenType = b.type; seenName = f
      return { text: 'x', failed: 0 }
    }, { loadAudio, toWav: async () => { throw new Error('ENCODING_FAILED') } })
    // 这条不能被「让失败可见」改掉：降级是刻意的（§50/§51 的既有行为）
    assert.equal(seenType, 'audio/webm', '转码失败后必须仍退回原容器（不比现状差）')
    assert.equal(seenName, 'meeting.webm')
    assert.equal(r.applied, true)
  })

  it('★ 转码失败 + 网关拒收（退回 webm 后返回空文本）⇒ 根因仍在', async () => {
    const r = await refetchFullTranscript('m1', async () => ({ text: '', failed: 0 }), {
      loadAudio,
      toWav: async () => { throw new Error('ENCODING_FAILED') },
    })
    assert.equal(r.reason, 'empty-transcript')
    assert.equal(r.applied, false)
    assert.equal(r.transcodeFailed, true,
      '此时 reason 只是下游症状（网关拒收），根因「转码失败」必须单独可读，否则无法定位')
  })

  it('★ 转码失败后连上传都抛了 ⇒ transcodeFailed 不能被 transcribe-failed 盖掉', async () => {
    const r = await refetchFullTranscript('m1', async () => { throw new Error('400 input_audio.format') }, {
      loadAudio,
      toWav: async () => { throw new Error('ENCODING_FAILED') },
    })
    assert.match(String(r.reason), /^transcribe-failed/)
    assert.equal(r.transcodeFailed, true, '更早的根因被下游症状覆盖了')
    assert.match(String(r.transcodeError), /ENCODING_FAILED/)
  })

  it('转码失败但侥幸被接受（applied:true）⇒ 仍要透出这次失败', async () => {
    const r = await refetchFullTranscript('m1', async () => ({ text: '侥幸成功', failed: 0 }), {
      loadAudio,
      toWav: async () => { throw new Error('ENCODING_FAILED') },
    })
    assert.equal(r.applied, true)
    assert.equal(r.transcodeFailed, true, '成功掩盖了这次转码失败 —— 下次同样输入会静默劣化')
  })

  it('负控：转码成功时不得凭空出现 transcodeFailed（形状不能变）', async () => {
    const r = await refetchFullTranscript('m1', async () => ({ text: '正常', failed: 0 }), {
      loadAudio,
      toWav: async () => new ArrayBuffer(64),
    })
    assert.equal('transcodeFailed' in r, false, '成功路径被塞了多余字段')
    assert.equal('transcodeError' in r, false)
  })

  it('负控：压根没传 toWav（未启用转码）≠ 转码失败', async () => {
    const r = await refetchFullTranscript('m1', async () => ({ text: 'x', failed: 0 }), { loadAudio })
    assert.equal('transcodeFailed' in r, false, '「没传 toWav」是正常配置，不该被报成失败')
  })
})

// ─────────────────────────────────────────────────────────────────────────
// §927 · 整场重转**分块**：长会议不该被腰斩
//
// ── 这道门守的是什么 ──
// 收尾的整场重转原先是**一次**整场调用。两道墙都在服务端：
//
//   ① **时间墙**：服务端 `handleSttTranscribeFull` 是
//      `context.WithTimeout(r.Context(), fullTranscribeTimeout)`，
//      `fullTranscribeTimeout = 10 * time.Minute`
//      （`backend/internal/server/server_stt_stream.go:39`）
//      ⇒ **整个请求**（含逐段串行转写）只有 10 分钟预算。
//      段数由服务端的静音切段决定（上限 25 s，§923 实测 3.03–10.5 段/分钟）
//      ⇒ 1 小时会议每次上游调用只剩 0.95–3.30 s、4 小时只剩 0.24–0.83 s
//      ⇒ **腰斩 ⇒ 整场重转白跑**（用户拿回的是段级文本）。
//
//   ② **字节墙**（§923 没记，本节补）：全量端点走 **JSON base64**，
//      服务端解码后音频上限 `maxFullAudioBytes = 400 << 20`（= 400 MiB）
//      ⇒ 16 kHz 单声道 16-bit = 32 000 B/s ⇒ 只够 **3.55 小时**
//      ⇒ 超过 3.55 小时的会议，请求**压根发不出去**（不是超时，是没有结果）。
//
// ⇒ 修法（**服务端零改动**）：客户端按静音边界切块，逐块各发一次
//   ⇒ 每块各拿一份 10 分钟预算（时间墙按块摊薄）
//   ⇒ 每块请求体恒定十几 MB（字节墙与会议时长**脱钩**）。
//
// ⇒ 这道门钉的是**性质**（字节不丢、块合法、切在静音、失败形态不变），
//   不是「文件里有没有某个词」。
/**
 * 独立实现：某点处**居中 100 ms** 窗口的 RMS。
 *
 * ⚠ 为什么必须居中：切点质量取决于**两侧**是否安静 ——
 *   只看一侧的话，切点可能正好落在词尾，词照样被劈开。
 * ⚠ 门槛 0.012 = 服务端 `silenceFloor`（`backend/internal/stt/full.go:109`）。
 *
 * ⚠⚠ **§928**：这个门槛只在**合成夹具**上可判（真静音 = 采样全 0）。
 * 真实会议录音里 100 ms 居中窗低于 0.012 的**只占 11.0%**
 * ⇒ 真实可断言的命题是「切点比**硬切**安静」。
 */
const centeredRms = (wav: ArrayBuffer, sec: number): number => {
  const bytes = new Uint8Array(wav)
  const dv = new DataView(wav)
  const sampleRate = dv.getUint32(24, true)
  const half = Math.round(0.05 * sampleRate)
  const n = Math.round(0.1 * sampleRate)
  const start = Math.round(sec * sampleRate) - half
  let sum = 0
  for (let f = start; f < start + n; f++) {
    const v = dv.getInt16(44 + f * 2, true) / 32768
    sum += v * v
  }
  return Math.sqrt(sum / n)
}

/** 静音门槛（= 服务端 `silenceFloor`）。 */
const SILENCE_RMS = 0.012

/**
 * 块长**上界**（秒）：任何一个块都不会超过这个长度。
 *
 * ⚠⚠ **块长 ≠ 块长参数**（§928.6）：切点是在「目标点 ± 搜索窗」里**吸附**出来的 ⇒
 *   - 相邻两切点之差最大 = `块长 + 2×搜索窗`；
 *   - **尾块**还能多出一个「最小块长」（网格点离文件尾太近时循环直接 break）。
 * ⇒ 上界 = `max(块长 + 2×搜索窗, 块长 + 最小块长)`。
 *
 * ⚠ 这个公式写在**判据侧**（而不是生产模块里导出一个函数）——
 * 因为那个函数只被测试引用 ⇒ 是**死能力** ⇒ `check:dead-features` 会红（§928.11 的教训）。
 * ⇒ 于是「公式」与「行为」必须**互相钉住**：
 *   ① 这条公式算出来的上界要**钉住实际块长**（I 组那两条）；
 *   ② 默认参数下的上界要**钉住文档里的那个数**（下面那条断言）⇒
 *      改了 `FULL_TRANSCRIBE_CHUNK_SECONDS` / 搜索窗 / 最小块长 ⇒
 *      文档 §928.6 的「540 s / 6.3 s」必须同步改，否则当场红。
 */
const chunkLengthBound = (chunkSeconds: number, searchSeconds: number, minChunkSeconds: number): number =>
  Math.max(chunkSeconds + 2 * searchSeconds, chunkSeconds + minChunkSeconds)

  /**
 * 造一段 WAV：每 `speechEvery` 秒的开头 `speechLen` 秒是 220 Hz 正弦，
 * 其余是**真静音**（采样值全 0）。
 *
 * 为什么用「真静音」而不是「很轻的底噪」：静音处窗口 RMS 精确为 0，
 * 语音处 ≈ amp/√2 ⇒ 两档可分 ⇒ 判据能用「RMS < 0.012」判定「切在静音里」。
 */
const buildWav = (seconds: number, opts?: { speechEvery?: number; speechLen?: number; amp?: number }): ArrayBuffer => {
  const sampleRate = 16000
  const speechEvery = opts?.speechEvery ?? 3
  const speechLen = opts?.speechLen ?? 1
  const amp = opts?.amp ?? 0.3
  const frames = Math.round(seconds * sampleRate)
  const data = new Uint8Array(frames * 2) // 全 0 ⇒ 静音
  const dv = new DataView(data.buffer)
  for (let startSec = 0; startSec < seconds; startSec += speechEvery) {
    const from = Math.round(startSec * sampleRate)
    const to = Math.min(frames, Math.round((startSec + speechLen) * sampleRate))
    for (let f = from; f < to; f++) {
      const t = f / sampleRate
      dv.setInt16(f * 2, Math.round(Math.sin(2 * Math.PI * 220 * t) * amp * 32767), true)
    }
  }
  const buffer = new ArrayBuffer(44 + data.length)
  const out = new DataView(buffer)
  const w = (o: number, s: string) => { for (let i = 0; i < s.length; i++) out.setUint8(o + i, s.charCodeAt(i)) }
  w(0, 'RIFF'); out.setUint32(4, 36 + data.length, true); w(8, 'WAVE')
  w(12, 'fmt '); out.setUint32(16, 16, true); out.setUint16(20, 1, true); out.setUint16(22, 1, true)
  out.setUint32(24, sampleRate, true); out.setUint32(28, sampleRate * 2, true)
  out.setUint16(32, 2, true); out.setUint16(34, 16, true)
  w(36, 'data'); out.setUint32(40, data.length, true)
  new Uint8Array(buffer, 44).set(data)
  return buffer
}

/** 通用夹具：30 秒「每 3 秒 1 秒正弦 + 其余真静音」。 */
const WAV = buildWav(30)

describe('H · §927 整场重转分块（长会议不再被腰斩）', () => {
  // ── 夹具：16 kHz 单声道 16-bit PCM WAV（客户端 `toWav` 的真实产物形态）──


  /** 独立解析：取 data 载荷（**不复用**被测模块的解析器，否则判据自证）。 */
  const dataPayload = (wav: ArrayBuffer): Uint8Array => {
    const bytes = new Uint8Array(wav)
    const dv = new DataView(wav)
    assert.equal(
      String.fromCharCode(bytes[0], bytes[1], bytes[2], bytes[3]), 'RIFF',
      '夹具不是 RIFF —— 量具坏了',
    )
    return bytes.subarray(44, 44 + dv.getUint32(40, true))
  }

  const sameBytes = (a: Uint8Array, b: Uint8Array): boolean => {
    if (a.length !== b.length) return false
    for (let i = 0; i < a.length; i++) if (a[i] !== b[i]) return false
    return true
  }

  const loadAudio = async () => new Blob([new Uint8Array([0x1a, 0x45, 0xdf, 0xa3])], { type: 'audio/webm' })
  const toWav = async () => WAV

  // ── ① 切分本身 ──

  it('★ 切分后 PCM 必须逐字不变（不丢、不重、不乱序）', () => {
    const plan = planWavChunks(WAV, { chunkSeconds: 5, searchSeconds: 1.5, minChunkSeconds: 1 })
    assert.ok(plan, '30 秒 / 5 秒一块 ⇒ 必须能切')
    assert.ok(plan.bounds.length >= 2, '只切出一块 ⇒ 等于没切')
    const joined: number[] = []
    for (let i = 0; i < plan.bounds.length; i++) {
      const chunk = sliceWavChunk(WAV, plan, i)
      for (const b of dataPayload(chunk)) joined.push(b)
    }
    const orig = dataPayload(WAV)
    assert.equal(joined.length, orig.length, `总字节数变了：${joined.length} vs ${orig.length}`)
    assert.ok(sameBytes(Uint8Array.from(joined), orig), 'PCM 逐字比对失败 ⇒ 切分改动了音频')
  })

  it('★ 每块都必须是合法 WAV（RIFF/data 长度自洽、采样率不变、能被再解析）', () => {
    const plan = planWavChunks(WAV, { chunkSeconds: 5, searchSeconds: 1.5, minChunkSeconds: 1 })!
    for (let i = 0; i < plan.bounds.length; i++) {
      const chunk = sliceWavChunk(WAV, plan, i)
      const bytes = new Uint8Array(chunk)
      const dv = new DataView(chunk)
      const layout = parseWavLayout(bytes)
      assert.ok(layout, `第 ${i} 块解析失败 ⇒ 切出来的东西服务端认不出`)
      assert.equal(dv.getUint32(4, true) + 8, bytes.length, `第 ${i} 块 RIFF 长度不自洽`)
      assert.equal(dv.getUint32(layout.dataOffset - 4, true), layout.dataLength, `第 ${i} 块 data 长度不自洽`)
      assert.equal(layout.sampleRate, 16000, `第 ${i} 块采样率被改了`)
      assert.equal(layout.channels, 1, `第 ${i} 块声道数被改了`)
    }
  })

  it('★ 每个切点都必须落在**夹具的**静音里（⚠ 合成夹具性质，真实录音上不成立 ⇒ 见 §928）', () => {
    // ⚠⚠ 这条判据只在**合成夹具**上成立（真静音 = 采样全 0 ⇒ 两档可分）。
    // §928 用 12.54 min 真人会议语料实测：100 ms 居中窗低于 0.012 的**只占 11.0%**
    // ⇒ 真实录音上「切点落在静音里」**不是可达命题**，
    // 可断言的命题是「**切点比硬切安静**」（I 组那条，无真静音夹具）。
    // ⇒ 保留这条是为了守住「搜索确实在找安静处」这件事（合成夹具上可精确判定）。
    const plan = planWavChunks(WAV, { chunkSeconds: 5, searchSeconds: 1.5, minChunkSeconds: 1 })!
    assert.ok(plan.cutPointsMs.length >= 1, '没有切点 ⇒ 这条判据是恒真的')
    for (const ms of plan.cutPointsMs) {
      const rms = centeredRms(WAV, ms / 1000)
      assert.ok(rms < SILENCE_RMS, `切点 ${ms.toFixed(0)}ms 的窗口 RMS=${rms.toFixed(5)} ⇒ 不在静音里（词会被劈开）`)
    }
  })

  it('★ 量具自证：硬切目标里必须有落在语音里的（否则「切在静音」这条判据形同虚设）', () => {
    // 负控：把切点退回「块长的整数倍」（= 不做静音搜索的硬切）会切在哪？
    const targets: number[] = []
    for (let s = 5; s < 30; s += 5) targets.push(s)
    const loud = targets.filter((s) => centeredRms(WAV, s) >= SILENCE_RMS)
    assert.ok(loud.length >= 1,
      '夹具不够对抗：所有硬切目标都恰好落在静音里 ⇒ 「切在静音」这条判据证明不了静音搜索起了作用')
  })

  it('★ plan 不得持有块字节（块必须现切现用 ⇒ 内存不随块数增长）', () => {
    const plan = planWavChunks(WAV, { chunkSeconds: 5, searchSeconds: 1.5, minChunkSeconds: 1 })!
    const held: string[] = []
    const walk = (v: unknown, path: string): void => {
      if (v instanceof ArrayBuffer) { if (v !== WAV) held.push(path); return }
      if (ArrayBuffer.isView(v)) { if (v.buffer !== WAV) held.push(path); return }
      if (Array.isArray(v)) { v.forEach((x, i) => walk(x, `${path}[${i}]`)); return }
      if (v && typeof v === 'object') {
        for (const [k, x] of Object.entries(v)) walk(x, `${path}.${k}`)
      }
    }
    walk(plan, 'plan')
    assert.deepEqual(held, [], `plan 持有了音频副本：${held.join(', ')} —— 一次切完会让 8 小时会议的内存翻倍`)
  })

  it('★ 量具自证：切点必须选**真实最安静**的一段（负半周的小幅样本是安静的，不是响的）', () => {
    // 造一段**没有零**的音频（零会把两套口径的差别抹平）：
    //   A 段 = 恒定 -100 ⇒ 真实 RMS ≈ 0.003（安静）
    //            但**无符号**读法会读成 (65536-100)/32768 ≈ 2.0（响）
    //   B 段 = 恒定 +10000 ⇒ 真实 RMS ≈ 0.305（响），两套口径一致
    // ⇒ 正确的度量必然把切点选在 A 段（< 5 s）；
    //   无符号读法会把 A 当成最响 ⇒ 切点选到 B 段（≥ 10 s）⇒ 判红。
    const seconds = 20
    const sampleRate = 16000
    const frames = seconds * sampleRate
    const data = new Uint8Array(frames * 2)
    const dv = new DataView(data.buffer)
    for (let f = 0; f < frames; f++) {
      dv.setInt16(f * 2, f < frames / 2 ? -100 : 10000, true)
    }
    const buffer = new ArrayBuffer(44 + data.length)
    const out = new DataView(buffer)
    const w = (o: number, s: string) => { for (let i = 0; i < s.length; i++) out.setUint8(o + i, s.charCodeAt(i)) }
    w(0, 'RIFF'); out.setUint32(4, 36 + data.length, true); w(8, 'WAVE')
    w(12, 'fmt '); out.setUint32(16, 16, true); out.setUint16(20, 1, true); out.setUint16(22, 1, true)
    out.setUint32(24, sampleRate, true); out.setUint32(28, sampleRate * 2, true)
    out.setUint16(32, 2, true); out.setUint16(34, 16, true)
    w(36, 'data'); out.setUint32(40, data.length, true)
    new Uint8Array(buffer, 44).set(data)

    const plan = planWavChunks(buffer, { chunkSeconds: 10, searchSeconds: 20, minChunkSeconds: 1 })!
    assert.ok(plan.cutPointsMs.length >= 1, '没有切点 ⇒ 这条判据是恒真的')
    const cutSec = plan.cutPointsMs[0] / 1000
    assert.ok(cutSec < 5,
      `切点落在 ${cutSec.toFixed(2)}s ⇒ 选的不是真实最安静的那一段（负半波被当成了响 ⇒ 符号位口径错了）`)
  })

  it('短音频不切（装得下一块 ⇒ 保持单次调用 ⇒ 行为逐字不变）', () => {
    assert.equal(planWavChunks(buildWav(3), { chunkSeconds: 5, searchSeconds: 1.5, minChunkSeconds: 1 }), null,
      '短音频不该切')
  })

  it('非 16-bit PCM（webm 兜底路径）不切（没有 demuxer ⇒ 保持单次调用）', () => {
    assert.equal(planWavChunks(new ArrayBuffer(64)), null, '非 WAV 不该切')
  })

  // ── ② 接线（行为层：真的把请求发出去）──

  it('★ 接线：够长的音频必须发多次请求，块数正确，每块都是合法 WAV，拼回去 == 原音频', async () => {
    const plan = planWavChunks(WAV, { chunkSeconds: 5 })!
    const calls: ArrayBuffer[] = []
    const names: string[] = []
    const r = await refetchFullTranscript('m1', async (blob, filename) => {
      const buf = await blob.arrayBuffer()
      calls.push(buf)
      names.push(filename)
      return { text: `块${calls.length}`, failed: 0 }
    }, { loadAudio, toWav, chunkSeconds: 5 })
    assert.equal(r.applied, true, '分块后整体失败了')
    assert.equal(calls.length, plan.bounds.length, `请求数 ${calls.length} ≠ 计划块数 ${plan.bounds.length}`)
    assert.ok(calls.length >= 2, '只发了一次 ⇒ 没分块')
    for (const n of names) {
      assert.equal(n, 'meeting.wav', `块的文件名是 ${n} ⇒ 服务端按扩展名判容器 ⇒ 波形会被当成 webm 拒收`)
    }
    let total = 0
    const joined: number[] = []
    for (const buf of calls) {
      const bytes = new Uint8Array(buf)
      const dv = new DataView(buf)
      assert.equal(dv.getUint32(4, true) + 8, bytes.length, '发出去的块不是合法 WAV')
      total += dv.getUint32(40, true)
      for (const b of dataPayload(buf)) joined.push(b)
    }
    const orig = dataPayload(WAV)
    assert.equal(total, orig.length, '发出去的字节总数与原音频不符')
    assert.ok(sameBytes(Uint8Array.from(joined), orig), '发出去的块拼起来 != 原音频')
  })

  it('短音频仍只发一次请求（行为不变）', async () => {
    let n = 0
    await refetchFullTranscript('m1', async () => { n += 1; return { text: 'x', failed: 0 } },
      { loadAudio, toWav: async () => buildWav(3), chunkSeconds: 5 })
    assert.equal(n, 1, '短音频被切了 ⇒ 行为变了')
  })

  it('★ 生产默认块长必须是 8 分钟（不是拍脑袋的数字）', async () => {
    // 9 分钟的音频：只有块长 < 9 分钟才会切成 2 块 ⇒ 用它钉住默认值。
    const long = buildWav(9 * 60)
    let n = 0
    await refetchFullTranscript('m1', async () => { n += 1; return { text: `块${n}`, failed: 0 } },
      { loadAudio, toWav: async () => long })
    assert.equal(n, 2, `9 分钟的音频切成了 ${n} 块（默认块长 ${FULL_TRANSCRIBE_CHUNK_SECONDS}s）`)
  })

  it('无 WAV（webm 兜底）仍单次调用', async () => {
    let n = 0
    await refetchFullTranscript('m1', async () => { n += 1; return { text: 'x', failed: 0 } },
      { loadAudio, toWav: async () => null })
    assert.equal(n, 1, '没有 WAV 时不该切（那不是 WAV，切了也是乱切）')
  })

  // ── ③ 合并语义 ──

  it('★ 合并：文本按顺序拼接、failed 求和、回执时间轴平移、index 重新连续编号', async () => {
    const merged = mergeChunkedOutcomes([
      { offsetSec: 0, outcome: { text: 'A', failed: 1, segments: [{ index: 0, startSec: 0, endSec: 2, text: 'A' }] } },
      { offsetSec: 10, outcome: { text: 'B', failed: 2, segments: [{ index: 0, startSec: 0, endSec: 3, text: 'B' }] } },
    ])
    assert.equal(merged.text, 'A\nB', '合并后的文本顺序错了')
    assert.equal(merged.failed, 3, 'failed 必须求和')
    assert.equal(merged.segments?.length, 2)
    assert.equal(merged.segments?.[1].index, 1, 'index 必须重新连续编号')
    assert.equal(merged.segments?.[1].startSec, 10, '回执时间轴必须平移（否则说话人归属全错）')
    assert.equal(merged.segments?.[1].endSec, 13)
  })

  it('★ 失败占位符的段号必须是**全局**的（不能让用户读到「第 1 段」而它其实是第 37 段）', async () => {
    const merged = mergeChunkedOutcomes([
      { offsetSec: 0, outcome: { text: 'A', failed: 0, segments: [{ index: 0, startSec: 0, endSec: 1, text: 'A' }] } },
      { offsetSec: 10, outcome: { text: '', failed: 1, segments: [{ index: 0, startSec: 1, endSec: 2, error: '401' }] } },
    ])
    assert.match(merged.text, /［第 2 段转写失败：401］/, '占位符的段号没换成全局的')
    assert.ok(!/［第 1 段转写失败/.test(merged.text), '块内段号漏出来了')
  })

  it('负控：没有失败时，合并文本必须与「直接拼块文本」逐字相同（重建不许改动正常内容）', async () => {
    const merged = mergeChunkedOutcomes([
      {
        offsetSec: 0,
        outcome: {
          text: '第一块\n第二段', failed: 0,
          segments: [
            { index: 0, startSec: 0, endSec: 1, text: '第一块' },
            { index: 1, startSec: 1, endSec: 2, text: '第二段' },
          ],
        },
      },
      { offsetSec: 10, outcome: { text: '第三块', failed: 0, segments: [{ index: 0, startSec: 0, endSec: 1, text: '第三块' }] } },
    ])
    assert.equal(merged.text, '第一块\n第二段\n第三块')
  })

  // ── ④ 失败语义（不许出现新的失败形态）──

  it('★ 一块失败不许把整场拖死（其余块的内容仍然要留下）', async () => {
    let n = 0
    const r = await refetchFullTranscript('m1', async () => {
      n += 1
      if (n === 2) throw new Error('CHUNK_2_FAILED')
      return { text: `块${n}`, failed: 0 }
    }, { loadAudio, toWav, chunkSeconds: 5 })
    assert.equal(r.applied, true, '一块失败把整场拖死了')
    assert.match(r.text ?? '', /块1/)
    assert.match(r.text ?? '', /块3/)
    assert.equal(r.failedSegments, 1, '失败的块必须计数（真实段数未知 ⇒ 记 1 ⇒ 少报但不说谎）')
  })

  it('★ 全部块都抛错 ⇒ reason 仍是 transcribe-failed（不许出现新的失败形态）', async () => {
    const r = await refetchFullTranscript('m1', async () => { throw new Error('ALL_FAILED') },
      { loadAudio, toWav, chunkSeconds: 5 })
    assert.equal(r.applied, false)
    assert.match(String(r.reason), /^transcribe-failed/, '失败形态变了')
    assert.match(String(r.reason), /ALL_FAILED/)
  })

  it('★ 整块都是服务端 ok:false（HTTP 200 但无内容）⇒ 与单次调用同义：empty-transcript', async () => {
    const r = await refetchFullTranscript('m1', async () => ({
      text: '', failed: 0, error: 'stt m: 全量转写没有任何有效文本（3 段全部失败，首段错误：401）',
    }), { loadAudio, toWav, chunkSeconds: 5 })
    assert.equal(r.reason, 'empty-transcript', '与单次调用的失败形态不一致')
    assert.equal(r.applied, false)
  })

  it('★ 纯静音的块**不算失败**（不许虚报「有 N 段没转出来」）', async () => {
    let n = 0
    const r = await refetchFullTranscript('m1', async () => {
      n += 1
      if (n === 2) {
        return {
          text: '', failed: 0,
          error: 'stt m: 全量转写没有任何有效文本（0 段全部失败，首段错误：（无逐段错误，全部段被判定为纯静音而跳过））',
        }
      }
      return { text: `块${n}`, failed: 0 }
    }, { loadAudio, toWav, chunkSeconds: 5 })
    assert.equal(r.applied, true)
    assert.equal(r.failedSegments, undefined, '纯静音不是失败 —— 虚报会让用户以为录音有缺')
  })

  it('负控：同形态但错误文案里没有「纯静音」⇒ 必须算失败', async () => {
    let n = 0
    const r = await refetchFullTranscript('m1', async () => {
      n += 1
      if (n === 2) {
        return { text: '', failed: 0, error: 'stt m: 全量转写没有任何有效文本（3 段全部失败，首段错误：401）' }
      }
      return { text: `块${n}`, failed: 0 }
    }, { loadAudio, toWav, chunkSeconds: 5 })
    assert.equal(r.failedSegments, 1, '真失败没被计数')
  })

  // ── ⑤ 两道墙的算术登记（读服务端真值，不是抄常量）──

  it('★ 块长推导的算术必须成立（8 分钟 ⇒ 最坏一块的每次上游调用仍有 ≥ 6.3 s 预算；§928 更正）', () => {
    const goSrc = read('../../../../backend/internal/server/server_stt_stream.go')
    const m = /fullTranscribeTimeout\s*=\s*(\d+)\s*\*\s*time\.Minute/.exec(goSrc)
    assert.ok(m, '读不到服务端的 fullTranscribeTimeout —— 判据失明（不许当成通过）')
    const budgetSec = Number(m[1]) * 60
    // 最碎的实测段频（§923.3：真实录音 10.5 段/分钟）
    const segsPerMinute = 10.5
    // 每次上游调用至少需要的秒数（**要求值不是读数**：网关 key 401，未实测）
    const minPerCallSec = 5
    // ⚠⚠ §928 更正：**块长 ≠ 块长参数**。
    // 切点是在「目标点 ± 搜索窗」里吸附出来的 ⇒ 相邻两切点之差最大是
    // `块长 + 2×搜索窗`；**尾块**还能多出一个「最小块长」。
    // ⇒ 真上界 = max(块长 + 2×搜索窗, 块长 + 最小块长)（见本文件的 chunkLengthBound）。
    // §928 之前这里按「块长 + 0」算 ⇒ 报出 7.1 s ⇒ **余量被高估了 12%**
    //（结论没变：仍高于 5 s 的要求值；但推导必须按真上界算）。
    const maxChunkSec = chunkLengthBound(
      FULL_TRANSCRIBE_CHUNK_SECONDS, FULL_TRANSCRIBE_CUT_SEARCH_SECONDS, MIN_CHUNK_SECONDS,
    )
    // ⭐ 钉住**默认参数下的那个数**（文档 §928.6 写的是 540 s / 6.3 s）
    // ⇒ 改了块长、搜索窗或最小块长 ⇒ 文档必须同步改，否则当场红。
    assert.equal(maxChunkSec, 540,
      `默认块长上界从 540 s 变成 ${maxChunkSec} s ⇒ 文档 §928.6 的「540 s / 6.3 s」必须同步改`)
    const calls = (maxChunkSec / 60) * segsPerMinute
    const perCall = budgetSec / calls
    assert.ok(perCall > minPerCallSec,
      `最坏一块的每次上游调用只剩 ${perCall.toFixed(2)} s（要求 ≥ ${minPerCallSec} s）⇒ 块长 ${FULL_TRANSCRIBE_CHUNK_SECONDS / 60} 分钟太长`)
  })

  it('★ 字节墙是真的：服务端 400 MiB 只够 3.55 小时 ⇒ 整场发不出去', () => {
    const goSrc = read('../../../../backend/internal/server/server_stt_stream.go')
    const m = /maxFullAudioBytes\s*=\s*(\d+)\s*<<\s*(\d+)/.exec(goSrc)
    assert.ok(m, '读不到服务端的 maxFullAudioBytes —— 判据失明（不许当成通过）')
    const limitBytes = Number(m[1]) * 2 ** Number(m[2])
    const bytesPerSec = 16000 * 2 // 16 kHz 单声道 16-bit
    const oneShotSec = limitBytes / bytesPerSec
    assert.ok(oneShotSec < 4 * 3600,
      `服务端 ${limitBytes} B 只够 ${(oneShotSec / 3600).toFixed(2)} 小时 ⇒ 4 小时的会议整场发不出去`)
    // 分块后：单次请求体只由块长决定，与会议时长无关
    const perRequest = (FULL_TRANSCRIBE_CHUNK_SECONDS) * bytesPerSec
    assert.ok(perRequest < limitBytes, '单块请求体必须远小于服务端上限')
    const plan = planWavChunks(WAV, { chunkSeconds: 5, searchSeconds: 1.5, minChunkSeconds: 1 })!
    for (let i = 0; i < plan.bounds.length; i++) {
      const [from, to] = plan.bounds[i]
      const chunk = sliceWavChunk(WAV, plan, i)
      const payload = dataPayload(chunk)
      const sec = (to - from) / 16000
      assert.ok(Math.abs(payload.length - sec * bytesPerSec) <= 2,
        `块的字节数 ${payload.length} 与时长 ${sec}s 不匹配`)
    }
  })
})

// ══════════════════════════════════════════════════════════════════════════
// I · §928 真实录音实测后的更正
//
// §928 用**真实会议音频**（12.54 min / 16 kHz / 单声道 / 16 bit）实测了
// §927 的分块与服务端的 `SplitWAV`；读数与结论见设计文档 §928。
//
// 这组判据钉的是**真实录音上可达**的命题（不是合成夹具上的）：
//   ① 块长上界（预算推导的依据，§928 更正过）；
//   ② 「切点比硬切安静」（真实录音上「落在静音里」**不可达** ⇒ 只有这条可断言）；
//   ③ 找不到静音**也必须切**（真实录音上「静音」只占 11% ⇒ 不许因此退化成整场一次）。
// ══════════════════════════════════════════════════════════════════════════
describe('I · §928 真实录音实测后的更正', () => {
  /**
   * 造一段**没有真静音**的 WAV：全程底噪高于门槛 0.012，
   * 另有一段「凹陷」比别处安静、但**仍高于门槛**。
   *
   * ⭐ 为什么必须有这个夹具（§928 的核心读数）：
   * 真实会议录音里 100 ms 居中窗低于 0.012 的**只占 11.0%**
   * ⇒「切点落在静音里」在真实录音上**不是可达命题**
   * ⇒ 能断言的只有「切点比**硬切**安静」。
   *
   * ⚠ 幅度用**交替正负**的方波（不是直流）：真实底噪零均值；
   *   直流会让「RMS」与「幅度」脱钩，夹具就失去对抗性。
   */
  const buildNoisyWav = (seconds: number, opts: {
    loud: number
    dip?: number
    dipFrom?: number
    dipTo?: number
  }): ArrayBuffer => {
    const sampleRate = 16000
    const frames = Math.round(seconds * sampleRate)
    const data = new Uint8Array(frames * 2)
    const dv = new DataView(data.buffer)
    const dip = opts.dip ?? opts.loud
    const dipFrom = opts.dipFrom ?? 0
    const dipTo = opts.dipTo ?? 0
    for (let f = 0; f < frames; f++) {
      const t = f / sampleRate
      const inDip = t >= dipFrom && t < dipTo
      const amp = inDip ? dip : opts.loud
      dv.setInt16(f * 2, Math.round((f % 2 === 0 ? amp : -amp) * 32767), true)
    }
    const buffer = new ArrayBuffer(44 + data.length)
    const out = new DataView(buffer)
    const w = (o: number, s: string) => { for (let i = 0; i < s.length; i++) out.setUint8(o + i, s.charCodeAt(i)) }
    w(0, 'RIFF'); out.setUint32(4, 36 + data.length, true); w(8, 'WAVE')
    w(12, 'fmt '); out.setUint32(16, 16, true); out.setUint16(20, 1, true); out.setUint16(22, 1, true)
    out.setUint32(24, sampleRate, true); out.setUint32(28, sampleRate * 2, true)
    out.setUint16(32, 2, true); out.setUint16(34, 16, true)
    w(36, 'data'); out.setUint32(40, data.length, true)
    new Uint8Array(buffer, 44).set(data)
    return buffer
  }

  /**
   * 造一段「**均匀响** + 指定位置有凹陷」的 WAV。
   *
   * ⭐ 为什么需要它（§928）：通用夹具只能当回归护栏，抓不住「搜索窗被放宽」；
   * 要抓它，夹具必须**对抗**：
   *   凹陷 A 在 3.5 s ⇒ 切点 1 早吸附；
   *   凹陷 B 在 **12.5 s** ⇒ 落在原搜索窗 [8.5, 11.5] **之外**、
   *   却落在放宽后的窗口 [7, 13] **之内**
   * ⇒ 窗口一旦被放宽，切点 2 会从 8.5 s 跳到 12.5 s
   * ⇒ 块长从 5.0 s 涨到 9.0 s ⇒ 超过上界 8 s ⇒ 判红。
   */
  const buildUniformWithDips = (seconds: number, loud: number, dips: Array<[number, number]>): ArrayBuffer => {
    const sampleRate = 16000
    const frames = Math.round(seconds * sampleRate)
    const data = new Uint8Array(frames * 2)
    const dv = new DataView(data.buffer)
    for (let f = 0; f < frames; f++) {
      const t = f / sampleRate
      let amp = loud
      for (const [from, to] of dips) {
        if (t >= from && t < to) { amp = loud * 0.2; break }
      }
      dv.setInt16(f * 2, Math.round((f % 2 === 0 ? amp : -amp) * 32767), true)
    }
    const buffer = new ArrayBuffer(44 + data.length)
    const out = new DataView(buffer)
    const w = (o: number, s: string) => { for (let i = 0; i < s.length; i++) out.setUint8(o + i, s.charCodeAt(i)) }
    w(0, 'RIFF'); out.setUint32(4, 36 + data.length, true); w(8, 'WAVE')
    w(12, 'fmt '); out.setUint32(16, 16, true); out.setUint16(20, 1, true); out.setUint16(22, 1, true)
    out.setUint32(24, sampleRate, true); out.setUint32(28, sampleRate * 2, true)
    out.setUint16(32, 2, true); out.setUint16(34, 16, true)
    w(36, 'data'); out.setUint32(40, data.length, true)
    new Uint8Array(buffer, 44).set(data)
    return buffer
  }

  it('★ 块长上界 = 块长 + 2×搜索窗（或 + 最小块长，取大者）—— 预算推导的唯一依据', () => {
    const chunkSeconds = 5
    const searchSeconds = 1.5
    const minChunkSeconds = 1
    const plan = planWavChunks(WAV, { chunkSeconds, searchSeconds, minChunkSeconds })!
    const bound = chunkLengthBound(chunkSeconds, searchSeconds, minChunkSeconds)
    for (let i = 0; i < plan.bounds.length; i++) {
      const [from, to] = plan.bounds[i]
      const len = (to - from) / plan.layout.sampleRate
      assert.ok(len <= bound + 1e-6,
        `第 ${i} 块长 ${len.toFixed(3)}s 超过上界 ${bound}s ⇒ 预算推导（按上界算的那条）会失真`)
    }
  })

  it('★ 块长上界必须**抗得住搜索窗被放宽**（夹具对抗：放宽窗口 ⇒ 切点跳进外侧的凹陷 ⇒ 块长超标）', () => {
    // 凹陷 A 在 3.5 s（早吸附）、凹陷 B 在 12.5 s（原窗 [8.5,11.5] 之外、放宽窗 [7,13] 之内）。
    const wav = buildUniformWithDips(30, 0.05, [[3.25, 3.75], [12.25, 12.75]])
    const chunkSeconds = 5
    const searchSeconds = 1.5
    const minChunkSeconds = 1
    const plan = planWavChunks(wav, { chunkSeconds, searchSeconds, minChunkSeconds })!
    const bound = chunkLengthBound(chunkSeconds, searchSeconds, minChunkSeconds)
    for (let i = 0; i < plan.bounds.length; i++) {
      const [from, to] = plan.bounds[i]
      const len = (to - from) / plan.layout.sampleRate
      assert.ok(len <= bound + 1e-6,
        `第 ${i} 块长 ${len.toFixed(3)}s 超过上界 ${bound}s ⇒ 搜索窗被放宽（或切点逻辑变了）而没人发现`)
    }
  })

  it('★ 切点必须比**硬切**安静（真实录音上「落在静音里」不可达 ⇒ 这才是可断言的命题）', () => {
    // 底噪 0.05、凹陷 0.018 —— **全程没有任何窗口低于 0.012** ⇒
    // 「切点落在静音里」在这个夹具上**不可达** ⇒ 只能断言「比硬切安静」。
    const loud = 0.05
    const dip = 0.018
    const wav = buildNoisyWav(20, { loud, dip, dipFrom: 6.5, dipTo: 8.5 })
    const plan = planWavChunks(wav, { chunkSeconds: 10, searchSeconds: 4, minChunkSeconds: 1 })!
    assert.ok(plan.cutPointsMs.length >= 1, '没有切点 ⇒ 这条判据是恒真的')
    const cutSec = plan.cutPointsMs[0] / 1000
    const cutRms = centeredRms(wav, cutSec)
    const hardRms = centeredRms(wav, 10) // 硬切目标（= 块长参数）
    // 量具自证：夹具必须对抗 —— 硬切目标必须明显比凹陷响。
    assert.ok(hardRms > dip * 1.5,
      `夹具不够对抗：硬切目标 RMS=${hardRms.toFixed(4)} 不比凹陷 ${dip} 响 ⇒ 这条判据证明不了搜索起了作用`)
    // 量具自证：这个夹具里**没有**低于门槛的点 ⇒ 「落在静音里」确实不可达。
    assert.ok(cutRms >= SILENCE_RMS,
      `夹具里出现了低于门槛的点（RMS=${cutRms.toFixed(5)}）⇒ 夹具没起到「无真静音」的作用`)
    assert.ok(cutRms < hardRms,
      `切点 RMS=${cutRms.toFixed(5)} 不比硬切 ${hardRms.toFixed(5)} 安静 ⇒ 静音搜索没起作用`)
  })

  it('★ 找不到静音**也必须切**（真实录音上「静音」只占 11% ⇒ 不许因此退化成整场一次）', () => {
    // 全程一样响（连凹陷都没有）⇒ 「切在静音里」在物理上不可能。
    const wav = buildNoisyWav(20, { loud: 0.05 })
    const plan = planWavChunks(wav, { chunkSeconds: 10, searchSeconds: 4, minChunkSeconds: 1 })
    assert.ok(plan, '没有安静点就不切 ⇒ 长会议退回整场一次 ⇒ 撞服务端 10 分钟预算（腰斩）')
    assert.ok(plan.bounds.length >= 2, `只切出一块 ⇒ 等于没切（${plan.bounds.length} 块）`)
  })
})

// ══════════════════════════════════════════════════════════════════════════
// J · §954 原生链（Android）：**整场重转从盘上已有的分片流式供给**
//
// ── 缺陷链（§954.1，每一步都在生产代码里可核对）──
//   ① `isBackgroundMicSupported()` 在原生端恒为真 ⇒ `nativeMode = true`；
//   ② `new VadSegmenter(...)` 在 `if (!this.nativeMode)` 里 ⇒ 段器不存在；
//   ③ `stop()` 的 `fullBlob = (await this.vadSegmenter?.stop()) ?? null` ⇒ 恒 null；
//   ④ `saveMeetingAudio` 全仓只有一处调用，在 ③ 的 `if` 里 ⇒ 完整音频从不落盘；
//   ⑤ `loadMeetingAudio` 恒返回 null ⇒ `refetchFullTranscript` 恒 `audio-not-found`
//      ⇒ **两阶段转写的第二阶段在生产主路径上从不发生**（§826 已定性）。
//
// ── 本组的修法 ──
//   整场音频**早就在盘上**（`saveAudioPart` 把每一片都写进了
//   `files/recordings/{meetingId}/part-N.*`）⇒ 缺的只是「把它接起来」。
//   ⇒ 改成**按块读出来直接送** ⇒ ① 不产生任何新存储（不拼整段、不写新文件）
//   ⇒ ② 峰值内存 **O(块)** 而不是 O(时长)。
//
// ⚠ 下面这组是**行为层**（真牙齿）：喂真字节、量真读数，
//   不是「文件里有这个词」那种零牙断言。
// ══════════════════════════════════════════════════════════════════════════
import {
  NativeWavChunkPlanner,
  buildWavHeader,
  audioPartExtForMime,
  audioPartRelativePath,
} from './native-audio-parts.ts'

/** 造一片 16 kHz 单声道 16-bit 的 WAV（**每片一个不同的响度** ⇒ 切点有得挑）。 */
function makeWavPart(seconds: number, value: number): Uint8Array {
  const n = Math.round(seconds * 16000)
  const pcm = new Uint8Array(n * 2)
  // ⚠ 用「整片同一个值」而不是逐样本算正弦：前者同样能让窗口 RMS 有差异，
  //   但 O(n) 只是一次 memset ⇒ 8 小时的夹具也跑得动。
  const v = Math.max(-32768, Math.min(32767, Math.round(value)))
  const u = v < 0 ? v + 65536 : v
  for (let i = 0; i < n; i++) {
    pcm[i * 2] = u & 0xff
    pcm[i * 2 + 1] = (u >> 8) & 0xff
  }
  const header = buildWavHeader(pcm.length, 16000, 1, 16)
  const out = new Uint8Array(header.length + pcm.length)
  out.set(header, 0)
  out.set(pcm, header.length)
  return out
}

/** 一段 WAV 的时长（秒）。 */
function wavSecondsOf(bytes: Uint8Array): number {
  const lay = parseWavLayout(bytes)
  assert.ok(lay, '不是可解析的 16-bit PCM WAV')
  return lay.dataLength / (lay.sampleRate * lay.blockAlign)
}

/** 喂进规划器，返回切好的块。 */
function planFromParts(count: number, partSeconds = 8): Array<{ wav: Uint8Array; offsetSec: number; durationSec: number }> {
  const planner = new NativeWavChunkPlanner()
  const out: Array<{ wav: Uint8Array; offsetSec: number; durationSec: number }> = []
  for (let i = 0; i < count; i++) {
    // 响度按片轮换 ⇒ 搜索窗里有真有假的安静点 ⇒ 切点逻辑被真的走到
    const value = (i % 7) * 900 - 2700
    for (const c of planner.push(makeWavPart(partSeconds, value))) out.push(c)
  }
  for (const c of planner.flush()) out.push(c)
  return out
}

describe('J · §954 原生链：分片接成整场音频的块（行为层）', () => {
  it('★ 一秒都不许丢：切出来的块加起来 == 喂进去的总时长', () => {
    // 70 片 × 8 s = 560 s（刚好够 §954 的「组」阈值 480+20+60）
    const chunks = planFromParts(70)
    const total = chunks.reduce((n, c) => n + wavSecondsOf(c.wav), 0)
    assert.ok(Math.abs(total - 560) < 0.001, `总时长 ${total}s ≠ 560s ⇒ 切点处丢了音频`)
  })

  it('★ 时间轴必须连续（每块的 offsetSec == 上一块的起点 + 上一块的时长）', () => {
    // ⇒ 这条是「说话人归属」的前提：回执的时间轴靠 offsetSec 平移回整场。
    const chunks = planFromParts(150)
    let expect = 0
    for (const c of chunks) {
      assert.ok(Math.abs(c.offsetSec - expect) < 1e-6,
        `第 ${chunks.indexOf(c)} 块起点 ${c.offsetSec}s ≠ 期望 ${expect}s ⇒ 时间轴断了`)
      expect += c.durationSec
    }
    assert.ok(chunks.length >= 2, `只切出 ${chunks.length} 块 ⇒ 没切`)
  })

  it('★★ 块长必须与会议时长无关（1 小时与 4 小时的块长一致）⇒ 峰值内存 O(块)', () => {
    // 1 小时 = 450 片 × 8 s；4 小时 = 1800 片 × 8 s。
    const one = planFromParts(450)
    const four = planFromParts(1800)
    const maxBytes = (cs: typeof one) => Math.max(...cs.map((c) => c.wav.length))
    const m1 = maxBytes(one)
    const m4 = maxBytes(four)
    assert.ok(Math.abs(m1 - m4) <= 44,
      `1 小时的块 ${m1} B、4 小时的块 ${m4} B ⇒ 块长随时长变 ⇒ 峰值内存 O(时长)`)
    // 且单块本身必须远小于「整场」：整场 4 小时 = 1800 × 8 × 32000 = 460.8 MB
    assert.ok(m4 < 20 * 1024 * 1024, `单块 ${(m4 / 1024 / 1024).toFixed(1)} MB ⇒ 不是 O(块)`)
  })

  it('★ 块数随时长线性增长（1 小时 8 块左右、4 小时 32 块左右）', () => {
    const one = planFromParts(450)
    const four = planFromParts(1800)
    const ratio = four.length / one.length
    assert.ok(ratio > 3.5 && ratio < 4.5, `块数比 ${ratio.toFixed(2)}（应 ≈4）⇒ 块长不恒定`)
  })

  it('★ 读不到 / 不是 WAV 的一片必须**整条中止**（不许静默跳过 ⇒ 会让时间轴错位）', () => {
    const planner = new NativeWavChunkPlanner()
    assert.throws(() => planner.push(new Uint8Array([1, 2, 3, 4])),
      /不是可解析的 16-bit PCM WAV/,
      '不是 WAV 的一片被静默跳过了 ⇒ 那一段变成洞 ⇒ 洞之后的偏移与墙钟错位')
  })

  it('★ 布局不一致的一片必须中止（采样率/声道变了 ⇒ 拼出来的 WAV 是坏的）', () => {
    const planner = new NativeWavChunkPlanner()
    planner.push(makeWavPart(8, 1000))
    const other = buildWavHeader(16000 * 2, 8000, 1, 16) // 采样率不同
    const body = new Uint8Array(other.length + 16000 * 2)
    body.set(other, 0)
    assert.throws(() => planner.push(body), /布局不一致/,
      '布局不一致被静默接受了 ⇒ 拼出来的 WAV 播放/转写都会错')
  })

  it('★ 落盘扩展名必须按实际 MIME 推（原生链是 WAV，不是 webm）', () => {
    assert.equal(audioPartExtForMime('audio/wav'), 'wav', '原生链的产物是 WAV ⇒ 扩展名必须是 wav')
    assert.equal(audioPartExtForMime('audio/webm'), 'webm')
    assert.equal(audioPartExtForMime(''), 'webm', '未知 MIME 退回 webm（老行为）')
    assert.equal(
      audioPartRelativePath('m1', 7, 'wav'), 'recordings/m1/part-7.wav',
      '分片路径规则变了 ⇒ 写与读会对不上',
    )
  })
})

describe('J · §954 分块音频源接入整场重转（行为层）', () => {
  /** 造一个假的分块音频源。 */
  function fakeChunks(count: number, seconds: number) {
    const calls: Array<{ bytes: number; offsetSec: number }> = []
    async function* gen() {
      for (let i = 0; i < count; i++) {
        const offsetSec = i * seconds
        const bytes = makeWavPart(seconds, 1000)
        calls.push({ bytes: bytes.length, offsetSec })
        yield {
          blob: new Blob([bytes], { type: 'audio/wav' }),
          offsetSec,
          durationSec: seconds,
        }
      }
    }
    return { gen, calls }
  }

  it('★★ 给了分块源就**不许再去取整场音频**（原生链上它恒 null ⇒ 早退就永远轮不到分块源）', async () => {
    const { gen, calls } = fakeChunks(2, 8)
    let loadAudioCalled = false
    const res = await refetchFullTranscript('m1', async () => ({
      text: '一段话', failed: 0, segments: [{ index: 0, startSec: 0, endSec: 8, text: '一段话' }],
    }), {
      loadAudio: async () => { loadAudioCalled = true; return null },
      loadAudioChunks: () => gen(),
    })
    assert.equal(loadAudioCalled, false, '仍然去取整场音频 ⇒ 原生链上会在 audio-not-found 上早退')
    assert.equal(res.applied, true, `没跑成整场重转：${res.reason ?? ''}`)
    assert.equal(calls.length, 2, `应当逐块各发一次，实际 ${calls.length} 次`)
  })

  it('★ 回执的时间轴必须按 offsetSec 平移（否则第 2 块以后的回执全落到会议开头）', async () => {
    const { gen } = fakeChunks(2, 8)
    const res = await refetchFullTranscript('m1', async (_blob, _name) => ({
      text: 'x', failed: 0, segments: [{ index: 0, startSec: 0, endSec: 8, text: 'x' }],
    }), { loadAudioChunks: () => gen() })
    assert.ok(res.segments && res.segments.length === 2, `回执块数不对：${JSON.stringify(res.segments)}`)
    assert.ok(Math.abs((res.segments![1].startSec ?? 0) - 8) < 1e-6,
      `第 2 块的起点 ${res.segments![1].startSec}s ≠ 8s ⇒ 时间轴没平移 ⇒ 说话人归属全错`)
  })

  it('★ 一块都没有 ⇒ 与「压根没有音频」**同义**（不新增失败形态）', async () => {
    async function* empty() { /* 一块也不给 */ }
    const res = await refetchFullTranscript('m1', async () => ({ text: 'x', failed: 0 }), {
      loadAudioChunks: () => empty(),
    })
    assert.equal(res.applied, false)
    assert.equal(res.reason, 'audio-not-found', `失败形态变了：${res.reason}`)
  })

  it('★ 全部块抛错 ⇒ 失败形态仍是 transcribe-failed（与单次调用同义）', async () => {
    const { gen } = fakeChunks(2, 8)
    const res = await refetchFullTranscript('m1', async () => { throw new Error('网络断了') }, {
      loadAudioChunks: () => gen(),
    })
    assert.equal(res.applied, false)
    assert.match(String(res.reason), /^transcribe-failed/, `失败形态变了：${res.reason}`)
  })

  it('★ 全部块空文本 ⇒ 失败形态仍是 empty-transcript', async () => {
    const { gen } = fakeChunks(2, 8)
    const res = await refetchFullTranscript('m1', async () => ({ text: '', failed: 0 }), {
      loadAudioChunks: () => gen(),
    })
    assert.equal(res.applied, false)
    assert.equal(res.reason, 'empty-transcript', `失败形态变了：${res.reason}`)
  })

  it('★★ 整场真实时长必须被量出来（§805 的时长护栏在原生链上复活）', async () => {
    const { gen } = fakeChunks(3, 8)
    const res = await refetchFullTranscript('m1', async () => ({ text: 'x', failed: 0 }), {
      loadAudioChunks: () => gen(),
    })
    assert.ok(typeof res.measuredAudioSec === 'number', '没有量出整场时长 ⇒ §805 的护栏恒判 no-ground-truth')
    assert.ok(Math.abs((res.measuredAudioSec ?? 0) - 24) < 0.001,
      `量出的时长 ${res.measuredAudioSec}s ≠ 24s`)
  })

  it('★ 负控：不提供分块源时，原生链上就是「没有音频」（这条判据必须能区分两条路）', async () => {
    const res = await refetchFullTranscript('m1', async () => ({ text: 'x', failed: 0 }), {
      loadAudio: async () => null,
    })
    assert.equal(res.applied, false)
    assert.equal(res.reason, 'audio-not-found', '不提供分块源时仍是老行为（说明上面几条真的走的是分块源那条路）')
  })
})

describe('J · §954 接线层（源码扫描，⚠ 按 §818 的教训对「信号变死代码」零牙）', () => {
  const finalizeSrc = () => stripComments(read('./meeting-recording-finalize.ts'))
  const audioPartsSrc = () => stripComments(read('./audio-parts.ts'))

  it('★ 收尾必须把分块源传进 refetchFullTranscript（否则 §954 整条链是死代码）', () => {
    const s = finalizeSrc()
    const at = s.indexOf('refetchFullTranscript(')
    assert.ok(at > 0, '收尾里找不到 refetchFullTranscript 调用')
    const window = s.slice(at, at + 600)
    // ⚠ 断言必须钉**「传的是 deps 那个」**而不是「窗口里出现过 loadAudioChunks 这几个字」
    //   —— 否则 `loadAudioChunks: undefined` 这种「把整条链掐断」的写法照样绿。
    assert.match(window, /loadAudioChunks:\s*deps\.loadAudioChunks/,
      'refetchFullTranscript 的调用点上没有把 deps.loadAudioChunks 传进去 ⇒ 分块源永远传不进去')
  })

  it('★ 默认分块源必须**只在原生平台**生效（非原生返回 null ⇒ Web/iOS 行为一个字不变）', () => {
    const s = finalizeSrc()
    const at = s.indexOf('defaultLoadAudioChunks')
    assert.ok(at > 0, '找不到默认分块源')
    const window = s.slice(at, at + 400)
    assert.match(window, /Capacitor\.isNativePlatform\(\)/,
      '默认分块源没有按平台分派 ⇒ Web/iOS 上也会去读盘上的分片')
    assert.match(window, /return null/,
      '默认分块源在非原生平台上必须返回 null')
  })

  it('★ 分片清单**不许整列读音频**（data_base64 那一列在 Web 链上装的是整场音频）', () => {
    const s = audioPartsSrc()
    const at = s.indexOf('listAudioPartFiles')
    assert.ok(at > 0, '找不到 listAudioPartFiles')
    const window = s.slice(at, at + 600)
    assert.doesNotMatch(window, /data_base64/,
      '清单查询把 data_base64 也读出来了 ⇒ 整场音频一次性进内存 ⇒ 峰值 O(时长)')
    assert.match(window, /SELECT seq/, '清单查询没有显式列出需要的列')
  })

  it('★ 落盘路径不许再写死 .webm（原生链的产物是 WAV）', () => {
    const s = audioPartsSrc()
    const at = s.indexOf('export async function saveAudioPart')
    assert.ok(at > 0, '找不到 saveAudioPart')
    const window = s.slice(at, at + 1200)
    assert.doesNotMatch(window, /part-\$\{seq\}\.webm/,
      '落盘又写死了 .webm ⇒ 文件名与内容不符')
    assert.match(window, /audioPartExtForMime/, '落盘没有按 MIME 推扩展名')
  })
})
