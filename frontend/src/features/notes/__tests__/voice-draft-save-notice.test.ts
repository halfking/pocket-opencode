// voice-draft-save-notice.test.ts — §832：语音草稿**存不下来**时必须有用户可见的出口。
//
// 缺陷：`NoteListView` 的两条建草稿路径（页面在场 `onMicToggle` /
// 跨页补建 `pickupPendingRecording`）此前**都没有 try/catch**，而建草稿要写
// 原生文件或 Web 的 base64 ⇒ 写失败一路抛到事件处理器的调用方，
// 而前端**没有** unhandledrejection 兜底 ⇒ 用户看到的是
// 「录音正常结束、界面恢复原样、什么都没有」：没笔记、没报错，整段录音白录。
//
// ⚠ 与 §820 同族但更重：§820 至少留下一条「谎报完好」的会议记录，这里连记录都没有。
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import { describeVoiceDraftSaveFailure } from '../voice-draft-save-notice.ts'

const HERE = dirname(fileURLToPath(import.meta.url))
const VIEW = readFileSync(join(HERE, '../NoteListView.vue'), 'utf8')

describe('§832 · 文案层（行为，纯函数）', () => {
  const w = describeVoiceDraftSaveFailure({ error: new Error('QuotaExceededError') })

  it('★ 必须说清是什么没存下来（不是「转写失败」）', () => {
    assert.match(w, /草稿没能保存/, '没说清是草稿保存失败')
    assert.match(w, /QuotaExceededError/, '原始原因没进去，排查无从下手')
  })

  it('★★ 必须说清数据不会自动恢复，并给出唯一可行的动作（先抄文字、再重录）', () => {
    assert.match(w, /不会自动恢复|不会.*恢复/, '没说清「录音不会自动回来」')
    assert.match(w, /重录/, '没给出用户能做的动作 —— 只报错不给路，用户会以为只能干等')
  })

  it('★ 跨页补建要带上上下文（那是另一条路径，排查方式不同）', () => {
    const p = describeVoiceDraftSaveFailure({ error: new Error('x'), via: 'pending-pickup' })
    assert.match(p, /补建失败/, '跨页补建没点明上下文')
    assert.notEqual(p, describeVoiceDraftSaveFailure({ error: new Error('x') }),
      '两条路径给了同一句文案 ⇒ 排查分不清是哪条路坏的')
  })

  it('负控：非 Error / 空异常不得产出 undefined 或 [object Object]', () => {
    for (const e of [null, undefined, 42, {}]) {
      const t = describeVoiceDraftSaveFailure({ error: e })
      assert.doesNotMatch(t, /undefined|\[object Object\]|NaN/, `异常形态 ${String(e)} 产出了假读数：${t}`)
      assert.match(t, /未知原因/, '取不到原因时必须给兜底，而不是空白或 undefined')
    }
  })
})

// ── 接线层（源码扫描，⚠ 对「信号变死代码」零牙，只保证两条路径都接上了）──
describe('§832 · 接线（扫描层，零牙已如实标注）', () => {
  const bodyOf = (marker: string): string => {
    const at = VIEW.indexOf(marker)
    assert.ok(at > 0, `找不到函数体：${marker}`)
    return VIEW.slice(at, at + 1200)
  }

  it('★★ 两条建草稿路径都必须被 try 包住', () => {
    for (const marker of ['async function onMicToggle()', 'async function pickupPendingRecording()']) {
      const body = bodyOf(marker)
      assert.match(body, /try \{/, `${marker} 没有 try ⇒ 写失败会变成未处理的 rejection`)
      assert.match(body, /catch \(e\)/, `${marker} 没有 catch`)
      assert.match(body, /describeVoiceDraftSaveFailure\(/, `${marker} 的 catch 没有用纯函数决策`)
      assert.match(body, /console\.error\(/, `${marker} 的失败没有进日志`)
    }
  })

  it('★★ 错误必须渲染出来，且**不能**挂在会被顶掉的 v-else-if 链上', () => {
    assert.match(VIEW, /<p v-if="draftSaveError"[^>]*role="alert"/,
      'draftSaveError 没有独立的错误行（挂在 v-else-if 链上会被下一条分支顶掉）')
    assert.doesNotMatch(VIEW, /v-else-if="draftSaveError"/,
      '挂在 v-else-if 链上：录音已停止时它会被 recError / statusText 那几条分支顶掉')
  })

  it('★ 再次录音前要清掉上一次的失败（否则旧错误会一直挂着）', () => {
    const body = bodyOf('async function onMicToggle()')
    assert.match(body, /draftSaveError\.value = ''/, '新一轮录音没有清掉上一次的保存失败')
  })

  it('⚠ 前提登记：两条路径仍然是「adopt 优先、不重复建」', () => {
    for (const marker of ['async function onMicToggle()', 'async function pickupPendingRecording()']) {
      assert.match(bodyOf(marker), /if \((stopped|pending)\.draftId\) await adoptVoiceDraft\(/,
        `${marker} 不再走 adoptVoiceDraft ⇒ 一次录音会留下两条笔记`)
    }
  })
})
