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
    // ★ 这条原本是恒真：`() => { scan(); return true }` —— 调一次 scan 就无条件通过，
    //   把 walk 的错误处理整个打坏它照样 PASS（已用变异实测，见设计文档 §149.5）。
    //   真断言问的是「walk 真的走进仓库了吗」，所以直接问 walk 看见了多少**文件**，
    //   而不问 scan() 命中了几**处** —— 后者取决于工作树状态（本机当前树有命中、
    //   HEAD 上是 0 处），拿它当判据会造出一条「换个 checkout 就红」的脆弱用例。
    //   两半都要：正向（每个 root 真的非空）+ 负控（不存在的目录必须返回 0，
    //   否则上面那个 >0 本身就是恒真，与被替换掉的那条没有区别）。
    ['变盲·门禁扫得到自己建的样本目录之外', () => {
      const seen = ROOTS.map((r) => walk(path.join(ROOT, r)).length)
      const allReal = ROOTS.every((r, i) => {
        try {
          return statSync(path.join(ROOT, r)).isDirectory() && seen[i] > 0
        } catch {
          return false
        }
      })
      const negative = walk(path.join(ROOT, '__no_such_dir__')).length === 0
      return allReal && negative
    }],
    // ⚠ 原来这条是**结构上不可能失败**的（docs/design §204.2）：EXTS = {.go,.sql}
    //   而 SELF 是 .mjs ⇒ 这个门自己的文件根本进不了自己的扫描面，:116 的自指豁免
    //   是**死代码**，于是 `hits.every(h => resolve(ROOT,h.file) !== SELF)`
    //   在任何输入下都成立 —— 它报的是一个恒真的绿灯。
    //   照抄「自指」两个字而不查前提，就会把状态②换成另一种形状。
    //
    //   改成断言**前提本身**：这个门**刻意**只扫 Go/SQL 里的智能引号，不扫 .mjs，
    //   所以自指豁免当前是死的。哪天有人把 '.mjs' 加进 EXTS，这条立刻转红 ——
    //   那正是该提醒他的时刻：:116 会从死代码变成活代码，需要另配一条真能测到自指的用例。
    ['自指·前提：SELF 的扩展名不在 EXTS 里 ⇒ :116 自指豁免当前是死代码', () =>
      !EXTS.has(path.extname(SELF))],
    // 正控：上面那条会不会也是恒真？只有 EXTS 非空、且 walk 真看得见文件时，
    //   「SELF 不在 EXTS 里」才是一条**有信息**的前提而不是废话。
    ['自指·正控：EXTS 非空且 walk 对每个 ROOT 都真的看见文件', () =>
      EXTS.size > 0 && ROOTS.every((r) => {
        try { return statSync(path.join(ROOT, r)).isDirectory() && walk(path.join(ROOT, r)).length > 0 } catch { return false }
      })],
  ]
  let bad = 0
  for (const [name, fn] of cases) {
    let pass = false
    try { pass = fn() === true } catch (e) { console.error(`  ${name} 抛错：${e.message}`) }
    if (!pass) bad++
    console.log(`  ${pass ? 'PASS' : 'FAIL'}  ${name}`)
  }
  // ★ 条数下限闸（2026-10-07，docs/design §103.1）。本文件的夹具是**数组字面量**，
  //   不是 add()/push() —— 一条按调用形态做的普查会漏掉它（§103.2 的扫描器就漏了）。
  //   修前实测：把 19 个数组元素全删 ⇒ `selftest: 0/0 通过` + EXIT=0（假绿）。
  //   下限只能手工改这个常量，不接受命令行参数。
  //   ⚠ 2026-10-08 提高 15 -> 20：把恒真的「自指·门禁不扫自己」换成
  //     「前提 + 正控」两条（§204.2）。原来 19 条配下限 15 ⇒ 删掉任意 4 条仍全绿，
  //     而「删掉这两条自指用例」正好落在 15 与 19 之间 ⇒ 下限压根拦不住。
  const MIN_SELFTEST_CASES = 20;
  if (cases.length < MIN_SELFTEST_CASES) {
    console.error(`[smart-quotes] 自检只跑了 ${cases.length}/${MIN_SELFTEST_CASES} 例 —— cases 数组被改过。`);
    console.error('   「0/0 通过」不是通过：守卫空转时的读数和它要抓的病一模一样。');
    process.exit(2);
  }
  console.log(`\nselftest: 实跑 ${cases.length - bad} 例 / 声明 ${cases.length} 例，通过`)
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
