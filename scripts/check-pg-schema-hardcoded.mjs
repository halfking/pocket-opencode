#!/usr/bin/env node
/**
 * check-pg-schema-hardcoded.mjs — 门禁：探针脚本里不许把 PG schema 写死。
 *
 * ## 为什么
 *
 * 一批「直接查库对照」的探针把 `opencode_pocket.` 写进了 SQL。这有两个后果：
 *
 *  1. 它们**只能**对着共享开发库跑 ⇒ 失败时 SEED 会留在**另一会话**的库里。
 *     这正是 BUG-V14（清理写在 happy path）的放大器。
 *  2. 想在隔离后端（`POCKET_PG_SCHEMA=opencode_pocket_verify`）上验证它们时，
 *     断言会去查**另一个** schema，于是要么假失败，要么更糟 —— 静悄悄地
 *     对着错库给出「通过」。
 *
 * 修法是让 schema 跟随后端配置（`backend/internal/config/config.go` 的
 * `POCKET_PG_SCHEMA`，默认值相同），探针读 `process.env.POCKET_PG_SCHEMA`。
 * 这道门禁守住第 1 步。
 *
 * ## 判据纪律
 *
 * 只认**真正限定了表名**的写法 `opencode_pocket.`：
 *   - `FROM opencode_pocket.tasks`            → 报（真问题）
 *   - `FROM ${SCHEMA}.tasks`                  → 不报（正确写法）
 *   - `process.env.POCKET_PG_SCHEMA || 'opencode_pocket'` → 不报（那是默认值，不是限定名）
 *
 * 宁可漏报也不误报：误报的门禁会被 `--list | head` 忽略掉，比没有更糟。
 *
 * 用法：
 *   node scripts/check-pg-schema-hardcoded.mjs            # 门禁模式，有命中 exit 1
 *   node scripts/check-pg-schema-hardcoded.mjs --list     # 只列出，不改退出码
 *   node scripts/check-pg-schema-hardcoded.mjs --selftest # 敏感度/特异度/变盲三项自证
 */
import { readFileSync, readdirSync, statSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const SELF = path.resolve(fileURLToPath(import.meta.url))
const ROOT = path.resolve(path.dirname(SELF), '..')
const SCRIPTS = path.join(ROOT, 'scripts')

// 只认「schema 名 + 点」。`'opencode_pocket'`（无点）是默认值字面量，不算。
const HARDCODED = /\bopencode_pocket\s*\./i

/**
 * 对一行文本判定：是否写死了 schema。
 * 纯函数 —— selftest 直接喂它构造样本，不依赖文件系统。
 *
 * 整行注释（`// …` / ` * …` / `-- …` / `# …`）归为 INFO 不算命中：
 * 注释里的表名只是文档，不会写库。把它算成失败，恰恰是「过宽的规则让判据
 * 失去可信度」——报得太吵的门禁，下一周就会被 `--list | head` 忽略掉。
 */
export function lineIsHardcoded(line) {
  if (/^\s*(\/\/|\*|--|#)/.test(line)) return false
  return HARDCODED.test(line)
}

function walk(dir, acc = []) {
  for (const e of readdirSync(dir)) {
    const p = path.join(dir, e)
    const st = statSync(p)
    if (st.isDirectory()) { if (e !== 'node_modules' && e !== '.git') walk(p, acc) }
    else if (e.endsWith('.mjs')) acc.push(p)
  }
  return acc
}

function scan() {
  const hits = []
  for (const f of walk(SCRIPTS)) {
    if (path.resolve(f) === SELF) continue          // 自指豁免：两边都用 resolve，比字符串更稳
    const lines = readFileSync(f, 'utf8').split(/\r?\n/)
    lines.forEach((l, i) => { if (lineIsHardcoded(l)) hits.push({ file: path.relative(ROOT, f), line: i + 1, text: l.trim() }) })
  }
  return hits
}

if (process.argv.includes('--selftest')) {
  const cases = [
    // 敏感度：该报的必须报
    ['敏感度·真问题  FROM opencode_pocket.tasks', () => lineIsHardcoded('const q = `select * from opencode_pocket.tasks;`') === true],
    ['敏感度·真问题  写在模板串里', () => lineIsHardcoded("psql('select count(*) from opencode_pocket.finance_transactions;')") === true],
    // 特异度：不该报的一律不报
    ['特异度·已改成 ${SCHEMA}', () => lineIsHardcoded('psql(`select * from ${SCHEMA}.finance_transactions;`)') === false],
    ['特异度·默认值字面量（无点）', () => lineIsHardcoded("const SCHEMA = process.env.POCKET_PG_SCHEMA || 'opencode_pocket';") === false],
    ['特异度·无关注释', () => lineIsHardcoded('// 写死 opencode_pocket 会让本脚本只能对着共享库跑') === false],
    ['特异度·整行注释里的表名只算 INFO', () => lineIsHardcoded('// 判据：每步都用 PG 直查 opencode_pocket.scheduled_tasks 兜底') === false],
    ['特异度·星号注释', () => lineIsHardcoded(' * select from opencode_pocket.tasks') === false],
    ['特异度·别的 schema', () => lineIsHardcoded('select * from opencode_pocket_verify.finance_transactions;') === false],
    // 变盲对照：空输入不能被判成有问题
    ['变盲·空串不报', () => lineIsHardcoded('') === false],
    ['变盲·无关文本不报', () => lineIsHardcoded('const PORT = Number(process.env.POCKET_API_PORT || 8088);') === false],
    // 自指豁免必须真的生效
    ['自指·门禁不扫自己', () => { const hits = scan(); return hits.every((h) => path.resolve(ROOT, h.file) !== SELF) }],
  ]
  let bad = 0
  for (const [name, fn] of cases) {
    let pass = false
    try { pass = fn() === true } catch (e) { console.error(`  ${name} 抛错：${e.message}`) }
    if (!pass) bad++
    console.log(`  ${pass ? 'PASS' : 'FAIL'}  ${name}`)
  }
  console.log(`\nselftest: ${cases.length - bad}/${cases.length} 通过`)
  process.exit(bad ? 1 : 0)
}

const hits = scan()
for (const h of hits) console.log(`${h.file}:${h.line}  ${h.text}`)

if (process.argv.includes('--list')) {
  console.log(`\n${hits.length} 处写死的 schema`)
  process.exit(0)
}
if (hits.length) {
  console.error(`\n✗ ${hits.length} 处把 PG schema 写死了。改用 process.env.POCKET_PG_SCHEMA。`)
  process.exit(1)
}
console.log('OK：探针脚本里没有写死的 PG schema')
