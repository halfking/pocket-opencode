// meeting-todo-already-added-notice.test.mjs — §410：会议详情页「总结」重复点击时，
// 不得把「本次一条都没新建」说成「行动项没有可解析的期限，未建提醒」。
//
// ── 缺陷 ──
//
// `createMeetingTodos` 返回四个计数器：
//   { count, reminders, reminderPlanned, unresolved }
// 而 `MeetingDetailView.onSummarize` 只解构了**三个** —— `count` 被丢掉，
// `buildMeetingReminderNote(reminders, reminderPlanned, unresolved)` 也没有第四个形参。
// 于是「本次一条都没新建」与「行动项没有可解析的期限」在返回值上**完全同形**（0/0/0）。
//
// ⚠ 这不是假想路径，两条都是生产里的主路径：
//   ① `alreadyCreatedTodo`（§153）让「总结」按钮可以重复点，
//      而**既有门 `meeting-todo-persist-idempotency.test.mjs:122` 就断言
//      第二次调用 `count === 0`** —— 那个 0 是既有门自己钉死的。
//   ② 录完音的 `finalizeRecording` → `ingestMeetingArtifacts` 已经拿
//      **同一份 `liveSummary.actionItems`** 写进了 local_todos；
//      用户随后点「总结」时 `alreadyCreatedTodo` 逐条命中。
//
// 实测（改前）：`buildMeetingReminderNote(0, 0, 0)` ⇒
//   「已生成当前总结（行动项没有可解析的期限，未建提醒）」
// 而事实是：待办在库里且带真实 due_at，提醒也已建成。
// ⇒ 产品在**用户唯一能看到的那句话**上把自己的状态说反了。
//
// ── 为什么随手记侧没有这个洞 ──
//
// `note-todo-notice.ts` 的 `buildNoteTodoNotice` 首行就是
// `if (result.created <= 0) return bk` —— §147 落地时**顺手把 created 接上了**。
// 会议侧是同一族需求的另一个出口，当时没跟上（同 §150/§153 的形状）。
//
// ── 这道门钉什么 ──
//
// 1. 行为层（纯函数返回值，就是用户实际看到的那句话）：4 例。
// 2. 形态层（调用点必须把 `count` 传下去，且注释不算接线）：3 例。
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, it } from 'node:test'

import { buildMeetingReminderNote } from '../meeting-reminder-note.ts'
import { balancedSpan } from '../../../__tests__/source-scan.mjs'

const HERE = path.dirname(fileURLToPath(import.meta.url))
const MEETINGS = path.resolve(HERE, '..')
const DETAIL = path.join(MEETINGS, 'MeetingDetailView.vue')
const PERSIST = path.join(MEETINGS, 'meeting-todo-persist.ts')

const RAW = readFileSync(DETAIL, 'utf8')

/** 与 meeting-action-items-source.test.mjs 同款：注释里的同一句话不算接线。 */
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

const PERSIST_CODE = stripJsComments(readFileSync(PERSIST, 'utf8'))

/** onSummarize 的函数体（形态判据只看这一段，避免扫到文件别处）。
 *
 * ⚠ §428：原来是 `CODE.slice(at, at + 1600)`，而 onSummarize 实测 **2845** 字符
 *   —— 窗口只覆盖 56.2%，末尾约 28 行在窗口之外。
 *   它自称「函数体」却只给前缀，而 §427 那类修复恰恰是**在函数末尾追加**的
 *   （当时把 onSummarize 改成调用 renderTranscript）。
 *   ⇒ 改成按花括号配平取整个函数体：覆盖由语法决定，不由字符数决定。
 */
function summarizeBody() {
  const span = balancedSpan(CODE, 'async function onSummarize(', '{')
  assert.ok(span, 'MeetingDetailView.vue 找不到 onSummarize')
  return span.span
}

describe('§410 · 行为层：重复点「总结」时用户看到的那句话', () => {
  it('★ created=0（全部命中查重）不得说成「没有可解析的期限 / 未建提醒」', () => {
    const s = buildMeetingReminderNote(0, 0, 0, 0)
    assert.ok(!/没有可解析的期限/.test(s),
      `把「一条都没新建」说成了「没有可解析的期限」：${s}`)
    assert.ok(!/未建提醒|未能创建/.test(s),
      `事实上提醒早已建成，却说本次没建：${s}`)
    assert.match(s, /未重复|此前已加入/,
      `没有告诉用户「这些行动项此前就已经加过了」：${s}`)
  })

  it('★ 四参与三参在同一组数字上必须给出不同的话（否则第四格形同虚设）', () => {
    const withCount = buildMeetingReminderNote(0, 0, 0, 0)
    const withoutCount = buildMeetingReminderNote(0, 0, 0)
    assert.notEqual(withCount, withoutCount,
      `传了 created 却得到同一句 ⇒ 第四格没有生效：${withCount}`)
    // 三参形态必须逐字保持原样，既有判据（reminder-outcome-honesty）钉的就是它。
    assert.equal(withoutCount, '（行动项没有可解析的期限，未建提醒）',
      '三参形态被改动了 ⇒ 既有门 reminder-outcome-honesty 钉的字符串失效')
  })

  it('★★ 负控：这一支必须排在 reminderPlanned 判定**之前**，否则 0/0 仍会说错', () => {
    // 复刻「把 created 分支挪到 reminderPlanned<=0 之后」的错误实现：
    // 那时 created===0 落到 planned===0 的老支，说出与缺陷版本逐字相同的话。
    const buggy = (reminders, reminderPlanned, unresolved, created) => {
      const miss = unresolved > 0 ? `，${unresolved} 条期限没听清` : ''
      if (reminderPlanned <= 0) {
        return unresolved > 0
          ? `（${unresolved} 条期限没听清，未建提醒）`
          : '（行动项没有可解析的期限，未建提醒）'
      }
      if (created !== undefined && created <= 0) return '（行动项此前已加入待办，本次未重复添加）'
      if (reminders <= 0) return `，但 ${reminderPlanned} 条日程提醒未能创建${miss}`
      if (reminders < reminderPlanned) return `（${reminders}/${reminderPlanned} 条提醒已建${miss}）`
      return miss
    }
    assert.equal(buggy(0, 0, 0, 0), '（行动项没有可解析的期限，未建提醒）',
      '负控本身写错了：排错后的实现不该复现缺陷文案')
    assert.notEqual(buggy(0, 0, 0, 0), buildMeetingReminderNote(0, 0, 0, 0),
      '真实实现与「分支排错」的实现同形 ⇒ 顺序判据没钉住')
  })

  it('★★ 新分支不得掩盖真实的提醒失败（created>0 时失败仍要如实说）', () => {
    const s = buildMeetingReminderNote(0, 2, 0, 3)
    assert.match(s, /未能创建/, `真实的提醒失败被新分支吞掉了：${s}`)
    assert.ok(!/未重复/.test(s), `created>0 却报「未重复」：${s}`)
    // 边界守卫写反（created <= 0 ⇒ created < 0）时 created===0 会掉回老支，
    // 上面第一条就会红；这里补一条 created=1 的正向，确认守卫没有过度拦截。
    assert.ok(!/未重复/.test(buildMeetingReminderNote(1, 1, 0, 1)),
      'created=1 也被判成「未重复」⇒ 守卫写成了 created <= 1')
  })
})

describe('§410 · 形态层：count 不能再被调用点丢掉', () => {
  it('★ onSummarize 必须把四个计数器一起解构出来', () => {
    const decl = summarizeBody().match(/const\s*\{([^}]*)\}\s*=\s*await createMeetingTodos/)
    assert.ok(decl, 'onSummarize 没有解构 createMeetingTodos 的返回值')
    // ⚠ 刻意**不**写死顺序：本门第一版写的是 `/{count(?:: created)?,[\s\S]{0,80}?reminders/`，
    //   结果和既有门 `reminder-outcome-honesty.test.ts:426` 犯了同一个毛病 ——
    //   两道门都把「count 必须在左花括号之后第一位」当成了契约。
    //   真实契约是「四个计数器都在」，顺序无关。
    //   （那道既有门我没动：改我的代码去适配它，见 MeetingDetailView 的注释。）
    for (const n of ['reminders', 'reminderPlanned', 'unresolved', 'count']) {
      assert.ok(new RegExp(`\\b${n}\\b`).test(decl[1]),
        `解构里没有 ${n} ⇒「{${decl[1]}}」`)
    }
  })

  it('★ onSummarize 必须把 created 传给 buildMeetingReminderNote', () => {
    const body = summarizeBody()
    const call = body.match(/buildMeetingReminderNote\(([^)]*)\)/)
    assert.ok(call, 'onSummarize 没有调 buildMeetingReminderNote')
    assert.match(call[1], /created|count/,
      `buildMeetingReminderNote 没有收到 count（第三格缺席）：(${call[1]})`)
  })

  it('★★ createMeetingTodos 必须真的返回 count（否则上面两判据在测一个不存在的字段）', () => {
    assert.match(PERSIST_CODE, /return\s*\{[^}]*\bcount\b/,
      'createMeetingTodos 的返回语句里没有 count ⇒ 上面两判据钉的是一个不存在的字段')
    assert.match(PERSIST_CODE, /count\+\+/,
      '循环里没有累加 count ⇒ 它恒为 0，首跑也会被说成「此前已加入」')
  })
})