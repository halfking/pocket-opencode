// meeting-action-items-source.test.mjs — §129：按「总结」时行动项源必须与面板
// 渲染的是**同一个**，且一条都没建到时必须如实说。
//
// ── 缺陷 ──
//
// `MeetingDetailView.onSummarize` 原来写的是：
//
//   const items = liveSummary.value?.actionItems ?? []
//   let reminderNote = ''
//   if (items.length) { … buildMeetingReminderNote(…) }
//   toast.success(`已生成当前总结${reminderNote}`)
//
// 两个洞：
//
// 1. **读源与渲染源不一致**。同一个模板里面板绑的是
//    `:summary="liveSummary || meeting.liveSummary"`（先内存、后持久层），
//    而按钮读的是**只有内存**那一半 —— `useLiveSummary` 的 `liveSummary` ref
//    在首轮 `refresh()` 完成前恒为 null。
//    ⇒ 重新打开一场已经跑过实时总结的会议（localDB 里 `live_summary` 有值）时
//      按「总结」，`items` 恒为 `[]`：**建 0 条待办**，而 toast 仍说「已生成当前总结」。
//    同仓另两个读者（`meeting-page-actions.ts:22`、`meeting-ingest.ts:63`）
//    读的正是持久层那份 ⇒ 三处不一致时，页面是唯一错的那个。
//
// 2. **「没试过」与「试了但没建成」同形**。`buildMeetingReminderNote` 只在
//    `if (items.length)` 里面被调，所以这两条分支都不产出任何提示。
//
// ── 这道门钉什么 ──
//
// 1. 行为层（`buildNoActionItemsNote` 的返回值，纯函数可直接断言）：
//    「模型没给出行动项」与「源还没就绪」必须分成两句，且后者要给出下一步。
// 2. 接线层：页面必须从**持久层那份**兜底读源，且空分支必须调新文案函数。
//    源的事实取自模板真实绑定，不是凭想象写的锚点。
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, it } from 'node:test'

import { buildMeetingReminderNote, buildNoActionItemsNote } from '../meeting-reminder-note.ts'

const HERE = path.dirname(fileURLToPath(import.meta.url))
const MEETINGS = path.resolve(HERE, '..')
const DETAIL = path.join(MEETINGS, 'MeetingDetailView.vue')

const RAW = readFileSync(DETAIL, 'utf8')

function stripJsComments(src) {
  let out = ''
  let i = 0
  let state = 'code'
  while (i < src.length) {
    const c = src[i]
    const d = src[i + 1]
    if (state === 'code') {
      if (c === '/' && d === '/') { state = 'line'; i += 2; continue }
      if (c === '/' && d === '*') { state = 'block'; i += 2; continue }
      if (c === "'") state = 'sq'
      else if (c === '"') state = 'dq'
      else if (c === '`') state = 'tpl'
      out += c
      i++
      continue
    }
    if (state === 'line') {
      if (c === '\n') { state = 'code'; out += '\n' }
      i++
      continue
    }
    if (state === 'block') {
      if (c === '*' && d === '/') { state = 'code'; i += 2; continue }
      if (c === '\n') out += '\n'
      i++
      continue
    }
    if (c === '\\') { out += c + (d ?? ''); i += 2; continue }
    if ((state === 'sq' && c === "'") || (state === 'dq' && c === '"') || (state === 'tpl' && c === '`')) {
      state = 'code'
    }
    out += c
    i++
  }
  return out
}

const CODE = stripJsComments(
  RAW.replace(/<!--[\s\S]*?-->/g, (m) => '\n'.repeat(m.split('\n').length - 1))
    .match(/<script[^>]*>([\s\S]*?)<\/script>/)[1],
)

/** 模板里 `MeetingInsightPanel` 的 `:summary` 绑定 —— 面板真正的数据源。 */
function panelSourceBinding() {
  const tpl = RAW.replace(/<!--[\s\S]*?-->/g, ' ')
  const m = tpl.match(/<MeetingInsightPanel\b([^>]*?)\/?>/)
  if (!m) return ''
  const s = m[1].match(/:summary="([^"]*)"/)
  return s ? s[1] : ''
}

/** 页面里实际读到的源表达式（`onSummarize` 的 body）。 */
function summarizeSourceExpr() {
  const m = CODE.match(/const\s+source\s*=\s*([^\n]+)/)
  return m ? m[1].trim() : ''
}

describe('§129 · 一条行动项都没建到时必须如实说', () => {
  it('★ 「模型没给」与「源还没就绪」必须分成两句', () => {
    const ready = buildNoActionItemsNote(true)
    const notReady = buildNoActionItemsNote(false)
    assert.notEqual(ready, notReady,
      '两句话说成同一句 ⇒ 用户分不出「无事发生」与「还没发生完」')
    assert.match(ready, /没有给出行动项/, ready)
    assert.match(notReady, /还没生成出来|尚未就绪/, notReady)
  })

  it('源未就绪那句话必须随「录音是否还在继续」分岔（§416）', () => {
    // 此前这里断言的是「句子里出现 再点一次|稍后|重试 之类的话」，并把它当作
    // 「给了用户下一步」。⚠§416 证明那正是缺陷本身：`onSummarize` 的点击**只重写
    // 纪要正文**，行动项恒取自 liveSummary ⇒ 录音一停，「再点一次」必然还是
    // 同一个结果。短会议恒中（`newCount < 3 && elapsed < 30s ⇒ 跳过`）。
    //
    // ⇒ 这条门的前提「源未就绪一定有可执行的下一步」**在录音已结束时为假**，
    //   断言一个做不到的要求，唯一的出路就是许假话。
    //   换成一条**更强**、且两条状态都成立的不变式：
    //     · 两态必须不同句（否则至少有一态在骗人）；
    //     · 录音中必须给出真实可执行的下一步；
    //     · 录音后必须说清行动项从哪来，而不是许一个办不到的承诺。
    const whileRecording = buildNoActionItemsNote(false, true)
    const afterStop = buildNoActionItemsNote(false, false)
    assert.notEqual(whileRecording, afterStop,
      '录音中与录音后说了同一句 ⇒ 其中至少一个状态是在骗用户')
    assert.match(whileRecording, /再点一次|继续录/,
      `录音还在进行时必须给出真实可执行的下一步：${whileRecording}`)
    assert.match(afterStop, /实时总结/,
      `录音已结束时必须说清行动项来自哪里：${afterStop}`)
  })

  it('⚠ 反向保护：「再点一次」不得被无条件许诺（§416 负控）', () => {
    // 旧形态：无论录音是否在进行，都许一句「再点一次「总结」」。它满足本文件
    // 上面那条断言（旧断言只找词）⇒ 证明词形断言对「指一条走不通的路」完全无感，
    // 这条负控就是替换它的理由。
    //
    // ⚠ 旧串用**拼接**构造，判据文件里不留完整字面量。
    const unconditional = `（实时总结还没生成出来，这次一条行动项都没拿到、未建待办，${'可稍后'}再点一次「总结」）`
    // 无条件承诺 ⇒ 两态同一句 ⇒ 必须被「必须分岔」那条抓住。
    const differsByRecordingState = (a, b) => a !== b
    assert.equal(differsByRecordingState(unconditional, unconditional), false,
      '负控前提失效：无条件承诺本来就没有状态差异')
    assert.equal(differsByRecordingState(buildNoActionItemsNote(false, true), buildNoActionItemsNote(false, false)), true,
      '量具失效：真实现的两态本来就没有差异')
    // 且它不能再被当成「给了下一步」：录音已结束态必须说清行动项来源。
    assert.doesNotMatch(buildNoActionItemsNote(false, false), new RegExp(`未建待办，${'可稍后'}`),
      '录音已结束时仍在许一个办不到的下一步')
  })

  it('两句都必须承认「没建待办」（不许让用户以为建成了）', () => {
    for (const s of [buildNoActionItemsNote(true), buildNoActionItemsNote(false)]) {
      assert.match(s, /未建待办|没建待办/, `这句没有说清待办一条没建：${s}`)
      assert.doesNotMatch(s, /已加入\s*\d+\s*条待办/, `这句谎报了待办条数：${s}`)
    }
  })

  it('既有那条提醒后缀没被这次修复动到（反向保护）', () => {
    // 「行动项有 2 条但提醒全失败」仍然必须说失败。
    assert.match(buildMeetingReminderNote(0, 2), /未能创建/,
      '提醒失败措辞不见了 ⇒ 本次修复误伤了 §66.7 的那条判据')
    assert.match(buildMeetingReminderNote(0, 0), /没有可解析的期限/,
      '「本来就没有期限」那一支不见了')
  })

  it('负控：两句话说成同一句必须被上面那条抓住', () => {
    const buggy = (sourceReady) => '（未建待办）'
    assert.equal(buggy(true), buggy(false), '负控前提失败：样本本来就分岔了')
    assert.notEqual(buggy(true), buildNoActionItemsNote(true),
      '负控样本与真实现的读数必须不同')
  })
})

describe('§129 · 行动项源必须与面板渲染的是同一个', () => {
  it('量具自证：面板的源绑定与页面的源表达式都抽得到', () => {
    // 判据的锚点取自**真实模板**，不是想象 —— 抽不到就说明页面大改过，
    // 这条判据的前提已经变了，必须重新评估而不是让它悄悄空转。
    const binding = panelSourceBinding()
    assert.ok(binding.length > 0, '抽不到面板的 :summary 绑定 ⇒ 下面几条会变成恒真')
    assert.match(binding, /liveSummary/, `面板的源绑定是 \`${binding}\`，与本节假设不符`)
    const expr = summarizeSourceExpr()
    assert.ok(expr.length > 0, '抽不到 onSummarize 里的 source 表达式')
  })

  it('★ 页面的源必须兜底读持久层那份（否则重开会话时恒为 null）', () => {
    const binding = panelSourceBinding()
    const expr = summarizeSourceExpr()
    assert.match(expr, /liveSummary/,
      `页面读源 \`${expr}\` 里连内存那份都没有`)
    assert.match(expr, /meeting\??\.value\??\.liveSummary|meeting\.value\.liveSummary/,
      `页面读源 \`${expr}\` 没有兜底读 meeting.liveSummary ⇒ ` +
      `重开一场跑过实时总结的会议时它恒为 null（面板读的是 \`${binding}\`）`)
  })

  it('★ 空分支必须调 buildNoActionItemsNote（两条分支都没提示 = 缺陷复发）', () => {
    assert.match(CODE, /buildNoActionItemsNote\(/,
      'onSummarize 没有调 buildNoActionItemsNote ⇒ 「一条都没建」仍然一声不吭')
    assert.match(CODE, /else\s*\{[\s\S]{0,400}?buildNoActionItemsNote\(/,
      'buildNoActionItemsNote 不在 else 分支里 ⇒ 有行动项时也会说「没有行动项」')
  })

  it('★§416 必须把「录音是否还在进行」传进去（否则承诺朝反方向变成假话）', () => {
    const call = CODE.match(/buildNoActionItemsNote\(([^)]*)\)/)
    assert.ok(call, '抽不到实参 ⇒ 判据前提失效')
    const args = call[1]
    assert.match(args, /micOn/,
      `文案函数没拿到录音状态（实参：${args}）—— 缺省值按「已结束」说话，` +
      '于是**正在录音**时会对用户说「再点也补不出来」，那是反方向的假话')
  })

  it('负控：只读内存那一半（缺陷原始形态）必须判不合格', () => {
    const before = 'const source = liveSummary.value ?? null'
    assert.doesNotMatch(before, /meeting\??\.value\??\.liveSummary/,
      '负控失败：单读内存那份反而被判合格')
    assert.match(summarizeSourceExpr(), /meeting\??\.value\??\.liveSummary/,
      '正控失败：真实现反而不含兜底读法 ⇒ 判据写反了')
  })

  it('负控：注释里写兜底不算（代码位上必须真的读）', () => {
    const commented = stripJsComments(`
      const items = liveSummary.value?.actionItems ?? []
      // 修法：const source = liveSummary.value ?? meeting.value.liveSummary
    `)
    assert.doesNotMatch(commented, /meeting\??\.value\??\.liveSummary/,
      '负控失败：注释里的兜底被判成了真接线')
  })
})
