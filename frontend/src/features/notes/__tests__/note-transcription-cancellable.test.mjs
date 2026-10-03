// note-transcription-cancellable.test.mjs
//
// 锁住「兜底转写可强制终止」这条能力。
//
// 2026-10-03 普查后台作业取消能力时，这是全表**最后一个**空缺，也是唯一
// 落在用户投诉链路上的：点「停止录音」之后，runStop() 会同步等一段**最长
// 10 分钟**的兜底全量转写（/api/stt/transcribe-full，服务端
// fullTranscribeTimeout = 10 分钟，客户端 11 分钟，外层 withTimeout 10 分钟）。
//
// 那段窗口里此前：recording 已是 false、error 还是空、FAB 忙碌禁用、
// 没有进度、没有取消入口。用户看到的就是「点了停止没反应」，而且他
// 唯一能做的操作是重启应用。
//
// 服务端一直是可中止的——handleSttTranscribeFull 的 ctx 派生自
// r.Context()（server_stt_stream.go:82）——缺的只是前端接线。
//
// 负控见文件末尾。

import assert from 'node:assert/strict'
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, it } from 'node:test'

import { noteRecorderUiState } from '../note-recording.ts'

const HERE = path.dirname(fileURLToPath(import.meta.url))
// HERE = frontend/src/features/notes/__tests__ → 上溯 3 级 = frontend/src
const SRC = path.resolve(HERE, '..', '..', '..')
const RUNTIME = path.join(SRC, 'native', 'recordingRuntime.ts')
const STT_API = path.join(SRC, 'api', 'stt-settings.ts')
const VIEW = path.join(SRC, 'features', 'notes', 'NoteListView.vue')
// frontend/src 再上溯两级才是仓库根（第一版这里只上了一级，去到了
// frontend/backend，报 ENOENT——路径算错时错误信息看着像"文件不存在"，
// 很容易被误判成源码问题）
const GO_STREAM = path.resolve(SRC, '..', '..', 'backend', 'internal', 'server', 'server_stt_stream.go')

/** 取 `function <name>(...) { … }` 的函数体（大括号配平），null = 没找到。 */
export function fnBody(src, decl) {
  const start = src.indexOf(decl)
  if (start < 0) return null
  const open = src.indexOf('{', start)
  if (open < 0) return null
  let depth = 0
  for (let i = open; i < src.length; i++) {
    if (src[i] === '{') depth++
    else if (src[i] === '}') {
      depth--
      if (depth === 0) return src.slice(open, i + 1)
    }
  }
  return null
}

const runtime = fs.readFileSync(RUNTIME, 'utf8')
const sttApi = fs.readFileSync(STT_API, 'utf8')
const view = fs.readFileSync(VIEW, 'utf8')
const goStream = fs.readFileSync(GO_STREAM, 'utf8')

describe('兜底转写可强制终止（需求：后台 api 可以强行终止）', () => {
  it('cancelTranscription 在 cancelTranscription 函数体内真的 abort', () => {
    const body = fnBody(runtime, 'cancelTranscription(): boolean {')
    assert.notEqual(body, null, '找不到 NoteRecorderRuntime.cancelTranscription（判据可能已失效）')
    assert.match(
      body,
      /\.abort\(\)/,
      'cancelTranscription 必须真的 abort，否则又退回「只置标记、在途请求继续跑满」',
    )
  })

  it('没有在途转写时返回 false（不是假成功）', () => {
    const body = fnBody(runtime, 'cancelTranscription(): boolean {')
    assert.match(
      body,
      /return false/,
      '没有在途请求可停时应如实返回 false，让调用方知道「什么都没停掉」',
    )
  })

  it('兜底转写那条请求真的带上了 signal', () => {
    const body = fnBody(runtime, 'if (!this.transcript.value.trim() && audioBlob.size > 0) {')
    assert.notEqual(body, null, '找不到兜底转写分支（判据可能已失效）')
    assert.match(body, /controller\.signal/, 'transcribeFull 没有收到 signal，abort 传不到 HTTP 层')
    assert.match(body, /this\.transcribeAbort = controller/, '中止器没挂到实例上，切页/重入就找不到它')
  })

  it('transcribeFull 的签名与 http 选项都接了 signal', () => {
    assert.match(
      sttApi,
      /async transcribeFull\([^)]*signal\?: AbortSignal[^)]*\)/,
      'transcribeFull 没有 signal 形参',
    )
    const body = fnBody(sttApi, "return http<SttFullResult>('/api/stt/transcribe-full'")
    assert.notEqual(body, null, '找不到 transcribeFull 的 http 调用')
    assert.match(body, /\bsignal\b/, 'transcribe-full 的请求选项里没有 signal')
  })

  it('中止不是被当成转写失败弹给用户', () => {
    const body = fnBody(runtime, 'if (!this.transcript.value.trim() && audioBlob.size > 0) {')
    assert.match(
      body,
      /if \(controller\.signal\.aborted\)/,
      '没有区分「用户中止」与「转写失败」，AbortError 会被当成失败弹红字',
    )
    assert.match(
      body,
      /音频仍已保存/,
      '中止文案必须说明音频还在——否则用户以为整段录音丢了',
    )
  })

  it('中止入口真的挂在界面上，且只在收尾转写期间出现', () => {
    assert.match(view, /@click="cancelTranscription"/, '状态行上没有「停止转写」按钮')
    assert.match(view, /v-if="recorderUi\.canCancel"/, '取消按钮没有挂在 canCancel 条件上')
    // canCancel 只在 'stopping' 为真 —— 录音中/空闲时不该出现这个按钮。
    assert.equal(noteRecorderUiState('recording').canCancel, false)
    assert.equal(noteRecorderUiState('idle').canCancel, false)
    assert.equal(noteRecorderUiState('stopping').canCancel, true)
  })

  it('服务端这条路由的 ctx 确实派生自 r.Context()（abort 才真是终止）', () => {
    // 判据只认 `context.WithTimeout(r.Context(), …)` 这种字面量形态。
    // 如果哪天改成 context.Background()，客户端 abort 就只能让前端撒手、
    // 服务端照跑 10 分钟并继续烧上游 ASR 配额——这条断言就是为了拦那个。
    assert.match(
      goStream,
      /context\.WithTimeout\(r\.Context\(\),\s*fullTranscribeTimeout\)/,
      'handleSttTranscribeFull 的 ctx 不再派生自 r.Context()，客户端 abort 不再是真终止',
    )
  })
})

describe('判据自检：负控必须转红', () => {
  const bodyOf = () => fnBody(runtime, 'cancelTranscription(): boolean {')

  it('把 abort 摘掉 → 能力判据转红', () => {
    const re = /\.abort\(\)/
    assert.equal(re.test(bodyOf()), true, '判据在真实代码上就没命中过')
    // 必须真的删掉：写成 /* c.abort() */ 的话文本里仍含 ".abort()"，判据照样绿。
    const broken = runtime.replace(/\n\s*c\.abort\(\)/, '\n    void 0')
    assert.notEqual(broken, runtime, '负控样本没有真的摘掉 abort（替换没命中）')
    assert.equal(
      re.test(fnBody(broken, 'cancelTranscription(): boolean {')),
      false,
      '负控本该转红却判成了通过',
    )
  })

  it('transcribeFull 不接 signal → 接线判据转红', () => {
    const broken = sttApi.replace(
      /async transcribeFull\(audioBlob: Blob, filename = 'meeting\.wav', signal\?: AbortSignal\)/,
      "async transcribeFull(audioBlob: Blob, filename = 'meeting.wav')",
    )
    assert.notEqual(broken, sttApi, '负控样本没有真的删掉 signal 形参（替换没命中）')
    assert.equal(
      /async transcribeFull\([^)]*signal\?: AbortSignal[^)]*\)/.test(broken),
      false,
      '负控本该转红却判成了通过',
    )
  })

  it('服务端 ctx 改用 context.Background() → 服务端判据转红', () => {
    const broken = goStream.replace(
      'context.WithTimeout(r.Context(), fullTranscribeTimeout)',
      'context.WithTimeout(context.Background(), fullTranscribeTimeout)',
    )
    assert.notEqual(broken, goStream, '负控样本没有真的换掉 ctx 来源（替换没命中）')
    assert.equal(
      /context\.WithTimeout\(r\.Context\(\),\s*fullTranscribeTimeout\)/.test(broken),
      false,
      '负控本该转红却判成了通过',
    )
  })

  it('取消按钮在空闲/录音态消失 → 条件判据转红', () => {
    // 真负控：把 idle 分支的 canCancel 改成 true，判据必须报红。
    // （第一版这里写的是 `assert.equal(noteRecorderUiState('idle').canCancel, true === false)`，
    //  那是句永远成立的话——负控写成这样等于没写。）
    const src = fs.readFileSync(path.join(SRC, 'features', 'notes', 'note-recording.ts'), 'utf8')
    const broken = src.replace(
      "return { busy: false, ariaLabel: '开始录音', statusText: '', canCancel: false }",
      "return { busy: false, ariaLabel: '开始录音', statusText: '', canCancel: true }",
    )
    assert.notEqual(broken, src, '负控样本没有真的改到 idle 分支（替换没命中）')
    assert.match(
      broken,
      /ariaLabel: '开始录音', statusText: '', canCancel: true/,
      '负控样本改错了位置，判据没被真正检验',
    )
    // 判据的形态就是「idle 的 canCancel 必须是 false」；样本里它是 true。
    assert.notEqual(
      /ariaLabel: '开始录音'[^}]*canCancel: false/.test(broken),
      true,
      '负控本该转红却判成了通过',
    )
  })
})
