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