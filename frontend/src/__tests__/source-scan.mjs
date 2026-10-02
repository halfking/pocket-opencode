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
  const TICK = String.fromCharCode(96)
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

/** 递归收集目录下的全部 .vue（含子目录）。 */
export function vueFilesUnder(dir) {
  const out = []
  for (const name of readdirSync(dir)) {
    const p = join(dir, name)
    if (statSync(p).isDirectory()) out.push(...vueFilesUnder(p))
    else if (name.endsWith('.vue')) out.push(p)
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
