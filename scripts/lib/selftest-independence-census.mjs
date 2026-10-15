// selftest-independence-census.mjs —— 普查「`--selftest` 是不是独立于仓库真实状态」。
//
// 只报事实，不下结论，不自动改任何东西。
//
// ── 为什么值得单列 ────────────────────────────────────────────────────────
// §133.7 实测踩到（openpocket）：`scripts/check-maestro-flows.mjs` 的
// `const CFG = loadConfig()` 在**模块顶层**、早于 `--selftest` 分支。
// 于是把某个守卫的判定体摘掉时，真实配置的检查先触发 `process.exit(1)`，
// **自检一行都没打印**，退出码 1 —— 与「自检挂了」在退出码上**不可区分**。
// ⇒ 变异读数作废，只好改用行为等价改写重做。
//
// 这不是那个文件独有的毛病，而是**一类**：自检的价值在于「它只回答自检的问题」；
// 一旦它先碰了仓库真实状态，失败信号就不再专属。
//
// ── 判据（可判定，不含猜测）────────────────────────────────────────────────
// 1. 找到处理 `--selftest` 的那一行（dispatch 行）D；
// 2. 枚举 D 之前的**顶层语句**（不在任何函数/块内）；
// 3. 其中形如 `name(...)` 的调用，若 `name` 是**本文件自己声明的函数**，则沿调用图往下追；
// 4. 追的过程中只要碰到下面两类里的任意一类，就记一条事实：
//      · `process.exit` —— 能把自检整个带走；
//      · 文件系统读（readFileSync / readdirSync / existsSync / statSync …）
//        —— 结论依赖仓库当前状态。
//
// ⚠ 已知边界（不掩盖）：
//   · 调用图只跟**本文件内**的函数声明；调用 import 进来的函数不跟。
//   · 深度封顶（`MAX_DEPTH`），超深的不再追 ⇒ 可能**少报**。
//   · 「顶层调用」按**括号深度 0** 判定；对象字面量里的方法调用不算顶层。
//   · 跑普查**不等于**说这类自检就是坏的：本工具只回答
//     「它在跑自己的用例之前，会不会先碰仓库状态或提前退出」。

import { readFileSync, readdirSync, existsSync, statSync } from 'node:fs'
import { dirname, join, relative } from 'node:path'
import { fileURLToPath } from 'node:url'

const here = dirname(fileURLToPath(import.meta.url))
const REPO = join(here, '..', '..')

const DEFAULT_SCAN_DIRS = ['scripts', 'frontend/scripts', 'frontend/scripts/lib']
const SCAN_DIRS = process.argv[2]
  ? [process.argv[2].startsWith('/') ? process.argv[2] : join(REPO, process.argv[2])]
  : DEFAULT_SCAN_DIRS

const MAX_DEPTH = 4
const FS_READS = /\b(readFileSync|readdirSync|existsSync|statSync|lstatSync|readFile|globSync|opendirSync)\s*\(/
const HAS_EXIT = /\bprocess\.exit\s*\(/
// ⚠️ v5 之前的 DISPATCH 只认 `process.argv.includes('--selftest')` **一种写法**，
//   于是漏掉了两种真实存在的 dispatch（实测两个）：
//     · `if (argv.includes('--selftest'))`            —— argv 是本地变量名
//     · `(process.argv[2]||'').toLowerCase()==='--selftest'` —— 取下标 + 比较
//   ⇒ 「14 个处理 --selftest」是**漏报**读数。漏报方向比多报更危险：
//   多报会被当成噪声，漏报会让整份「未审清单」短一截而没人发现。
//   口径放宽到「argv 标识符 与 '--selftest' 同处一个短窗口内」，并用 60 字窗口
//   兜住 `toLowerCase()` 之类夹在中间的变换。
const DISPATCH = /\b\w*argv\w*\s*(?:\.\s*includes\s*\(|\[[^\]]*\][\s\S]{0,50}?[=!]==?\s*)[\s\S]{0,60}?['"]--selftest['"]/i
const FN_DECL = /^(?:async\s+)?function\s+(\w+)\s*\(/
// 「看起来像调用、其实不是」的词：控制流与一元运算符。
// ★ v4 漏了这条，把 `if (…)` / `for (…)` 全记成「没跟的调用」，
//   一屏噪声，把真东西淹掉 —— 普查器多报与少报同样不可信。
const KEYWORDS = new Set([
  'if', 'for', 'while', 'switch', 'catch', 'return', 'typeof', 'await',
  'new', 'void', 'delete', 'do', 'else', 'function', 'class', 'import', 'require',
])
// ★ v2 补了一条：顶层调用有两种形态 —— 裸调用 `loadConfig()`，
//   与**赋值** `const CFG = loadConfig()`。原来只认前者
//   ⇒ §133.7 那个真实的例子被漏掉，普查报「0 处」。
//   （这就是 §133.3 第 6 条的又一次同款：工具的形态假设比现实窄。）
const TOP_CALL = /^(?:(?:const|let|var)\s+[\w$]+\s*=\s*)?(?:await\s+)?([A-Za-z_$][\w$]*)\s*\(/

/**
 * 五态扫描：算出每个字符的状态。
 * ⚠ 字符串里的 `//` 不能当行注释；块注释的收尾记号不能写在本注释里（会提前闭合）。
 * ⚠ 反引号要额外当模板串处理（本仓判据里出现过插值，含 `}` 与引号）。
 *
 * ★ v1 栽过两次，两次都是「扫了 0 个还报绿」：
 *   ① 只留「纯代码」字符 ⇒ **把字符串内容也 blank 掉了**
 *      ⇒ 而本工具要找的模式 `'--selftest'` **就住在字符串里** ⇒ 永远匹配不到
 *      ⇒ 22 个带 selftest 的文件被读成 0 个。
 *   ⇒ 所以分两张表：**结构表**（只留代码，算括号深度）与**可检索表**（代码+字符串内容，只 blank 注释）。
 *      模式匹配走后者，括号深度走前者。
 *
 * @returns {{structural: Uint8Array, searchable: Uint8Array}}
 */
function scan(src) {
  const n = src.length
  const structural = new Uint8Array(n) // 1 = 真代码
  const searchable = new Uint8Array(n) // 1 = 代码或字符串内容（注释为 0）
  let i = 0
  let state = 'code'
  while (i < n) {
    const c = src[i]
    const d = src[i + 1]
    if (state === 'code') {
      if (c === '/' && d === '/') { state = 'line'; i += 2; continue }
      if (c === '/' && d === '*') { state = 'block'; i += 2; continue }
      if (c === "'") { searchable[i] = 1; state = 'sq'; i += 1; continue }
      if (c === '"') { searchable[i] = 1; state = 'dq'; i += 1; continue }
      if (c === '`') { searchable[i] = 1; state = 'bq'; i += 1; continue }
      structural[i] = 1; searchable[i] = 1; i += 1; continue
    }
    if (state === 'line') { if (c === '\n') { structural[i] = 1; searchable[i] = 1; state = 'code' } i += 1; continue }
    if (state === 'block') { if (c === '*' && d === '/') { state = 'code'; i += 2; continue } i += 1; continue }
    if ((state === 'sq' || state === 'dq' || state === 'bq') && c === '\\') { searchable[i] = 1; if (i + 1 < n) searchable[i + 1] = 1; i += 2; continue }
    if ((state === 'sq' && c === "'") || (state === 'dq' && c === '"') || (state === 'bq' && c === '`')) {
      searchable[i] = 1; state = 'code'; i += 1; continue
    }
    searchable[i] = 1
    i += 1
  }
  return { structural, searchable }
}

/** 按掩码把不要的部分替换成空格，保留所有偏移与行号。 */
function applyMask(src, mask) {
  let out = ''
  for (let i = 0; i < src.length; i++) out += mask[i] ? src[i] : ' '
  return out
}

/** 找出本文件里所有 `function name(...)` 声明，并返回各自的花括号体（偏移区间）。 */
function functionBodies(clean) {
  const bodies = new Map()
  const re = /^(?:async\s+)?function\s+(\w+)\s*\(/gm
  for (let m; (m = re.exec(clean)); ) {
    // 找到参数列表的收尾 `)`，再找紧随其后的 `{`
    let i = m.index + m[0].length
    let depth = 1
    while (i < clean.length && depth > 0) {
      const ch = clean[i]
      if (ch === '(') depth++
      else if (ch === ')') depth--
      i++
    }
    if (depth !== 0) continue
    const braceStart = clean.indexOf('{', i)
    if (braceStart < 0) continue
    let d = 0
    let j = braceStart
    for (; j < clean.length; j++) {
      if (clean[j] === '{') d++
      else if (clean[j] === '}') { d--; if (d === 0) { j++; break } }
    }
    bodies.set(m[1], { start: braceStart, end: j })
  }
  return bodies
}

function lineOf(clean, idx) {
  let n = 1
  for (let i = 0; i < idx; i++) if (clean[i] === '\n') n++
  return n
}

/** 深度 0 的行 = 顶层行。 */
function topLevelLines(clean, limit) {
  const out = []
  let depth = 0
  let lineStart = 0
  for (let i = 0; i < limit; i++) {
    const c = clean[i]
    if (c === '\n') {
      if (depth === 0) out.push({ start: lineStart, end: i })
      lineStart = i + 1
      continue
    }
    if (c === '{' || c === '(' || c === '[') depth++
    else if (c === '}' || c === ')' || c === ']') depth--
  }
  if (lineStart < limit) out.push({ start: lineStart, end: limit })
  return out
}

const findings = []
// ★ 全部「有 dispatch」的文件（含零 finding 的）。表头报 14、下面只列 10 时，
//   读者无法区分「4 个干净」与「4 个丢了」——两者在输出上长得一模一样。
//   ⇒ 必须把干净的也显式列出来，否则这个读数不可自证。
const allSelftests = []
let scanned = 0
let withSelftest = 0

for (const dir of SCAN_DIRS) {
  if (!existsSync(dir)) {
    console.error(`❌ 扫描根不存在：${dir}`)
    process.exit(2)
  }
  for (const name of readdirSync(dir)) {
    const p = join(dir, name)
    if (!statSync(p).isFile() || !name.endsWith('.mjs')) continue
    scanned += 1
    const src = readFileSync(p, 'utf8')
    const { structural, searchable } = scan(src)
    const clean = applyMask(src, structural)  // 算括号深度 / 顶层行用
    const find = applyMask(src, searchable)  // 找模式用（字符串内容保留）
    const disp = find.match(DISPATCH)
    if (!disp) continue
    withSelftest += 1
    allSelftests.push({
      file: relative(REPO, p).split('\\').join('/'),
      selftestLine: lineOf(clean, disp.index),
    })

    const bodies = functionBodies(clean)
    const dispIdx = disp.index

    // dispatch 之前的顶层语句里，形如 name(...) 的调用
    const hazards = []
    const unfollowed = [] // ★「扫到但没解析」必须与「没扫到」分开记
    for (const ln of topLevelLines(clean, dispIdx)) {
      // dispatch 那一行本身要排除：它是 `if (process.argv…) {`，
      // 否则会把 `if` 当成一个「没跟的调用」记进去。
      if (ln.start <= dispIdx && ln.end >= dispIdx) continue
      const text = clean.slice(ln.start, ln.end).trim()
      if (!text) continue
      if (/^(?:import|export)\b/.test(text)) continue
      if (FN_DECL.test(text + ' ')) continue
      const m = text.match(TOP_CALL)
      if (!m) continue
      const callee = m[1]
      if (KEYWORDS.has(callee)) continue
      if (!bodies.has(callee)) {
        // 本文件没声明它 —— 大概是 import 进来的。本工具不跟 import。
        unfollowed.push({ callee, line: lineOf(clean, ln.start) })
        continue
      }
      // 沿调用图追（只跟本文件内、带深度封顶）
      const seen = new Set()
      const queue = [[callee, 0]]
      while (queue.length) {
        const [fn, depth] = queue.shift()
        if (seen.has(fn) || depth > MAX_DEPTH) continue
        seen.add(fn)
        const b = bodies.get(fn)
        if (!b) continue
        const body = clean.slice(b.start, b.end)
        if (HAS_EXIT.test(body) && !hazards.some((h) => h.kind === 'exit' && h.why === fn)) {
          hazards.push({ kind: 'exit', why: fn })
        }
        if (FS_READS.test(body) && !hazards.some((h) => h.kind === 'fs' && h.why === fn)) {
          hazards.push({ kind: 'fs', why: fn })
        }
        for (const inner of body.matchAll(/(?:^|[^\w$.])([A-Za-z_$][\w$]*)\s*\(/g)) {
          const nm = inner[1]
          if (bodies.has(nm)) queue.push([nm, depth + 1])
        }
      }
    }

    if (hazards.length || unfollowed.length) {
      findings.push({
        file: relative(REPO, p).split('\\').join('/'),
        selftestLine: lineOf(clean, dispIdx),
        hazards,
        unfollowed,
      })
    }
  }
}

console.log(`【自检独立性普查】扫了 ${scanned} 个 .mjs，其中 ${withSelftest} 个处理 --selftest`)
const hitFiles = findings.filter((f) => f.hazards.length).length
const unfollowedTotal = findings.reduce((n, f) => n + (f.unfollowed?.length || 0), 0)
// ★「没扫到」与「扫到但没解析」必须分两栏，否则 0 读数会被当成「全都没问题」。
console.log(`分栏：**没扫到** —— ${withSelftest} 个带自检的文件里 ${hitFiles} 个命中；` +
  `**扫到但没解析**（顶层调用了 import 进来的函数，本工具不跟）${unfollowedTotal} 处。`)
if (!findings.length) {
  console.log('✅ 没有「跑用例之前先碰仓库状态或提前退出」的自检。')
}

// ★ 自证：表头的计数必须能被下面两段数字**加回来**。
//   只列有 finding 的、只打印有 hazard 的，两处都会让「总数」变成不可核对的数字。
const withFindings = new Set(findings.map((f) => f.file))
const cleanSelftests = allSelftests.filter((f) => !withFindings.has(f.file))
console.log(
  `\n自证：有 finding ${findings.length} + 零 finding ${cleanSelftests.length} = ` +
    `${findings.length + cleanSelftests.length}（表头 ${withSelftest}）`,
)
if (cleanSelftests.length) {
  console.log('零 finding 的（有自检、这一项没命中）：')
  for (const f of cleanSelftests) console.log(`   ✅ ${f.file}（--selftest 在第 ${f.selftestLine} 行）`)
}
if (findings.length + cleanSelftests.length !== withSelftest) {
  console.error('❌ 计数对不上：普查器自己的读数不可信，先修工具。')
  process.exit(2)
}

console.log('')
for (const f of findings) {
  console.log(`⚠️  ${f.file}（--selftest 在第 ${f.selftestLine} 行）`)
  for (const h of f.hazards) {
    console.log(`   · ${h.kind === 'exit' ? '顶层调用链里出现 process.exit' : '顶层调用链里读文件系统'}: ${h.why}()`)
  }
  if (f.unfollowed && f.unfollowed.length) {
    const names = f.unfollowed.map((u) => `${u.callee}@L${u.line}`)
    const shown = names.slice(0, 4).join(', ')
    console.log(`   · 未跟（本文件没声明，多半是 import 进来的）${f.unfollowed.length} 处: ${shown}${names.length > 4 ? ' …' : ''}`)
  }
  console.log('')
}
console.log('含义：这些自检**在跑自己的用例之前**就已经读了仓库状态或可能提前退出。')
console.log('⇒ 变异实验里出现「自检一行都没打印」的读数，多半是这里造成的，别当成「判据有牙」。')
console.log('（本工具只报事实；不跟 import 进来的函数、调用图深度封顶 ' + MAX_DEPTH + '，可能少报。）')
