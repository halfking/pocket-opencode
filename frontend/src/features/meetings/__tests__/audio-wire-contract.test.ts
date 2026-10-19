// audio-wire-contract.test.ts — 「落盘音频 → 上传给网关的那份」这条接缝的行为门。
//
// ★ 这条接缝此前**在 node 里根本跑不到**：`refetchFullTranscript` 第一件事就是
//   `loadMeetingAudio`（IndexedDB + URL.createObjectURL，node 里两者都没有），
//   所以单测只能验到「降级不抛」那一步。
//   §50 的 webm 400 就是这么漏了四轮 —— 门是绿的，真机是坏的。
//   本轮给 `refetchFullTranscript` 开了 `loadAudio` 注入口，
//   于是「webm 进、wav 出」从**阅读理解题**变成**可执行断言**。
//
// ⚠ 上游模拟器不是编的：它复刻的是 2026-10-07 对真网关的实测
//   POST {gateway}/audio/transcriptions  model=mimo-v2.5-asr
//     webm → 400 invalid_audio_request
//           `audio format "webm" is not supported by the chat-audio bridge
//            (supported: mp3, wav)`
//     wav  → 200 {"duration":18,"text":"…"}
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { refetchFullTranscript } from '../meeting-final-transcript.ts'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

/** 真机产物：`VadSegmenter.stop()` 产的就是 `audio/webm`。 */
const WEBM = new Blob([new Uint8Array([0x1a, 0x45, 0xdf, 0xa3, 0x9f, 0x42, 0x86, 0x81])], { type: 'audio/webm' })
const WAV = new Blob([new Uint8Array(64)], { type: 'audio/wav' })

/** 复刻真网关行为的假上游：webm 直接 400，wav 返回真输出。 */
function fakeGateway() {
  const seen: { type: string; filename: string }[] = []
  const call = async (blob: Blob, filename: string) => {
    seen.push({ type: blob.type, filename })
    if (blob.type === 'audio/webm') {
      // 真网关原文：invalid_audio_request / 不支持 webm
      throw new Error('invalid_audio_request: audio format "webm" is not supported by the chat-audio bridge (supported: mp3, wav)')
    }
    return {
      text: '下周三下午三点，跟王总还有林兰开个客户评审会。',
      failed: 0,
      segments: [
        { index: 0, startSec: 0, endSec: 9, text: '下周三下午三点，跟王总还有林兰开个客户评审会。' },
        { index: 1, startSec: 9, endSec: 18, text: '要准备对比表。' },
      ],
    }
  }
  return { call, seen }
}

const load = (blob: Blob) => async () => blob
/** 假的转码：webm → wav（只换容器，不碰内容）。 */
const fakeToWav = async (blob: Blob) => {
  if (blob.type !== 'audio/webm') return null
  return new Uint8Array([0x52, 0x49, 0x46, 0x46]).buffer  // 'RIFF'
}

describe('接缝：真机 webm 必须被转成 wav 才上传（§50）', () => {
  it('★ 不给 toWav 时，webm 原样上传 ⇒ 上游 400 ⇒ 整段重转失败', async () => {
    // 这条是**回归锚**：它把「没有转码会发生什么」钉成可执行事实。
    // 之前它只是一段注释里的推断。
    const gw = fakeGateway()
    const r = await refetchFullTranscript('m1', gw.call, { loadAudio: load(WEBM) })
    assert.equal(r.applied, false, 'webm 直传竟然成功了？与真网关实测不符')
    assert.match(String(r.reason), /transcribe-failed/, `实测 reason=${r.reason}`)
    assert.deepEqual(gw.seen, [{ type: 'audio/webm', filename: 'meeting.webm' }],
      '上游收到的不是 webm —— 说明模拟器与真网关行为不一致')
  })

  it('★★ 给了 toWav 后，上传的是 wav.filename 也是 wav ⇒ 整段重转成功', async () => {
    const gw = fakeGateway()
    const r = await refetchFullTranscript('m1', gw.call, { loadAudio: load(WEBM), toWav: fakeToWav })
    assert.equal(r.applied, true, `给了 toWav 仍然失败：${r.reason}`)
    assert.equal(gw.seen.length, 1)
    assert.equal(gw.seen[0].type, 'audio/wav', '转码没生效，上传的还是 webm')
    assert.equal(gw.seen[0].filename, 'meeting.wav', 'filename 也没跟着换 —— 后端按扩展名判容器')
    assert.ok(r.segments && r.segments.length === 2,
      `逐段回执丢了 ⇒ 说话人归因会整个失效（实测 ${JSON.stringify(r.segments)}）`)
  })

  it('★ 已经是 wav 的音频不得被二次转码（toWav 返回 null 即放行）', async () => {
    const gw = fakeGateway()
    const r = await refetchFullTranscript('m1', gw.call, { loadAudio: load(WAV), toWav: fakeToWav })
    assert.equal(r.applied, true)
    assert.deepEqual(gw.seen, [{ type: 'audio/wav', filename: 'meeting.wav' }])
  })

  it('★ 转码抛错必须降级而不是炸掉整条收尾', async () => {
    const gw = fakeGateway()
    const boom = async () => { throw new Error('decodeAudioData failed') }
    const r = await refetchFullTranscript('m1', gw.call, { loadAudio: load(WEBM), toWav: boom })
    // 回落成原 blob 上传 ⇒ 上游 400 ⇒ applied=false，**但不抛**
    assert.equal(r.applied, false)
    assert.match(String(r.reason), /transcribe-failed/)
    assert.deepEqual(gw.seen, [{ type: 'audio/webm', filename: 'meeting.webm' }],
      '转码失败后没有回落到原 blob —— 那会让整条收尾抛错')
  })

  it('★ 音频没落盘时必须降级成 audio-not-found（不得当成成功）', async () => {
    const gw = fakeGateway()
    const r = await refetchFullTranscript('m1', gw.call, { loadAudio: async () => null, toWav: fakeToWav })
    assert.deepEqual({ applied: r.applied, reason: r.reason }, { applied: false, reason: 'audio-not-found' })
    assert.equal(gw.seen.length, 0, '没有音频却仍然发起了上传')
  })
})

// ─────────────────────────────────────────────────────────────────────────
// §818 · 转码失败必须**留痕**，且唯一的生产调用方必须真的把它喊出来。
describe('§818 转码失败的可见性（真网关行为复刻）', () => {
  it('★ 真链路上（webm → 400）必须同时带 transcodeFailed 与下游 symptom', async () => {
    const gw = fakeGateway()
    const boom = async () => { throw new Error('decodeAudioData failed') }
    const r = await refetchFullTranscript('m1', gw.call, { loadAudio: load(WEBM), toWav: boom })
    assert.equal(r.applied, false)
    // 下游症状：网关拒收 webm
    assert.match(String(r.reason), /transcribe-failed|invalid_audio_request/)
    // ★ 根因：此前这个信息在空 catch 里被吞掉，生产里没有任何出口
    assert.equal(r.transcodeFailed, true,
      '真链路上转码失败必须留痕 —— 否则 reason 只是网关的 400，根因丢失')
    assert.match(String(r.transcodeError), /decodeAudioData failed/)
  })

  it('负控：转码成功时不得出现 transcodeFailed', async () => {
    const gw = fakeGateway()
    const r = await refetchFullTranscript('m1', gw.call, { loadAudio: load(WEBM), toWav: fakeToWav })
    assert.equal(r.applied, true)
    assert.equal('transcodeFailed' in r, false)
  })

  it('★ 失败文案决策必须落在**跑得到的纯函数**里，而不是调用点', () => {
    // ⚠ 这条只能是接线扫描：`finalizeRecording` 依赖 localDB + HTTP，在 node 里跑不到。
    //   第一版修复把判断写在调用点，判据对着 `console.warn` 匹配 ——
    //   实测把 warn 改成 `void 0 && console.warn` 照样 8/8 全绿，**零牙**。
    //   ⇒ 现在只要求「决策由纯函数产出、调用点按结果 warn」，
    //   真正的牙齿在 `finalizeFailureNotice` 的行为用例上。
    const src = readFileSync(
      join(dirname(fileURLToPath(import.meta.url)), '../meeting-recording-finalize.ts'),
      'utf8',
    )
    assert.match(src, /finalizeFailureNotice\s*\(/,
      '调用点没走纯函数 ⇒ 决策又回到跑不到的地方（§52 的老坑）')
    assert.match(src, /const notice = finalizeFailureNotice\([^)]*\)\s*\n\s*if \(notice\)\s+console\.warn/,
      '必须拿到文案才 warn（而不是无条件 warn 或完全不 warn）')
  })
})
