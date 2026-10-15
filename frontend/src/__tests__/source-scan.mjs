// source-scan.mjs — 给「扫源码」类判据用的公共底座。
//
// 为什么需要它（2026-10-03 实测）：
//   扫 .vue 找「谁在用 APP_VERSION 常量」时，判据命中了自己写的那行说明注释
//   ——「这里问的是真实能力而不是 security.keystore_v1 那个恒 false 的静态开关」。
//   于是判据把**散文**当成了代码：它既没法证明实现用了静态开关，
//   也会逼着下一个人删掉解释才敢通过。
//
//   这不是把判据调松，而是把「判据看的是代码」变成可核查的事实。
//   同族教训：LoginView 那次是模板里的裸字面量，MoreHubView 这次是注释里的
//   关键词名 —— 两次的共同点都是「搜索方法看不见它和它想找的东西的区别」。
//
// 剥注释是**有损**的，所以它的行为被 source-scan.test.mjs 钉住：
// 哪天有人把规则改得看不见代码了，那条自检会先转红，而不是让下游判据集体失明。

import { readFileSync, readdirSync, statSync } from 'node:fs'
import { join, relative, sep } from 'node:path'

/** 反引号。computedArgs 与 balancedSpan 都要用它跳过模板串。 */
const TICK = String.fromCharCode(96)

/** 把一段注释抹掉，但**保留其中的换行** —— 否则行号会错位，
 *  判据报出来的「哪个文件第几行」就指不到真正的地方。 */
function blank(comments) {
  return comments.replace(/[^\n]/g, ' ')
}

/**
 * 去掉 HTML 注释、块注释和行注释，保留代码。
 *
 * 「// 前面是冒号」不算注释（https:// 这类 URL / 正则字面量 / 除法）——
 * 否则会把一整行代码从 URL 处截断，可能正好藏住后面某个要找的串。
 */
export function stripVueComments(src) {
  return src
    .replace(/<!--[\s\S]*?-->/g, blank)
    .replace(/\/\*[\s\S]*?\*\//g, blank)
    .split('\n')
    .map((line) => {
      let i = line.indexOf('//')
      while (i !== -1 && line[i - 1] === ':') i = line.indexOf('//', i + 2)
      return i === -1 ? line : line.slice(0, i)
    })
    .join('\n')
}

/**
 * 找出所有 computed(...) 的实参文本。
 *
 * 为什么不用正则硬凑（第一版就是这么错的）：
 *   这个代码库**不写分号**，所以「computed 后面到下一个 APP_VERSION 之间没有 ;」
 *   这种字符类护栏完全失效 —— 上一行的 userName computed 会被判成
 *   「把常量接进了 computed」，在**正确代码**上误报。
 *   一旦判据在正确代码上误报，下一个人只会去改代码，不会怀疑判据。
 *
 * 改成按括号配对精确截取实参，与代码风格无关。
 */
export function computedArgs(code) {
  const out = []
  for (let i = 0; i < code.length; i++) {
    if (!code.startsWith('computed', i)) continue
    if (i > 0 && /[\w$]/.test(code[i - 1])) continue // 词边界：myComputed 不算
    let j = i + 'computed'.length
    while (j < code.length && /\s/.test(code[j])) j++
    if (code[j] === '<') {
      // 允许 computed<HubItem[]> 这种显式泛型参数
      let depth = 0
      while (j < code.length) {
        if (code[j] === '<') depth++
        else if (code[j] === '>') {
          depth--
          if (depth === 0) { j++; break }
        }
        j++
      }
      while (j < code.length && /\s/.test(code[j])) j++
    }
    if (code[j] !== '(') continue
    let depth = 0
    let quote = null
    let arg = ''
    for (; j < code.length; j++) {
      const ch = code[j]
      if (quote) {
        if (ch === '\\') { arg += ch + (code[j + 1] ?? ''); j++; continue }
        if (ch === quote) quote = null
        arg += ch
        continue
      }
      if (ch === "'" || ch === '"' || ch === TICK) { quote = ch; arg += ch; continue }
      if (ch === '(') depth++
      else if (ch === ')') {
        depth--
        if (depth === 0) break
      }
      arg += ch
    }
    out.push(arg)
    i = j
  }
  return out
}

/**
 * 从锚点起，按**配平**截取完整的语法单元 —— 这是「取够 N 个字符」的反面。
 *
 * 为什么（2026-10-11 §428 实测）：
 *   `code.slice(at, at + 600)` 这种上限窗口把**覆盖率交给了排版**。
 *   同一个数字，两个方向坏得不一样：
 *     · 肯定断言 ⇒ 上限偏小 = **假红**（一次正常重构就把门打红，
 *       而人会去改判据或删注释 —— 见 §426.7 那次撞红）；
 *     · 否定断言 ⇒ 上限偏小 = **假绿**（它在自己**看不见的范围**里宣布通过）。
 *   余量多少呢？随手加个实参、改个变量名就变，而**没人拍过这条约束**。
 *   上一条 computedArgs 已经把同一纪律写成结论（与代码风格无关），
 *   只是没推广到其余窗口。
 *
 *   实测余量（2026-10-11）：`finalizeRecording(` 的实参区 332 / 364 字符，
 *   而窗口是 600 —— 余量 236。也就是说**今天还没越界**，
 *   但没有门保证它不越界：下次有人给这个调用加个选项对象就可能变假绿。
 *
 * @param {string} code   已剥注释的源码（注释里的词会骗过按字符截取的判据）
 * @param {string} anchor 锚点文本，取**第一次**出现
 * @param {'('|'{'} [openCh] 从哪个括号开始配平；默认取 anchor 的末字符。
 *                         函数体要传 `'{'` —— 它的锚点以 `(` 结尾，但该配平的是花括号。
 * @returns {{at:number, open:number, close:number, span:string, inner:string}|null}
 *          anchor 不存在时返回 null（让调用方自己的 `at > 0` 判据继续管这件事，
 *          免得底座悄悄返回空串把下游的「找不到」提示吃掉）。
 *          span 含锚点与配对括号；inner 只含括号内部。
 *
 * ⚠⚠ **找函数体时必须先配平参数表**（2026-10-11 实测，见 §428.7）：
 *   真实代码 `api/meetings.ts` 的 `fallbackRefine(` 签名第 4 行是
 *   `meta?: { title?: string; participants?: string[] }` ——
 *   参数表里有**类型注解的花括号**。`indexOf('{', at)` 命中的是**它**，
 *   于是配平从那个对象类型开始、收尾在它的 `}`，
 *   而**整个函数体落在窗口之外**：判据没报错，只是静默看不见了。
 *   ⭐ 这与 §427 在 `render-transcript-speaker-fallback.test.mjs` 的
 *   `funcBody` 上踩到的是同一颗雷；把它修在底座上，别再各处重犯。
 */
export function balancedSpan(code, anchor, openCh) {
  const at = code.indexOf(anchor)
  if (at < 0) return null
  const startCh = openCh ?? anchor[anchor.length - 1]
  let searchFrom = at
  // ⚠⚠ 找函数体时**必须先配平参数表**（2026-10-11 实测踩到，见本文件下方说明）。
  //   参数表里可能有类型注解的花括号：真实代码里就有
  //   `meta?: { title?: string; participants?: string[] }`，
  //   直接 `indexOf('{', at)` 会命中**它**，于是配平从那个对象类型开始，
  //   收尾在它的 `}` —— 函数体整段落到了窗口之外，判据静默变瞎。
  if (startCh === '{') {
    let parenOpen = -1
    if (anchor.endsWith('(')) {
      // 锚点自带左括号（`async function foo(`）—— 注意这里**不能**用
      // `at + anchor.length`，那已经越过它了（第一版就是这么写的，
      // 结果配平步骤从未触发，自检当场把它抓了出来）。
      parenOpen = at + anchor.length - 1
    } else {
      let j = at + anchor.length
      while (j < code.length && /\s/.test(code[j])) j++
      if (code[j] === '(') parenOpen = j
    }
    if (parenOpen >= 0) {
      const parenClose = matchClose(code, parenOpen, '(')
      if (parenClose < 0) return null
      searchFrom = parenClose
    }
  }
  const open = code.indexOf(startCh, searchFrom)
  if (open < 0) return null
  const close = matchClose(code, open, startCh)
  if (close < 0) return null
  return { at, open, close, span: code.slice(at, close + 1), inner: code.slice(open + 1, close) }
}

/**
 * 从锚点一直取到**包围它的那层代码块的收尾花括号**。
 *
 * 为什么要有第二种构造（2026-10-11，§428 续）：
 *   有一类判据的锚点是「一条语句」，而它要盯的东西在**下一句**，
 *   例如 `const x = f(...)` 之后紧跟的 `if (x) …`。
 *   这时配平实参（`balancedSpan(…, '(')`）会把范围缩到**括号内部**，
 *   反而看不见断言的目标 —— 而正确的单元是「这条语句所在的块」。
 *   实测那一处：锚点在 `try {` 块里，块尾就是紧随其后的 `return`，
 *   比原来的「取 400 个字符」精确一个数量级。
 *
 * ⚠ **这不是「函数体」**：它取的是**最内层**的未闭合 `{`。
 *   锚点若在函数体顶层，取到的就是整个函数体（与 `balancedSpan(…, '{')` 同解）。
 *
 * @returns {{at:number, open:number, close:number, span:string}|null}
 *          anchor 不存在、或锚点前面没有任何未闭合的 `{` 时返回 null。
 */
export function spanToEnclosingBlock(code, anchor) {
  const at = code.indexOf(anchor)
  if (at < 0) return null
  const open = lastUnclosedBrace(code, at)
  if (open < 0) return null
  const close = matchClose(code, open, '{')
  if (close < 0) return null
  return { at, open, close, span: code.slice(at, close + 1) }
}

/** 跳过注释与字符串/模板串，从 open 处（指向 startCh）找配对的收尾括号下标。 */
function matchClose(code, open, startCh) {
  const closeCh = startCh === '(' ? ')' : startCh === '{' ? '}' : startCh
  let depth = 0
  let quote = null
  for (let i = open; i < code.length; i++) {
    const ch = code[i]
    if (quote) {
      if (ch === '\\') { i++; continue }
      if (ch === quote) quote = null
      continue
    }
    if (ch === "'" || ch === '"' || ch === TICK) { quote = ch; continue }
    if (ch === '/' && i + 1 < code.length) {
      // 注释里的括号不参与配平（判据读的源码通常已剥注释，这里是双保险）
      if (code[i + 1] === '/') {
        while (i < code.length && code[i] !== '\n') i++
        continue
      }
      if (code[i + 1] === '*') {
        const end = code.indexOf('*/', i + 2)
        i = end < 0 ? code.length : end + 1
        continue
      }
    }
    if (ch === startCh) depth++
    else if (ch === closeCh) {
      depth--
      if (depth === 0) return i
    }
  }
  return -1
}

/** before 之前**最内层那个仍未闭合的 `{` 的下标；没有则 -1。 */
function lastUnclosedBrace(code, before) {
  const stack = []
  let quote = null
  for (let i = 0; i < before; i++) {
    const ch = code[i]
    if (quote) {
      if (ch === '\\') { i++; continue }
      if (ch === quote) quote = null
      continue
    }
    if (ch === "'" || ch === '"' || ch === TICK) { quote = ch; continue }
    if (ch === '/' && i + 1 < code.length) {
      if (code[i + 1] === '/') {
        while (i < before && code[i] !== '\n') i++
        continue
      }
      if (code[i + 1] === '*') {
        const end = code.indexOf('*/', i + 2)
        i = end < 0 ? before : end + 1
        continue
      }
    }
    if (ch === '{') stack.push(i)
    else if (ch === '}') stack.pop()
  }
  return stack.length ? stack[stack.length - 1] : -1
}

/**
 * 找「看起来像版本号」的裸字面量。
 *
 * 这是 LoginView 那个缺陷的通用形状：模板里直接写死 v1.2.0-mobile，
 * 连变量都不是。它不会出现在任何 import 里，所以「谁 import 了常量」
 * 那类判据看不见它 —— 必须单独扫字面量。
 */
export function findVersionLiterals(code) {
  const out = []
  code.split('\n').forEach((line, idx) => {
    for (const m of line.matchAll(/\bv?\d+\.\d+\.\d+\b/g)) {
      out.push({ line: idx + 1, text: m[0] })
    }
  })
  return out
}

/**
 * 递归收集目录下的全部 .vue（含子目录）。
 *
 * ⚠ 2026-10-14（§638）：这里原先**裸调** `statSync(p)`，而本函数会递归整棵
 * `frontend/src`。并发跑全套测试时，`z-index-ladder.test.mjs` 会往
 * `frontend/src/__probe_fixture__` **临时建目录再删掉**（§631 定位的那场竞争）；
 * 递归撞上那个窗口 ⇒ `statSync` 抛 ENOENT。
 *
 * ⭐ 实测后果不是「少扫一个文件」，而是**整份测试文件在 import 期就炸**：
 * `app-version-display-coverage.test.mjs:53` 在模块顶层调本函数 ⇒ 抛错 ⇒
 * 它的 7 个用例**一个都没注册** ⇒ census 记 0 ⇒ runner 的「跑到了但一个用例
 * 都没产出」判红 ⇒ `npm run gates` 第 3 项失败（实测 12 轮里复现 1 轮）。
 *
 * ⚠ §632 当时把「递归遍历 `frontend/src` 的扫描方」数成 6 个并全部加固，
 * **漏了这个共享 helper** —— 它是被别的测试 import 进去的，统计时不算「测试文件」。
 * ⇒ 教训：**枚举扫描方不能只数测试文件**，共享 helper 同样是扫描方。
 *
 * 只吞 ENOENT：并发下文件「恰好不存在」是真的；其余异常（权限、IO 错误）
 * 一律照抛 —— 护栏只挡并发窗口，不掩盖真实读取失败。
 * ★ 变异验证（M1）：同一枚悬空软链在**修复前** EXIT=1（ENOENT），**修复后** EXIT=0；
 * 自指软链（ELOOP）两侧都 EXIT=1 ⇒ 护栏只吞 ENOENT，不掩盖别的失败。
 */
export function vueFilesUnder(dir) {
  const out = []
  for (const name of readdirSync(dir)) {
    const p = join(dir, name)
    try {
      if (statSync(p).isDirectory()) out.push(...vueFilesUnder(p))
      else if (name.endsWith('.vue')) out.push(p)
    } catch (err) {
      if (err?.code !== 'ENOENT') throw err
    }
  }
  return out
}

/** 读文件并剥掉注释；返回 { src, code }，src 供「注释里也别留误导」类断言用。 */
export function readVueCode(absPath) {
  const src = readFileSync(absPath, 'utf8')
  return { src, code: stripVueComments(src) }
}

/** 统一成正斜杠的相对路径，用作判据输出里给人看的文件名。 */
export function relPosix(root, abs) {
  return relative(root, abs).split(sep).join('/')
}
