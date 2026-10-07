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
import { resolveUploadTarget, shouldAdoptFullTranscript } from './meeting-final-transcript.ts'

const HERE = dirname(fileURLToPath(import.meta.url))
const read = (f: string) => readFileSync(join(HERE, f), 'utf8')

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
  const src = () => read('../sessions/useSessionLiveRecord.ts')

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
    assert.match(
      src(),
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
    const s = read('../sessions/useSessionLiveRecord.ts')
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
