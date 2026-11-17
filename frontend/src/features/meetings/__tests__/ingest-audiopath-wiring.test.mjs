// ingest-audiopath-wiring.test.mjs — 层3修复（2026-11-14）的**接线**门。
//
// ★ 这道门要抓的缺陷
//
// 「sttApi.transcribe 支持本地优先」早在 stt.ts:94 就实现了，但它空转了
// 一个多月：ingest-speech.ts 从不传 audioPath ⇒ 本地分支永远被跳过 ⇒
// 生产 STT 一挂整场转写全灭（2026-11-13 真机实测现场）。
// **断点不在决策层（stt.ts），在接线层（ingest-speech.ts）** ——
// 而接线层的门禁此前全是「调用存在」形态，看不见「值真的流过去了」。
//
// ★ 为什么用 esbuild 打包期替桩
//
// ingest-speech.ts import 的 sttApi（拖 http→config）、saveAudioPart
// （拖 @capacitor/filesystem）在 node 下都跑不动（§52 同一个坑）。
// ⇒ 打包期把 I/O 换成内存假件，拿真 ingestSpeechBlob 跑，
//   断言「落盘返回的 filePath 剥掉 file:// 后真的出现在 transcribe 的入参里」。
import assert from 'node:assert/strict'
import { describe, it, before } from 'node:test'
import path from 'node:path'
import fs from 'node:fs'
import os from 'node:os'
import { fileURLToPath, pathToFileURL } from 'node:url'
import { build } from 'esbuild'

function asModulePath(p) {
  return JSON.stringify(String(p).replace(/\\/g, '/'))
}

const HERE = path.dirname(fileURLToPath(import.meta.url))
const SRC = path.resolve(HERE, '..') // → src/features/meetings
const WORK = fs.mkdtempSync(path.join(os.tmpdir(), 'pocket-ingest-wire-'))

const seen = {
  saveAudioPartCalls: [],   // (meetingId, seq, blob, mimeType, db)
  saveAudioPartReturn: null, // 本次让 saveAudioPart 返回什么
  transcribeCalls: [],      // (opts, signal, assertCurrent)
  transcribeResult: null,
  transcribeThrows: null,
  saveSegmentCalls: [],     // (seg, db)
  updateTranscriptCalls: [],
  embeddings: 0,
  diarizerIdentify: 0,
}
globalThis.__wireSeen = seen

/** import 说明 → 假件源码。⚠ 桩必须与真模块同形（导出集对得上）。 */
const STUB_SOURCES = {
  './recording-operation': `
    export function captureRecordingOperation() {
      const mode = globalThis.__wireSeen.moduleMode || 'cloud'
      return {
        assertLocal() {},
        // local 模式 ⇒ ModulePolicyLocalError（真机第四层故障的触发点）
        assertCloud() {
          if (mode === 'local') {
            const e = new Error('module_policy_local')
            e.body = { error: 'module_policy_local', module: 'meetings' }
            throw e
          }
        },
        assertCurrent() {},
        localDb: { __stub: 'local', __kind: 'local-edit' },
        cloudDb: mode === 'cloud' ? { __stub: 'cloud', __kind: 'cloud-sync' } : null,
      }
    }`,
  'vue': `
    export function unref(v) { return v }`,
  '../../api/stt': `
    export const sttApi = {
      async transcribe(opts, signal, assertCurrent) {
        globalThis.__wireSeen.transcribeCalls.push({ opts, signal, assertCurrent })
        if (globalThis.__wireSeen.transcribeThrows) throw globalThis.__wireSeen.transcribeThrows
        return globalThis.__wireSeen.transcribeResult || { text: '桩转写结果', confidence: 0.9, engine: 'cloud' }
      },
    }`,
  '../../native/speaker-embedding': `
    export async function extractEmbeddingResult() {
      globalThis.__wireSeen.embeddings++
      return { ok: false, embedding: null }
    }`,
  '../../native/detect-lang': `
    export function detectLang() { return 'zh' }`,
  './audio-parts': `
    export async function saveAudioPart(meetingId, seq, blob, mimeType, db) {
      globalThis.__wireSeen.saveAudioPartCalls.push({ meetingId, seq, mimeType })
      return globalThis.__wireSeen.saveAudioPartReturn || { id: 'part-x', filePath: null }
    }`,
  './live-translate': `
    export async function liveTranslate() { return null }`,
  './meetings-live': `
    export async function updateSegmentTranslation() {}`,
  './meetings-store.ts': `
    export async function saveSegment(seg, db) {
      globalThis.__wireSeen.saveSegmentCalls.push({ seg, db })
      return 'seg-1'
    }
    export async function updateTranscript(id, text, opts) {
      globalThis.__wireSeen.updateTranscriptCalls.push({ id, text, opts })
    }`,
  './meetings-store': `
    export async function saveSegment(seg, db) {
      globalThis.__wireSeen.saveSegmentCalls.push({ seg, db })
      return 'seg-1'
    }
    export async function updateTranscript(id, text, opts) {
      globalThis.__wireSeen.updateTranscriptCalls.push({ id, text, opts })
    }`,
  '../../native/speaker-diarization': `
    export const SpeakerDiarizer = class {}`,
  './meeting-dedup.ts': `
    export function renderTranscript(segments) { return segments.map((s) => s.text).join('') }`,
  './segment-drop-notice.ts': `
    export function describeSegmentDrop(input) { return 'segment dropped: ' + input.seq }`,
  '../../api/data-policy.ts': `
    export function assertCloudModule() {}`,
  '../../api/abort.ts': `
    export function isAbortError(e) {
      return !!e && typeof e === 'object' && e.name === 'AbortError'
    }`,
}

const stubFiles = new Map()
for (const [spec, code] of Object.entries(STUB_SOURCES)) {
  const f = path.join(WORK, spec.replace(/[^a-zA-Z0-9]/g, '_') + '.mjs')
  fs.writeFileSync(f, code, 'utf8')
  stubFiles.set(spec, f)
}

let mod

before(async () => {
  const entry = path.join(WORK, 'entry.ts')
  fs.writeFileSync(entry,
    `export { ingestSpeechBlob, nativePartPathForSherpa } from ${asModulePath(path.resolve(SRC, 'ingest-speech.ts'))}\n`)
  await build({
    entryPoints: [entry],
    bundle: true,
    format: 'esm',
    platform: 'neutral',
    mainFields: ['module', 'main'],
    outfile: path.join(WORK, 'out.mjs'),
    logLevel: 'error',
    plugins: [{
      name: 'stub-io',
      setup(b) {
        for (const [spec, file] of stubFiles) {
          const re = new RegExp(spec.replace(/[.*+?^${}()|[\]\\]/g, '\\$&') + '$')
          b.onResolve({ filter: re }, () => ({ path: file }))
        }
      },
    }],
  })
  mod = await import(pathToFileURL(path.join(WORK, 'out.mjs')).href)
})

function reset() {
  seen.saveAudioPartCalls.length = 0
  seen.saveAudioPartReturn = null
  seen.transcribeCalls.length = 0
  seen.transcribeResult = null
  seen.transcribeThrows = null
  seen.saveSegmentCalls.length = 0
  seen.updateTranscriptCalls.length = 0
  seen.embeddings = 0
  seen.diarizerIdentify = 0
  seen.moduleMode = 'cloud'
}

const SEGMENTS = []
const DIARIZER = { identify: () => ({ profileId: 'p1', label: '说话人 1' }) }

async function ingest(blob, over = {}) {
  return mod.ingestSpeechBlob({
    meetingId: 'm-wire',
    blob,
    startMs: 0,
    endMs: 3000,
    seq: 1,
    diarizer: DIARIZER,
    segments: SEGMENTS,
    segmentProfiles: new Map(),
    ...over,
  })
}

describe('层3修复 · ingestSpeechBlob 的 audioPath 接线行为', () => {
  it('★★ WAV 分片落盘返回 file:// URI ⇒ transcribe 收到剥好前缀的裸路径', async () => {
    reset()
    seen.saveAudioPartReturn = { id: 'part-1', filePath: 'file:///data/user/0/app/files/recordings/m-wire/part-1.wav' }
    const blob = new Blob(['stub'], { type: 'audio/wav' })
    await ingest(blob)
    assert.equal(seen.transcribeCalls.length, 1)
    const opts = seen.transcribeCalls[0].opts
    assert.equal(opts.audioPath,
      '/data/user/0/app/files/recordings/m-wire/part-1.wav',
      'file:// 前缀没剥掉 ⇒ SherpaPlugin 的 new File() 拿 URI 当路径 ⇒ 文件不存在')
    assert.equal(opts.audioBlob, blob, 'blob 丢了 ⇒ 云端兜底通道断了')
    assert.equal(opts.module, 'meetings')
  })

  it('★ webm 分片且原生落盘存在 ⇒ 仍传裸路径，交给 Sherpa 自校验（Blob MIME 可能丢失）', async () => {
    reset()
    seen.saveAudioPartReturn = { id: 'part-2', filePath: 'file:///data/recordings/m-wire/part-2.webm' }
    const blob = new Blob(['stub'], { type: 'audio/webm' })
    await ingest(blob)
    assert.equal(seen.transcribeCalls[0].opts.audioPath, '/data/recordings/m-wire/part-2.webm',
      '原生 filePath 存在时不应依赖 Blob MIME 过滤，否则 Android 空 MIME 会让本地转写失效')
  })

  it('★ 落盘失败（filePath=null）⇒ 不传 audioPath，云端路径照常工作', async () => {
    reset()
    seen.saveAudioPartReturn = { id: 'part-3', filePath: null }
    const blob = new Blob(['stub'], { type: 'audio/wav' })
    await ingest(blob)
    assert.equal(seen.transcribeCalls[0].opts.audioPath, undefined)
    assert.equal(seen.transcribeCalls.length, 1, '落盘失败不该阻断转写（§828 既有语义）')
  })

  it('★★ 负控：把生产接回「不传 audioPath」的旧形状，本门必须翻红（不许恒绿）', async () => {
    // 直接验证决策依据：如果 ingest 不再从 saveAudioPart 返回值取 filePath，
    // transcribe 的入参里就不会出现裸路径 —— 用「返回 null filePath」模拟旧形状，
    // 上面第 3 条已断言 undefined；这里再钉一次「返回了 URI 却没出现在入参」不可能：
    reset()
    seen.saveAudioPartReturn = { id: 'part-4', filePath: 'file:///data/recordings/m-wire/part-4.wav' }
    const blob = new Blob(['stub'], { type: 'audio/wav' })
    await ingest(blob)
    const passed = seen.transcribeCalls[0].opts.audioPath
    assert.ok(passed && !passed.startsWith('file://'),
      `transcribe 收到的 audioPath=${JSON.stringify(passed)} —— 接线断了（正是层3缺陷的形状）`)
  })

  it('前提登记：转写成功路径仍然建段落库、仍然按 startMs 有序插入（别把接线修成回归）', async () => {
    reset()
    const blob = new Blob(['stub'], { type: 'audio/wav' })
    const saved = await ingest(blob)
    assert.ok(saved, '转写成功却没建段')
    assert.equal(seen.saveSegmentCalls.length, 1)
    assert.equal(saved.text, '桩转写结果')
  })

  it('★ nativePartPathForSherpa 纯函数：null 与前缀剥离', () => {
    assert.equal(mod.nativePartPathForSherpa('file:///a/b.wav', 'audio/wav'), '/a/b.wav')
    assert.equal(mod.nativePartPathForSherpa(null, 'audio/wav'), undefined)
    assert.equal(mod.nativePartPathForSherpa('file:///a/b.webm', 'audio/webm'), '/a/b.webm')
    // 已是裸路径（理论形态）⇒ 原样透传，不损坏
    assert.equal(mod.nativePartPathForSherpa('/a/b.wav', 'audio/wav'), '/a/b.wav')
  })

  it('★★ 模块策略 local（真机第四层故障）⇒ 转写不被 assertCloud 拦死，段落落本地库', async () => {
    reset()
    seen.moduleMode = 'local'   // captureRecordingOperation 桩：cloudDb=null + assertCloud 抛 ModulePolicyLocalError
    seen.saveAudioPartReturn = { id: 'part-l1', filePath: 'file:///data/recordings/m-wire/part-l1.wav' }
    const blob = new Blob(['stub'], { type: 'audio/wav' })
    const saved = await ingest(blob)
    assert.ok(saved, 'local 模式下整段被 assertCloud 拦死 ⇒ 每段 segment failed（真机 16:39 现场）')
    assert.equal(seen.transcribeCalls.length, 1, 'local 模式连转写都没发起 ⇒ 本地引擎没机会跑')
    assert.equal(seen.transcribeCalls[0].opts.audioPath, '/data/recordings/m-wire/part-l1.wav')
    // 落库句柄必须是 localDb（cloudDb 在 local 模式是 null，旧代码 cloudDb! 把 null 递进去）
    assert.equal(seen.saveSegmentCalls.length, 1)
    assert.equal(seen.saveSegmentCalls[0].db.__kind, 'local-edit',
      'local 模式落库没走 localDb 句柄 ⇒ 旧代码 cloudDb! 把 null 递给 saveSegment ⇒ ownerOf(null) TypeError')
    assert.equal(seen.saveSegmentCalls[0].seg.text, '桩转写结果')
  })

  it('★ cloud 模式落库仍走 cloudDb（两种租约不混用 —— 回归守卫）', async () => {
    reset()
    seen.moduleMode = 'cloud'
    const blob = new Blob(['stub'], { type: 'audio/wav' })
    await ingest(blob)
    assert.equal(seen.saveSegmentCalls.length, 1)
    assert.equal(seen.saveSegmentCalls[0].db.__kind, 'cloud-sync',
      'cloud 模式落库被换成了 local-edit 租约 ⇒ 丢失 policy epoch 校验（行为回归）')
  })
})
