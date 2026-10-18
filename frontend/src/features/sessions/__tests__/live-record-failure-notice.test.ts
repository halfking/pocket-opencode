// live-record-failure-notice.test.ts — §839：会话内实时录音链的三个失败形态不得无声。
//
// ① **建会议失败**（createMeeting 裸 await，而 `toggle()` 被 `@live-record`
//    直接绑成事件处理器）⇒ 用户点麦克风什么都不会发生，也没有提示。
//
// ② **启动失败后的收尾被 `.catch(() => {})` 主动静默** ⇒ 它要防的后果
//    （遗留 recording 状态导致每次进会话都弹「上次录音未正常结束」）**恰恰是它
//    自己失败时会发生的后果**，而那条警告对根本没录成的用户是**误导**。
//
// ③ **收尾时读不回分段**（getMeetingWithSegments 裸 await）⇒ finalizeRecording
//    整段不发生（整段重转/说话人归因/精校/行动项全没有），而此时录音已停、
//    状态已写 completed、面板已关 ⇒ 用户看到的是「正常结束」。
//    ⚠ 处置是**停下**而不是继续：残缺输入交给会重写 transcript 的链更危险。
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import {
  describeLiveRecordFailure,
  LIVE_RECORD_FAIL_TAG,
  type LiveRecordStage,
} from '../live-record-failure-notice.ts'

const HERE = dirname(fileURLToPath(import.meta.url))
const SRC = readFileSync(join(HERE, '../useSessionLiveRecord.ts'), 'utf8')
const VIEW = readFileSync(join(HERE, '../SessionConversationView.vue'), 'utf8')

const codeOnly = (text: string): string =>
  text.split('\n').filter((l) => {
    const t = l.trim()
    return !(t.startsWith('//') || t.startsWith('*') || t.startsWith('/*'))
  }).join('\n')

const fnBody = (code: string, name: string, max = 2600): string => {
  const at = code.indexOf(name)
  assert.ok(at > 0, `★ 阳性对照失败：找不到 ${name}`)
  const rest = code.slice(at + name.length, at + name.length + max)
  const next = rest.search(/\n(?:export )?(?:async )?function [A-Za-z_$]/)
  return next > 0 ? code.slice(at, at + name.length + next) : code.slice(at, at + name.length + max)
}

describe('§839 · 文案（行为，纯函数）', () => {
  it('★★★ 三个形态必须是三句不同的话（用户动作完全不同）', () => {
    const a = describeLiveRecordFailure('create')
    const b = describeLiveRecordFailure('cleanup')
    const c = describeLiveRecordFailure('load-segments')
    assert.equal(new Set([a, b, c]).size, 3, '三个形态给了重复文案')
  })

  it('★★★ 「收尾失败」必须说清后果是「一直显示录音中」+ 反复弹「上次录音未正常结束」', () => {
    const b = describeLiveRecordFailure('cleanup')
    assert.match(b, /录音中/, '没说清列表会一直显示「录音中」')
    assert.match(b, /每次进入|以后每次/, '没说清会反复弹那条警告')
    assert.match(b, /删掉|删除/, '没告诉用户怎么收拾这条残留记录')
  })

  it('★★ 「收尾读取失败」必须说清：录音完好、但整段校对与行动项没生成', () => {
    const c = describeLiveRecordFailure('load-segments')
    assert.match(c, /录音已停止|录音完好/, '没说清录音本身没事（用户会以为白录了）')
    assert.match(c, /校对|精校/, '没说清哪一步没做')
    assert.match(c, /行动项/, '没说清行动项也没生成')
  })

  it('★★★ 「建会议失败」必须说清「这次没有留下任何内容」（否则用户会去删不存在的记录）', () => {
    // ⚠ 判据只认「没有留下」这几个字：并集里加一条宽松的备选（第一版有
    //   「没有创建」）⇒ 删掉后半句后前半句仍在，整条断言废掉（实测 0 红）。
    const a = describeLiveRecordFailure('create')
    assert.match(a, /没有留下|未留下/, '没说清有没有留下残留记录')
  })

  it('★ 每个形态都必须给可行动作', () => {
    for (const s of ['create', 'cleanup', 'load-segments'] as LiveRecordStage[]) {
      assert.match(describeLiveRecordFailure(s), /重试|检查|删掉|重新/, `${s} 只报错不给路`)
    }
  })

  it('★ 未知阶段仍必须给一句人话（不得空串 / undefined）', () => {
    const t = describeLiveRecordFailure('nope' as LiveRecordStage)
    assert.ok(t.length > 0, '未知阶段产出空串')
    assert.doesNotMatch(t, /undefined|\[object|NaN/, `未知阶段产出假读数：${t}`)
  })

  it('负控：任意异常形态都不得把技术串漏给用户', () => {
    for (const e of [null, undefined, 42, {}, { message: 'SQLITE_FULL' }]) {
      for (const s of ['create', 'cleanup', 'load-segments'] as LiveRecordStage[]) {
        const t = describeLiveRecordFailure(s, { error: e })
        assert.doesNotMatch(t, /undefined|\[object Object\]|NaN|SQLITE/, `${s}/${String(e)} 漏了技术串：${t}`)
      }
    }
  })

  it('★ 诊断前缀必须点名是会话录音链（与会议侧那几处区分）', () => {
    assert.match(LIVE_RECORD_FAIL_TAG, /session-live-record/, '')
  })
})

// ── 接线（源码扫描，⚠ 对「信号变死代码」零牙，只保证真的接上了）──
describe('§839 · 接线（扫描层，零牙已如实标注）', () => {
  it('★★★ start() 里建会议必须被 try 包住且失败要出声', () => {
    const b = fnBody(codeOnly(SRC), 'async function start() {')
    // ⚠ 不能用「函数体里有没有 try」：start() 后面还有**收尾那处 try**，
    //   变异把建会议那处换成 `if (1) {` 时它照样全绿（实测 0 红）。
    //   ⇒ 判据只问「**建会议调用之前**有没有 try」。
    const atCreate = b.indexOf('await createMeeting(')
    assert.ok(atCreate > 0, '★ 阳性对照失败：找不到建会议那一步')
    const head = b.slice(0, atCreate)
    assert.match(head, /try \{/, '建会议那一步没有被 try 包住 ⇒ 点麦克风写库失败时零反馈')
    assert.match(head, /let m: LocalMeeting/, '没有先把返回值声明出来（说明是裸 await）')
    // ⚠ catch 处理器当然在调用**之后** —— 第一版把它也塞进「调用之前」的窗口，
    //   于是正确写法反而恒红（又一次「窗口切错位置」）。
    const catchAt = b.indexOf('catch (e)')
    assert.ok(catchAt > atCreate, '建会议那一步没有紧随其后的 catch')
    assert.match(b, /console\.error\(LIVE_RECORD_FAIL_TAG, 'create', e\)/, '建会议失败没有进日志')
    assert.match(b, /describeLiveRecordFailure\('create'/, '建会议失败没有用纯函数出文案')
  })

  it('★★★ 收尾不得再被 `.catch(() => {})` 静默（本次修的正是它）', () => {
    const b = fnBody(codeOnly(SRC), 'async function start() {')
    assert.doesNotMatch(b, /\.catch\(\(\) => \{\}\)/,
      '仍有 `.catch(() => {})` ⇒ 收尾失败被静默，而它防的正是自己失败时的后果')
    assert.match(b, /console\.error\(LIVE_RECORD_FAIL_TAG, 'cleanup', e\)/, '收尾失败没有进日志')
    assert.match(b, /describeLiveRecordFailure\('cleanup'\)/, '收尾失败没有用纯函数出文案')
  })

  it('★★ 收尾失败时不得连弹两条 error（单一出口）', () => {
    const b = fnBody(codeOnly(SRC), 'async function start() {')
    // 收尾失败分支必须用三元选择，而不是无条件再补一句「无法开始录音」。
    assert.match(b, /toast\.error\(cleanupFailed\s*\n?\s*\? describeLiveRecordFailure\('cleanup'\)[\s\S]{0,80}无法开始录音'\)/,
      '收尾失败与「无法开始录音」不是单一出口 ⇒ 用户会连弹两条 error')
  })

  it('★★★ stop() 里读分段必须被 try 包住，且失败后**停下**（不得继续跑收尾编排）', () => {
    const b = fnBody(codeOnly(SRC), 'async function stop() {')
    const atRead = b.indexOf('await getMeetingWithSegments(id)')
    assert.ok(atRead > 0, '★ 阳性对照失败：找不到读分段那一步')
    const head = b.slice(Math.max(0, atRead - 300), atRead)
    assert.match(head, /try \{/, '读分段没有被 try 包住 ⇒ 整段收尾编排会静默不发生')
    assert.match(head, /let stored/, '没有先把 stored 声明出来（说明是裸 await）')
    // 失败分支必须在读之后**返回**，且不得落在 finalizeRecording 之前继续跑。
    const catchAt = b.indexOf('catch (e)', atRead)
    assert.ok(catchAt > atRead, '★ 阳性对照失败：找不到读分段的 catch')
    const between = b.slice(catchAt, b.indexOf('await finalizeRecording('))
    assert.match(between, /return/, '读分段失败后没有 return ⇒ 仍会带着空分段去跑会重写 transcript 的链')
    assert.match(between, /describeLiveRecordFailure\('load-segments'/, '读分段失败没有用纯函数出文案')
  })

  it('⚠ 前提登记：toggle 确实被直接绑成事件处理器（① 的前提）', () => {
    assert.match(codeOnly(VIEW), /@live-record="liveRecord\.toggle"/,
      'toggle 不再被直接绑定 ⇒ §839/①「未处理 rejection 就到此为止」的前提变了')
  })
})
