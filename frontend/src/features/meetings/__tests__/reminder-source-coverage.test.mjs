// reminder-source-coverage.test.mjs —— §466
//
// 锁住「自动建日程提醒的来源标识全覆盖」这条不变量。
//
// ── 为什么是这条，而不是「生产点有几个」 ──────────────────────────────
//
// §465.1 数清了：`scheduledTasksApi.create` 有 5 个调用点，
// 而**自动**生产提醒的只有 `ensureTodoReminder` 一个函数。
//
// 但「函数只有一个」这个事实本身不够 —— 真正决定用户能不能诊断问题的，是
// 每条提醒**能不能说清自己来自哪条链**。`buildReminderInput` 的入参带了
// `source: 'meeting-ingest' | 'meeting-summary' | 'note-voice'`
// （`meeting-due-plan.ts:61`，注释写「来源标记，落进 payload 便于排查」）。
//
// ⇒ 于是可守的不变量不是「调用点有几个」，而是：
//   **① 类型里声明的每个 source，都必须真的被生产；**
//   **② 每个生产调用点都必须带 source（不许省）；**
//   **③ 不许出现类型里没有的 source（拼写错误会抓）。**
//
// 失败形态都是真的：
//   · 某条链接进来时忘了传 source ⇒ 那条链的提醒**和其他链长得一模一样**，
//     用户报「收到两条重复提醒」时**无法区分是哪条链产出的**；
//   · source 写成 `'meeting_ingest'` ⇒ 同样无法区分，且 TS 类型本该挡住
//     （⚠ 所以本门还要确认类型声明本身没被放宽成 string）。
//
// ── 纪律（§463/§464 攒下来的，每条都踩过）────────────────────────
//
// ① `maskNonCode` 必须**抹掉注释正文**，不能只处理定界符。
// ② 认「调用」用**裸名 + 紧跟 `(`**；传 `foo(` 会把括号吃掉后恒为 0，
//    而 0 恰好就是「某条链没接上」这个结论本身。
// ③ 测试目录必须排除 —— 否则 stub 里的 `ensureTodoReminder(` 会把读数抬上去。

import assert from 'node:assert/strict'
import { readFileSync, readdirSync, statSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, it } from 'node:test'

const HERE = path.dirname(fileURLToPath(import.meta.url))
const SRC = path.resolve(HERE, '..', '..', '..')            // frontend/src
const MEETINGS = path.join(SRC, 'features', 'meetings')
const NOTES = path.join(SRC, 'features', 'notes')
/** 类型声明所在文件。 */
const DUE_PLAN = path.join(MEETINGS, 'meeting-due-plan.ts')
/** 判据自身文件名 —— 排除的唯一文件。 */
const SELF = 'reminder-source-coverage.test.mjs'

/** 掩掉注释**正文**与字符串**内容**；引号与换行保留。 */
function maskNonCode(src) {
  const out = src.split('')
  const n = src.length
  let state = 'code'
  let i = 0
  while (i < n) {
    const c = src[i]
    const c2 = src[i + 1]
    if (state === 'code') {
      if (c === '/' && c2 === '/') { out[i] = ' '; out[i + 1] = ' '; state = 'line'; i += 2; continue }
      if (c === '/' && c2 === '*') { out[i] = ' '; out[i + 1] = ' '; state = 'block'; i += 2; continue }
      if (c === "'") { state = 'sq'; i++; continue }
      if (c === '"') { state = 'dq'; i++; continue }
      if (c === '`') { state = 'tq'; i++; continue }
      i++; continue
    }
    if (state === 'line') {
      if (c === '\n') { state = 'code'; i++; continue }
      out[i] = ' '; i++; continue
    }
    if (state === 'block') {
      if (c === '*' && c2 === '/') { out[i] = ' '; out[i + 1] = ' '; state = 'code'; i += 2; continue }
      if (c !== '\n') out[i] = ' '
      i++; continue
    }
    if (c === '\\') { out[i] = ' '; if (i + 1 < n && src[i + 1] !== '\n') out[i + 1] = ' '; i += 2; continue }
    if ((state === 'sq' && c === "'") || (state === 'dq' && c === '"') || (state === 'tq' && c === '`')) { state = 'code'; i++; continue }
    if (c !== '\n') out[i] = ' '
    i++
  }
  return out.join('')
}

/**
 * 只剥注释，**保留字符串内容**。
 *
 * ⚠ 为什么需要第二个掩码器：`maskNonCode` 会把字符串内容也抹掉，而
 * `source: 'meeting-ingest' | …` 的枚举值**本身就是字符串字面量** ——
 * 用 `maskNonCode` 去抽会得到 0 个值，而 **0 恰好就是「没有声明 source」这个结论**。
 * ⇒ 「我要找的东西住在被抹掉的那一层里」时，必须换一个掩码器，
 * 而不是调正则。
 */
function stripComments(src) {
  const out = src.split('')
  const n = src.length
  let state = 'code'
  let i = 0
  while (i < n) {
    const c = src[i]
    const c2 = src[i + 1]
    if (state === 'code') {
      if (c === '/' && c2 === '/') { out[i] = ' '; out[i + 1] = ' '; state = 'line'; i += 2; continue }
      if (c === '/' && c2 === '*') { out[i] = ' '; out[i + 1] = ' '; state = 'block'; i += 2; continue }
      if (c === "'") { state = 'sq'; i++; continue }
      if (c === '"') { state = 'dq'; i++; continue }
      if (c === '`') { state = 'tq'; i++; continue }
      i++; continue
    }
    if (state === 'line') {
      if (c === '\n') { state = 'code'; i++; continue }
      out[i] = ' '; i++; continue
    }
    if (state === 'block') {
      if (c === '*' && c2 === '/') { out[i] = ' '; out[i + 1] = ' '; state = 'code'; i += 2; continue }
      if (c !== '\n') out[i] = ' '
      i++; continue
    }
    // 字符串态：**保留内容**，只跳过定界符与转义（否则会把两段字符串连成一段）
    if (c === '\\') { i += 2; continue }
    if ((state === 'sq' && c === "'") || (state === 'dq' && c === '"') || (state === 'tq' && c === '`')) { state = 'code'; i++; continue }
    i++
  }
  return out.join('')
}

/** 读一个目录下的生产源码；**排除 `__tests__` 与本文件**（纪律③）。
 *  ⚠ `label` 必须带上：合并两棵根不同的树时，相对路径会互相覆盖，
 *    表现为「实测列表里 notes 的文件少了」——同样是 0/少 读数。 */
function prodSources(dir, label) {
  const out = new Map()
  const walk = (d) => {
    for (const name of readdirSync(d)) {
      if (name === '__tests__') continue
      const p = path.join(d, name)
      const st = statSync(p)
      if (st.isDirectory()) { walk(p); continue }
      if (!/\.(ts|vue)$/.test(name)) continue
      if (name === SELF) continue
      out.set(path.join(label, path.relative(dir, p)), readFileSync(p, 'utf8'))
    }
  }
  if (dir && statSync(dir).isDirectory()) walk(dir)
  return out
}

/**
 * 在**已掩码**的代码里找出「真的调用」某函数的位置。
 * ⚠ `name` 必须是**裸名**；传 `foo(` 会把括号吃掉，随后又在括号后找 `(`，恒为 0。
 * ⚠ 必须排除**函数声明**：被测函数自己所在的那个文件里
 *   `export async function ensureTodoReminder(` 长得和调用一模一样，
 *   不排除就会把**定义处**算成第 4 个生产点。
 */
function callsIn(code, name) {
  const hits = []
  let i = 0
  while (i < code.length) {
    const at = code.indexOf(name, i)
    if (at < 0) break
    if (code.slice(at + name.length, at + name.length + 2).trimStart().startsWith('(')) {
      const lineStart = code.lastIndexOf('\n', at) + 1
      const before = code.slice(lineStart, at)
      const isDecl = /\b(?:async\s+)?function\s*$/.test(before) || /\bfunction\s+[\w$]*\s*$/.test(before)
      if (!isDecl) hits.push(at)
    }
    i = at + name.length
  }
  return hits
}

/** 抹掉注释与字符串内容后再找调用。 */
function callsOf(src, name) {
  return callsIn(maskNonCode(src), name)
}

/** 取第 `idx` 个调用点所在的那一段实参文本（从 `(` 到配平的 `)`）。 */
function argTextOf(code, callAt, name) {
  const open = code.indexOf('(', callAt + name.length)
  let depth = 0
  for (let i = open; i < code.length; i++) {
    if (code[i] === '(') depth++
    else if (code[i] === ')') {
      depth--
      if (depth === 0) return code.slice(open + 1, i)
    }
  }
  return null
}

/** 从类型声明里抽出 `source:` 字段的枚举字面量。 */
function declaredSources() {
  const code = stripComments(readFileSync(DUE_PLAN, 'utf8'))
  const at = code.indexOf('source:')
  assert.ok(at >= 0, `在 ${path.basename(DUE_PLAN)} 里找不到 source 字段 —— 量具失效？`)
  const semi = code.indexOf('\n', at)
  const line = code.slice(at, semi < 0 ? code.length : semi)
  return [...line.matchAll(/'([a-z-]+)'/g)].map((m) => m[1])
}

const trees = new Map([...prodSources(MEETINGS, 'meetings'), ...prodSources(NOTES, 'notes')])
const CALLEE = 'ensureTodoReminder'

/** 收集全部生产调用点及其 source 字面量。
 *
 * ⚠ 这里刻意用 `stripComments`（**保留字符串内容**）而不是 `maskNonCode`：
 *   `source: 'meeting-ingest'` 的值**本身就是字符串字面量**，
 *   用会抹掉字符串的掩码去抽，读数是「没有 source」——
 *   而「没有 source」正是本节要防的那个结论。 */
function producers() {
  const out = []
  for (const [rel, src] of trees) {
    const code = stripComments(src)
    for (const at of callsIn(code, CALLEE)) {
      const args = argTextOf(code, at, CALLEE)
      // ⚠⚠ 字面量必须**照单全收**：第一版写的是 /'([a-z-]+)'/，
      //   结果 `note_voice` 这种**含下划线的拼写错误匹配不上** ⇒ source 被读成 null
      //   ⇒ 「不许出现类型里没有的 source」那条判据直接跳过它。
      //   ⇒ **防拼写错误的那道门，恰好对含下划线的拼写错误失明**（外部变异实测）。
      //   ⇒ 现在抓的是「引号里的任意内容」，比对交给调用方去做。
      const m = args ? args.match(/source\s*:\s*'([^']*)'/) : null
      out.push({ rel, source: m ? m[1] : null, line: code.slice(0, at).split('\n').length })
    }
  }
  return out.sort((a, b) => a.rel.localeCompare(b.rel) || a.line - b.line)
}

describe('§466 · 自动建日程提醒的 source 标识全覆盖', () => {
  // ── 量具先自证 ────────────────────────────────────────────────────
  it('★ 量具自证：能抽到 source 的枚举声明，且恰好 3 个', () => {
    const declared = declaredSources()
    assert.equal(
      declared.length, 3,
      `类型声明里 source 现在有 ${declared.length} 个枚举值（${declared.join(', ')}）——\n` +
        '  ⚠ 若是你**有意**新增了一条链的来源，请连带确认每个新值都真的被生产。',
    )
  })

  it('★ 量具自证：生产调用点恰好是已知那三个文件', () => {
    const got = producers().map((p) => p.rel)
    const want = [
      path.join('meetings', 'meeting-ingest.ts'),
      path.join('meetings', 'meeting-todo-persist.ts'),
      path.join('notes', 'note-todo-persist.ts'),
    ].sort()
    assert.deepEqual(
      got, want,
      `生产调用点的文件集合变了。\n  实测：${JSON.stringify(got)}\n  期望：${JSON.stringify(want)}\n` +
        '  ⚠ 若是有意新增，提醒它必须同时带上可区分的 source。',
    )
  })

  // ── 三条不变量 ────────────────────────────────────────────────────
  it('每个生产调用点都必须带 source（不许省）', () => {
    const missing = producers().filter((p) => p.source === null)
    assert.deepEqual(
      missing.map((p) => `${p.rel}:${p.line}`), [],
      '这些调用点没传 source ⇒ 该链产出的提醒与其它链**无法区分**，\n' +
        '  用户报「收到重复提醒」时就定位不到是哪个链产出的。',
    )
  })

  it('类型里声明的每个 source 都必须真的被生产（反向：覆盖）', () => {
    const declared = declaredSources()
    const produced = new Set(producers().map((p) => p.source))
    const unused = declared.filter((s) => !produced.has(s))
    assert.deepEqual(
      unused, [],
      `这些 source 在类型里声明了，却没有任何链生产它：${unused.join(', ')}\n` +
        '  ⇒ 要么删掉声明，要么把那条链接上。',
    )
  })

  it('不许出现类型里没有的 source（拼写错误会抓）', () => {
    const declared = new Set(declaredSources())
    const unknown = producers().filter((p) => p.source !== null && !declared.has(p.source))
    assert.deepEqual(
      unknown.map((p) => `${p.rel}:${p.line} → '${p.source}'`), [],
      '出现了类型里没有的 source ⇒ 大概率是拼写错误（TS 类型本该挡住，挡住就更说明类型被放宽过）',
    )
  })

  it('三条链各自用自己那个 source（不得串用）', () => {
    // 串用的后果：随手记建出来的提醒被标成会议来源，
    // 排查「重复提醒」时会指向错误的链。
    const byRel = Object.fromEntries(producers().map((p) => [p.rel, p.source]))
    const want = {
      [path.join('meetings', 'meeting-ingest.ts')]: 'meeting-ingest',
      [path.join('meetings', 'meeting-todo-persist.ts')]: 'meeting-summary',
      [path.join('notes', 'note-todo-persist.ts')]: 'note-voice',
    }
    for (const [rel, expect] of Object.entries(want)) {
      assert.equal(byRel[rel], expect, `${rel} 的 source 串了：实测 ${byRel[rel]}，期望 ${expect}`)
    }
  })

  // ── 负控：证明掩码与「调用形态」真的在起作用 ──────────────────────
  it('★ 负控：注释里的同形调用不得被计入', () => {
    const code = maskNonCode(`
      // await ensureTodoReminder({ text: 'x', source: 'meeting-ingest' })
      /* await ensureTodoReminder({ source: 'note-voice' }) */
    `)
    assert.equal(callsOf(code, CALLEE).length, 0, '注释里的调用被算成了生产点')
  })

  it('★ 负控：只 import 不调用不算生产点', () => {
    const code = maskNonCode(
      "import { ensureTodoReminder } from '../meetings/meeting-due-reminder'\n" +
      'const f = typeof ensureTodoReminder\n',
    )
    assert.equal(callsOf(code, CALLEE).length, 0, '只 import 被算成了生产点')
  })

  it('★ 负控：测试目录里的同形内容不得进入生产源集合', () => {
    // ⚠ 第一版这条对照的前提是「测试目录里有 `ensureTodoReminder(` 的 stub」。
    //   修完「声明当调用」那个 bug 之后，那两个 stub 都是
    //   `export async function ensureTodoReminder(input)` —— **声明形态**，
    //   于是被正确过滤掉了，对照的前提反而失效。
    //   ⇒ 教训：**负控也会因为被测代码变对而空转**，要断言**规则**而不是**某个样本**。
    const withTests = [...trees.keys()].filter((k) => k.split(path.sep).includes('__tests__'))
    assert.deepEqual(withTests, [], `生产源集合里混进了测试文件：${withTests.join(', ')}`)

    // 且规则本身要真的有效：往一个 __tests__ 路径塞文件也不会被收进来。
    const planted = prodSources(MEETINGS, 'meetings')
    const plantedTests = [...planted.keys()].filter((k) => k.includes('__tests__'))
    assert.deepEqual(plantedTests, [], '排除 __tests__ 的规则失效')

    // 反向：生产文件**确实**被收进来了（否则上面两条都是空集相等）
    assert.ok(
      [...trees.keys()].some((k) => k.endsWith('note-todo-persist.ts')),
      '生产源集合里没有随手记那条 ⇒ 抽取侧本身坏了，本节所有 0/少 读数都不可信',
    )
  })
})