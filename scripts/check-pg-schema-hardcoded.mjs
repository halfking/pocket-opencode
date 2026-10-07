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
import { readFileSync, readdirSync, statSync, existsSync as existsSyncSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const SELF = path.resolve(fileURLToPath(import.meta.url))
const ROOT = path.resolve(path.dirname(SELF), '..')

// 扫描根：目录 + 扩展名 + 可选的排除谓词。
//
// ★ 2026-10-07 扩到 `backend/cmd`。原因：原范围只有 `scripts/**.mjs`，
//   而 `backend/cmd/gwdbg` 正是本文件头注释描述的那一类探针 ——
//   「直查 PG，验证 POST 是否真的落库」。它从 `POCKET_POSTGRES_DSN` 取 DSN
//   （**可以**指向隔离后端），但 SQL 里写死了 schema 名，
//   ⇒ 一旦指向隔离后端，写入落隔离 schema、查询读共享库
//   ⇒ **静悄悄地给出「没落库」的错结论** —— 就是头注释点名的「更糟」那一档。
//   实测扩之前**全仓没有任何门看得见它**：
//   这道门只扫 `scripts/**.mjs`；Go 侧那道 `pg_test_isolation_guard_test.go`
//   在 `filepath.Walk` 里显式 `if !strings.HasSuffix(path, "_test.go") { return nil }`
//   ⇒ **只管测试文件**。两边各扫一半，夹在中间的 `cmd/` 没人看。
//
// ★ `_test.go` 由那道 Go 守卫管（它带探针白名单与逐条理由，比这里完整），
//   这里**故意不扫**，免得两道门对同一批文件各报一半、谁也说不清全貌。
const SCAN_ROOTS = [
  { dir: path.join(ROOT, 'scripts'), ext: '.mjs' },
  { dir: path.join(ROOT, 'backend', 'cmd'), ext: '.go', skip: (e) => e.endsWith('_test.go') },
]
// 声明的扫描根必须真的存在。少了这一条，路径写错时这道门会「扫了 0 个还报绿」。
for (const r of SCAN_ROOTS) {
  if (!existsSyncSync(r.dir)) {
    console.error(`❌ 声明的扫描根不存在：${r.dir}`)
    process.exit(2)
  }
}

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

function walk(dir, ext, skip, acc = []) {
  for (const e of readdirSync(dir)) {
    const p = path.join(dir, e)
    const st = statSync(p)
    if (st.isDirectory()) { if (e !== 'node_modules' && e !== '.git') walk(p, ext, skip, acc) }
    else if (e.endsWith(ext) && !(skip && skip(e))) acc.push(p)
  }
  return acc
}

function scan() {
  const hits = []
  for (const r of SCAN_ROOTS) {
    for (const f of walk(r.dir, r.ext, r.skip)) {
      if (path.resolve(f) === SELF) continue          // 自指豁免：两边都用 resolve，比字符串更稳
      const lines = readFileSync(f, 'utf8').split(/\r?\n/)
      lines.forEach((l, i) => { if (lineIsHardcoded(l)) hits.push({ file: path.relative(ROOT, f), line: i + 1, text: l.trim() }) })
    }
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
  // ★ 下限闸：`cases` 是一个**字面量数组**，删掉几条与删掉全部一样不会报错。
  //   实测（本轮 2026-10-08）：把数组清空 ⇒ 打印「selftest: 0/0 通过」且 EXIT=0。
  //   **「0/0 通过」不是通过**：那是判据失明时的读数，和真通过长得一模一样。
  //   与 check-exit-reflects-verdict.mjs 的 MIN_SELFTEST_CASES、build-mobile.mjs 的同名常量同一形状。
  //   需要放宽只能手工改这个常量，不接受命令行参数。
  const MIN_SELFTEST_CASES = 6;
  if (cases.length < MIN_SELFTEST_CASES) {
    console.error(`selftest: 只跑了 ${cases.length}/${MIN_SELFTEST_CASES} 例 —— 字面量数组被删过。`);
    console.error('「0/0 通过」不是通过：那是判据失明时的读数。');
    process.exit(2);
  }
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
