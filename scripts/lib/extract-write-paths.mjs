/**
 * lib/extract-write-paths.mjs — 前端写路径静态提取器（BUG-L 同类缺陷审计用）。
 *
 * 单独成模块是为了让 audit-write-routes.mjs（静态前缀对账）与
 * probe-write-methods.mjs（真后端 method 级探测）共用同一套提取逻辑：
 * 两份实现一旦漂移，静态结论就不可信了。
 *
 * 提取过程中踩过的两个坑都固化在下面，**不要"简化"回去**：
 *
 *  1. 假阴性：只扫 services/ 且把 `${BASE}/notes` 折叠成 ':seg/notes'，
 *     不以 '/' 开头被丢弃 -> 报「0 write calls」。看着像"全部通过"，其实
 *     什么都没查。修法：先按同文件 const 展开模块常量，再折叠剩余变量。
 *
 *  2. 假阳性：用 /http\((...)\)(\s\S{0,300}?method:...)/ 这类跨行正则时，
 *     会把 email.ts:172 的 **GET** 调用和 175 行另一个调用里的
 *     method:'POST' 吸成一条 -> 4 条不存在的写路径。修法：括号平衡，
 *     只在**本次调用自己的实参**里找 method。
 */
import { readdirSync, readFileSync, statSync } from 'node:fs'
import { join, relative } from 'node:path'

const WRITE_METHODS = ['POST', 'PATCH', 'DELETE', 'PUT']
const SKIP_DIRS = new Set(['__tests__', 'node_modules', 'dist', '.vite'])

/** 递归收集 .ts（排除测试与声明文件）。 */
export function collectTs(dir, out = []) {
  for (const name of readdirSync(dir)) {
    if (SKIP_DIRS.has(name)) continue
    const p = join(dir, name)
    if (statSync(p).isDirectory()) collectTs(p, out)
    else if (name.endsWith('.ts') && !name.endsWith('.d.ts') && !name.endsWith('.test.ts')) out.push(p)
  }
  return out
}

/** 收集同文件的字符串常量：const BASE = '/api/flashcards' */
function constStrings(src) {
  const map = new Map()
  const re = /const\s+([A-Z_][A-Z0-9_]*)\s*(?::\s*string\s*)?=\s*'([^']*)'/g
  let m
  while ((m = re.exec(src)) !== null) map.set(m[1], m[2])
  return map
}

/** 展开常量后折叠剩余 ${...}；无法判定首段时标记 unresolved。 */
function toPattern(pathExpr, consts) {
  let out = pathExpr
  for (let pass = 0; pass < 5; pass++) {
    const next = out.replace(/\$\{([A-Za-z_][A-Za-z0-9_]*)\}/g, (whole, name) =>
      consts.has(name) ? consts.get(name) : whole,
    )
    if (next === out) break
    out = next
  }
  const unresolvedHead = out.startsWith('${')
  out = out
    .replace(/\$\{[^}]*\}/g, ':seg')
    .replace(/[`'"]/g, '')
    .split('?')[0]
  if (!out.startsWith('/')) return null
  out = out.length > 1 ? out.replace(/\/+$/, '') : out
  return { pattern: out, unresolvedHead }
}

/**
 * 用空白覆盖注释，把字符串的引号本身替换成空格但**保留串内容**。
 * 保留内容是必须的：路径就住在 `'/api/notes'` / `` `${BASE}/notes` `` 里。
 */
function maskNonCode(src) {
  const out = src.split('')
  let i = 0
  const blank = (from, to) => {
    for (let k = from; k < to && k < out.length; k++) if (out[k] !== '\n') out[k] = ' '
  }
  while (i < src.length) {
    const c = src[i]
    const d = src[i + 1]
    if (c === '/' && d === '/') {
      const nl = src.indexOf('\n', i)
      blank(i, nl < 0 ? src.length : nl)
      i = nl < 0 ? src.length : nl
    } else if (c === '/' && d === '*') {
      const end = src.indexOf('*/', i + 2)
      blank(i, end < 0 ? src.length : end + 2)
      i = end < 0 ? src.length : end + 2
    } else if (c === "'" || c === '"' || c === '`') {
      const quote = c
      let j = i + 1
      while (j < src.length) {
        if (src[j] === '\\') { j += 2; continue }
        if (src[j] === quote) break
        j++
      }
      out[i] = ' '
      if (j < src.length) out[j] = ' '
      i = j + 1
    } else {
      i++
    }
  }
  return out.join('')
}

/** 从 '(' 起做括号平衡，返回配对 ')' 下标；不配平返回 -1。 */
function matchBracket(src, open) {
  let depth = 0
  for (let i = open; i < src.length; i++) {
    const c = src[i]
    if (c === '(' || c === '[' || c === '{') depth++
    else if (c === ')' || c === ']' || c === '}') {
      depth--
      if (depth === 0) return i
    }
  }
  return -1
}

/** 扫描源码里所有 name( 调用，返回 { name, index, args }。 */
function findCalls(src) {
  const out = []
  const masked = maskNonCode(src)
  const callRe = /([A-Za-z_$][\w$.]*)\s*(?:<[^<>()]*>)?\s*\(/g
  let m
  while ((m = callRe.exec(masked)) !== null) {
    const open = m.index + m[0].length - 1
    const close = matchBracket(masked, open)
    if (close < 0) continue
    out.push({ name: m[1], index: m.index, args: src.slice(open + 1, close) })
    callRe.lastIndex = close + 1
  }
  return out
}

/**
 * 主入口：返回 [{ method, pattern, file, line, unresolved }]。
 * pattern 形如 /api/flashcards/:seg/notes（模板变量已折叠为 :seg）。
 */
export function extractWritePaths(srcRoot) {
  const findings = []
  for (const file of collectTs(srcRoot)) {
    const src = readFileSync(file, 'utf8')
    const consts = constStrings(src)
    // 注意：relative 的第一个参数是**目录**，第二个是**文件路径**。
    // 这里曾经误传 src（文件内容字符串），导致报告里的 file 列打印出整段源码。
    const rel = join('frontend/src', relative(srcRoot, file))

    for (const call of findCalls(src)) {
      if (call.name !== 'http' && call.name !== 'fetch') continue
      const methodMatch = call.args.match(/method:\s*'(POST|PATCH|DELETE|PUT)'/)
      if (!methodMatch) continue // GET 调用不是写路径
      const first = call.args.trimStart().match(/^([`'"][^`'"]*[`'"])/)
      if (!first) continue
      const parsed = toPattern(first[1].slice(1, -1), consts)
      if (!parsed) continue
      findings.push({
        method: methodMatch[1],
        pattern: parsed.pattern,
        file: rel,
        line: src.slice(0, call.index).split('\n').length,
        unresolved: parsed.unresolvedHead,
      })
    }

    // 形态 C: apiClient.post('/x') —— 方法名即动词
    const methodCallRe =
      /\b(?:api|apiClient|httpClient|client)\.(post|patch|delete|put|POST|PATCH|DELETE|PUT)\s*\(\s*([`'"][^`'"]*[`'"])/g
    let m
    while ((m = methodCallRe.exec(src)) !== null) {
      const parsed = toPattern(m[2].slice(1, -1), consts)
      if (!parsed) continue
      findings.push({
        method: m[1].toUpperCase(),
        pattern: parsed.pattern,
        file: rel,
        line: src.slice(0, m.index).split('\n').length,
        unresolved: parsed.unresolvedHead,
      })
    }
  }
  return findings
}
