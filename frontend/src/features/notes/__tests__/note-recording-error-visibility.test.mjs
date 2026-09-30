/**
 * 笔记录音转写失败的可见性回归（2026-09-30 真机审计）。
 *
 * 真机报「点击停止没有办法停止，语音没有转成文字」。停止死锁已修（实测
 * 513ms 退出录音态），但「没有转成文字」的表现是**完全静默**：
 * NoteRecorderRuntime.runStop() 把兜底转写的失败写进 this.error，
 * 而唯一渲染它的 NoteRecordingStudio 只在 isRecording 为真时挂载 ——
 * 一点停止组件就卸载，错误随之从界面上消失，用户只看到「点完没反应」。
 *
 * 这里锁住两条约定：
 *   1. 录音结束后仍要把错误显示出来（不能随 Studio 一起卸载）
 *   2. 下一次录音开始时错误要被清空（不能一直挂着）
 */
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, it } from 'node:test'

const HERE = dirname(fileURLToPath(import.meta.url))
const read = (rel) => readFileSync(join(HERE, rel), 'utf8')

describe('note recording error visibility', () => {
  const view = read('../NoteListView.vue')

  it('停止录音后仍渲染转写错误（不随 Studio 卸载消失）', () => {
    // 录音中：Studio 渲染
    assert.match(view, /v-if="isRecording"[\s\S]{0,200}NoteRecordingStudio/,
      '录音中应挂载 NoteRecordingStudio')
    // 停止后：仍有独立的错误提示分支
    assert.match(view, /v-else-if="recError"[\s\S]{0,200}recordErrorText/,
      '停止录音后必须仍渲染转写错误提示')
  })

  it('错误文案走统一归一，不直接上屏原始异常', () => {
    assert.match(view, /apiError\(recError\.value,\s*'errors\.sttNotConfigured'\)/,
      '应复用 useApiError 归一，避免 "Failed to fetch" / 英文错误 JSON 上屏')
  })

  it('错误提示有实际样式（Studio 的 scoped 类不作用到本页）', () => {
    const style = view.slice(view.lastIndexOf('<style'))
    assert.match(style, /\.studio-error\s*\{/, '本页需自带 .studio-error 样式')
    assert.match(style, /\.studio-error[\s\S]{0,200}var\(--danger\)/, '应使用 danger 颜色')
  })

  it('新一次录音会清空旧错误，不会一直挂在界面上', () => {
    const rt = read('../../../native/recordingRuntime.ts')
    const start = rt.slice(rt.indexOf('async start()'), rt.indexOf('async start()') + 1200)
    assert.match(start, /this\.error\.value = ''/,
      'start() 必须清空上一轮的错误')
  })
})
