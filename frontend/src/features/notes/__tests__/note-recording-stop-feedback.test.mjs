// note-recording-stop-feedback.test.mjs
//
// 用户报的「笔记录音点击停止没反应」在代码里的确切形态。
//
// 状态机：recordingRuntime.runStop() 在**第一行**就把 recording 置 false
// （麦克风立刻释放、计时器立刻停），然后同步等一段**最长 10 分钟**的兜底
// 转写（transcribeFull：客户端超时 11 分钟 / 外层 withTimeout 10 分钟）。
// 这段窗口里：
//
//   recording === false              → FAB 立刻变回「开始录音」的空闲外观
//   v-if="isRecording"               → NoteRecordingStudio 被卸载，实时文本消失
//   error 仍是空的                    → 它只在**失败**时才写
//   再点 FAB → toggle() 见 phase==='stopping' 直接返回进行中的 promise
//
// 也就是说：最长 10 分钟的**零反馈**，而且再点毫无变化。
// 这道测试钉住「收尾窗口必须被界面显式表达」这条契约。

import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

import { noteRecorderUiState, nextRecordingState } from '../note-recording.ts'

const HERE = dirname(fileURLToPath(import.meta.url))

describe('noteRecorderUiState —— 收尾窗口必须可见', () => {
  it('stopping：必须表达为忙碌态，并给出文案', () => {
    const s = noteRecorderUiState('stopping')
    assert.equal(s.busy, true, '停止后收尾期间 FAB 必须显示为忙碌/不可点')
    assert.ok(s.statusText.length > 0, '必须有一句「正在转写…」的文案')
    assert.match(s.ariaLabel, /转写|稍候/)
  })

  it('recording：可点、标签是「停止录音」、不显示状态行', () => {
    const s = noteRecorderUiState('recording')
    assert.equal(s.busy, false)
    assert.equal(s.ariaLabel, '停止录音')
    assert.equal(s.statusText, '')
  })

  it('idle：可点、标签是「开始录音」、不显示状态行', () => {
    const s = noteRecorderUiState('idle')
    assert.equal(s.busy, false)
    assert.equal(s.ariaLabel, '开始录音')
    assert.equal(s.statusText, '')
  })

  it('忙碌态只在 stopping 出现（不得污染录音中/空闲）', () => {
    for (const p of ['idle', 'recording']) {
      assert.equal(noteRecorderUiState(p).busy, false, `${p} 不该是忙碌态`)
    }
  })
})

describe('状态机：recording → stopping → idle', () => {
  it('三态闭合，收尾窗口一定被经过', () => {
    const afterToggle = nextRecordingState('idle', 'toggle')
    assert.equal(afterToggle, 'recording')
    const afterStop = nextRecordingState(afterToggle, 'toggle')
    assert.equal(afterStop, 'stopping')
    assert.equal(noteRecorderUiState(afterStop).busy, true)
    assert.equal(nextRecordingState(afterStop, 'drafted'), 'idle')
  })
})

describe('接线：UI 必须真的用上这个状态（结构断言）', () => {
  const widget = readFileSync(join(HERE, '..', 'VoiceRecorderWidget.vue'), 'utf8')
  const list = readFileSync(join(HERE, '..', 'NoteListView.vue'), 'utf8')

  it('FAB 组件提供 busy 形态，而不是只在录音中才变化', () => {
    assert.match(widget, /busy\?:\s*boolean/, 'VoiceRecorderWidget 缺少 busy prop')
    assert.match(widget, /:disabled="busy"/, 'busy 时必须真的禁用按钮')
    assert.match(
      widget,
      /recording,\s*busy/,
      'FAB 的 class 必须同时反映 recording 与 busy——只有 recording 的话，' +
        '点停止后它会立刻退回空闲外观，正是「点了没反应」的观感来源',
    )
  })

  it('列表页渲染「正在转写」状态行', () => {
    assert.match(list, /noteRecorderUiState/, 'NoteListView 没有使用 noteRecorderUiState')
    assert.match(list, /recorderUi\.statusText/, 'NoteListView 没有渲染状态文案')
    assert.match(list, /:busy="recorderUi\.busy"/, 'NoteListView 没有把 busy 传给 FAB')
  })

  it('**对照组**：状态行不能盖住错误行', () => {
    // 没有这条，上面那条可能在错误分支被删掉时照样绿。
    // 两者必须并存：收尾中显示「正在转写」，失败时显示真实原因。
    const errIdx = list.indexOf('v-else-if="recError"')
    const busyIdx = list.indexOf('v-else-if="recorderUi.statusText"')
    assert.ok(errIdx > 0, '错误分支不见了')
    assert.ok(busyIdx > 0, '状态分支不见了')
    assert.ok(errIdx < busyIdx, '错误分支必须排在状态分支前面（v-else-if 是排他的）')
  })
})
