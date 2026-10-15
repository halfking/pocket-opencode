// unwired-gate-census.mjs —— 普查「写了自证却从没被驱动」的门。
//
// 只报事实，不下结论，不自动改任何东西。
//
// ── 为什么值得单列 ────────────────────────────────────────────────────────
// §136.4 手工发现 `check-pg-schema-hardcoded.mjs` 有 11 条真用例、自检 11/11，
// 但**全仓没有任何门调它** —— 最后一次改动（2026-10-03）的 commit 标题恰恰是
// 「…**30 处写死 PG schema 锁死隔离验证**」。
// ⇒ 一次手工抽查只抓到 1 条 ⇒ 说明这是个**类**，不是个案。
//
// ── 口径（重要：不做「没被接线的门」这种宽口径）──────────────────────────
// 朴素口径会报「329 个候选里 290 个没接线」，但其中绝大多数是
// `diag-*` / `probe-*` / `verify-*` 一次性脚本 —— 把它们算成「没接线的门」
// 是**误导**。所以本工具只取一个**作者明确表达过意图**的信号：
//
//   **该脚本带 `--selftest` 开关。**
//
// 写自证机制是要花成本的，它说明作者认为「这个判据值得长期复核」；
// 一个从不被调用的脚本，写再好的自检也只是自我安慰。
//
// 三档：
//   WIRED_AND_DRIVEN  被 npm 脚本引用，且该脚本名登记在 gates.json
//   WIRED_NOT_DRIVEN  被 npm 脚本引用，但**不在** gates.json 里（§114 的脸②）
//   UNWIRED           没有任何 npm 脚本引用它
//
// ⚠ 已知边界（不掩盖）：
//   · UNWIRED **不等于**它该被接线 —— 可能是有意手工跑的探针。
//     所以对每条 UNWIRED 另报一件事实：**全仓提到它的行里，有几条是真调用、
//     有几条只是注释**。全仓唯一引用都是注释 ⇒ 强烈提示「没人真的跑它」。
//   · 只认 npm scripts 与 gates.json 两处；Makefile / CI 里直接 node 的路径不覆盖。
//   · 按 basename 匹配，若两个目录有同名文件会混。

import { readFileSync, readdirSync, existsSync, statSync } from 'node:fs'
import { dirname, join, relative } from 'node:path'
import { fileURLToPath } from 'node:url'

const here = dirname(fileURLToPath(import.meta.url))
const REPO = join(here, '..', '..')
const DEFAULT_SCAN_DIRS = ['scripts', 'frontend/scripts']
const SCAN_DIRS = process.argv[2]
  ? [process.argv[2].startsWith('/') ? process.argv[2] : join(REPO, process.argv[2])]
  : DEFAULT_SCAN_DIRS

const PKG = join(REPO, 'frontend', 'package.json')
const GATES = join(REPO, 'frontend', 'gates.json')

for (const p of [PKG, GATES]) {
  if (!existsSync(p)) {
    console.error(`❌ 缺必需输入：${p}`)
    process.exit(2)
  }
}

/** gates.json 里登记的门名（它按 **npm 脚本名**登记，不是实现文件名）。 */
function gateNamesIn(o, acc = new Set()) {
  if (typeof o === 'string') {
    if (/^(check|audit|verify|test|ci):/.test(o)) acc.add(o)
  } else if (Array.isArray(o)) {
    for (const x of o) gateNamesIn(x, acc)
  } else if (o && typeof o === 'object') {
    for (const v of Object.values(o)) gateNamesIn(v, acc)
  }
  return acc
}

const pkg = JSON.parse(readFileSync(PKG, 'utf8'))
const scripts = pkg.scripts ?? {}
const gateNames = gateNamesIn(JSON.parse(readFileSync(GATES, 'utf8')))

/** npm 脚本名 -> 它命令里提到的 .mjs 文件名集合 */
const nameToFiles = new Map()
for (const [name, cmd] of Object.entries(scripts)) {
  const files = new Set()
  for (const m of String(cmd).matchAll(/[\w./-]+\.mjs/g)) files.add(m[0].split('/').pop())
  nameToFiles.set(name, files)
}

/** 全仓源码文件（用于「谁提到它」） */
const SELF_PATH = fileURLToPath(import.meta.url)
function allSourceFiles() {
  const out = []
  const skip = new Set(['.git', 'node_modules', 'dist', 'build', '.venv', '__pycache__', 'vendor'])
  const walk = (d) => {
    for (const e of readdirSync(d)) {
      if (skip.has(e) || e.startsWith('.')) continue
      const p = join(d, e)
      // ★ 自指豁免：普查器自己的注释里会提到这些文件名（举例说明用），
      //   不排除就会把自己的说明算成「别处提到了它」。
      if (p === SELF_PATH) continue
      const st = statSync(p)
      if (st.isDirectory()) walk(p)
      else if (/\.(mjs|js|ts|go|json|yml|yaml)$/.test(e)) out.push(p)
    }
  }
  walk(REPO)
  return out
}

/**
 * 把一行分成「真调用」还是「仅注释」，并跨行跟踪块注释状态。
 *
 * ⚠ v1 在这里报过**假调用**：它只剥 `//`，不认 `* ` 开头的块注释行，
 *   于是 `check-pg-schema-hardcoded.mjs` 自己头注释里的 4 行用法说明
 *   被算成「真调用 4 次」—— 而它全仓**一处都没被执行过**（已正向查证）。
 * ⇒ 「提到它」与「执行它」是两件事；列错了比不列更糟。
 */
function classifyRefs(txt, base) {
  let calls = 0
  let comments = 0
  let inBlock = false
  for (const line of txt.split('\n')) {
    // 先把这一行扫描一遍，更新块注释状态
    let i = 0
    while (i < line.length) {
      if (!inBlock) {
        if (line[i] === '/' && line[i + 1] === '/') break
        if (line[i] === '/' && line[i + 1] === '*') { inBlock = true; i += 2; continue }
        i++
      } else {
        if (line[i] === '*' && line[i + 1] === '/') { inBlock = false; i += 2; continue }
        i++
      }
    }
    const at = line.indexOf(base)
    if (at >= 0) {
      const before = line.slice(0, at)
      // ★ v2 又栽了一次：只剥了 `/*`，**漏剥 `//`** ⇒ `//   · 同批的 \`check-x.mjs\``
      //   被判成真调用。块注释行内（` * …`）由跨行的 inBlock 兜住，
      //   行注释必须在这里显式剥掉。
      const stripped = before.replace(/\/\*[\s\S]*$/, '').replace(/\/\/.*$/, '').trim()
      const onlyCommentSyntax = /^[\s*]*$/.test(stripped)
      if (inBlock || onlyCommentSyntax) comments++
      else calls++
    }
  }
  return { calls, comments }
}

const sources = allSourceFiles()

function referencesOf(base) {
  let calls = 0
  let comments = 0
  for (const p of sources) {
    let txt
    try { txt = readFileSync(p, 'utf8') } catch { continue }
    if (!txt.includes(base)) continue
    const r = classifyRefs(txt, base)
    calls += r.calls
    comments += r.comments
  }
  return { calls, comments }
}

// ── 第四档：CI 直接调 ────────────────────────────────────────────────────
// ★ v3 的 UNWIRED 标签**过度声称过**：实测 `check-smart-quotes` 被
//   `.github/workflows/backend.yml` 的 `run:` 里直接调用
//   （`node scripts/check-smart-quotes.mjs --selftest`），
//   也就是说它**一直在跑**，只是没经过 `gates.json`。
//   ⇒ 只看 npm scripts + gates.json 会把「跑着的门」误报成「没接线」。
//   所以第四档必须扫 workflow。YAML 的注释是 `#`，判据要认它。
function ciInvocations(base) {
  const dir = join(REPO, '.github', 'workflows')
  if (!existsSync(dir)) return []
  const out = []
  for (const name of readdirSync(dir)) {
    if (!/\.(ya?ml)$/.test(name)) continue
    const p = join(dir, name)
    let txt
    try { txt = readFileSync(p, 'utf8') } catch { continue }
    if (!txt.includes(base)) continue
    for (const [i, line] of txt.split('\n').entries()) {
      if (!line.includes(base)) continue
      if (/^\s*#/.test(line)) continue // YAML 注释不算调用
      out.push(`${name}:${i + 1}  ${line.trim().slice(0, 70)}`)
    }
  }
  return out
}

const rows = []
let scanned = 0

for (const dir of SCAN_DIRS) {
  if (!existsSync(dir)) {
    console.error(`❌ 扫描根不存在：${dir}`)
    process.exit(2)
  }
  for (const name of readdirSync(dir)) {
    const p = join(dir, name)
    if (!statSync(p).isFile() || !name.endsWith('.mjs')) continue
    scanned += 1
    const txt = readFileSync(p, 'utf8')
    if (!txt.includes('--selftest')) continue
    const owners = []
    for (const [scriptName, files] of nameToFiles) if (files.has(name)) owners.push(scriptName)
    const ci = ciInvocations(name)
    let status
    if (owners.length && owners.some((o) => gateNames.has(o))) status = 'WIRED_AND_DRIVEN'
    else if (owners.length) status = 'WIRED_NOT_DRIVEN'
    else if (ci.length) status = 'CI_DRIVEN'
    else status = 'UNWIRED'
    rows.push({
      file: relative(REPO, p).split('\\').join('/'),
      status,
      owners,
      ci,
      refs: status === 'UNWIRED' ? referencesOf(name) : null,
    })
  }
}

const by = { WIRED_AND_DRIVEN: [], WIRED_NOT_DRIVEN: [], CI_DRIVEN: [], UNWIRED: [] }
for (const r of rows) by[r.status].push(r)

console.log(`【未接线门普查】扫了 ${scanned} 个 .mjs，其中 ${rows.length} 个带 --selftest`)
console.log(`  gates.json 登记 ${gateNames.size} 个门名 · package.json 有 ${Object.keys(scripts).length} 条脚本`)
console.log(`  四档：gates 驱动 ${by.WIRED_AND_DRIVEN.length} · 有 npm 脚本但 gates 没登记 ${by.WIRED_NOT_DRIVEN.length} · workflow 直接调 ${by.CI_DRIVEN.length} · 真的没人调 ${by.UNWIRED.length}`)

if (!rows.length) {
  console.log('✅ 没有带自证的脚本。')
  process.exit(0)
}

console.log('')
for (const r of by.WIRED_NOT_DRIVEN) {
  console.log(`⚠️  WIRED_NOT_DRIVEN  ${r.file}  ← npm 脚本 ${r.owners.join(', ')}（gates.json 里没有）`)
}
for (const r of by.CI_DRIVEN) {
  console.log(`ℹ️  CI_DRIVEN  ${r.file}`)
  for (const c of r.ci) console.log(`   workflow: ${c}`)
}
for (const r of by.UNWIRED) {
  const { calls, comments } = r.refs
  console.log(`❌ UNWIRED  ${r.file}`)
  console.log(`   全仓提到它：真调用 ${calls} 行 · 仅注释 ${comments} 行` +
    (calls === 0 ? '  ⇒ **没有任何一处真的执行它**' : ''))
}
if (by.UNWIRED.length) console.log('\nUNWIRED 不等于「该接线」—— 判据是作者意图，事实只摆在这儿。')