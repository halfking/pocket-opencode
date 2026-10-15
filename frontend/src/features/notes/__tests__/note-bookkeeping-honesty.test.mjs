// note-bookkeeping-honesty.test.mjs — §127：服务端无条件做的自动记账，
// 用户必须被告知 —— 而且不许谎报。
//
// ── 缺陷（真缺口，已定位多轮，本轮修）──
//
// `handleNoteSummarize`（backend/internal/server/server_assistant.go:704-755）
// 每次请求都会尝试自动记账，**没有任何调用方开关**：
//   · `s.financeStore` 在 `server.go:299` 恒非 nil；
//   · `finance.NewRecognizer().Parse()` 对「中午吃饭花了 38 块」这种普通口语就命中；
//   · 响应里回 7 个键，其中 3 个是记账结果：`transactions` / `bookkeeping` /
//     `bookkeeping_mismatch`。
//
// 而**随手记列表页**（录音停止后自动走的那条路）此前只解构
// `{ summary, action_items }` —— 记账三键在那一侧被直接丢弃。
// 纯记账笔记（没有行动项）尤其惨：`created === 0` 让
// `buildNoteTodoNotice` 的首行 `if (result.created <= 0) return ''` 直接早退，
// 连一个字的提示都没有。
// ⇒ **用户的账上凭空多一笔，屏幕上完全不知情。**
//
// ── 这道门钉什么 ──
//
// 1. **行为层**（`buildNoteTodoNotice` 的返回值，纯函数可直接断言）：
//    早退的判据必须是「**什么都没做**」而不是「没建待办」；
//    `bookkeeping === 'existing'` **不许**说成「已入账」（那一刻服务端什么都没创建）。
// 2. **接线层**：每个 summarize 入口都必须把记账结果接进提示。
//    清单由机器给（扫 features/notes/ 下所有能触发 summarize 的文件）。
//
// ── 为什么不用「扫源码里有没有 bookkeeping」这种判据 ──
//
// 声明了不等于消费了：本仓已被「只声明四键」骗过一次（§125）。
// 这里要求的是**调用点实参里带上了记账**—— 扫注释里的同一段话不算数。
import assert from 'node:assert/strict'
import { readFileSync, readdirSync, statSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, it } from 'node:test'

import { buildNoteTodoNotice } from '../note-todo-notice.ts'

const HERE = path.dirname(fileURLToPath(import.meta.url))
const NOTES = path.resolve(HERE, '..')
const EXT = new Set(['.vue', '.ts', '.mjs', '.js'])

function sourceFiles(dir, acc = []) {
  for (const name of readdirSync(dir)) {
    const full = path.join(dir, name)
    if (statSync(full).isDirectory()) {
      if (name === '__tests__' || name === 'node_modules') continue
      sourceFiles(full, acc)
    } else if (EXT.has(path.extname(name)) && !/\.test\.[a-z]+$/.test(name)) {
      acc.push(full)
    }
  }
  return acc
}

/** 剥 JS 注释：状态机判断注释，输出保留字符串内容（沿用 §125 已验证的形态）。 */
function stripJsComments(src) {
  let out = ''
  let i = 0
  const n = src.length
  let state = 'code'
  while (i < n) {
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

function codeOf(raw, file) {
  const noHtml = raw.replace(/<!--[\s\S]*?-->/g, (m) => '\n'.repeat(m.split('\n').length - 1))
  if (!file.endsWith('.vue')) return stripJsComments(noHtml)
  const m = noHtml.match(/<script[^>]*>([\s\S]*?)<\/script>/)
  return m ? stripJsComments(m[1]) : ''
}

/**
 * 按括号/花括号/方括号**三重深度**配平，切出 `call(` 的全部顶层实参。
 *
 * ⚠ 第一版只数圆括号，结果 `buildNoteTodoNotice({ created, reminders, ... }, {...})`
 *   的**第一个对象字面量的逗号被当成顶层分隔符** ⇒ 第 2 个实参读成 `reminders`，
 *   两个真入口一起被判未接线。
 *   ⇒ 与 §131 的教训同款：**拿分隔符当边界前，先确认那个分隔符在真实写法里
 *   真的是分隔符**。§125 那份同名字段能过，纯粹因为那里的实参里没有对象字面量。
 *   同时做字符串感知，否则实参里的字符串含逗号/括号会再切错一次。
 */
function callArgs(code, call) {
  const i = code.indexOf(call)
  if (i < 0) return null
  let paren = 0
  let brace = 0
  let bracket = 0
  let started = false
  let cur = ''
  const args = []
  for (let j = i + call.length - 1; j < code.length; j++) {
    const c = code[j]
    if (c === "'" || c === '"' || c === '`') {
      // 字符串照抄到收尾的同类引号（转义跳过下一个字符）
      const quote = c
      cur += c
      j++
      while (j < code.length && code[j] !== quote) {
        if (code[j] === '\\') { cur += code[j]; j++; if (j < code.length) { cur += code[j]; j++ } ; continue }
        cur += code[j]
        j++
      }
      cur += quote
      continue
    }
    if (c === '(') { paren++; started = true; continue }
    if (c === ')') {
      paren--
      if (paren === 0 && brace === 0 && bracket === 0) { args.push(cur); return args.map((s) => s.trim()) }
      if (paren > 0) cur += c
      continue
    }
    if (c === '{') { brace++; cur += c; continue }
    if (c === '}') { brace--; cur += c; continue }
    if (c === '[') { bracket++; cur += c; continue }
    if (c === ']') { bracket--; cur += c; continue }
    if (c === ',' && paren === 1 && brace === 0 && bracket === 0) { args.push(cur); cur = ''; continue }
    if (started) cur += c
  }
  return null
}

/** 能触发笔记总结的两种写法（与 §125 门同口径）。 */
const ENTRY_PATTERNS = [
  /notesApi\.summarize\s*\(/,
  /\/api\/notes\/[^\n`]*?\/summarize/,
]

function entryPoints() {
  const hits = []
  for (const full of sourceFiles(NOTES)) {
    const code = codeOf(readFileSync(full, 'utf8'), full)
    if (ENTRY_PATTERNS.some((re) => re.test(code))) hits.push(full)
  }
  return hits.sort()
}

/**
 * 这个入口把记账结果接进提示了吗？返回未接线的理由（空串 = 已接）。
 *
 * 判据卡在**实参**上：`buildNoteTodoNotice(` 的第二个实参里必须出现
 * 记账三键之一（`transactions` / `bookkeeping` / `bookkeepingMismatch`）。
 * ⚠ 只看 `transactions.length > 0` 这种模板侧渲染**不算**：详情页的记账卡片
 *   挂在 `v-if="summary"` 里面，模型给出记账却没给出 summary 时整块不显示。
 */
function noticeReasons(code) {
  const args = callArgs(code, 'buildNoteTodoNotice(')
  if (args === null) return ['根本没有调用 buildNoteTodoNotice ⇒ 收尾做了什么都不说']
  if (args.length < 2) return ['buildNoteTodoNotice 只传了一个实参，缺记账结果 ⇒ 记账没进提示']
  const second = args[1] ?? ''
  if (!/transactions|bookkeeping/i.test(second)) {
    return [`buildNoteTodoNotice 的第 2 个实参是 \`${second}\` ⇒ 记账结果没进提示`]
  }
  return []
}

// ───────────────────────── 夹具 ─────────────────────────

const NO_TODO = { created: 0, reminders: 0, reminderPlanned: 0, unresolved: 0 }
const THREE_TODO = { created: 3, reminders: 2, reminderPlanned: 2, unresolved: 0 }

/** 服务端记账「本次新建」的响应片段。 */
const BK_CREATED = {
  transactions: [{ id: 't1', type: 'expense', amount: 38, category: '餐饮' }],
  bookkeeping: 'created',
  bookkeepingMismatch: false,
}
/** 服务端记账「此前已记、未重复」的响应片段。 */
const BK_EXISTING = {
  transactions: [{ id: 't0', type: 'expense', amount: 38, category: '餐饮' }],
  bookkeeping: 'existing',
  bookkeepingMismatch: false,
}

describe('§127 · 记账发生了就必须说出来', () => {
  it('★ 纯记账笔记（0 待办 + 已入账）不许一个字都不说', () => {
    // 这正是缺陷现场：createNoteTodos 返回 created=0 ⇒ 旧的首行早退直接
    // return '' ⇒ 用户的账上多了一笔，屏幕上一个字都没有。
    const out = buildNoteTodoNotice(NO_TODO, BK_CREATED)
    assert.notEqual(out, '', '纯记账笔记仍然静默 ⇒ 缺陷没修干净')
    assert.match(out, /入账/, `提示里没说出账被动了：${out}`)
    assert.match(out, /38/, `提示里没有金额，用户无法判断动的是哪一笔：${out}`)
  })

  it('早退的判据是「什么都没做」，不是「没建待办」', () => {
    assert.equal(buildNoteTodoNotice(NO_TODO), '',
      '既没有待办也没有入账时仍要出声 ⇒ 屏幕会挂一堆无关提示')
    assert.equal(buildNoteTodoNotice(THREE_TODO).includes('入账'), false,
      '没入账时提示里不该出现「入账」')
    // 方向：支出/收入必须说清，否则「+38」还是「-38」用户分不清
    const income = buildNoteTodoNotice(NO_TODO, {
      ...BK_CREATED,
      transactions: [{ id: 't2', type: 'income', amount: 5000, category: '工资' }],
    })
    assert.match(income, /收入/, `收入方向没说出来：${income}`)
  })

  it('★ existing 不许谎报成「已入账」（那一刻服务端什么都没创建）', () => {
    const out = buildNoteTodoNotice(NO_TODO, BK_EXISTING)
    assert.ok(!/已自动入账/.test(out),
      `existing 说成了「已自动入账」= 谎报一次写入：${out}`)
    assert.match(out, /此前已记/, `existing 必须说清「此前已记」：${out}`)
  })

  it('账对不上时必须提示以记账页为准', () => {
    const out = buildNoteTodoNotice(NO_TODO, { ...BK_EXISTING, bookkeepingMismatch: true })
    assert.match(out, /不一致/, `金额/方向不一致没有提示：${out}`)
    assert.match(out, /记账页/, `不一致时必须指向核对入口：${out}`)
  })

  it('记账与待办同在一条提示里（用户只该看到一行）', () => {
    const out = buildNoteTodoNotice(THREE_TODO, BK_CREATED)
    assert.match(out, /已加入 3 条待办/, out)
    assert.match(out, /入账/, `待办提示里把记账吞掉了：${out}`)
    assert.equal(out.split('，').length >= 2, true, `两者应当出现在同一句里：${out}`)
  })

  it('负控：transactions 是空数组（服务端恒回数组）⇒ 按「没入账」处理', () => {
    // ⚠ 键恒在、服务端可能返回 []。判「有没有入账」只能看长度。
    assert.equal(buildNoteTodoNotice(NO_TODO, { transactions: [], bookkeeping: '' }), '',
      '空数组被当成了有账 ⇒ 每次 summarize 都会提示「已入账」')
    assert.equal(buildNoteTodoNotice(NO_TODO, undefined), '', '不传第二个参数时不应凭空造账')
  })

  it('负控：判据确实能红（把记账那一段抽掉，早退就又回来了）', () => {
    // 与缺陷原始形态同形的「假实现」：只按 created 早退。
    const buggy = (result, bk) => {
      if (result.created <= 0) return ''
      void bk
      return `已加入 ${result.created} 条待办`
    }
    assert.equal(buggy(NO_TODO, BK_CREATED), '',
      '负控前提失败：样本竟然说了话')
    assert.notEqual(buggy(NO_TODO, BK_CREATED), buildNoteTodoNotice(NO_TODO, BK_CREATED),
      '确认项：负控样本与真实现的读数确实不同')
  })
})

describe('§127 · 每个 summarize 入口都必须把记账接进提示', () => {
  const entries = entryPoints()

  it('量具自证：扫得到两个已知入口', () => {
    const names = entries.map((f) => path.basename(f))
    assert.ok(entries.length >= 2, `只扫出 ${names.length} 个入口（${names}）⇒ 抽取器大概率坏了`)
    assert.ok(names.includes('NoteListView.vue'), `列表页没被扫出来 ⇒ 抽取器有盲区（${names}）`)
    assert.ok(names.includes('NoteDetailView.vue'), `详情页没被扫出来 ⇒ 抽取器有盲区（${names}）`)
    assert.ok(!names.some((f) => /test\.|\.test$/.test(f)), `测试文件混进了入口清单：${names}`)
  })

  it('★ 每个入口都必须把记账结果接进提示（清单由机器给）', () => {
    const broken = []
    for (const full of entries) {
      const reasons = noticeReasons(codeOf(readFileSync(full, 'utf8'), full))
      if (reasons.length) broken.push(`${path.basename(full)}：${reasons.join('；')}`)
    }
    assert.deepEqual(broken, [],
      '这些 summarize 入口会让服务端记账，却没告诉用户：\n  - ' + broken.join('\n  - '))
  })

  it('负控：只传一个实参的原始形态必须判未接线', () => {
    const before = `
      const { summary, action_items: actionItems } = await notesApi.summarize(id)
      const { created } = await createNoteTodos(id, actionItems, title)
      todoNotice.value = buildNoteTodoNotice({ created })
    `
    const reasons = noticeReasons(stripJsComments(before))
    assert.ok(reasons.some((r) => r.includes('记账')),
      `负控失败：单实参形态被判成了已接线：${JSON.stringify(reasons)}`)
  })

  it('负控：第二个实参里只有无关值（实参名对、值错）也判未接线', () => {
    const wrongValue = `
      const { summary } = await notesApi.summarize(id)
      todoNotice.value = buildNoteTodoNotice({ created: summary })
    `
    assert.ok(noticeReasons(stripJsComments(wrongValue)).length > 0,
      '负控失败：传错值被判成了已接线')
  })

  it('负控：记账键只写在注释里 ⇒ 仍判未接线', () => {
    const commented = `
      const { summary } = await notesApi.summarize(id)
      // 这里应该把 { transactions, bookkeeping } 也传进去
      todoNotice.value = buildNoteTodoNotice({ created: 1 })
    `
    assert.ok(noticeReasons(stripJsComments(commented)).length > 0,
      '负控失败：注释里的接线被判成了真接线')
  })

  it('负控：完全没调用提示函数 ⇒ 判未接线', () => {
    const silent = `
      const { summary } = await notesApi.summarize(id)
      summary.value = summary
    `
    assert.ok(noticeReasons(stripJsComments(silent)).some((r) => r.includes('没有调用')),
      '负控失败：一声不吭的入口被判成了已接线')
  })
})
