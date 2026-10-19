// meeting-append-notice.test.ts — §836：「把一句话写进会议记录」失败时不得无声，
// 且实时字幕的 commit 不得用 `void` 吞掉 rejection。
//
// §131 一次加了**两条**用户写入路径：
//   ① 更正一段已有转写（onEditSegment）—— 当时就有 try/catch + toast.error
//   ② 追加一句新的话（onAppendUtterance）—— **完全没有**
// ⇒ 同族缺陷的另一半一直没被看见（§835 修的正是 ① 这条链上的另一个入口）。
//
// ② 那条路径上有三次串行写库，失败形态不止一种：
//   A. 什么都没存（saveSegment 抛错）      ⇒ 用户打的那句话凭空消失
//   B. 只存了一半（分段进了、transcript 列没进）⇒ 界面看得见、AI 总结/相关推荐/
//      精校读不到 ⇒ 两处真相不一致
//   C. 全存下了、只是收尾没做完（refreshRelated 抛错）⇒ 用户会以为失败而重复操作
//
// ⚠ 扫描断言一律先经 `codeOnly()` 剥掉整行注释，并且用 `fnBody()` **以函数声明为界**
//   （§834、§835 连续两轮实测：固定宽度窗口会串到相邻函数）。
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import {
  describeAppendFailure,
  LIVE_CAPTION_PERSIST_FAIL,
  LIVE_CAPTION_PERSIST_FAIL_TAG,
  MEETING_APPEND_FAIL_TAG,
  type AppendStage,
} from '../meeting-append-notice.ts'

const HERE = dirname(fileURLToPath(import.meta.url))
const read = (rel: string) => readFileSync(join(HERE, rel), 'utf8')
const RUNTIME = read('../../../native/recordingRuntime.ts')
const DETAIL = read('../MeetingDetailView.vue')

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

describe('§836 D1 · 文案（行为，纯函数）', () => {
  it('★★★ 「什么都没存」与「只存了一半」必须是两句不同的话', () => {
    // 这是本节的核心：若两句相同，用户在「只存了一半」时会重说一遍，
    // 得到**两条重复分段**，而那正是我们要防的后果。
    const none = describeAppendFailure('segment')
    const half = describeAppendFailure('transcript')
    assert.notEqual(none, half, '两种处境给了同一句文案')
  })

  it('★★★ 「只存了一半」必须说清「已经加进分段」且指向受影响的消费者', () => {
    const half = describeAppendFailure('transcript')
    assert.match(half, /已经加进分段|已.*加进分段/, '没说清这句话其实存进去了')
    assert.match(half, /全文/, '没说清缺的是会议全文列')
    assert.match(half, /总结|精校/, '没说清哪些消费者读不到它')
  })

  it('★★ 「什么都没存」必须让用户重说一次（那是唯一能救的动作）', () => {
    for (const s of ['runtime', 'segment'] as AppendStage[]) {
      assert.match(describeAppendFailure(s), /再说一次|再说一遍/, `${s} 没给出可行动作`)
    }
  })

  it('★★ 「全存下了只是收尾没做完」不得说成失败（否则用户会重复操作）', () => {
    const t = describeAppendFailure('related')
    assert.match(t, /已经加进会议记录|已经加进/, '没说清正文其实已经写进去了')
    assert.match(t, /不影响正文/, '没说清正文不受影响')
  })

  it('★ 未知 stage 仍必须给一句人话（不得空串、不得 undefined）', () => {
    const t = describeAppendFailure('nope' as AppendStage)
    assert.ok(t.length > 0, '未知 stage 产出了空串')
    assert.doesNotMatch(t, /undefined|\[object|NaN/, `未知 stage 产出假读数：${t}`)
  })

  it('负控：任意异常形态都不得渗进用户可见文案', () => {
    for (const e of [null, undefined, 42, {}]) {
      for (const s of ['runtime', 'segment', 'transcript', 'related'] as AppendStage[]) {
        const t = describeAppendFailure(s, { error: e })
        assert.doesNotMatch(t, /undefined|\[object Object\]|NaN/, `${s}/${String(e)} 产出假读数：${t}`)
      }
    }
  })

  it('★ 两个诊断前缀必须区分得开（D1 手工追加 vs D2 实时字幕）', () => {
    assert.match(MEETING_APPEND_FAIL_TAG, /追加/, '')
    assert.match(LIVE_CAPTION_PERSIST_FAIL_TAG, /实时字幕/, '')
    assert.notEqual(MEETING_APPEND_FAIL_TAG, LIVE_CAPTION_PERSIST_FAIL_TAG, '两个前缀撞了 ⇒ 控制台里分不清')
  })
})

describe('§836 D2 · 实时字幕入库失败（行为，纯函数）', () => {
  it('★★★ 必须说清「字幕还在显示、但没存进去」（否则用户以为字幕就是记录）', () => {
    assert.match(LIVE_CAPTION_PERSIST_FAIL, /字幕还在显示|仍在显示/, '没说清字幕还在滚')
    assert.match(LIVE_CAPTION_PERSIST_FAIL, /没有存进|没存进/, '没说清没存进去')
  })

  it('★★ 不得谎报录音坏了（那会让用户以为会议要重来）', () => {
    assert.doesNotMatch(LIVE_CAPTION_PERSIST_FAIL, /录音.*(坏了|中断|丢失)/, '文案让用户误以为录音出了问题')
  })
})

// ── 接线（源码扫描，⚠ 对「信号变死代码」零牙，只保证真的接上了）──
describe('§836 · 接线（扫描层，零牙已如实标注）', () => {
  const appendBody = (): string => fnBody(codeOnly(DETAIL), 'async function onAppendUtterance(')

  it('★★★ 整条追加流程必须在 try 里，且失败要出声', () => {
    const b = appendBody()
    assert.match(b, /try \{/, 'onAppendUtterance 没有 try ⇒ 用户打的话会静默消失')
    assert.match(b, /catch \(e\)/, 'onAppendUtterance 没有 catch')
    assert.match(b, /console\.error\(MEETING_APPEND_FAIL_TAG, appendStage\.value, e\)/, '失败没有进日志')
    assert.match(b, /toast\.error\(describeAppendFailure\(appendStage\.value/, '失败没有用纯函数出文案')
  })

  it('★★★ 三个写库点各自记 stage（否则文案会说错用户的处境）', () => {
    const b = appendBody()
    assert.match(b, /appendStage\.value = 'segment'[\s\S]*await saveSegment\(/,
      'saveSegment 之前没有记 segment 阶段')
    assert.match(b, /appendStage\.value = 'transcript'[\s\S]*await updateTranscript\(/,
      'updateTranscript 之前没有记 transcript 阶段（只存了一半的形态会被说成「什么都没存」）')
    assert.match(b, /appendStage\.value = 'related'[\s\S]*await refreshRelated\(/,
      'refreshRelated 之前没有记 related 阶段')
  })

  it('★★ 「segment 阶段」必须落在 saveSegment 之后、updateTranscript 之前（顺序决定语义）', () => {
    // 若把 stage 标在 saveSegment **之前**，则 saveSegment 抛错时用户会收到
    // 「已经加进分段、只是全文没同步」⇒ 他不会重说 ⇒ 那句话真的丢了。
    const b = appendBody()
    const atSegment = b.indexOf("appendStage.value = 'segment'")
    const atSave = b.indexOf('await saveSegment(')
    const atTranscript = b.indexOf("appendStage.value = 'transcript'")
    assert.ok(atSegment > 0 && atSave > atSegment && atTranscript > atSave,
      '三个标记的顺序不对：saveSegment 抛错时不得被告知「已存一半」')
  })

  it('⚠ 前提登记：既有门 `segment-edit-proofread` 依赖的 `if (!saved)` 分支仍在同一函数里', () => {
    // §836 第一版把流程拆成 `appendUtteranceInner`，那会让这道门失明
    // （它用 fnBody(…, 'onAppendUtterance') 找落库分支）⇒ 改回单函数内联 try。
    const b = appendBody()
    assert.match(b, /if \(!saved\)/, '落库分支已不在 onAppendUtterance 内 ⇒ 既有门会失明')
    // ⚠ 只钉 `if (!saved)` 不够：把整个分支**搬走**、同时在原处留一个同名空壳，
    //   那条断言照样全绿（实测 0 红）⇒ 这里同时要求分支的**内容**（saveSegment）也在体内。
    assert.match(b, /await saveSegment\(/, '落库分支只剩空壳，真正的写入被搬到了别处 ⇒ 既有门失明')
  })

  it('★★★ 实时字幕的 commit 不得再被 `void` 吞掉 rejection', () => {
    const b = fnBody(codeOnly(RUNTIME), 'private startLiveCaption()')
    assert.ok(b.length > 0, '★ 阳性对照失败：抽不到 startLiveCaption')
    assert.doesNotMatch(b, /void this\.appendText\(text\)\s*\}/,
      'commit 又回到 `void this.appendText(text)` ⇒ 抛错时什么都不发生')
    assert.match(b, /void this\.appendText\(text\)\.catch\(/, 'commit 没有兜住 rejection')
    assert.match(b, /this\.sttError\.value = LIVE_CAPTION_PERSIST_FAIL/, '入库失败没有写进可见的错误行')
    assert.match(b, /console\.warn\(LIVE_CAPTION_PERSIST_FAIL_TAG/, '入库失败没有进日志')
  })
})
