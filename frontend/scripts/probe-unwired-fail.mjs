// probe-unwired-fail.mjs —— **一次性排查探针**（不是门禁）。
//
// 扫 gates 里每个脚本，找出这个形状：
//     if (坏情况) {
//       console.error('❌ …')      ← 报告了失败
//     }                             ← 但这个分支里没有 process.exit / throw
//
// 这正是 §72 在 check-test-coverage.mjs 里发现的洞：
// 判据**已经算出来并打印了**，退出码却不用它 ⇒ 门报绿。
// 那个洞是人肉翻代码找到的；这个探针把同一形状一次扫完 36 道门。
//
// ⚠ 它的输出是**候选清单，不是结论**：静态扫描分不清
//   「这个 ❌ 只是提示，后面还有别的分支会退出」与「真的没人退出」。
//   每一条都必须回到源码逐个确认。
import { readFileSync, existsSync } from 'node:fs'
import { join, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'

const HERE = dirname(fileURLToPath(import.meta.url))
const ROOT = join(HERE, '..')
const gates = JSON.parse(readFileSync(join(ROOT, 'gates.json'), 'utf8')).gates

/** 取 npm script 字符串：门可能定义在 frontend/package.json，也可能定义在仓库根。 */
function scriptsOf(name) {
  const out = {}
  for (const base of [join(ROOT, 'package.json'), join(ROOT, '..', 'package.json')]) {
    if (!existsSync(base)) continue
    Object.assign(out, JSON.parse(readFileSync(base, 'utf8')).scripts ?? {})
  }
  return out[name]
}
/** 找出所有含 ❌ 的 console.error 行下标。 */
function errLines(src) {
  const out = []
  src.split('\n').forEach((l, i) => { if (/console\.(error|warn)\s*\(.*❌/.test(l)) out.push(i) })
  return out
}

/**
 * 往上找**最近的那层 `if`**。
 *
 * ⚠ 第二版修的错：第一版把 `for|while` 也当成分支边界，于是 ❌ 在
 *   `for` 循环体里时，命中的是**内层 for**（循环体里当然没有 exit），
 *   而真正带 `process.exit(1)` 的是**外层 if** ⇒ 又一批假阳性
 *   （check-dead-features / check-i18n-keys 都栽在这里）。
 *   ⇒ 只认 `if`，且要求它的缩进**严格小于** ❌ 所在行。
 */
function enclosingBranch(lines, i) {
  const myIndent = lines[i].match(/^\s*/)[0].length
  for (let k = i - 1; k >= 0 && k > i - 80; k--) {
    const m = lines[k].match(/^(\s*)if\s*\(/)
    if (m && m[1].length < myIndent) return { line: k, indent: m[1].length }
  }
  return null
}

/**
 * 把字符串/模板字符串的内容**挖空**（保留引号与行长），再做花括号配平。
 *
 * ⚠ 这是第一版最大的错误：直接按字符数 `{` `}` 会把 `` `❌ ${loc} 缺 ${n} 个` ``
 *   里的 `${...}` 当成块边界，深度立刻错位 ⇒ **整块的 exit 看不见**
 *   ⇒ 满屏假阳性（实测 14 条候选里前 4 条全是假的：
 *   check-i18n 用 `failed` 延迟标志、run-mjs-tests 的 `process.exit(1)`
 *   就在同一个 if 块里第 3 行）。先挖空再配平。
 */
function blankLiterals(line) {
  let out = ''
  let i = 0
  while (i < line.length) {
    const c = line[i]
    if (c === '`') {                       // 模板字符串：挖到匹配的 ` （跳过 ${ } 里的嵌套反引号）
      out += '`'; i++
      while (i < line.length) {
        if (line[i] === '\\') { out += '  '; i += 2; continue }
        if (line[i] === '`') { out += '`'; i++; break }
        out += line[i] === '\n' ? '\n' : ' '
        i++
      }
      continue
    }
    if (c === "'" || c === '"') {          // 普通字符串
      const q = c; out += c; i++
      while (i < line.length) {
        if (line[i] === '\\') { out += '  '; i += 2; continue }
        if (line[i] === q) { out += q; i++; break }
        out += line[i] === '\n' ? '\n' : ' '
        i++
      }
      continue
    }
    out += c; i++
  }
  return out
}

/** 从 open 行起做花括号配平（**在挖空字面量之后**的行上数），返回块结束行（含）。 */
function blockEnd(rawLines, open) {
  const lines = rawLines.map(blankLiterals)
  let depth = 0
  for (let k = open; k < lines.length; k++) {
    for (const ch of lines[k]) {
      if (ch === '{') depth++
      else if (ch === '}') { depth--; if (depth === 0) return k }
    }
  }
  return lines.length - 1
}

/** 这个分支到底会不会让门失败？三种合法写法都要认。
 *  ⚠ 第三版补的：`process.exitCode = N` 也是合法失败（device-matrix 三处都用它），
 *    前两版只认 `process.exit(` / `throw` ⇒ 又一批假阳性。 */
const FAILS_RE = /process\.exit\s*\(|process\.exitCode\s*=|throw\s/

const findings = []
const scanned = new Set()
for (const g of gates) {
  const cmd = scriptsOf(g)
  if (!cmd) continue
  for (const m of cmd.matchAll(/([\w./-]+\.mjs)/g)) {
    const raw = m[1]
    const abs = raw.startsWith('../') ? join(ROOT, '..', raw.slice(3)) : join(ROOT, raw)
    if (!existsSync(abs) || scanned.has(abs)) continue
    scanned.add(abs)
    const text = readFileSync(abs, 'utf8')
    const lines = text.split('\n')
    for (const i of errLines(text)) {
      const br = enclosingBranch(lines, i)
      const end = br ? blockEnd(lines, br.line) : lines.length - 1
      const body = lines.slice(br ? br.line : 0, end + 1).join('\n')
      const fails = FAILS_RE.test(body)
      if (!fails) {
        findings.push({ gate: g, file: raw, line: i + 1, branch: br ? br.line + 1 : null, text: lines[i].trim().slice(0, 90) })
      }
    }
  }
}

// （前两版都是「先出结论、再发现仪器自己错了」：模板字面量里的 `${}` 破坏了括号配平，
//   然后 for/while 抢走了本该属于外层 if 的边界。14 → 11 → ? 全是仪器自己的错。）
const SELF_CASES = [
  {
    name: '§72 的形状：❌ 直接在 if 里、分支末尾没有 exit ⇒ 必须被抓',
    src: [
      'if (deadGlobs.length) {',
      "  console.error('❌ glob 一个文件都没匹配到')",
      '}',
    ],
    want: true,
  },
  {
    name: '循环嵌在 if 里、exit 在 if 末尾 ⇒ 不该被抓（第一版就栽在这）',
    src: [
      'if (added.length) {',
      "  console.error('❌ 新增')",
      '  for (const a of added) {',
      "    console.error(`❌ ${a}`)",
      '  }',
      '  process.exit(1)',
      '}',
    ],
    want: false,
  },
  {
    name: '❌ 在 for 循环里、外层 if 有 exit ⇒ 不该被抓',
    src: [
      'if (missing.length) {',
      "  console.error('x')",
      '  for (const f of missing) console.error(`❌ 从未被执行：${f}`)',
      '  process.exit(1)',
      '}',
    ],
    want: false,
  },
]

let selfOk = true
console.log('【自检】')
for (const c of SELF_CASES) {
  const got = c.src.some((l, i) => {
    if (!/console\.(error|warn)\s*\(.*❌/.test(l)) return false
    const br = enclosingBranch(c.src, i)
    const end = br ? blockEnd(c.src, br.line) : c.src.length - 1
    const body = c.src.slice(br ? br.line : 0, end + 1).join('\n')
    return !FAILS_RE.test(body)
  })
  const ok = got === c.want
  if (!ok) selfOk = false
  console.log(`  ${ok ? '通过' : '失败'}  ${c.name}（实得「报出=${got}」，期望 ${c.want}）`)
}
if (!selfOk) {
  console.error('\n❌ 自检没过 —— 本探针的输出不可用，先修它再看候选清单。')
  process.exit(2)
}
console.log('  ⚠ 已知盲区（静态法看不穿，靠人读源码兜底）：')
console.log('     ① `failed = true` 这类**延迟标志**模式：❌ 所在 if 里没有 exit，')
console.log('        真正的 exit 在文件末尾 ⇒ 本探针会**误报**（check-i18n-keys 就是这种）。')
console.log('     ② ❌ 只是提示、真正的失败由调用方判断（device-matrix 的 helper 函数）。\n')

console.log(`扫了 ${gates.length} 道门、${scanned.size} 个脚本，发现 ${findings.length} 处「打印 ❌ 但所在分支没有 process.exit / throw」：\n`)
for (const f of findings) {
  console.log(`  ${f.gate}  ${f.file}:${f.line}${f.branch ? `（分支起于 :${f.branch}）` : '（顶层）'}`)
  console.log(`      ${f.text}`)
}
