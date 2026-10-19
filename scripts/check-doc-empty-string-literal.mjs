#!/usr/bin/env node
/**
 * check-doc-empty-string-literal.mjs —— 门禁：紧邻顶层声明的 doc comment
 * 里不许出现相邻的两个 ASCII 单引号 ''。
 *
 * ## 为什么
 *
 * 2026-10-04 审计实测（详见 docs/handoff/2026-10-04-round41-gofmt-is-the-culprit.md §3）：
 * go/format（即 gofmt）会主动把 Go 源码 doc comment 里相邻的两个 ASCII 单引号 ''
 * 改写成 U+201D ”（右双引号）。这是标准库 go1.27 的行为，**不是**本仓脚本造成的。
 *
 * 直接后果：注释里记录的「SQL 空串字面量 ''」会被静默改写成 ”，而下游读者
 * 把 ” 当成普通中文排版字符略过 —— 注释描述的语义与代码实际执行的**不再一致**。
 *
 * 最小复现（5 行文件，无任何本仓脚本参与）：
 *
 * ```go
 * package p
 *
 * // SQL: importance <> '' or x
 * func F() {}
 * ```
 *
 * `gofmt` 改写后变成 `// SQL: importance <> ” or x`。round40 当时误判
 * 「不是工具是生成」，round41 用 `go/format.Source` 单独跑得到同样结果
 * ⇒ 是标准库行为（见 round41 §1）。
 *
 * ## 边界（明说，不假装完备）
 *
 * 实测 5 行的 7 种位置（脚本内 selftest 同样 7 类样本照搬）：

 *   - 紧邻 package 的 doc comment        → ❌ 被改成 ”
 *   - 紧邻 func    的 doc comment        → ❌ 被改成 ”
 *   - 紧邻 type    的 doc comment        → ❌ 被改成 ”
 *   - 紧邻 var/const 的 doc comment     → ❌ 被改成 ”（同属顶层声明）
 *   - 空行隔开（不算 doc comment）       → ✅ 保留
 *   - 字段 doc（struct 字段注释）        → ✅ 保留
 *   - 函数体内的注释                    → ✅ 保留
 *   - 行尾同行注释                      → ✅ 保留
 *   - SQL 字符串字面量内的 ''           → ✅ 保留（gofmt 不动字符串内容）
 *   - 反引号对内的 ''（紧贴顶层声明）   → ❌ 被改成 ”（gofmt 改写不看反引号边界）
 *
 * ⚠ **本门只扫 `//` 形式的 doc comment**。多行块注释（`/` `*` … `*` `/`）
 * 也是 gofmt 改写的对象（probe6 实测 L1/L2 都被改），但本轮**不覆盖** —— Go 代码里
 * 这种形态几乎只见于包级 doc，round41 的 7 处真损坏也都是 `//`，先抓大头。
 * 若哪天引入多行块注释紧贴顶层声明并触发问题，按本门同样策略补一轮。
 *
 * ## 判据自证（防止本门自己静默失效）
 *
 * --selftest 同时测「敏感度」（必报红）、「特异度」（必不报红）、「变盲」
 * （判据退化成恒真/恒假）、「自指」（门自身不被自己扫到）四组，并设**条数下限**，
 * 防止 cases 数组被改小后「0/0 通过」假绿。
 *
 * ## 负控怎么做 —— 以及一个真会把人带沟里的坑（2026-10-18 实测）
 *
 * 负控必须在**真正的 doc comment** 上注入，判据是在「顶层声明的正上方」。
 * 踩过的坑：`backend/internal/email/store_select_scan_parity_test.go` 顶部
 * L3–L59 那段超长注释**不是 doc comment** —— 它上面是 `package email`、
 * 下面是**空行 + `import (`**，两端都不贴任何顶层声明（Go 里 import 前也不能有
 * doc comment）。在那里注入 `''`，本门**正确地不报**，gofmt 也**正确地不改**
 * （实测 `gofmt <file> | sed -n '4p'` 逐字节未变）—— 即门没坏，注入点选错了。
 * ★ 所以「负控不红」的第一嫌疑是**注入点**，不是判据；真要区分就拿 gofmt
 * 当量具：`gofmt <file> | grep -n '<你注入的那一行文字>'`，改了说明是 doc，
 * 没改说明压根不是 doc comment，门不报是对的。
 * 该文件里真正可用的负控锚点是 `paritySite` 那个 type 的 doc
 * （紧贴 `type paritySite struct {`），在它上面注入即报红并点名到行号。
 *
 * 用法：
 *   node scripts/check-doc-empty-string-literal.mjs              # 门禁模式，有命中 exit 1
 *   node scripts/check-doc-empty-string-literal.mjs --list       # 只列出命中（exit 0）
 *   node scripts/check-doc-empty-string-literal.mjs --selftest   # 敏感度/特异度/变盲自证
 */
import { readFileSync, readdirSync, statSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const SELF = path.resolve(fileURLToPath(import.meta.url))
const ROOT = path.resolve(path.dirname(SELF), '..')

// 只扫 .go：本仓 .sql 用 -- 注释，且 round41 的 7 处真损坏都在 Go 源码。
const EXT = new Set(['.go'])
const ROOTS = ['backend']
const SKIP_DIRS = new Set(['node_modules', '.git', 'vendor', 'dist', 'build'])

// gofmt 改写规则（probe5 实测）：从左到右，贪心地把每对相邻 ' 替换为 ”。
// 三个 ''''  → ”' （最后一根单引号是单数保留）；
// SQL 字面量里的 ''   → ”  ⇒ 任何「包含至少一对相邻 '」的 doc comment 都会被改。
// 用 /''/ 检测「至少一对」即可，不必数全部对的个数。
const EMPTY_PAIR = /''/

// 顶层声明：package / func / type / var / const。method 也是 func 且无缩进，一并命中。
const TOP_DECL = /^(?:func|type|package|var|const)\s/

// godoc 「缩进代码块」是紧贴顶层声明的 doc 里**唯一** gofmt 不动 '' 的位置
//（probe8 实测：``//\t<code>`` 这种行被 godoc 识别为 preformatted block，
// gofmt 跳过）。判据：行去掉前导 `//` 后，**紧跟一个 tab 或 4+ 个空格**——
// 一个空格不算（那是普通注释行碰巧前面带个空格，写法 `// 文字`，按普通行处理）。
// 这正是 round41 §4.2 的「缩进代码块」写法，本门必须放过，否则会
// 把 round41 自己留下的 7 处合法修复全部判红（self-falsification）。
const INDENTED_CODE_LINE = /^\/\/(\t| {4,})/

/**
 * 抽出一段 Go 源码里的「紧邻顶层声明的 doc-comment 块」。
 * 返回 [{ startLine, endLine, lines: string[] [REDACTED:custom]indexed；
 * 0-indexed。
 *
 * 纯函数 —— selftest 直接喂构造样本，不依赖文件系统。
 *
 * 实现要点：
 *   - 紧邻：contiguous `//` 行紧贴顶层声明，中间不能有空白行/代码/字符串；
 *   - 顶层：`^(func|type|package|var|const)\s`（方法以 `func (r *T)` 形式
 *     出现时也是 0 缩进，一并命中）；
 *   - 块边界：遇到首个非 `//` 行即停，含空 `//`（仅 // 的行）也算块内。
 */
export function findTightDocComments(lines) {
  const blocks = []
  for (let i = 0; i < lines.length; i++) {
    if (!TOP_DECL.test(lines[i])) continue
    let j = i - 1
    const block = []
    while (j >= 0) {
      const prev = lines[j]
      // 整行就是 `//` 注释（含纯 //）—— 计入块
      if (prev.trim().startsWith('//')) {
        block.unshift(prev)
        j--
        continue
      }
      // 其它任何内容（代码、空白、字符串、块注释）—— 终止
      break
    }
    if (block.length > 0) {
      blocks.push({ startLine: i - block.length, endLine: i - 1, lines: block })
    }
  }
  return blocks
}

/** 给定一段 doc-comment 块，返回包含 '' 的行（行号相对整个文件，0-indexed）。
 *
 * ⚠ **跳过缩进代码块**（INDENTED_CODE_LINE）：godoc 把 `//\t<code>` 形式的行
 * 识别为 preformatted block，gofmt 不动它们，里面的 '' 也不会被改写。
 * 不跳过就会把 round41 §4.2 自己写下的 7 处合法修复全部判红。
 */
export function emptyStringLiteralHits(block) {
  const out = []
  for (let i = 0; i < block.lines.length; i++) {
    if (INDENTED_CODE_LINE.test(block.lines[i])) continue
    if (EMPTY_PAIR.test(block.lines[i])) {
      out.push({ line: block.startLine + i, text: block.lines[i] })
    }
  }
  return out
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
    } else if (EXT.has(path.extname(e))) {
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
      const blocks = findTightDocComments(lines)
      for (const b of blocks) {
        for (const h of emptyStringLiteralHits(b)) {
          hits.push({
            file: path.relative(ROOT, f).replace(/\\/g, '/'),
            line: h.line + 1,
            blockStart: b.startLine + 1,
            blockEnd: b.endLine + 1,
            text: h.text.trim(),
          })
        }
      }
    }
  }
  return hits
}

if (process.argv.includes('--selftest')) {
  const cases = [
    // ── 敏感度：紧邻顶层声明的 doc comment 含 '' 必须报 ──
    ["敏感·紧邻 func 的 doc 含 ''",
      () => findTightDocComments([
        'package p',
        "// SQL: a <> '' or x",
        'func F() {}',
      ]).length === 1],
    ["敏感·紧邻 type 的 doc 含 ''",
      () => findTightDocComments([
        'package p',
        "// T  (workspace_id='')",
        'type T struct{}',
      ]).length === 1],
    ["敏感·紧邻 package 的 doc 含 ''",
      () => findTightDocComments([
        "// Package p  handles ''.",
        'package p',
      ]).length === 1],
    ["敏感·紧邻 var 的 doc 含 ''",
      () => findTightDocComments([
        'package p',
        "// V1  (init='')",
        'var V1 = 1',
      ]).length === 1],
    ["敏感·紧邻 const 的 doc 含 ''",
      () => findTightDocComments([
        'package p',
        "// C1  (default='')",
        'const C1 = "x"',
      ]).length === 1],
    ["敏感·doc 含 '''（三个，奇数尾巴）",
      () => findTightDocComments([
        'package p',
        "// x = '''",
        'func F() {}',
      ]).length === 1],
    ["敏感·doc 反引号对内含 ''（gofmt 也会改）",
      () => findTightDocComments([
        'package p',
        '// `select * from t where a <> \'\'`',
        'func F() {}',
      ]).length === 1],
    ["敏感·多行 doc 中第二行含 ''",
      () => findTightDocComments([
        'package p',
        '// F does X.',
        "// params  ''=空",
        'func F() {}',
      ]).length === 1],

    // ── 特异度：以下情形必须不报 ──
    ["特异·字段 doc 含 '' 不报",
      () => findTightDocComments([
        'package p',
        'type T struct {',
        "  // X has '' for empty",
        '  X int',
        '}',
      ]).length === 0],
    ["特异·函数体内注释含 '' 不报",
      () => findTightDocComments([
        'package p',
        'func F() {',
        "  // body has ''",
        '  _ = 1',
        '}',
      ]).length === 0],
    ["特异·行尾同行注释含 '' 不报",
      () => findTightDocComments([
        'package p',
        "func F() {} // trailing '' ok",
      ]).length === 0],
    ["特异·空行隔开的不算 doc 不报",
      () => findTightDocComments([
        'package p',
        'func Pre() {}',
        '',
        "// has '' but separated",
        '',
        'func After() {}',
      ]).length === 0],
    ["特异·SQL 字符串字面量含 '' 不报",
      () => findTightDocComments([
        'package p',
        "const q = \"select * from t where a <> ''\"",
        'func F() {}',
      ]).length === 0],
    ["特异·单引号 ' 不报（不是 ''）",
      () => {
        // 输入：紧贴 func 的 doc 里只有单 ' 而无 ''，hits 必为 0
        const lines = [
          'package p',
          "// has 'single'",
          'func F() {}',
        ]
        const blocks = findTightDocComments(lines)
        const total = blocks.reduce((n, b) => n + emptyStringLiteralHits(b).length, 0)
        return total === 0
      }],
    ["特异·package 之前无 doc 不报",
      () => findTightDocComments(['package p']).length === 0],
    ["特异·紧邻 func 但前面是代码行（不是 doc）不报",
      () => findTightDocComments([
        'package p',
        'var _ = 1',
        'func F() {}',
      ]).length === 0],

    // ── 缩进代码块（round41 §4.2 的合法修复形态）：'' 在 //\t<code> 行内必须不报 ──
    ["特异·缩进代码块（tab 缩进）内含 ''（gofmt 不动，跳过）",
      () => {
        const lines = [
          'package p',
          '// Probe:',
          '//',
          "//\t''  in code block",
          "//\t'normal'  in code block",
          '//',
          '// then back to text',
          'func F() {}',
        ]
        const blocks = findTightDocComments(lines)
        const total = blocks.reduce((n, b) => n + emptyStringLiteralHits(b).length, 0)
        return total === 0
      }],
    ["特异·缩进代码块（4 空格缩进）内含 ''（gofmt 也不动）",
      () => {
        const lines = [
          'package p',
          '// Probe:',
          '//',
          "//    ''  in code block",
          '//',
          'func F() {}',
        ]
        const blocks = findTightDocComments(lines)
        const total = blocks.reduce((n, b) => n + emptyStringLiteralHits(b).length, 0)
        return total === 0
      }],
    // 反向：同一个 doc 里 '' 在普通行（不在缩进代码块）必须报
    ["敏感·doc 里同时有缩进代码块和裸 '' ⇒ 裸的报、码块的不报",
      () => {
        const lines = [
          'package p',
          '// Probe:',
          '//',
          "//\t''  in code block",
          '//',
          "// 裸的 '' 在普通行",
          'func F() {}',
        ]
        const blocks = findTightDocComments(lines)
        const total = blocks.reduce((n, b) => n + emptyStringLiteralHits(b).length, 0)
        return total === 1
      }],

    // ── 行号：避免「块对了行号错」型假绿 ──
    ["行号·命中返回 0-indexed 行号与文件其它行一致",
      () => {
        const lines = [
          'package p',         // 0
          '',                  // 1
          "// x <> '' ok",     // 2
          'func F() {}',       // 3
        ]
        const blocks = findTightDocComments(lines)
        if (blocks.length !== 1) return false
        const hits = emptyStringLiteralHits(blocks[0])
        return hits.length === 1 && hits[0].line === 2
      }],

    // ── 变盲：判据不能退化成「恒真」或「恒假」 ──
    ['变盲·空源码不报',
      () => findTightDocComments([]).length === 0],
    ['变盲·只有 var/const 声明无 doc 不报',
      () => findTightDocComments(['package p', 'var x = 1']).length === 0],

    // ── 自指：本门不扫自己（门自身 .mjs 不在 EXTS 里 ⇒ :walk 不会拾起） ──
    ['自指·前提：SELF 的扩展名不在 EXTS 里 ⇒ 自指豁免当前是死代码',
      () => !EXT.has(path.extname(SELF))],
    // 正控：EXTS 非空且 walk 对每个 ROOT 都真的看见文件
    ['自指·正控：EXTS 非空且 walk 对每个 ROOT 都真的看见文件',
      () => EXT.size > 0 && ROOTS.every((r) => {
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
  // 条数下限闸（2026-10-07，docs/design §103.1）。本文件的夹具是**数组字面量**，
  //   不是 add()/push() —— 一条按调用形态做的普查会漏掉它（§103.2 的扫描器就漏了）。
  //   修前实测：把 18 个数组元素全删 ⇒ `selftest: 0/0 通过` + EXIT=0（假绿）。
  //   下限只能手工改这个常量，不接受命令行参数。
  const MIN_SELFTEST_CASES = 18
  if (cases.length < MIN_SELFTEST_CASES) {
    console.error(`[doc-empty-string] 自检只跑了 ${cases.length}/${MIN_SELFTEST_CASES} 例 —— cases 数组被改过。`)
    console.error('   「0/0 通过」不是通过：守卫空转时的读数和它要抓的病一模一样。')
    process.exit(2)
  }
  console.log(`\nselftest: 实跑 ${cases.length - bad} 例 / 声明 ${cases.length} 例，通过`)
  process.exit(bad ? 1 : 0)
}

const hits = scan()
for (const h of hits) {
  console.log(`${h.file}:${h.line}  (doc 块 ${h.blockStart}-${h.blockEnd})  ${h.text}`)
}

if (process.argv.includes('--list')) {
  console.log(`\n${hits.length} 处紧邻 doc 含 ''（gofmt 会把它改写成 U+201D ”）`)
  process.exit(0)
}
if (hits.length) {
  console.error(`\n✗ ${hits.length} 处紧邻顶层声明的 doc comment 含相邻单引号 ''。`)
  console.error(`  go/format 会主动把它改写成 U+201D ”（右双引号），注释描述的 SQL/API 语义将不再与代码一致。\n`)
  console.error(`  哪些位置会被改写（round41 §3.1 实测）：`)
  console.error(`    ❌ 紧邻 func/type/package/var/const 的 doc comment（含反引号对内）`)
  console.error(`    ✅ 空行隔开的注释 / 字段 doc / 函数体内注释 / 行尾注释 / SQL 字符串字面量`)
  console.error(`\n  写法选择（round41 §3.2 实测）：`)
  console.error(`    1. 字面量是这段注释的重点 ⇒ 放进 doc comment 内的缩进代码块（gofmt 稳定），`)
  console.error(`       例：`)
  console.error(`         // 探针：`)
  console.error(`         //    SELECT * FROM t WHERE user_id = '' AND workspace_id = 'default'`)
  console.error(`         //    ⇒ 返回零行`)
  console.error(`       代码块前后各留一条空 // 分隔线（goffmt 要求）。`)
  console.error(`    2. 只是顺带提到 ⇒ 改用中文措辞（「空串」「空字符串」），与该文件既有风格一致。`)
  process.exit(1)
}
console.log("OK：紧邻 doc comment 里没有相邻单引号 ''（gofmt 不会跑去改写）")
