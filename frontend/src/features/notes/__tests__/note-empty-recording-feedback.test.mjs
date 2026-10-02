// note-empty-recording-feedback.test.mjs
//
// 用户报的「笔记录音点击停止没反应、语音没有转成文字」在代码里的**另一半**形态。
//
// 已有护栏 note-recording-stop-feedback.test.mjs 守的是「收尾窗口要可见」
// （phase=stopping 的忙碌态与状态行）。本条守的是另一段：
//
// 部分 Android WebView 的 MediaRecorder 既不派 onstop 也不派
// ondataavailable（stop() 里那个 3 秒兜底正是为这种 WebView 准备的）。
// 此时 chunks 为空，于是：
//
//   · 兜底转写整段被跳过（它要求 audioBlob.size > 0）
//   · error 保持空串（它只在「失败」时才写，而这条既没失败也没成功）
//   · pendingResult.text 是空串，NoteListView 的
//     `if (pending && pending.text.trim())` 把它**静默丢弃**
//
// 用户点了停止：没有草稿、没有错误提示、音频也是空的。整个过程像是
// 没发生过 —— 这正是「点击停止没有办法停止」被报出来的观感。
//
// 判据抽成纯函数 emptyRecordingNotice：判据本身要能被真正执行到。
// 就地写 if 只能靠源码结构断言守着，而结构断言分不清「代码在跑」和
// 「注释里写过同样的话」——本轮前面几次负控假红都栽在这上面。

import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

import { emptyRecordingNotice } from '../note-recording.ts'

const HERE = dirname(fileURLToPath(import.meta.url))

describe('emptyRecordingNotice —— 判据本身', () => {
  it('文字空 + 音频空：必须给出一句能行动的话', () => {
    const msg = emptyRecordingNotice(false, 0)
    assert.ok(msg.length > 0, '「什么都没录到」却一句话不说，正是被报成卡死的那一段')
    assert.match(msg, /没有录到|麦克风/, '文案要指向「没录到声音」而不是泛泛的失败')
  })

  it('有文字：不得提示（有成果，该建草稿而不是报错）', () => {
    // 这条是防误伤：原生流式已经转出文字、只是没攒到分片，
    // 是**成功**路径，报错会让用户以为录音白录了。
    assert.equal(emptyRecordingNotice(true, 0), '')
  })

  it('文字空 + 有音频：不得提示（兜底转写会接手并自己报错）', () => {
    assert.equal(emptyRecordingNotice(false, 4096), '')
  })

  it('两者都有：不得提示', () => {
    assert.equal(emptyRecordingNotice(true, 4096), '')
  })

  it('**元护栏**：本文件自己的判据不是空转', () => {
    // 反面对照：把上面三条里最容易写成恒真的那条（第三条）反向验证一次。
    // 如果 emptyRecordingNotice 退化成 `return ''`，第 1 条会转红；
    // 如果退化成 `return 'x'`，第 2/3/4 条会转红。两条方向都被钉住。
    assert.equal(emptyRecordingNotice(false, 0).length > 0, true)
    assert.equal(emptyRecordingNotice(true, 0).length > 0, false)
    assert.equal(emptyRecordingNotice(false, 1).length > 0, false)
  })
})

describe('接线：runtime 必须真的用上这个判据（结构断言）', () => {
  const runtime = readFileSync(join(HERE, '..', '..', '..', 'native', 'recordingRuntime.ts'), 'utf8')

  it('import 了 emptyRecordingNotice', () => {
    assert.match(runtime, /emptyRecordingNotice/, 'recordingRuntime 没有引入 emptyRecordingNotice')
  })

  it('调用它时传的是「有没有文字」与「音频字节数」', () => {
    const i = runtime.indexOf('emptyRecordingNotice(')
    assert.ok(i > 0, 'runtime 里没有 emptyRecordingNotice 的调用')
    const call = runtime.slice(i)
    // 实参必须来自真正的运行数据，不能是两个字面量（那等于判据从未被求值）。
    assert.match(call, /transcript\.value\.trim\(\)\s*!==\s*''/, '第一实参必须是「有没有文字」的实时判断')
    assert.match(call, /audioBlob\.size/, '第二实参必须是实际的音频字节数')
  })

  it('非空提示必须写进 error（否则界面那条错误行永远没内容）', () => {
    const i = runtime.indexOf('const emptyNotice = emptyRecordingNotice(')
    assert.ok(i > 0, '没找到 emptyNotice 赋值')
    const after = runtime.slice(i, i + 400)
    assert.match(
      after,
      /if \(emptyNotice\)\s*this\.error\.value\s*=\s*EMPTY_RECORDING_NOTICE/,
      '提示没有写进 this.error —— 列表页的 v-else-if="recError" 就永远不显示',
    )
  })

  it('写进去的必须是常量而不是判据返回值', () => {
    // 写成 `this.error.value = emptyNotice` 会被 stt-error-render-chain 的
    // 「error 写入点必须面向用户」不变量挡下（rhs 无字面文案、没过 sttFailureText）。
    // 那个不变量是对的：它的存在意义是「渲染层可以直接渲染 error」。
    // 所以这里按常量写入，并由那条护栏连同「常量必须是字面量」的校验一起守住。
    const i = runtime.indexOf('const emptyNotice = emptyRecordingNotice(')
    const after = runtime.slice(i, i + 400)
    assert.ok(
      !/this\.error\.value\s*=\s*emptyNotice\b/.test(after),
      '直接写判据返回值会绕过 error 文案不变量',
    )
  })

  it('不得无条件写 error（会把上面各条错误文案抹掉）', () => {
    assert.ok(
      !/this\.error\.value\s*=\s*emptyRecordingNotice\(/.test(runtime),
      'error 被赋成了判据的返回值——判据返回空串时会把已有错误清空',
    )
  })
})

describe('消费端：空结果不得被静默丢弃而不留痕迹', () => {
  const list = readFileSync(join(HERE, '..', 'NoteListView.vue'), 'utf8')

  it('列表页在非录音态仍然渲染 recError', () => {
    // 这是提示能被人看见的最后一环：NoteRecordingStudio 只在 isRecording
    // 时挂载，停止后错误必须由列表页自己那行接住。
    assert.match(list, /v-else-if="recError"/, '停止后没有渲染 error 的分支，提示等于没写')
  })
})
