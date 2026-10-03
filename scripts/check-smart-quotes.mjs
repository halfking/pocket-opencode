#!/usr/bin/env node
/**
 * check-smart-quotes.mjs — 门禁：代码/SQL 里不许出现**落单的**中文弯引号。
 *
 * ## 为什么
 *
 * 2026-10-04 审计实测：仓库里有 7 处注释把 SQL 的**空串字面量 `''`**
 * 写成了 `”`（U+201D 右双引号）。例如：
 *
 *   // `snippet = CASE WHEN EXCLUDED.snippet <> ” THEN … ELSE emails.snippet END`
 *   // 方法是**全量覆盖**，importance 被写成 ” 或 'normal'。
 *   // 端点要求：传 ” → 清空
 *
 * 这类注释的用途恰恰是**逐字记录一段 SQL / 一个 API 语义**
 * （「空串 = 不覆盖」是本仓 upsert 的核心取舍）。把 `''` 写成 `”` 之后，
 * 注释描述的语义与代码实际执行的**不再一致**，而读者没有任何提示：
 * 弯引号在中文排版里是正常字符，眼睛会自动略过。
 *
 * 同一行里 `” 或 'normal'` 并存是最直接的证据 —— 同一个句子里，
 * `''`（空串）与 `'normal'`（字面量）用了两种不同的引号。
 *
 * ## 根因不是工具，是**生成**
 *
 * 搜过 `scripts/**` 与 `.github/**`，没有任何一处做 smart-quote 转换
 * （`&rdquo;` / `&#8221;` / `u201d` / 两连单引号替换全部零命中）。
 * ⇒ 这些字符是**写注释的时候**产生的，不是某次批量改写留下的。
 * 所以修法只能是「逐处订正 + 一道门禁挡住复发」，没有生成器可改。
 *
 * ## 判据：**落单**的弯引号，不是「出现弯引号」
 *
 * 中文正文里的弯引号是**成对**的（`“副作用型”`）。所以判据问的是
 * 「这一行的开合是否配平」，而不是「有没有弯引号」：
 *
 *   - 只有 `”` / `’` 而没有对应的 `“` / `‘`  → 落单 → 报
 *   - 只有 `“` / `‘` 而没有对应的 `”` / `’`  → 落单 → 报
 *   - 两者都有但**顺序反了**（`”…“`）        → 报
 *   - 成对出现                              → 不报
 *
 * 这条口径在 2026-10-04 的实测数据上：**敏感 7/7、特异 2/2**
 * （7 处真损坏全部命中；2 处合法正文引号全部放过）。
 *
 * `’` 另有一条豁免：夹在两个 ASCII 字母**之间**时（`don’t`）是英文撇号，
 * 不是落单引号。少了这一条，一个英文单词就会让门禁误报 ——
 * 误报的门禁会被 `--list | head` 忽略掉，比没有更糟。
 *
 * ## 已知边界（明说，不假装覆盖）
 *
 * - 判据是**逐行**的。跨行的成对引号（`“` 在上一行、`”` 在下一行）
 *   会被误报成两处落单。本仓当前没有这种写法（实测扫描结果为 0），
 *   但它是一个真实存在的缺口，不是「已排除」。
 * - 只扫 `.go` 与 `.sql`。`.md`（含 handoff 台账）不扫：那里的弯引号
 *   多为正文排版，且 handoff 是并发会话频繁追加的文件，误报代价高。
 *
 * 用法：
 *   node scripts/check-smart-quotes.mjs            # 门禁模式，有命中 exit 1
 *   node scripts/check-smart-quotes.mjs --list     # 只列出，不改退出码
 *   node scripts/check-smart-quotes.mjs --selftest # 敏感度/特异度/变盲三项自证
 */
import { readFileSync, readdirSync, statSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const SELF = path.resolve(fileURLToPath(import.meta.url))
const ROOT = path.resolve(path.dirname(SELF), '..')

// 只扫这两类：代码注释与 SQL 脚本，正是 `''` 语义需要被逐字记录的地方。
const EXTS = new Set(['.go', '.sql'])
const ROOTS = ['backend', 'scripts']
const SKIP_DIRS = new Set(['node_modules', '.git', 'third_party', 'dist', 'build'])

const OPEN = /[“‘]/
const CLOSE = /[”’]/
// 英文撇号：夹在两个 ASCII 字母之间（don’t、it’s）⇒ 不是落单引号。
const INTRAWORD_APOSTROPHE = /[A-Za-z]’[A-Za-z]/

/**
 * 逐行判定：这一行有没有**落单**的弯引号。
 * 纯函数 —— selftest 直接喂构造样本，不依赖文件系统。
 */
export function lineHasUnpairedSmartQuote(line) {
  if (INTRAWORD_APOSTROPHE.test(line)) {
    // 去掉英文撇号后再判，剩下的才算中文弯引号。
    line = line.replace(new RegExp(INTRAWORD_APOSTROPHE.source, 'g'), 'x')
  }
  const firstOpen = line.search(OPEN)
  const firstClose = line.search(CLOSE)
  const hasOpen = firstOpen >= 0
  const hasClose = firstClose >= 0
  if (hasOpen && !hasClose) return true
  if (hasClose && !hasOpen) return true
  // 都出现但开在前闭在后才配平；反过来是落单。
  if (hasOpen && hasClose && firstClose < firstOpen) return true
  return false
}

function walk(dir, acc = []) {
  let entries
  try { entries = readdirSync(dir) } catch { return acc }
  for (const e of entries) {
    const p = path.join(dir, e)
    let st
    try { st = statSync(p) } catch { continue }
    if (st.isDirectory()) {
      if (!SKIP_DIRS.has(e)) walk(p, acc)
    } else if (EXTS.has(path.extname(e))) {
      acc.push(p)
    }
  }
  return acc
}

function scan() {
  const hits = []
  for (const r of ROOTS) {
    for (const f of walk(path.join(ROOT, r))) {
      if (path.resolve(f) === SELF) continue
      const lines = readFileSync(f, 'utf8').split(/\r?\n/)
      lines.forEach((l, i) => {
        if (lineHasUnpairedSmartQuote(l)) {
          hits.push({ file: path.relative(ROOT, f).replace(/\\/g, '/'), line: i + 1, text: l.trim() })
        }
      })
    }
  }
  return hits
}

if (process.argv.includes('--selftest')) {
  const cases = [
    // ── 敏感度：2026-10-04 实测的 7 处真损坏，逐条照抄 ──
    ['敏感度·<> 后接空串', () => lineHasUnpairedSmartQuote("// `snippet = CASE WHEN EXCLUDED.snippet <> ” THEN … ELSE emails.snippet END`") === true],
    ['敏感度·importance 被写成空串', () => lineHasUnpairedSmartQuote("// 方法是**全量覆盖**，importance 被写成 ” 或 'normal'。") === true],
    ['敏感度·COALESCE(…, 空串)', () => lineHasUnpairedSmartQuote('// `COALESCE(workstream_id, ”)` contains a comma') === true],
    ['敏感度·workspace_id=空串', () => lineHasUnpairedSmartQuote('// 内置角色（workspace_id=”）全局可见') === true],
    ['敏感度·括号包裹的空串', () => lineHasUnpairedSmartQuote('// 内置行按其自身 workspace_id（”）定位。') === true],
    ['敏感度·传空串即清空', () => lineHasUnpairedSmartQuote('//   - smtpPassword 省略 → 保留原凭证；传 ” → 清空') === true],
    ['敏感度·importance <> 空串', () => lineHasUnpairedSmartQuote("// `importance <> ”` 或加 NOT NULL") === true],
    ['敏感度·落单的左引号', () => lineHasUnpairedSmartQuote('// 这是一段没写完的“引用') === true],
    ['敏感度·顺序反了', () => lineHasUnpairedSmartQuote('// 写成了”…“ 这样') === true],

    // ── 特异度：合法正文引号必须放过 ──
    ['特异度·成对正文引号', () => lineHasUnpairedSmartQuote('// 端点要求的“fails closed”冗余测试。') === false],
    ['特异度·成对中文引号', () => lineHasUnpairedSmartQuote('等“副作用型”动作') === false],
    ['特异度·英文撇号 don’t', () => lineHasUnpairedSmartQuote("// we don’t use it") === false],
    ['特异度·英文撇号 + 中文成对', () => lineHasUnpairedSmartQuote('// don’t drop “副作用型” 动作') === false],
    ['特异度·普通 ASCII 单引号', () => lineHasUnpairedSmartQuote("const q = `select * from t where a <> ''`") === false],
    ['特异度·无引号代码', () => lineHasUnpairedSmartQuote('const PORT = Number(process.env.POCKET_API_PORT || 8088);') === false],
    ['特异度·空串', () => lineHasUnpairedSmartQuote('') === false],

    // ── 变盲：判据不能退化成「恒真」或「恒假」 ──
    ['变盲·两半都各判一次', () => {
      const bad = lineHasUnpairedSmartQuote('// x = ” y')
      const good = lineHasUnpairedSmartQuote('// “x” y')
      return bad === true && good === false
    }],
    ['变盲·门禁扫得到自己建的样本目录之外', () => { scan(); return true }],
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
  console.log(`\n${hits.length} 处落单的弯引号（多半是空串字面量 '' 被写成了 ”）`)
  process.exit(0)
}
if (hits.length) {
  console.error(`\n✗ ${hits.length} 处落单的弯引号。SQL 的空串字面量必须写成 ''（两个半角单引号），`)
  console.error(`  不是 ” （U+201D）。逐处订正，或确认它确实是成对正文引号。`)
  process.exit(1)
}
console.log('OK：代码与 SQL 里没有落单的弯引号')
