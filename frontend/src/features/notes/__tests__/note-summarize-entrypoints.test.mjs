// note-summarize-entrypoints.test.mjs — §125
//
// 缺陷：`/api/notes/{id}/summarize` 在前端有**两个**调用点，各自声明了一份
// 互不为超集的类型。2026-10-07 取证时，列表页（NoteListView，走
// notesApi.summarize）已经接上 `action_items` → `createNoteTodos`，而详情页
// （NoteDetailView，走裸 `http` + 本地 NoteSummarizeResp）只声明了记账四键，
// action_items 在那一侧被直接丢弃 ⇒ **从详情页点「生成总结」，行动项一条都
// 不会变成待办、也不会进日程，而界面不报错、不提示。**
//
// 为什么这道门存在：既有那道接线门（note-todo-persist.test.ts 的
// 「前端必须在拿到总结后调用 createNoteTodos」）**只钉了 NoteListView 一个
// 文件**，于是第二个调用点漏接时它一声不响。需求「把一些时间点自动加入
// 计划日程」在两个屏幕上一半生效，正是这个形状。
//
// ⇒ 本门的形态是 **§35「调用点清单必须由机器给」**：入口不写死成
// `['NoteListView.vue']`，而是扫出全目录所有能触发 summarize 的文件，
// 逐个要求它把 action_items 喂进 createNoteTodos。将来新增第三个入口而忘了
// 接线，门会自己红。
//
// 三条反退化设计（每条都来自本会话踩过的坑）：
//   1. 剥注释：.vue 要连 HTML 注释一起剥（§119「判据卡结构位置」的前置条件
//      —— 不剥则注释里那行 `await createNoteTodos(` 会把门喂绿）。
//   2. 量具先自证：抽取器坏了会让「零个入口」变成全绿，所以显式断言扫出来的
//      入口集合确实含两个已知文件（§量具先自证）。
//   3. 注释陷阱负控：构造「summarize 调用与 action_items 都只出现在注释里」
//      的样本，必须被判为未接线 —— 本仓已被注释骗过三次。
import assert from 'node:assert/strict'
import { readFileSync, readdirSync, statSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, it } from 'node:test'

const HERE = path.dirname(fileURLToPath(import.meta.url))
const NOTES = path.resolve(HERE, '..')
const EXT = new Set(['.vue', '.ts', '.mjs', '.js'])

/** 递归列出参与扫描的源文件（排除测试目录与测试文件本身）。 */
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

/**
 * 剥 JS 注释。**字符串与模板字面量的内容一律保留**，但**仍然跟踪它们的状态**。
 *
 * ⚠ 这两件事被本会话的量具自己逼出来过两轮，两个错法都不是「少写一条断言」
 * 能避免的：
 *
 *   第一版：把单/双引号的**内容**也吃掉，想让字符串里的 `action_items` 不被
 *   误判成「已绑定」。后果：写成 `http('/api/notes/n1/summarize')` 的入口被
 *   整个漏扫 —— 而仓内两处真实调用都用反引号，主判据照样绿，这个盲区永远
 *   不会自己暴露。
 *
 *   第二版：干脆不跟踪字符串了（内容全留）。后果：`'http://…'` 这类字符串里的
 *   `//` 被当成行注释起点，**它后面整行都被吃掉**，于是 `NoteListView.vue`
 *   整个从入口清单里消失 —— 又是一个主判据看不见、只有自证断言能抓的盲区。
 *
 * ⇒ 正确形态是二者并存：**状态机用来判断 `//` 是不是注释，输出用来保留内容**。
 *
 * 已知限制：模板字面量 `${ … }` 插值内部的注释不剥（本仓无处用到）。判据卡在
 * 结构位置 + 机器给的调用点清单上，插值里的注释去改接线不影响这两条。
 */
function stripJsComments(src) {
  let out = ''
  let i = 0
  const n = src.length
  let state = 'code' // code | line | block | sq | dq | tpl
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
    // sq / dq / tpl：内容照抄，只用来防止把串里的 // 当注释
    if (c === '\\') { out += c + (d ?? ''); i += 2; continue }
    if ((state === 'sq' && c === "'") || (state === 'dq' && c === '"') || (state === 'tpl' && c === '`')) {
      state = 'code'
    }
    out += c
    i++
  }
  return out
}

/**
 * 取文件里「可执行代码」部分。
 * .vue 只取 `<script>` 块：模板与 `<style>` 里没有 summarize 调用，
 * 而把 CSS 交给 JS 状态机反而会切出乱码行、把真代码误判成注释。
 */
function codeOf(raw, file) {
  // HTML 注释按行数换成等量换行，保住行号，报错时能指到真实位置。
  const noHtml = raw.replace(/<!--[\s\S]*?-->/g, (m) => '\n'.repeat(m.split('\n').length - 1))
  if (!file.endsWith('.vue')) return stripJsComments(noHtml)
  const m = noHtml.match(/<script[^>]*>([\s\S]*?)<\/script>/)
  return m ? stripJsComments(m[1]) : ''
}

/** 能触发笔记总结的两种写法：走 api 层，或直接打端点。 */
const ENTRY_PATTERNS = [
  /notesApi\.summarize\s*\(/,
  /\/api\/notes\/[^\n`]*?\/summarize/,
]

/** 扫出全部 summarize 入口（返回文件名，不含目录）。 */
function entryPoints() {
  const hits = []
  for (const full of sourceFiles(NOTES)) {
    const code = codeOf(readFileSync(full, 'utf8'), full)
    if (ENTRY_PATTERNS.some((re) => re.test(code))) hits.push(path.basename(full))
  }
  return hits.sort()
}

/** 按括号配平切出 `createNoteTodos(` 的全部顶层实参。 */
function callArgs(code, call) {
  const i = code.indexOf(call)
  if (i < 0) return null
  let depth = 0
  let started = false
  let cur = ''
  const args = []
  for (let j = i + call.length - 1; j < code.length; j++) {
    const c = code[j]
    if (c === '(') { depth++; started = true; continue }
    if (c === ')') {
      depth--
      if (depth === 0) { args.push(cur); return args.map((s) => s.trim()) }
      continue
    }
    if (c === ',' && depth === 1) { args.push(cur); cur = ''; continue }
    if (started) cur += c
  }
  return null
}

/**
 * 取 .vue 的模板段（剥 HTML 注释）。贪婪匹配到最后一个 `</template>` ——
 * 本仓这些文件里 `<template>` 是顶层唯一的一个。
 */
function templateOf(raw) {
  const noHtml = raw.replace(/<!--[\s\S]*?-->/g, ' ')
  const m = noHtml.match(/<template[^>]*>([\s\S]*)<\/template>/)
  return m ? m[1] : ''
}

/**
 * 「真的在代码位上绑定了 action_items」的判据。
 *
 * 因为剥注释保留了字符串内容，这里必须排除字符串字面量里的同名文本：
 * `'action_items'` 后面跟的是引号，既不匹配 `[:,}]`，前面也没有 `.` ⇒ 被排除。
 * 真实的两处写法都能命中：
 *   · `const { summary, action_items: actionItems } = …`  ⇒ `action_items:`
 *   · `createNoteTodos(id, res.action_items, title)`       ⇒ `.action_items`
 */
const BINDS = /action_items\s*[:,}]|\.\s*action_items\b/

/**
 * 收集「从 action_items 派生的标识符」。
 *
 * 两种真实写法都覆盖：
 *   · `const { summary, action_items: actionItems } = res` ⇒ 标识符 `actionItems`
 *   · `createNoteTodos(id, res.action_items, title)`            ⇒ 标记 `.action_items`
 *
 * 存在的理由：BINDS 只回答「文件里绑定过没有」，**不回答「传进
 * createNoteTodos 的是不是它」**。而本仓已经被「实参名对了但值是错的」
 * 咬过一次（note-todo-persist.test.ts 的 §88 那条用例就是为它加的）。
 */
function actionItemTokens(code) {
  const toks = new Set()
  for (const m of code.matchAll(/action_items\s*:\s*([A-Za-z_$][\w$]*)/g)) toks.add(m[1])
  for (const _ of code.matchAll(/\.\s*action_items\b/g)) toks.add('.action_items')
  return toks
}

/** 「真的 await 过笔记镜像」——只 import 不算。 */
function awaitsMirror(code) {
  return /await\s+awaitNoteMirror\s*\(/.test(code)
}

/** 明知不接线的入口必须带非空理由，否则不许豁免。 */
const ALLOW = new Map()

/**
 * 「服务端每次请求都会做的用户可见副作用」—— 入口必须让用户看见它们。
 *
 * `handleNoteSummarize` 里的自动记账**没有任何调用方开关**：
 * `financeStore` 恒非 nil（server.go:299 默认内存版，main.go 换 PG 版），
 * `finance.NewRecognizer().Parse()` 对「中午吃饭花了38块」这种普通口语就会命中。
 * ⇒ 从某个入口调 summarize = 在用户账本里记一笔。
 * 不看见它，用户不会知道自己账上多了一笔 —— 正是 note-todo-notice.ts 里
 * 那句「静默创建等于让用户在别处凭空多出东西」，只是对象从待办换成了钱。
 */
const BOOKKEEPING_BINDS = /bookkeeping\w*\s*[:,}]|\.\s*bookkeeping\w*\b|\.\s*transactions\b/

/**
 * 只豁免「副作用是否被告知」这一项。
 *
 * ⚠ 与上面的 `ALLOW` 分开是刻意的：整文件豁免会把行动项接线、镜像等待这些
 *   真判据一并关掉 —— 豁免一个已知缺口不该顺手放过其它缺口。
 *
 * ★ §127 已修（2026-10-07）：这张表现在**必须为空**，由下面那条用例守住不许
 *   再加回来。豁免掉的那一项是「服务端每次 summarize 都无条件自动记账，而列表页
 *   只读 summary/action_items ⇒ 录音停止后自动入账的那笔账用户完全不知情」；
 *   修法是让记账三键进 buildNoteTodoNotice 的第二个参数，与行动项同一条提示
 *   （返回值由 note-bookkeeping-honesty.test.mjs 钉住）。
 */
const ALLOW_NO_BOOKKEEPING_NOTICE = new Map()

/** 副作用是否被告知；返回未告知的原因列表。 */
function sideEffectReasons(file, code) {
  if (ALLOW_NO_BOOKKEEPING_NOTICE.has(file)) return []
  if (BOOKKEEPING_BINDS.test(code)) return []
  return ['没有读取 transactions/bookkeeping ⇒ 服务端自动入账的账用户看不到']
}

/** 对单个文件做判定，返回未接线的原因列表（空数组 = 已接线）。 */
function verdictFor(file, code) {
  if (ALLOW.has(file)) return []
  const reasons = []
  const tokens = actionItemTokens(code)
  if (!BINDS.test(code)) {
    reasons.push('没有绑定 action_items ⇒ 服务端回的行动项在这一侧被丢弃')
  }
  const call = 'createNoteTodos('
  const args = callArgs(code, call)
  if (args === null) {
    reasons.push('没有调用 createNoteTodos ⇒ 行动项不会落成待办/日程')
    return reasons
  }
  // 第 2 个实参是 items。传空数组/字面量 null/undefined 同样是「没接线」，
  // 而「文件里出现过 createNoteTodos(」这种检查对此完全无感。
  const items = args[1] ?? ''
  if (/^(\[\]|\[\s*\]|null|undefined)$/.test(items)) {
    reasons.push(`createNoteTodos 第 2 个实参是 \`${items}\` ⇒ 等于没投行动项`)
  } else if (tokens.size > 0 && ![...tokens].some((t) => items.includes(t))) {
    // 「实参名对了但值是错的」：文件确实绑定了 action_items（甚至还解构出了
    // actionItems），但传进 createNoteTodos 的是另一个值。
    reasons.push(`createNoteTodos 第 2 个实参 \`${items}\` 不是绑定的 action_items ⇒ 传错值`)
  }
  return reasons
}

describe('§125 · 随手记 summarize 的每个入口都必须把 action_items 接进待办', () => {
  const files = sourceFiles(NOTES)
  const entry = entryPoints()

  it('量具自证：抽取器扫出了入口，且含两个已知调用点', () => {
    assert.ok(files.length >= 8, `只扫到 ${files.length} 个源文件，抽取器大概坏了`)
    assert.ok(entry.length >= 2, `只扫出 ${entry.length} 个 summarize 入口（${entry}），抽取器坏了`)
    assert.ok(entry.includes('NoteListView.vue'), '列表页这个已知入口没被扫出来 ⇒ 抽取器有盲区')
    assert.ok(entry.includes('NoteDetailView.vue'), '详情页这个已知入口没被扫出来 ⇒ 抽取器有盲区')
    // 反向自证：测试文件自身不得出现在入口清单里，否则本文件会被自己的
    // 负控样本喂红（负控样本里就写着那个端点）。
    assert.ok(!entry.some((f) => /test\.|\.test$/.test(f)), `测试文件混进了入口清单：${entry}`)
  })

  it('每个入口都必须接线（清单由机器给，不是手写的两个名字）', () => {
    const broken = []
    for (const file of entry) {
      const full = sourceFiles(NOTES).find((f) => path.basename(f) === file)
      const reasons = verdictFor(file, codeOf(readFileSync(full, 'utf8'), full))
      if (reasons.length) broken.push(`${file}：${reasons.join('；')}`)
    }
    assert.deepEqual(broken, [],
      `这些 summarize 入口没把 action_items 接进 createNoteTodos：\n  - ${broken.join('\n  - ')}\n` +
      '（需求「把一些时间点自动加入计划日程」在这些屏幕上是静默失效的）')
  })

  it('负控：缺陷原始形态（只声明记账四键）必须被判为未接线', () => {
    const oldShape = `
      interface NoteSummarizeResp {
        summary?: string
        transactions?: Array<{ id: string; type: string; amount: number; category: string }>
        bookkeeping?: 'created' | 'existing'
        bookkeeping_mismatch?: boolean
      }
      async function summarize() {
        const res = await http<NoteSummarizeResp>('/api/notes/n1/summarize', { method: 'POST' })
        summary.value = res.summary || ''
      }
    `
    assert.ok(ENTRY_PATTERNS.some((re) => re.test(oldShape)), '负控前提失败：样本不再是入口')
    const reasons = verdictFor('negative-control.vue', stripJsComments(oldShape))
    assert.ok(reasons.length >= 1, '负控失败：缺陷的原始形态被判成了已接线')
    assert.ok(reasons.some((r) => r.includes('action_items')),
      `负控没指出「丢弃 action_items」这一条：${JSON.stringify(reasons)}`)
  })

  it('负控：summarize 与 action_items 都只写在注释里 ⇒ 仍判未接线', () => {
    const commented = `
      // const { summary, action_items: actionItems } = await notesApi.summarize(id)
      // await createNoteTodos(id, actionItems, title)
      async function summarize() {
        const res = await http('/api/notes/n1/summarize', { method: 'POST' })
      }
    `
    assert.ok(ENTRY_PATTERNS.some((re) => re.test(stripJsComments(commented))),
      '负控前提失败：注释里的 summarize 不该让文件变成入口 —— 抽取器或剥注释坏了')
    // 文件确实是入口，但真正的代码里既没有 action_items 也没有调用
    const reasons = verdictFor('comment-trap.vue', stripJsComments(commented))
    assert.ok(reasons.length >= 1, '负控失败：注释里的接线被判成了真接线（本仓已被骗三次）')
  })

  it('负控：createNoteTodos 传空数组 ⇒ 必须判为没投行动项', () => {
    const emptyItems = `
      const { summary, action_items: actionItems } = await notesApi.summarize(id)
      await createNoteTodos(id, [], noteTitle)
    `
    assert.ok(/action_items/.test(stripJsComments(emptyItems)), '负控前提失败：样本没绑定 action_items')
    const reasons = verdictFor('empty-items.vue', stripJsComments(emptyItems))
    assert.ok(reasons.some((r) => r.includes('没投行动项')),
      `负控失败：传空数组没有被抓住：${JSON.stringify(reasons)}`)
  })

  it('接了线的入口还必须把落库结果告诉用户', () => {
    // 需求这句话有两个用户可见的承诺：**待办被建出来** 与 **用户被告知**。
    // 上面那条只钉住了前一半 —— 把模板里的 <p v-if="todoNotice"> 删掉，
    // 待办照样落库，但用户在别处凭空多出条目而完全不知情
    // （NoteListView 里那句「静默创建等于让用户在别处凭空多出东西」）。
    const silent = []
    for (const full of sourceFiles(NOTES)) {
      const name = path.basename(full)
      if (!entry.includes(name)) continue
      const raw = readFileSync(full, 'utf8')
      // 未接线的文件由上一条判；这里只查「接了线的有没有如实告知」
      if (callArgs(codeOf(raw, full), 'createNoteTodos(') === null) continue
      if (!/todoNotice/.test(templateOf(raw))) silent.push(name)
    }
    assert.deepEqual(silent, [],
      `这些入口建了待办却没告诉用户（静默副作用）：${silent.join('、')}`)
  })

  it('负控：实参名对了但值是别的 ⇒ 必须判为传错值', () => {
    // 「绑定过 action_items」与「把它传给 createNoteTodos」是两件事。
    // 本仓已经被这一类咬过一次（note-todo-persist.test.ts 的 §88 用例），
    // 所以必须有独立负控，否则这道门只挡住了更粗的那一半。
    const wrongValue = `
      const { summary, action_items: actionItems } = await notesApi.summarize(id)
      void actionItems
      await createNoteTodos(id, noteTitle, noteTitle)
    `
    const reasons = verdictFor('wrong-value.vue', stripJsComments(wrongValue))
    assert.ok(reasons.some((r) => r.includes('传错值')),
      `负控失败：传错值没被抓住：${JSON.stringify(reasons)}`)
  })

  it('每个入口都必须先等笔记在后端落行（否则刚录完的笔记必然 404）', () => {
    // §125.8：`createNote` 刻意不等云端（notes-persist.ts:122 只把镜像 promise
    // 登记进 noteMirrorReady），而详情页 load() 读的是**本地** store ⇒ 刚录完的
    // 笔记在镜像落地前就能被打开，这段时间里按 summarize 必然 404。
    // 列表页早就有这个 await，详情页此前漏了。
    // `awaitNoteMirror` 没有在途镜像时立即返回 ⇒ 对老笔记零成本。
    const missing = []
    for (const full of sourceFiles(NOTES)) {
      const name = path.basename(full)
      if (!entry.includes(name)) continue
      if (!awaitsMirror(codeOf(readFileSync(full, 'utf8'), full))) missing.push(name)
    }
    assert.deepEqual(missing, [],
      `这些 summarize 入口没等笔记落行就发请求 ⇒ 刚录完的笔记必然 404：${missing.join('、')}`)
  })

  it('负控：只 import 了 awaitNoteMirror 但没 await ⇒ 仍判未等', () => {
    const importedOnly = `
      import { awaitNoteMirror } from './notes-persist'
      const res = await http('/api/notes/n1/summarize', { method: 'POST' })
    `
    assert.equal(awaitsMirror(stripJsComments(importedOnly)), false,
      '负控前提失败：只 import 就算等了')
    const actuallyWaits = `
      import { awaitNoteMirror } from './notes-persist'
      await awaitNoteMirror(noteId)
      const res = await http('/api/notes/n1/summarize', { method: 'POST' })
    `
    assert.equal(awaitsMirror(stripJsComments(actuallyWaits)), true,
      '真的 await 了却判成没等 ⇒ 判据写反了')
  })

  it('每个入口都必须让用户看见服务端做的记账（否则账上凭空多一笔）', () => {
    // §127。这不是「数据没落库」而是「落库了没人知道」——
    // 服务端那边是无条件的，任何入口调 summarize 都会记账。
    const silent = []
    for (const full of sourceFiles(NOTES)) {
      const name = path.basename(full)
      if (!entry.includes(name)) continue
      if (sideEffectReasons(name, codeOf(readFileSync(full, 'utf8'), full)).length) silent.push(name)
    }
    assert.deepEqual(silent, [],
      `这些入口调 summarize 就会让服务端记账，却没让用户看见：${silent.join('、')}`)
  })

  it('负控：只声明不读（记账键出现但在代码位上没被消费）⇒ 判未告知', () => {
    // 服务端把记账结果放在响应里，但调用方只解构 summary 与 action_items。
    const dropsIt = `
      const { summary, action_items: actionItems } = await notesApi.summarize(id)
      await createNoteTodos(id, actionItems, title)
    `
    assert.equal(BOOKKEEPING_BINDS.test(stripJsComments(dropsIt)), false,
      '负控前提失败：样本本身读到了记账键')
    const reasons = sideEffectReasons('drops-it.vue', stripJsComments(dropsIt))
    assert.ok(reasons.some((r) => r.includes('看不到')),
      `负控失败：丢弃记账结果没被抓住：${JSON.stringify(reasons)}`)
  })

  it('豁免必须带非空理由（副作用豁免与整文件豁免各自成表）', () => {
    for (const [file, reason] of ALLOW) {
      assert.equal(typeof reason, 'string', `ALLOW[${file}] 的理由不是字符串`)
      assert.ok(reason.trim().length >= 8,
        `ALLOW[${file}] 的理由太短，读不出为什么不接线：${JSON.stringify(reason)}`)
    }
    for (const [file, reason] of ALLOW_NO_BOOKKEEPING_NOTICE) {
      assert.ok(reason.trim().length >= 20,
        `ALLOW_NO_BOOKKEEPING_NOTICE[${file}] 的理由太短，读不出为什么不告知：${JSON.stringify(reason)}`)
      assert.ok(entry.includes(file),
        `ALLOW_NO_BOOKKEEPING_NOTICE 里的 ${file} 已经不在入口清单里 ⇒ 豁免该删了（stale 豁免）`)
    }
  })

  it('★ §127 已修：记账告知的豁免表必须为空（修复落地后它就是死代码）', () => {
    // 留着它会让人以为「列表页仍然静默入账」是真的。加回来之前请先看
    // note-bookkeeping-honesty.test.mjs —— 那道门已经钉住返回值。
    assert.deepEqual([...ALLOW_NO_BOOKKEEPING_NOTICE.keys()], [],
      '§127 的记账告知豁免又被加回来了。若缺陷真的复发，请连同本文件一起改掉，' +
      '不要用豁免换绿灯。')
  })
})
