#!/usr/bin/env node
/**
 * migrate-pg-schema.mjs — 把探针脚本里写死的 `opencode_pocket.` 换成 `${SCHEMA}.`。
 *
 * ## 为什么
 *
 * 写死 schema 的脚本**只能**对着共享开发库跑。它们失败时留下的 SEED
 * 就在另一会话的库里（BUG-V14 的放大器），而且想在隔离后端上验证它们时，
 * 断言会去查另一个 schema —— 静悄悄给出「通过」。
 *
 * ## 为什么不能一把 sed
 *
 * SQL 有三种引号风格，而 `${SCHEMA}` 只在**反引号**模板串里才成立：
 *   psql(`select ... from opencode_pocket.tasks`)        → 直接替换
 *   psql('select ... from opencode_pocket.tasks;')       → 必须先把引号换成反引号
 *   sql("select count(*) from opencode_pocket.x;")       → 同上
 * 换引号前必须断言串里**没有**反引号、**没有** `${`，否则换完就是语法错误或
 * 静默插值。撞上这两种的一律跳过并报告，交人工看。
 *
 * ## 机械不变量（每文件都查，不是只靠 node --check）
 *
 *  1. 替换数 == 该文件里 `opencode_pocket.` 出现数（一次过，不留半个）
 *  2. `SCHEMA` 声明恰好插入 1 次
 *  3. 改完 `node --check` 通过
 *  4. 任何一条不满足 → **回滚该文件**，并计入 skipped
 *
 * 用法：node scripts/migrate-pg-schema.mjs [--dry] [--list]
 *
 * ## 行尾说明（不是 bug，别去"修"它）
 *
 * 本仓库 `core.autocrlf=true` 且无 .gitattributes ⇒ **索引里一律是 LF**，
 * 工作区 checkout 时才是 CRLF。迁移按 `\n` 切分再 join，会给原本纯 CRLF 的文件
 * 插入 4 行裸 LF（= SCHEMA_DECL 那 4 行）⇒ 工作区出现 crlf/bareLF 混合。
 * 提交内容不受影响（git 归一化），下次 checkout 自动恢复。
 * 若确实要在工作区也保持一致：对这几个文件跑一次 CRLF 归一即可，不要改 SCHEMA_DECL。
 */
import { readFileSync, writeFileSync, readdirSync, statSync, unlinkSync } from 'node:fs'
import { execFileSync } from 'node:child_process'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const SELF = path.resolve(fileURLToPath(import.meta.url))
const ROOT = path.resolve(path.dirname(SELF), '..')
const SCRIPTS = path.join(ROOT, 'scripts')
const DRY = process.argv.includes('--dry')
const LIST = process.argv.includes('--list')

const SCHEMA_DECL = [
  "// PG schema：跟随后端配置（backend/internal/config/config.go 的 POCKET_PG_SCHEMA，默认值相同）。",
  "// 写死 opencode_pocket 会让本脚本只能对着共享库跑 —— 失败时 SEED 就留在别人的库里。",
  "const SCHEMA = process.env.POCKET_PG_SCHEMA || 'opencode_pocket';",
  "if (SCHEMA !== 'opencode_pocket') console.log(`PG schema = ${SCHEMA}（非共享库）`);",
].join('\n')

const isComment = (l) => /^\s*(\/\/|\*|--|#)/.test(l)
const occurrences = (s) => (s.match(/\bopencode_pocket\s*\./gi) || []).length
/**
 * 只数**代码行**里的出现次数。
 * ⚠️ 必须和 check-pg-schema-hardcoded.mjs 用同一套判定 —— 头一版这里按全文数，
 * 于是注释里的 `opencode_pocket.notes` 也算「还剩 1 处」，7 个跳过里有 6 个
 * 是这个不一致造成的：门禁说不报，迁移却卡住。判据不一致比判据错更费时间。
 */
function codeOccurrences(lines) {
  return lines.filter((l) => !isComment(l)).reduce((n, l) => n + occurrences(l), 0)
}

/** 找出该行里含 hardcoded schema 的字符串字面量。返回 [{start,end,quote,body}] */
function literalsIn(line) {
  const out = []
  const re = /(`(?:[^`\\]|\\.)*`)|('(?:[^'\\]|\\.)*')|("(?:[^"\\]|\\.)*")/g
  let m
  while ((m = re.exec(line)) !== null) out.push({ start: m.index, end: m.index + m[0].length, quote: m[0][0], body: m[0].slice(1, -1) })
  return out
}

function migrateLine(line) {
  if (isComment(line) || !occurrences(line)) return null
  const lits = literalsIn(line).filter((l) => occurrences(l.body))
  if (!lits.length) return null
  const edits = []
  for (const l of lits) {
    if (l.quote === '`') {
      edits.push({ start: l.start + 1, end: l.end - 1, text: l.body.replace(/\bopencode_pocket\s*\./gi, '${SCHEMA}.') })
    } else {
      if (l.body.includes('`') || l.body.includes('${')) return { skip: `含反引号或 \${}，换引号不安全：${line.trim().slice(0, 80)}` }
      edits.push({ start: l.start, end: l.end, text: '`' + l.body.replace(/\bopencode_pocket\s*\./gi, '${SCHEMA}.') + '`' })
    }
  }
  let out = line
  for (const e of edits.reverse()) out = out.slice(0, e.start) + e.text + out.slice(e.end)
  return { text: out, replaced: occurrences(line) }
}

function walk(dir, acc = []) {
  for (const e of readdirSync(dir)) {
    const p = path.join(dir, e)
    if (statSync(p).isDirectory()) { if (e !== 'node_modules' && e !== '.git') walk(p, acc) }
    else if (e.endsWith('.mjs')) acc.push(p)
  }
  return acc
}

const changed = [], skipped = []
for (const f of walk(SCRIPTS)) {
  if (path.resolve(f) === SELF) continue
  const orig = readFileSync(f, 'utf8')
  if (codeOccurrences(orig.split('\n')) === 0) continue
  if (orig.includes('const SCHEMA =')) continue          // 已迁移（verify-finance-* / diag-finance-workspace）

  const lines = orig.split('\n')
  let replaced = 0
  const notes = []
  const outLines = lines.map((l) => {
    const r = migrateLine(l)
    if (!r) return l
    if (r.skip) { notes.push(r.skip); return l }
    replaced += r.replaced
    return r.text
  })

  const left = codeOccurrences(outLines)
  if (left > 0) { skipped.push({ file: path.relative(ROOT, f), why: `还剩 ${left} 处代码里的写死 schema 未处理` }); continue }
  if (replaced === 0) { skipped.push({ file: path.relative(ROOT, f), why: '没有可安全替换的字面量' }); continue }

  // 插 SCHEMA 声明：锚点必须落在**语句边界**上。
  // ⚠️ 头一版只找「含 POCKET_PSQL 的行」，结果插进了 resolvePsql() 里多行数组
  //    字面量的**中间**（`process.env.POCKET_PSQL,` 那行），把脚本改成语法错误 ——
  //    而当时的 `node --check` 查的是磁盘上的**旧**文件，所以放行了。
  //    两处都得修：锚点收紧 + 语法检查对着新内容。
  const anchor = (l) => /;\s*$/.test(l) || /^\s*(const|let|function)\s/.test(l)
  let idx = -1
  outLines.forEach((l, i) => { if (idx < 0 && anchor(l) && /POCKET_PSQL/.test(l)) idx = i })
  if (idx < 0) outLines.forEach((l, i) => { if (idx < 0 && anchor(l) && /const\s+(psql|q|pg)\w*\s*=/.test(l)) idx = i })
  if (idx < 0) { skipped.push({ file: path.relative(ROOT, f), why: '找不到语句边界上的 POCKET_PSQL / psql 帮助函数锚点' }); continue }
  outLines.splice(idx + 1, 0, ...SCHEMA_DECL.split('\n'))

  const next = outLines.join('\n')
  // 不变量 2：SCHEMA 恰好一次
  if ((next.match(/const SCHEMA = process\.env\.POCKET_PG_SCHEMA/g) || []).length !== 1) {
    skipped.push({ file: path.relative(ROOT, f), why: 'SCHEMA 声明数不为 1' }); continue
  }
  // 不变量 3：语法。必须检查**新内容** —— 查磁盘上的旧文件等于没查。
  const tmp = path.join(path.dirname(f), `.migrate-check-${path.basename(f)}`)
  try {
    writeFileSync(tmp, next, 'utf8')
    execFileSync(process.execPath, ['--check', tmp], { stdio: 'pipe' })
  } catch (e) {
    skipped.push({ file: path.relative(ROOT, f), why: `node --check 失败：${String(e.stderr || e.message).split('\n').filter((l) => /Error/.test(l))[0] || 'unknown'}` })
    continue
  } finally {
    // 临时文件必须写到与目标**不同**的名字，且删在 finally —— 否则验证工具会覆盖被验证对象
    try { unlinkSync(tmp) } catch { /* 删不掉不该中断迁移 */ }
  }

  changed.push({ file: path.relative(ROOT, f), replaced, notes })
  if (!DRY) writeFileSync(f, next, 'utf8')
}

for (const c of changed) {
  console.log(`  迁移 ${String(c.replaced).padStart(2)} 处  ${c.file}`)
  for (const n of c.notes) console.log(`        [注意] ${n}`)
}
for (const s of skipped) console.log(`  跳过  ${s.file}  —— ${s.why}`)
console.log(`\n${changed.length} 个文件已迁移，${skipped.length} 个跳过。${DRY ? '（--dry，未落盘）' : ''}`)
if (LIST) process.exit(0)
process.exitCode = 0
