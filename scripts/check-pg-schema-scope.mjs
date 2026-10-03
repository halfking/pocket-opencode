// check-pg-schema-scope.mjs —— 门禁：`const SCHEMA` 必须声明在**模块顶层**。
//
// 这道门是被一次真实的静默事故逼出来的
// ----------------------------------------
// 2026-10-03 真机跑全量 Maestro 套件时，`tasks-crud.yaml` 的前置脚本
// `scripts/tasks-crud-fixture.mjs` 直接崩在
//     ReferenceError: SCHEMA is not defined
//     at scripts/tasks-crud-fixture.mjs:36
// 根因是 24abc616（"30 处写死 PG schema 锁死隔离验证"）那次批量改造：
// 它的插入锚点选在了**函数体首行**（`const q = (sql) => {` /
// `function resolvePsql() {` / for 循环体），于是
//     const SCHEMA = process.env.POCKET_PG_SCHEMA || 'opencode_pocket';
// 被插进了函数体，而 `${SCHEMA}` 的引用全在模块顶层 ——
// `const` 是块级作用域，函数外看不见它。
//
// 为什么它能一路绿灯进 main
// --------------------------
//   · `node --check` 只做语法检查，作用域错误是**运行时**错误，抓不到；
//   · 5 个坏文件里，只有 `tasks-crud-fixture.mjs` 会在跑 flow 之前被执行到；
//     另外 4 个（verify-email / verify-gateway / verify-marketplace-install /
//     find-note-in-pg）是**手动排障时**才跑的脚本，平时根本不执行，
//     所以「没人撞上」就一直没人知道；
//   · 同批的 `check-pg-schema-hardcoded.mjs`（不许写死 schema）只查
//     SQL 文本里有没有字面量，**完全不看声明在哪个作用域** ——
//     它对这次事故是恒真的，也就是它给的是假安心。
//
// 判据（为什么这么定）
// --------------------
//   1. 主判据：声明所在的花括号深度必须为 0。
//   2. 附带判据：模块顶层出现 `${SCHEMA}` 引用、却找不到顶层声明 —— 同样报错。
//      （这一条覆盖「声明被整段删掉」和「拼错成 SCHEMA_」两种情况。）
//   3. **覆盖下限**：扫描到的「声明了 SCHEMA 的文件数」低于 MIN_DECLARING_FILES
//      时**直接非 0 退出**。因为本门是「集合为空即通过」型判据 ——
//      抽取器哪天坏了、glob 写错了，它会安静地全绿。空集合同样满足
//      「没有违规」，这正是 §之前那类事故的形状。
//   4. 深度统计前**必须剥掉注释与字符串字面量**。第一版抽取器没剥，
//      把文档注释里的 `FROM ${SCHEMA}.tasks`、`migrate-pg-schema.mjs` 里
//      「要插入的文本」那个字符串数组、以及门禁自测里喂给
//      `lineIsHardcoded()` 的样本字符串全算成了真代码 —— 7 条里 3 条是假阳性。
//      **抽取器自己有假阳性时要修抽取器，不要去改结论。**
//
// 用法
// ----
//   node scripts/check-pg-schema-scope.mjs --selftest   # 只跑判据自测（含负控）
//   node scripts/check-pg-schema-scope.mjs             # 门禁本体
//   （接线方式与其它 check:* 一致：`--selftest && <本体>`）

import { readFileSync, readdirSync } from 'node:fs'
import { join, dirname, relative } from 'node:path'
import { fileURLToPath } from 'node:url'

const ROOT = join(dirname(fileURLToPath(import.meta.url)), '..')
const SCRIPTS = join(ROOT, 'scripts')

// 抽取器坏掉时的兜底阈值。当前仓库里声明 SCHEMA 的脚本约 20 个，
// 留出余量但仍高到「少扫一半就一定会喊」。
const MIN_DECLARING_FILES = 12

const DECL_RE = /const\s+SCHEMA\s*=\s*process\.env\.POCKET_PG_SCHEMA/
const REF_RE = /\$\{\s*SCHEMA\s*\}/

/**
 * 把一行里的注释与字符串内容替换成空格（保留列宽，便于按行号定位），
 * 只留下真正参与作用域计算的结构字符。
 *
 * 模板串要特殊处理：`${ ... }` 内部是**真代码**（`${SCHEMA}.tasks`），
 * 必须留下；而 `` ` `` 与其中间的普通文本必须抹掉。
 * 第一版没做这件事，注释里的 `${SCHEMA}` 被当成越界引用，直接制造了 3 条假阳性。
 */
function stripLine(line, state) {
  let out = ''
  let i = 0
  while (i < line.length) {
    const c = line[i]
    const n = line[i + 1]
    if (state.block) {
      if (c === '*' && n === '/') { state.block = false; out += '  '; i += 2; continue }
      out += ' '; i++; continue
    }
    if (state.tmpl) {
      if (c === '\\') { out += '  '; i += 2; continue }
      if (c === '$' && n === '{') { out += '  '; state.tmpl = false; state.expr = 0; i += 2; continue }
      if (c === '`') { out += ' '; state.tmpl = false; i++; continue }
      if (state.expr > 0) {
        if (c === '{') state.expr++
        else if (c === '}') { state.expr--; if (state.expr === 0) state.tmpl = true }
      }
      out += ' '
      i++
      continue
    }
    if (c === '/' && n === '/') { out += ' '.repeat(line.length - i); break }
    if (c === '/' && n === '*') { state.block = true; out += '  '; i += 2; continue }
    if (c === "'" || c === '"') {
      const q = c
      out += ' '
      i++
      while (i < line.length && line[i] !== q) { if (line[i] === '\\') { i += 2; continue } i++ }
      out += ' '
      i++
      continue
    }
    if (c === '`') { out += ' '; state.tmpl = true; i++; continue }
    out += c
    i++
  }
  return out
}

/**
 * 纯函数：给一份源码，返回作用域问题列表。抽出来是为了让 --selftest 能直接驱动它，
 * 而不必去改磁盘上的真文件（负控要证明的是**判据**会响，不是某个文件坏没坏）。
 */
export function findScopeDefects(source) {
  const raw = source.split(/\r?\n/)
  const state = { block: false, tmpl: false, expr: 0 }
  const code = raw.map((l) => stripLine(l, state))

  // depthAt[i] = 第 i 行**行尾**处的花括号嵌套深度
  const depthAt = []
  let d = 0
  for (const l of code) {
    d += (l.match(/\{/g) || []).length - (l.match(/\}/g) || []).length
    depthAt.push(d)
  }
  // 声明行所在行的**行首**深度：声明在第 i 行，则其所在块的深度是 depthAt[i-1]
  const depthBefore = (i) => (i === 0 ? 0 : depthAt[i - 1])

  const decls = []
  const refs = []
  code.forEach((l, i) => {
    if (DECL_RE.test(l)) decls.push(i)
    // 引用检测必须用**原始行**，但要额外要求剥离后这一行仍含 `SCHEMA` ——
    // 这样「文档注释里写的 FROM ${SCHEMA}.tasks」会被排除（它是注释，不是代码）。
    // 为什么要分家：stripLine 会把 `${` 抹成空格（模板串的普通文本要清掉），
    // 于是对**剥离后**的行跑 /\${SCHEMA}/ 永远匹配不到 —— 第一版就这么写的，
    // 结果是 selftest 第 7 条（顶层引用无顶层声明）恒不报错：
    // 判据自己失明了，却因为「别处能报」而看不出来。
    if (REF_RE.test(raw[i]) && /\bSCHEMA\b/.test(l)) refs.push(i)
  })
  const defects = []
  for (const i of decls) {
    const dep = depthBefore(i)
    if (dep !== 0) {
      defects.push({ kind: 'nested-decl', line: i + 1, depth: dep })
    }
  }
  // 顶层引用必须有顶层声明兜着。
  // ⚠️ 这条**不能**因为「一个声明都没找到」就提前 return 跳过 —— 那正是
  // 负控第 7 条抓到的一处自身漏洞：声明被整段删掉时（`decls.length === 0`）
  // 旧写法直接返回「无问题」，而 `${SCHEMA}` 的越界引用恰恰是那时候才出现的。
  // 判据 1 和判据 2 必须**各自独立**成立。
  const topRefs = refs.filter((i) => depthBefore(i) === 0)
  if (topRefs.length && !decls.some((i) => depthBefore(i) === 0)) {
    defects.push({ kind: 'ref-without-top-decl', lines: topRefs.map((i) => i + 1) })
  }
  return { defects, decls, refs }
}

function listScripts() {
  try {
    return readdirSync(SCRIPTS).filter((f) => f.endsWith('.mjs')).map((f) => join(SCRIPTS, f))
  } catch {
    return []
  }
}

// ---------------- 判据自测（含负控）----------------
function selftest() {
  const cases = []
  const add = (name, src, expectDefect) => cases.push({ name, src, expectDefect })

  // 正样本：声明在模块顶层 —— 不该报
  add('顶层声明', `const SCHEMA = process.env.POCKET_PG_SCHEMA || 'x';\nconst q = () => \`from \${SCHEMA}.t\`\n`, false)
  // 负样本 1：24abc616 的真实形态 —— 声明被塞进箭头函数体，引用在顶层
  add('箭头函数体内声明', `const q = (sql) => {\nconst SCHEMA = process.env.POCKET_PG_SCHEMA || 'x';\nreturn sql\n}\nconsole.log(\`from \${SCHEMA}.t\`)\n`, true)
  // 负样本 2：塞进具名函数体
  add('具名函数体内声明', `function r() {\nconst SCHEMA = process.env.POCKET_PG_SCHEMA || 'x';\n}\nconsole.log(\`\${SCHEMA}.t\`)\n`, true)
  // 负样本 3：塞进 for 循环体（verify-marketplace-install 的真实形态，深度 2）
  add('for 循环体内声明', `for (const c of xs) {\nconst SCHEMA = process.env.POCKET_PG_SCHEMA || 'x';\n}\nconsole.log(\`\${SCHEMA}.t\`)\n`, true)
  // 假阳性防护 1：文档注释里的示例不算数
  add('注释里的示例不报', `// 正确写法：FROM \${SCHEMA}.tasks\nconst SCHEMA = process.env.POCKET_PG_SCHEMA || 'x';\n`, false)
  // 假阳性防护 2：字符串数组里「要插入的声明文本」不算数（migrate-pg-schema 的真实形态）。
  //   ⚠️ 样本必须与真实文件同形：那里的 `${SCHEMA}` 出现在**字符串字面量**里
  //   （`replace(..., '${SCHEMA}.')`），不在真模板插值里。
  //   写成真模板（`const a = \`${SCHEMA}.t\``）就是另一个东西了 ——
  //   那确实是「顶层引用 ${SCHEMA} 却没有顶层声明」，门禁报错是对的，
  //   是样本把「要测的东西」换掉了。
  add('字符串数组里的声明文本不报', `const INS = [\n  "const SCHEMA = process.env.POCKET_PG_SCHEMA || 'x';",\n]\nconst a = s.replace(/x/g, '\${SCHEMA}.')\n`, false)
  // 判据 2：顶层引用但没有顶层声明（声明被整段删掉）
  add('顶层引用无顶层声明', `console.log(\`from \${SCHEMA}.t\`)\n`, true)

  let bad = 0
  for (const c of cases) {
    const got = findScopeDefects(c.src).defects.length > 0
    const ok = got === c.expectDefect
    if (!ok) bad++
    console.log(`${ok ? '  ✅' : '  ❌'} ${c.name}（期望${c.expectDefect ? '报错' : '不报'}，实际${got ? '报错' : '不报'}）`)
  }
  if (bad) {
    console.error(`\n❌ 判据自测 ${bad} 条不符 —— 门禁本身不可信，拒绝给结论。`)
    process.exit(3)
  }
  console.log(`✅ 判据自测 ${cases.length}/${cases.length} 通过（含 3 条负控 + 2 条假阳性防护）`)
}

if (process.argv.includes('--selftest')) {
  selftest()
  process.exit(0)
}

// ---------------- 门禁本体 ----------------
const files = listScripts()
let scanned = 0
const all = []
for (const f of files) {
  let src
  try { src = readFileSync(f, 'utf8') } catch { continue }
  const { defects, decls } = findScopeDefects(src)
  if (!decls.length) continue
  scanned++
  for (const d of defects) all.push({ file: relative(ROOT, f), ...d })
}

if (scanned < MIN_DECLARING_FILES) {
  console.error(
    `❌ 只扫到 ${scanned} 个声明了 SCHEMA 的脚本，低于下限 ${MIN_DECLARING_FILES}。\n` +
    `   本门是「没有违规即通过」，抽取器/glob 一坏就会安静全绿 —— 这里必须响亮退出。`,
  )
  process.exit(2)
}

if (all.length) {
  console.error(`❌ 有 ${all.length} 处 \`const SCHEMA\` 不在模块顶层（会在运行时 ReferenceError）：`)
  for (const d of all) {
    console.error(
      d.kind === 'nested-decl'
        ? `   ${d.file}:${d.line}  声明嵌在深度 ${d.depth} 的块里`
        : `   ${d.file}:${(d.lines || []).join(',')}  顶层用了 \${SCHEMA}，但没有顶层声明`,
    )
  }
  console.error('\n   修法：把该声明移到模块顶层（函数/循环体之外）。别改成 var —— 那是把静默错值换成更隐蔽的错值。')
  process.exit(1)
}
console.log(`OK：${scanned} 个声明了 SCHEMA 的脚本，声明全部在模块顶层`)
