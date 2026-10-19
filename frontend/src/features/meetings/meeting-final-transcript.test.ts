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
  FULL_TRANSCRIBE_CHUNK_SECONDS,
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
describe('H · §927 整场重转分块（长会议不再被腰斩）', () => {
  // ── 夹具：16 kHz 单声道 16-bit PCM WAV（客户端 `toWav` 的真实产物形态）──

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

  /**
   * 独立实现：某点处**居中 100 ms** 窗口的 RMS。
   *
   * ⚠ 为什么必须居中：切点质量取决于**两侧**是否安静 ——
   *   只看一侧的话，切点可能正好落在词尾，词照样被劈开。
   * ⚠ 门槛 0.012 = 服务端 `silenceFloor`（`backend/internal/stt/full.go:109`）。
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

  const sameBytes = (a: Uint8Array, b: Uint8Array): boolean => {
    if (a.length !== b.length) return false
    for (let i = 0; i < a.length; i++) if (a[i] !== b[i]) return false
    return true
  }

  const SILENCE_RMS = 0.012 // = 服务端 silenceFloor

  const WAV = buildWav(30)
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

  it('★ 每个切点都必须落在静音里（切在词中间 ⇒ 两侧各错一点）', () => {
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

  it('★ 块长推导的算术必须成立（8 分钟 ⇒ 每次上游调用仍有 7.1 s 预算）', () => {
    const goSrc = read('../../../../backend/internal/server/server_stt_stream.go')
    const m = /fullTranscribeTimeout\s*=\s*(\d+)\s*\*\s*time\.Minute/.exec(goSrc)
    assert.ok(m, '读不到服务端的 fullTranscribeTimeout —— 判据失明（不许当成通过）')
    const budgetSec = Number(m[1]) * 60
    // 最碎的实测段频（§923.3：真实录音 10.5 段/分钟）
    const segsPerMinute = 10.5
    // 每次上游调用至少需要的秒数（**要求值不是读数**：网关 key 401，未实测）
    const minPerCallSec = 5
    const calls = (FULL_TRANSCRIBE_CHUNK_SECONDS / 60) * segsPerMinute
    const perCall = budgetSec / calls
    assert.ok(perCall > minPerCallSec,
      `每块的每次上游调用只剩 ${perCall.toFixed(2)} s（要求 ≥ ${minPerCallSec} s）⇒ 块长 ${FULL_TRANSCRIBE_CHUNK_SECONDS / 60} 分钟太长`)
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
