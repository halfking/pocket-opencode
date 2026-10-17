// meeting-edit-failure-notice.test.ts — §835：会议详情页「一次主动编辑」的失败/半失败不得无声。
//
// **D1 元信息保存：面板先关，写库后才知道成没成**
//   `MeetingSettingsSheet.onSave()` 是 `emit('save', …); emit('close')` ——
//   Vue 的 emit 是**同步**的，父组件 `onMetaSave` 是 async 且**没人 await**
//   ⇒ 面板在写库结果出来之前就关掉了，form 随之销毁；而父侧
//   `await updateMeeting(...)` 没有 try/catch ⇒ 写失败时：一个未处理的
//   rejection + 零反馈 + **用户刚填的内容已从屏幕上消失**
//   ⇒ 失败与成功在界面上完全不可区分（面板关了 = 看起来存好了）。
//
// **D2 说话人改名：半改半不改，且完全无声**
//   `labelSpeaker` 此前是 `for (…) { await updateSegmentSpeaker(…) }`
//   ⇒ 任一段抛错就跳出循环：前面几段**已经改了**、后面几段**一个都没改**，
//   同一个人在正文里一会儿新名一会儿旧名；而 `onSpeakerLabel` 同样没有 try/catch。
//   更靠前一处：声纹标签的 `upsertVoiceprintLabel` 写失败会**直接跳过整个循环**。
//
// ⚠ 本文件所有扫描断言都先经 `codeOnly()` 剥掉整行注释 —— 本节的修复注释**必然**
//   要引述旧写法（`emit('close')`、`try/catch`），不剥就会在自己的注释上翻车
//   （§834 第 5 次、§835 第 6 次同款）。
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import {
  applySpeakerLabelToSegments,
  describeMeetingMetaSaveFailure,
  describeSpeakerRenameFailure,
  MEETING_META_SAVE_FAIL_TAG,
  SPEAKER_RENAME_FAIL_TAG,
} from '../meeting-edit-failure-notice.ts'

const HERE = dirname(fileURLToPath(import.meta.url))
const read = (rel: string) => readFileSync(join(HERE, rel), 'utf8')
const RUNTIME = read('../../../native/recordingRuntime.ts')
const DETAIL = read('../MeetingDetailView.vue')
const SHEET = read('../MeetingSettingsSheet.vue')

/**
 * 取某个函数的函数体（**以下一个函数声明为界**）。
 *
 * ⚠ 本轮实测踩到的坑：用「起点 + 固定字符数」取窗口时，窗口会**串到相邻函数** ——
 *   变异 E/F 把 `try {` 换成 `if (1) {`，本该打红，却因为下一个函数
 *   （onShareTodo）自带 `try {` 而全绿。⇒ 固定宽度窗口是不可靠的。
 */
const fnBody = (code: string, name: string, max = 1600): string => {
  const at = code.indexOf(name)
  assert.ok(at > 0, `★ 阳性对照失败：找不到 ${name}`)
  const rest = code.slice(at + name.length, at + name.length + max)
  const next = rest.search(/\n(?:export )?(?:async )?function [A-Za-z_$]/)
  return next > 0 ? code.slice(at, at + name.length + next) : code.slice(at, at + name.length + max)
}

const codeOnly = (text: string): string =>
  text.split('\n').filter((l) => {
    const t = l.trim()
    return !(t.startsWith('//') || t.startsWith('*') || t.startsWith('/*'))
  }).join('\n')

describe('§835 D2 · 逐段回填（行为，纯函数 + 注入写入）', () => {
  const targets = [['s1', 'p1'], ['s2', 'p2'], ['s3', 'p1']] as const

  it('★ 只处理 profileId 匹配的那些段', async () => {
    const seen: string[] = []
    const r = await applySpeakerLabelToSegments(targets, 'p1', '张伟', async (id) => { seen.push(id) })
    assert.deepEqual(seen, ['s1', 's3'], '写到了不属于该说话人的段')
    assert.equal(r.renamed, 2)
    assert.equal(r.failed, 0)
  })

  it('★★★ 中间一段失败，其余必须继续（这是本次缺陷的核心）', async () => {
    const seen: string[] = []
    const r = await applySpeakerLabelToSegments(targets, 'p1', '张伟', async (id) => {
      seen.push(id)
      if (id === 's1') throw new Error('SQLITE_BUSY')
    })
    assert.deepEqual(seen, ['s1', 's3'], '一段失败后其余段被跳过了 ⇒ 正文里会一半新名一半旧名')
    assert.equal(r.renamed, 1)
    assert.equal(r.failed, 1)
    assert.deepEqual(r.failedIds, ['s1'], '没记下是哪一段失败 ⇒ 用户无法判断哪些没改成')
  })

  it('★★ 全部失败时不得把它说成成功', async () => {
    const r = await applySpeakerLabelToSegments(targets, 'p1', '张伟', async () => { throw new Error('disk full') })
    assert.equal(r.renamed, 0)
    assert.equal(r.failed, 2)
  })

  it('★ 负控：写入函数**同步**抛错也只影响那一段', async () => {
    // 异步函数里 throw 会变成 rejected promise；而这里要防的是同步抛的形态
    // （例如注入的实现是个普通函数在参数校验时就炸了）。
    const r = await applySpeakerLabelToSegments(targets, 'p1', '张伟', ((id: string) => {
      if (id === 's1') throw new Error('sync boom')
    }) as unknown as (segId: string, n: string) => Promise<void>)
    assert.equal(r.failed, 1)
    assert.equal(r.renamed, 1)
  })

  it('⚠ 前提登记：空落点集是合法输入（不能抛）', async () => {
    const r = await applySpeakerLabelToSegments([], 'p1', '张伟', async () => { throw new Error('never') })
    assert.deepEqual(r, { renamed: 0, failed: 0, failedIds: [] })
  })
})

describe('§835 D2 · 文案（行为，纯函数）', () => {
  it('★★ 全部成功时不得提示（否则每次改名都吵一次）', () => {
    assert.equal(describeSpeakerRenameFailure({ renamed: 3, failed: 0, profileFailed: false }), '')
  })

  it('★★★ 部分失败必须带**具体数量**，并说清刷新后仍显示旧名', () => {
    const t = describeSpeakerRenameFailure({ renamed: 4, failed: 2, profileFailed: false })
    assert.match(t, /4/, '没说清改了几段')
    assert.match(t, /2/, '没说清几段没改成')
    assert.match(t, /旧名/, '没说清失败那几段在界面上是什么样子')
  })

  it('★★★ 全失败与部分失败必须是两句不同的话（用户动作不同）', () => {
    const all = describeSpeakerRenameFailure({ renamed: 0, failed: 5, profileFailed: false })
    const some = describeSpeakerRenameFailure({ renamed: 5, failed: 2, profileFailed: false })
    assert.notEqual(all, some, '两种形态给了同一句文案')
    assert.match(all, /没有生效|没能写入/, '全失败没被说成「没生效」')
  })

  it('★★ 声纹标签也失败时必须点明「下次不会被记住」', () => {
    const t = describeSpeakerRenameFailure({ renamed: 2, failed: 0, profileFailed: true })
    assert.match(t, /记住|下次/, '没点明声纹标签没写成 ⇒ 用户以为改名已被记住')
  })

  it('★ 必须说清录音仍在继续（否则用户会以为会议挂了）', () => {
    for (const o of [
      { renamed: 0, failed: 1, profileFailed: false },
      { renamed: 1, failed: 1, profileFailed: false },
      { renamed: 0, failed: 0, profileFailed: true },
    ]) {
      assert.match(describeSpeakerRenameFailure(o), /录音仍在继续/, `形态 ${JSON.stringify(o)} 没说清录音状态`)
    }
  })

  it('负控：任意计数组合都不得产出 undefined / NaN', () => {
    for (const renamed of [0, 1, 7]) {
      for (const failed of [0, 1, 3]) {
        for (const profileFailed of [false, true]) {
          const t = describeSpeakerRenameFailure({ renamed, failed, profileFailed })
          assert.doesNotMatch(t, /undefined|NaN/, `形态 ${renamed}/${failed}/${profileFailed} 产出假读数：${t}`)
        }
      }
    }
  })
})

describe('§835 D1 · 文案（行为，纯函数）', () => {
  const w = describeMeetingMetaSaveFailure({ error: new Error('SQLITE_FULL') })

  it('★★★ 必须说清面板**还开着**、输入还在（这正是修复带来的行为）', () => {
    // ⚠ 第一版写成 /保持打开|还在/ ⇒ 删掉前半句后，后半句「你刚填的内容**还在**输入框里」
    //   照样命中 ⇒ 判据无牙（实测 0 红）。⇒ 只认「面板」这个词。
    assert.match(w, /面板保持打开|面板还开着/, '没说清面板状态 ⇒ 用户会以为内容丢了而重填一遍')
    assert.match(w, /输入框/, '没说清刚填的内容还在')
  })

  it('★★ 必须给出可执行动作', () => {
    assert.match(w, /再保存一次|重试/, '只报错不给路')
    assert.match(w, /存储空间|本地/, '没指向可能的成因')
  })

  it('负控：任意异常形态不得产出 undefined / [object Object]', () => {
    for (const e of [null, undefined, 42, {}]) {
      assert.doesNotMatch(describeMeetingMetaSaveFailure({ error: e }), /undefined|\[object Object\]/,
        `异常形态 ${String(e)} 产出了假读数`)
    }
  })

  it('★ 两个诊断前缀必须与 §834 的收尾写库失败区分得开', () => {
    assert.match(MEETING_META_SAVE_FAIL_TAG, /元信息保存失败/, '')
    assert.match(SPEAKER_RENAME_FAIL_TAG, /说话人改名/, '')
    assert.notEqual(MEETING_META_SAVE_FAIL_TAG, SPEAKER_RENAME_FAIL_TAG, '两个前缀撞了 ⇒ 控制台里分不清')
  })
})

// ── 接线（源码扫描，⚠ 对「信号变死代码」零牙，只保证真的接上了）──
describe('§835 · 接线（扫描层，零牙已如实标注）', () => {
  it('★★★ sheet 的 save 不许再自己关面板（否则失败与成功不可区分）', () => {
    const body = fnBody(codeOnly(SHEET), 'function onSave()')
    assert.match(body, /emit\('save'/, 'onSave 不再 emit save')
    assert.doesNotMatch(body, /emit\('close'\)/,
      'onSave 又自己 emit(close) 了 ⇒ 面板在写库结果出来之前就关，用户填的内容会消失')
  })

  it('★★★ 父侧保存必须有 try/catch，且**成功才关面板**', () => {
    const body = fnBody(codeOnly(DETAIL), 'async function onMetaSave(')
    assert.match(body, /try \{/, 'onMetaSave 没有 try ⇒ 写库失败变成未处理的 rejection')
    assert.match(body, /catch \(e\)/, 'onMetaSave 没有 catch')
    assert.match(body, /console\.error\(MEETING_META_SAVE_FAIL_TAG, e\)/, '失败没有进日志')
    assert.match(body, /toast\.error\(describeMeetingMetaSaveFailure\(/, '失败没有用纯函数出文案')
    // 关闭必须在写库之后：catch 分支里 return，关面板那行排在 return 之后。
    assert.match(body, /return\s*\n\s*\}\s*\n\s*showSettings\.value = false/,
      '关闭面板的语句不再排在失败 return 之后 ⇒ 失败时面板仍会关掉（用户输入被销毁）')
  })

  it('★★ 改名入口必须 try/catch + 用纯函数决策', () => {
    const body = fnBody(codeOnly(DETAIL), 'async function onSpeakerLabel(')
    assert.match(body, /try \{/, 'onSpeakerLabel 没有 try')
    assert.match(body, /catch \(e\)/, 'onSpeakerLabel 没有 catch')
    assert.match(body, /describeSpeakerRenameFailure\(outcome\)/, '没有用纯函数出文案')
    // ⚠ 断言要的是「**有守卫**」：只写 doesNotMatch/存在性都会被
    //   `if (false) toast.warning(text)` 这种死写法骗过（实测 0 红）。
    assert.match(body, /if \(text\) \{\s*\n\s*toast\.warning\(text\)/,
      '半失败没有告警，或告警没有挂在 `if (text)` 上（全部成功时也会弹一句空的）')
  })

  it('★★★ runtime 侧必须走可测的逐段应用，且声纹写入单独兜住', () => {
    const body = fnBody(codeOnly(RUNTIME), 'async labelSpeaker(')
    assert.match(body, /applySpeakerLabelToSegments\(/, '逐段写入没有走可测的纯函数（判据测不到真实行为）')
    assert.doesNotMatch(body, /await updateSegmentSpeaker\(segId, displayName\)/,
      '调用点仍自己写 for 循环逐段 await ⇒ 一段失败就跳出循环（本次缺陷的原形态）')
    // 声纹标签那一处必须单独包住：它失败曾直接跳过整个分段循环。
    const upAt = body.indexOf('upsertVoiceprintLabel(')
    assert.ok(upAt > 0, '★ 阳性对照失败：找不到声纹标签写入')
    assert.match(body.slice(Math.max(0, upAt - 120), upAt + 400), /try \{/,
      '声纹标签写入没有被单独兜住 ⇒ 它失败会跳过整个分段循环（一段都没改）')
  })

  it('⚠ 前提登记：改名入口仍唯一（speaker-rename-recording-gate 的既有约束）', () => {
    const code = codeOnly(DETAIL)
    const opens = code.match(/showSpeakers\s*=\s*true/g) ?? []
    assert.equal(opens.length, 1, `发现 ${opens.length} 处把 showSpeakers 置 true ⇒ 改名入口不再唯一`)
  })
})
