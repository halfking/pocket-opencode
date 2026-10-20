#!/usr/bin/env node
/**
 * check-doc-empty-string-literal.mjs —— 门禁：doc comment 里不许出现相邻的两个
 * ASCII 单引号 ''（gofmt 会把它改写成 U+201D ”）。
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
 * ## ⭐⭐⭐ 本门为什么改成「让 gofmt 自己当判据」（2026-10-20 改）
 *
 * round48 之前，本门是**手写规则**去模仿 gofmt 的 doc-comment 行为
 * （哪些位置算 doc、哪些缩进算代码块、块注释算不算……）。
 * ⇒ 2026-10-20 实测：**手写规则与真实 gofmt 有 4 处不一致**（见下表），
 * 也就是说那版判据**既漏报又误报**。
 *
 * 根因（一行）：★ **「gofmt 会怎么改写」这件事，真值只有 gofmt 自己知道**；
 * 手写一份规则去模仿它，就是 §8.2 说的「抄一份实现再比一遍」——
 * Go 的 doc comment 格式化规则**跨版本变过**（Go 1.19 改过块注释的重排），
 * 手写副本会随 Go 升级**静默腐烂**。
 *
 * ⇒ 本门改成：**先用 JS 找出「注释里出现 ''」的候选，再用 gofmt 的真实输出裁决。**
 *
 * | 样本 | 手写规则(旧) | 真实 gofmt | 旧门禁 |
 * |---|---|---|---|
 * | 整块都是 tab 缩进的 `//` doc | 不报 | **改写** | ❌ 漏报 |
 * | 整块都是 4 空格缩进的 `//` doc | 不报 | **改写** | ❌ 漏报 |
 * | 函数体内、列 1 的 `//` 注释 | 不报 | **改写** | ❌ 漏报 |
 * | 多行块注释（斜杠星号形式）doc | 不报 | **改写** | ❌ 漏报（round48 §5.3 登记的缺口） |
 *
 * ★ 后两行正是 round48 §5 登记的「本门不覆盖块注释形式的 doc」与
 * 「`INDENTED_CODE_LINE` 豁免过宽」两笔欠账 —— 本轮一并销账。
 *
 * ## 裁决机制（外部真值，不是自洽往返）
 *
 * 对每个候选文件：
 *
 * 1. `base = gofmt(src)`，数出 base 里的 `”`（U+201D）个数 `C_base`；
 *    `C_src` = 原文里 `”` 的个数。
 *    ★ gofmt 只会**从 `''` 造出 `”`**（go/doc/comment/parse.go 的
 *    `case strings.HasPrefix(t, "''")`），且**从不删已有的 `”`** ⇒
 *    **`C_base > C_src` ⟺ 本文件里有 `''` 被 gofmt 改写了。**
 * 2. 若上一步成立，再**逐个候选**做「把这一处 `''` 换成同长度哨兵 `QQ`，
 *    重新 gofmt，看 `”` 计数有没有变少」——
 *    变少 ⇒ **这一处**正是 gofmt 会改写的那一处。
 * 3. **自洽性核对**：gofmt 每改写一处 `''` 就**恰好**多造一个 `”`
 *    （`”` 的唯一产地就是 `case strings.HasPrefix(t, "''")`）⇒
 *    命中数必须**等于** `C_base - C_src`。不等 ⇒ 逐处归因不可靠 ⇒
 *    该文件判「**无法判定**」（既不报红、也不放过），交人来看，
 *    而不是给一个可能错的结论。
 *
 * ⇒ 判据锚的是 **gofmt 的真实输出**，不是本门自己的往返比较。
 *
 * ## ⭐⭐⭐ oracle 是「哪个 gofmt」必须先定下来（2026-10-20 实测）
 *
 * **gofmt 不同版本对同一段 doc 的答案不同**。Go 1.25 给 go/printer 的
 * doc 判定加了 `tok != token.IDENT` ⇒ 例：
 *
 * ```go
 * type T struct {
 * // X has '' for empty
 * X int
 * }
 * ```
 *
 * Go 1.24：**当 doc comment ⇒ 改写成 `”`**；Go 1.25+：**不当 doc ⇒ 原样保留**。
 *
 * ⇒ 而本仓 CI 是 `setup-go` + `go-version-file: backend/go.mod`（go 1.27.x），
 *   开发机 PATH 上的 gofmt 未必是同一个版本 ⇒ **用错版本会得出与 CI 相反的结论**。
 *
 * ⇒ 所以本门**按 backend/go.mod 声明的大版本过滤 gofmt 候选**：
 *   逐个候选探测版本（GOROOT/VERSION → `go version -m`），取第一个大版本一致的；
 *   一个都没有 ⇒ **「无法判定」exit 1**，不是「通过」。
 *
 * ⚠ 2026-10-20 顺带修掉 `scripts/lib/which-first.mjs` 的一个真 bug：
 *   它的 PATH 分支**恒不成立**（`execFileSync` 返回的是 stdout 字符串，
 *   代码却去读 `r.stdout` / `r.status` ⇒ 两者恒 undefined）
 *   ⇒ 本仓门禁此前实际调用的是 `/usr/local/go/bin/gofmt`（go1.24.5），
 *   与 CI 的 1.27.x **不是同一个工具**。
 *
 * ## 边界（明说，不假装完备）
 *
 * - 本门只判**注释里**的 `''`（Go 感知的扫描器区分注释 / 字符串 / 字符字面量 /
 *   原始字符串）⇒ SQL 字符串字面量里的 `''` 不报（gofmt 也不动它们）。
 * - 缩进代码块（round41 §4.2 的合法修复形态）由 **gofmt 自己**放过：
 *   本门不再自己实现「缩进豁免」规则 ⇒ 不会有「豁免过宽导致漏报」
 *   （旧门禁的 `INDENTED_CODE_LINE` 恰好栽在这里）。
 * - 纯文本的单行块注释、old-style 全星号块注释、混合注释组
 *   （行注释与块注释相邻）：**gofmt 都不改写** ⇒ 本门也不报。
 *   这些不再由本门手写规则判定，而是**逐例由 gofmt 裁决**（见 `--selftest`）。
 * - ⚠ 本门**需要 gofmt**。找不到 gofmt（或没有与声明大版本一致的那个）时
 *   **直接 exit 1 并说明**，绝不「静默通过」——理由同 scripts/check-gofmt.mjs：
 *   **「无法判定」不是「通过」**。
 * - 本门**只覆盖 Go 源码**（`backend/` 下的 `.go`）。`.sql` 用 `--` 注释，
 *   不在 gofmt 的射程内。
 *
 * ## 判据自证（防止本门自己静默失效）
 *
 * --selftest 同时测「敏感度」（必报红）、「特异度」（必不报红）、「行号」、
 * 「变盲」（判据退化成恒真/恒假）、「自指」（门自身不被自己扫到）、
 * 「oracle」（用上的 gofmt 确实是声明工具链那一个）、
 * 以及**一组与真实 gofmt 交叉核对的样本**（Go 的行为若变，本门会当场报出来，
 * 而不是继续用一份过期的期望值自说自话），并设**条数下限**，
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
 *
 * 用法：
 *   node scripts/check-doc-empty-string-literal.mjs              # 门禁模式，有命中 exit 1
 *   node scripts/check-doc-empty-string-literal.mjs --list       # 只列出命中（exit 0）
 *   node scripts/check-doc-empty-string-literal.mjs --selftest   # 敏感度/特异度/变盲/交叉核对自证
 */
import { spawnSync } from 'node:child_process'
import {
  readFileSync, readdirSync, statSync, realpathSync,
  mkdtempSync, mkdirSync, copyFileSync, writeFileSync, rmSync,
} from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { whichFirst, gofmtCandidates } from './lib/which-first.mjs'

const SELF = path.resolve(fileURLToPath(import.meta.url))
const ROOT = path.resolve(path.dirname(SELF), '..')

// 只扫 .go：本仓 .sql 用 -- 注释，且 round41 的 7 处真损坏都在 Go 源码。
const EXT = new Set(['.go'])
const ROOTS = ['backend']
const SKIP_DIRS = new Set(['node_modules', '.git', 'vendor', 'dist', 'build'])

// 探针哨兵：与 "''" **等长**（2 字节）⇒ 替换后字节偏移不变、行结构不变，
// gofmt 的折行决策不受影响 ⇒ 「数 ” 变没变少」这个信号是干净的。
// 选 "QQ" 而不是 "\"\""：后者落在字符串字面量里会提前闭合字符串（实测
// 把 `const q = "a <> '' b"` 改成 `"a <> "" b"` 直接语法错）。
const SENTINEL = 'QQ'

// gofmt 只会从 '' 造出 ”，且从不删已有的 ” ⇒ 用「” 的个数」当信号。
const RIGHT_DQUOTE = '”'
const countRightQuote = (s) => {
  let n = 0
  for (let i = 0; i < s.length; i++) if (s[i] === RIGHT_DQUOTE) n++
  return n
}

// ---------------------------------------------------------------- gofmt 定位

// ───────────────────────────────────────────── 权威 gofmt：按声明工具链锁版本
//
// ⭐⭐⭐ 为什么必须锁版本（2026-10-20 实测，不是推理）
//
// 本门的判据 = 「gofmt 会不会把这处 '' 改写成 ”」。**gofmt 不同版本答案不同**：
//
//   Go 1.24.5 的 go/printer/printer.go：
//     if p.lastTok != token.IMPORT && p.posFor(...).Column == 1 && ...
//   Go 1.27.2 的 go/printer/printer.go：
//     if tok != token.IDENT && p.lastTok != token.IMPORT && p.posFor(...).Column == 1 && ...
//                                    ^^^^^^^^^^^^^^^ Go 1.25 新增
//
// ⇒ 例：struct 里列 1、紧跟字段名的注释
//      // X has '' for empty
//      X int
//    Go 1.24：**当 doc comment ⇒ 改写成 ”**；Go 1.25+：**不当 doc ⇒ 原样保留**。
//
// ⇒ 而本仓 CI 用 `setup-go` + `go-version-file: backend/go.mod`（go 1.27.1）
//   ⇒ **CI 的 gofmt 与开发机 PATH 上的 gofmt 可能不是同一个版本**，
//     用错版本会得出与 CI **相反**的结论（把 CI 认为干净的文件报红）。
//
// ⇒ 所以：判据的 oracle 必须是**本仓声明工具链**的那个 gofmt。
//   解析顺序不变（env → PATH → 常见位置），但**按版本过滤**：
//   逐个候选探测版本，取第一个与 backend/go.mod 声明的大版本一致者；
//   一个都没有 ⇒ 「无法判定」，exit 1（不是「通过」）。

/**
 * 读 backend/go.mod 的 `go` 指令 ⇒ 声明的大版本（如 '1.27'）。
 * 读不到 ⇒ null（调用方按「无法判定」处理，不静默降级）。
 */
export function readDeclaredGoMinor() {
  try {
    const src = readFileSync(path.join(ROOT, 'backend', 'go.mod'), 'utf8')
    const m = src.match(/^go\s+(\d+)\.(\d+)/m)
    return m ? `${m[1]}.${m[2]}` : null
  } catch {
    return null
  }
}

/**
 * 取某个 gofmt 二进制的版本 ⇒ {full:'1.27.2', minor:'1.27'} 或 null。
 * 两条来源：GOROOT 的 VERSION 文件 → `go version -m`。
 */
export function gofmtVersionOf(gofmtPath) {
  const parse = (s) => {
    const m = String(s).match(/go(\d+\.\d+)(?:\.(\d+))?/)
    if (!m) return null
    return { full: m[2] ? `${m[1]}.${m[2]}` : m[1], minor: m[1] }
  }
  try {
    const bin = realpathSync(gofmtPath)
    const goroot = path.resolve(path.dirname(bin), '..')
    const p = parse(readFileSync(path.join(goroot, 'VERSION'), 'utf8'))
    if (p) return p
  } catch { /* 不是标准安装布局 ⇒ 换下一条来源 */ }
  try {
    const r = spawnSync('go', ['version', '-m', gofmtPath], { encoding: 'utf8' })
    if (!r.error && r.status === 0) {
      const p = parse((r.stdout || '').split('\n')[0])
      if (p) return p
    }
  } catch { /* go 不在 PATH 上 */ }
  return null
}

/**
 * 从候选里挑出**与声明大版本一致**的那个 gofmt。
 *
 * ★ 这条是纯函数、候选表是参数 ⇒ 「一个都不匹配 ⇒ 判无法判定」这条**失败分支
 *   可被自证**（否则它只是一段从没跑过的代码，而「无法判定」恰恰是最该被验的分支：
 *   它是「门在量具失效时会不会静默放行」的唯一防线）。
 *
 * @param {string[]} candidates 候选位置（裸名或绝对路径）
 * @param {string|null} declared 声明的大版本，如 '1.27'；null = 读不到声明
 * @returns {{path:string,version:string,declared:string,probed:Array}
 *          |{error:string,declared:string|null,probed:Array}}
 */
export function pickMatchingGofmt(candidates, declared) {
  const probed = []
  const seen = new Set()
  for (const c of candidates) {
    const abs = whichFirst([c])
    if (!abs || seen.has(abs)) continue
    seen.add(abs)
    const version = gofmtVersionOf(abs)
    probed.push({ path: abs, version: version ? version.full : null })
    if (declared && version && version.minor === declared) {
      return { path: abs, version: version.full, declared, probed }
    }
  }
  if (!declared) {
    return {
      error: `读不到 backend/go.mod 的 go 指令 ⇒ 无法确定权威 gofmt 的版本。`,
      declared, probed,
    }
  }
  const lines = probed.map((p) => `    ${p.path}  →  ${p.version || '版本未知'}`).join('\n')
  return {
    error: `找不到与 backend/go.mod 声明的 Go ${declared} 匹配的 gofmt。\n`
      + `  已探测：\n${lines || '    （一个都没找到）'}\n`
      + `  本门的判据锚在**声明工具链**的 gofmt 上（Go 1.25 改过 doc comment 的判定，\n`
      + `  用错版本会得出与 CI 相反的结论）⇒ 这是「无法判定」，不是「通过」。\n`
      + `  修法：装 Go ${declared}.x，或用 GOFMT=/path/to/gofmt 显式指定。`,
    declared, probed,
  }
}

/** 用真实的候选表与真实的声明去解析权威 gofmt。 */
export function resolveAuthoritativeGofmt() {
  return pickMatchingGofmt(gofmtCandidates(), readDeclaredGoMinor())
}

const ORACLE = resolveAuthoritativeGofmt()
if (ORACLE.error) {
  console.error(`✗ ${ORACLE.error}`)
  process.exit(1)
}
const RESOLVED_GOFMT = ORACLE.path

/** 调 gofmt。返回 {out} 或 {error}。
 *  走 stdin（`gofmt` 无参时读 stdin）⇒ 不用在磁盘上造临时文件。
 *  ⚠ 找不到 gofmt 时返回 error，**由调用方决定**——绝不悄悄当成「通过」。 */
export function gofmtRun(src) {
  if (!RESOLVED_GOFMT) {
    return { error: `找不到 gofmt（PATH 上没有，常见安装位置也没命中）。装 Go 即可，或用 GOFMT=/path/to/gofmt 显式指定。` }
  }
  const r = spawnSync(RESOLVED_GOFMT, [], { input: src, encoding: 'utf8', maxBuffer: 64 * 1024 * 1024 })
  if (r.error) return { error: `调不动 gofmt（${RESOLVED_GOFMT}）：${r.error.message}` }
  if (r.status !== 0) {
    const first = String(r.stderr || '').trim().split('\n')[0]
    return { error: `gofmt 退出码 ${r.status}：${first}` }
  }
  return { out: r.stdout }
}

// ------------------------------------------------------- Go 感知的注释扫描器

/**
 * 找出 src 里所有**注释**的字节区间 [start, end)。
 *
 * 必须区分注释 / 字符串 / 字符字面量 / 原始字符串，否则会把
 * `const q = "a <> '' b"`（gofmt 不动）当成注释里的 ''（gofmt 会动）。
 *
 * Go 的词法（go/scanner）：
 *   - `//` 行注释：到行尾；
 *   - 斜杠星号形式的块注释：**不嵌套**（Go 规范：general comment 结束于第一个后续的结束标记），
 *     且**可以跨行**；
 *   - `"..."` 解释字符串：`\` 转义下一个字符；
 *   - `` `...` `` 原始字符串：无转义，可跨行；
 *   - `'...'` 字符字面量：`\` 转义下一个字符。
 */
export function scanCommentRanges(src) {
  const ranges = []
  const n = src.length
  let i = 0
  while (i < n) {
    const c = src[i]
    if (c === '/' && src[i + 1] === '/') {
      const start = i
      while (i < n && src[i] !== '\n') i++
      ranges.push([start, i])
      continue
    }
    if (c === '/' && src[i + 1] === '*') {
      const start = i
      i += 2
      while (i < n && !(src[i] === '*' && src[i + 1] === '/')) i++
      i = Math.min(n, i + 2)
      ranges.push([start, i])
      continue
    }
    if (c === '"') {
      i++
      while (i < n) {
        if (src[i] === '\\') { i += 2; continue }
        if (src[i] === '"') { i++; break }
        if (src[i] === '\n') break
        i++
      }
      continue
    }
    if (c === '`') {
      i++
      while (i < n && src[i] !== '`') i++
      i = Math.min(n, i + 1)
      continue
    }
    if (c === "'") {
      i++
      while (i < n) {
        if (src[i] === '\\') { i += 2; continue }
        if (src[i] === "'") { i++; break }
        if (src[i] === '\n') break
        i++
      }
      continue
    }
    i++
  }
  return ranges
}

/**
 * 找出 src 里所有**位于注释内**的 "''" 的字节偏移。
 *
 * 扫描用**不重叠**、从左到右的配对（与 go/doc/comment/parse.go 的
 * `case strings.HasPrefix(t, "''")` 的贪心消费一致）：
 * `''''` 被切成 2 对、`'''` 被切成 1 对。
 */
export function findQuotePairOffsets(src) {
  const ranges = scanCommentRanges(src)
  const out = []
  const re = /''/g
  let m
  while ((m = re.exec(src))) {
    if (ranges.some(([a, b]) => m.index >= a && m.index < b)) out.push(m.index)
  }
  return out
}

/** 字节偏移 → 1-based 行号。 */
export function lineOfOffset(src, offset) {
  let line = 1
  for (let i = 0; i < offset && i < src.length; i++) if (src[i] === '\n') line++
  return line
}

// ------------------------------------------------------------------ 裁决

/**
 * 裁决一个源文件里哪些「注释中的 ''」会被 gofmt 改写。
 *
 * 返回 { hits: [{offset, line}], error?: string }
 *
 * 机制见文件头「裁决机制」一节：
 *   1) C_base > C_src ⇒ 本文件存在被改写的 ''；
 *   2) 逐候选换哨兵重跑，」 计数变少 ⇒ 就是这一处。
 */
export function judgeSource(src) {
  const offsets = findQuotePairOffsets(src)
  if (offsets.length === 0) return { hits: [] }

  const base = gofmtRun(src)
  if (base.error) return { hits: [], error: base.error }

  const cBase = countRightQuote(base.out)
  const cSrc = countRightQuote(src)
  // gofmt 只增不减 ” ⇒ 若没变多，本文件没有任何 '' 被改写。
  if (cBase <= cSrc) return { hits: [] }

  const hits = []
  for (const off of offsets) {
    const probe = src.slice(0, off) + SENTINEL + src.slice(off + 2)
    const r = gofmtRun(probe)
    if (r.error) return { hits: [], error: r.error }
    if (countRightQuote(r.out) < cBase) hits.push({ offset: off, line: lineOfOffset(src, off) })
  }

  // ★ 自洽性核对：gofmt 每改写一处 '' 就**恰好**多造一个 ”（go/doc/comment/parse.go
  //   的 `case strings.HasPrefix(t, "''")` 是 ” 的唯一产地），且 ” 只可能来自 ''。
  //   ⇒ 命中数必须**等于**「” 的增量」。不等 ⇒ 归因不可靠 ⇒ 本文件「无法判定」
  //   （既不报红、也不放过），交人来看，而不是给一个可能错的结论。
  const delta = cBase - cSrc
  if (hits.length !== delta) {
    return {
      hits: [],
      error: `命中数(${hits.length}) 与 ” 的增量(${delta}) 不一致 ⇒ 逐处归因不可靠，无法判定`,
    }
  }
  return { hits }
}

// ------------------------------------------------------------------ 扫仓库

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
  const errors = []
  for (const r of ROOTS) {
    for (const f of walk(path.join(ROOT, r))) {
      if (path.resolve(f) === SELF) continue
      const src = readFileSync(f, 'utf8')
      // 快速路径：文件里根本没有 '' 就跳过 ⇒ 绝大多数文件不会调 gofmt。
      if (!src.includes("''")) continue
      const res = judgeSource(src)
      if (res.error) { errors.push({ file: path.relative(ROOT, f), error: res.error }); continue }
      for (const h of res.hits) {
        hits.push({
          file: path.relative(ROOT, f).replace(/\\/g, '/'),
          line: h.line,
          text: (src.split('\n')[h.line - 1] || '').trim(),
        })
      }
    }
  }
  return { hits, errors }
}

// ------------------------------------------------------------------ 自证

if (process.argv.includes('--selftest')) {
  const cases = []
  const add = (name, fn) => cases.push([name, fn])

  // ── 注释扫描器：必须区分注释与字符串 ──
  add('扫描·行注释内的 \'\' 找得到', () => findQuotePairOffsets("package p\n// a '' b\n").length === 1)
  add('扫描·块注释内的 \'\' 找得到', () => findQuotePairOffsets("package p\n/* a '' b */\n").length === 1)
  add('扫描·字符串字面量里的 \'\' 找不到', () => findQuotePairOffsets("package p\nconst q = \"a '' b\"\n").length === 0)
  add('扫描·原始字符串里的 \'\' 找不到', () => findQuotePairOffsets("package p\nconst q = `a '' b`\n").length === 0)
  add('扫描·字符字面量里的 \'\' 找不到', () => findQuotePairOffsets("package p\nvar c = '\\''\n").length === 0)
  add('扫描·行注释里的 // 不被当成字符串', () => findQuotePairOffsets("package p\n// a '' b // c\n").length === 1)
  add('扫描·块注释跨行', () => findQuotePairOffsets("package p\n/*\na '' b\n*/\n").length === 1)
  add('扫描·空源码无命中', () => findQuotePairOffsets('').length === 0)
  add('扫描·行号换算正确', () => {
    const src = "package p\n\n// a '' b\n"
    const offs = findQuotePairOffsets(src)
    return offs.length === 1 && lineOfOffset(src, offs[0]) === 3
  })

  // ── 敏感度：gofmt 真的会改写的，必须报红 ──
  add('敏感·紧邻 func 的 doc 含 \'\' 报红', () => judgeSource("package p\n// a <> '' b\nfunc F() {}\n").hits.length === 1)
  add('敏感·紧邻 type 的 doc 含 \'\' 报红', () => judgeSource("package p\n// T (id='')\ntype T struct{}\n").hits.length === 1)
  add('敏感·紧邻 var 的 doc 含 \'\' 报红', () => judgeSource("package p\n// V (init='')\nvar V = 1\n").hits.length === 1)
  add('敏感·紧邻 const 的 doc 含 \'\' 报红', () => judgeSource("package p\n// C (default='')\nconst C = \"x\"\n").hits.length === 1)
  add('敏感·package 的 doc 含 \'\' 报红', () => judgeSource("// Package p handles ''.\npackage p\n").hits.length === 1)
  add('敏感·多行 /* */ 块 doc 含 \'\' 报红（round48 §5.3 缺口）', () => judgeSource("package p\n/*\na <> '' b\n*/\nfunc F() {}\n").hits.length === 1)
  add('敏感·包级 /* */ 块 doc 含 \'\' 报红', () => judgeSource("/*\nPackage p a <> '' b\n*/\npackage p\n").hits.length === 1)
  add('敏感·整块 tab 缩进的 // doc 报红（旧门禁漏报）', () => judgeSource("package p\n//\t'' code\n//\t'' code2\nfunc F() {}\n").hits.length >= 1)
  add('敏感·整块 4 空格缩进的 // doc 报红（旧门禁漏报）', () => judgeSource("package p\n//    '' code\n//    '' code2\nfunc F() {}\n").hits.length >= 1)
  add('敏感·函数体内列 1 的 // 注释报红（旧门禁漏报）', () => judgeSource("package p\nfunc F() {\n// a <> '' b\n}\n").hits.length === 1)
  add('敏感·doc 里同时有缩进码块和裸 \'\' ⇒ 裸的报', () => {
    const src = "package p\n// Probe:\n//\n//\t''  in code block\n//\n// 裸的 '' 在普通行\nfunc F() {}\n"
    const r = judgeSource(src)
    // ⚠ hits 元素只有 {offset, line}，**没有 text 字段**（text 只在 scan() 里拼）。
    //   要拿那一行的文字得回原文按行号取 —— 别在 hits 上找 text。
    const lineText = (h) => (src.split('\n')[h.line - 1] || '')
    return r.hits.length === 1 && lineText(r.hits[0]).includes('裸的')
  })
  add('敏感·doc 含 \'\'\'（三个，奇数尾巴）报红', () => judgeSource("package p\n// x = '''\nfunc F() {}\n").hits.length === 1)
  add('敏感·doc 含 \'\'\'\'（四个）报红', () => {
    // 真实 gofmt 把 '''' 改写成**两个** ”（实测输出 "// x = ””"）⇒ 命中 2 处，不是 1 处。
    // 这里写死 2 而不是「≥1」：写死才能抓住「漏掉第 2 处」的回归。
    return judgeSource("package p\n// x = ''''\nfunc F() {}\n").hits.length === 2
  })
  add('敏感·doc 反引号对内含 \'\' 报红（gofmt 也会改）', () => judgeSource("package p\n// x `a <> '' b` y\nfunc F() {}\n").hits.length === 1)

  // ── 特异度：gofmt 不会改写的，必须不报 ──
  add('特异·字段 doc 含 \'\' 不报', () => {
    // ⚠ 这条的期望**依赖 Go 版本**：Go 1.25 给 go/printer 的 doc 判定加了
    //   `tok != token.IDENT` ⇒ struct 里紧跟字段名（IDENT）的列 1 注释**不再**是 doc。
    //   Go 1.24.5 实测会改写（报 1 处），Go 1.27.2 实测不改写（0 处）。
    //   ⇒ 这条之所以能写死，靠的是**本门把 oracle 锁到 backend/go.mod 声明的 1.27.x**
    //     （否则同一条门在两台机器上给相反结论）。
    return judgeSource("package p\ntype T struct {\n// X has '' for empty\nX int\n}\n").hits.length === 0
  })
  add('特异·缩进的函数体内注释含 \'\' 不报', () => judgeSource("package p\nfunc F() {\n\t// body has ''\n}\n").hits.length === 0)
  add('特异·行尾同行注释含 \'\' 不报', () => judgeSource("package p\nfunc F() {} // trailing '' ok\n").hits.length === 0)
  add('特异·空行隔开的不算 doc 不报', () => judgeSource("package p\nfunc Pre() {}\n\n// has '' but separated\n\nfunc After() {}\n").hits.length === 0)
  add('特异·SQL 字符串字面量含 \'\' 不报', () => judgeSource("package p\nconst q = \"select * from t where a <> ''\"\nfunc F() {}\n").hits.length === 0)
  add('特异·原始字符串含 \'\' 不报', () => judgeSource("package p\nconst q = `a <> ''`\nfunc F() {}\n").hits.length === 0)
  add('特异·缩进代码块（tab）内的 \'\' 不报（round41 §4.2 合法形态）', () => judgeSource("package p\n// prose\n//\t''  in code block\nfunc F() {}\n").hits.length === 0)
  add('特异·缩进代码块（4 空格）内的 \'\' 不报', () => judgeSource("package p\n// prose\n//    ''  in code block\nfunc F() {}\n").hits.length === 0)
  add('特异·单行 /* */ 块注释含 \'\' 不报（gofmt 不改单行块注释）', () => judgeSource("package p\n/* a <> '' b */\nfunc F() {}\n").hits.length === 0)
  add('特异·old-style 全星号块注释含 \'\' 不报（gofmt 不改它）', () => judgeSource("package p\n/*\n * a <> '' b\n */\nfunc F() {}\n").hits.length === 0)
  add('特异·混合注释组（// 紧邻 /* */）不报（gofmt 整组都不改）', () => judgeSource("package p\n// a <> '' b\n/* c */\nfunc F() {}\n").hits.length === 0)
  add('特异·混合注释组（/* */ 紧邻 //）不报', () => judgeSource("package p\n/* c */\n// a <> '' b\nfunc F() {}\n").hits.length === 0)
  add('特异·块 doc 后面隔空行不报', () => judgeSource("package p\n/*\na <> '' b\n*/\n\nfunc F() {}\n").hits.length === 0)
  add('特异·缩进的块注释（列 2）不报', () => judgeSource("package p\n\t/*\n\ta <> '' b\n\t*/\nfunc F() {}\n").hits.length === 0)
  add('特异·单引号 \' 不报（不是 \'\'）', () => judgeSource("package p\n// has 'single'\nfunc F() {}\n").hits.length === 0)

  // ── 变盲：判据不能退化成「恒真」或「恒假」 ──
  add('变盲·空源码不报', () => judgeSource('').hits.length === 0)
  add('变盲·只有 var/const 声明无 doc 不报', () => judgeSource('package p\nvar x = 1\n').hits.length === 0)
  add('变盲·同一文件两处 \'\' 只报该报的那两处', () => {
    const r = judgeSource("package p\n// a <> '' b\nfunc F() {}\n// c <> '' d\nfunc G() {}\n")
    return r.hits.length === 2 && r.hits[0].line === 2 && r.hits[1].line === 4
  })

  // ── 自指：本门不扫自己（门自身是 .mjs，不在 EXTS 里 ⇒ :walk 不会拾起） ──
  add('自指·前提：SELF 的扩展名不在 EXTS 里 ⇒ 自指豁免当前是死代码', () => !EXT.has(path.extname(SELF)))
  add('自指·正控：EXTS 非空且 walk 对每个 ROOT 都真的看见文件', () => EXT.size > 0 && ROOTS.every((r) => {
    try { return statSync(path.join(ROOT, r)).isDirectory() && walk(path.join(ROOT, r)).length > 0 } catch { return false }
  }))

  // ── oracle：判据锚的那个 gofmt 必须是**声明工具链**的那一个 ──
  add('oracle·backend/go.mod 的 go 指令读得到（不是「读失败所以放行」）', () => {
    const d = readDeclaredGoMinor()
    return d !== null && /^\d+\.\d+$/.test(d)
  })
  add('oracle·已解析到的 gofmt 版本与 backend/go.mod 声明的大版本一致', () => {
    // 这条是「同一份代码在两台机器上给相反结论」的防线：
    // Go 1.25 改过 doc comment 判定（go/printer 新增 `tok != token.IDENT`）
    // ⇒ oracle 版本不对，期望值就全废了。
    const declared = readDeclaredGoMinor()
    const v = gofmtVersionOf(RESOLVED_GOFMT)
    return !!declared && !!v && v.minor === declared
  })
  // ★ 下面几条专打**「无法判定」那条失败分支**。
  //   它是「量具失效时门会不会静默放行」的唯一防线 ⇒ 从前只是一段从没跑过的代码。
  add('oracle·版本不符的候选必须被拒绝（真二进制 + 伪造 VERSION）', () => {
    // 造一个**能用的** gofmt（二进制是真的），但让它的 VERSION 说自己是 go1.99.0
    // ⇒ 声明要 1.27 时必须**拒绝它**，不许「反正它能跑就拿来用」。
    const tmp = mkdtempSync(path.join(os.tmpdir(), 'doc-empty-gofmt-'))
    try {
      const bin = path.join(tmp, 'bin')
      mkdirSync(bin, { recursive: true })
      const fake = path.join(bin, 'gofmt')
      copyFileSync(RESOLVED_GOFMT, fake)
      writeFileSync(path.join(tmp, 'VERSION'), 'go1.99.0\n')
      const v = gofmtVersionOf(fake)
      if (!v || v.minor !== '1.99') return false // 量具自证：版本确实被读成 1.99
      const r = pickMatchingGofmt([fake], '1.27')
      return !!r.error && !r.path // ⇒ 必须是「无法判定」，不是「选中」
    } finally {
      rmSync(tmp, { recursive: true, force: true })
    }
  })
  add('oracle·候选表为空 ⇒ 判无法判定（不许静默放行）', () => {
    const r = pickMatchingGofmt([], '1.27')
    return !!r.error && !r.path
  })
  add('oracle·候选指向不存在的文件 ⇒ 判无法判定', () => {
    const r = pickMatchingGofmt(['/definitely/not/here/gofmt'], '1.27')
    return !!r.error && !r.path
  })
  add('oracle·声明读不到（null）⇒ 判无法判定（不许拿任意候选凑数）', () => {
    const r = pickMatchingGofmt([RESOLVED_GOFMT], null)
    return !!r.error && !r.path
  })
  add('oracle·与声明一致的候选必被选中', () => {
    const minor = gofmtVersionOf(RESOLVED_GOFMT).minor
    const r = pickMatchingGofmt([RESOLVED_GOFMT], minor)
    return !r.error && r.path === RESOLVED_GOFMT
  })

  // ── ⭐ 交叉核对：同一批样本，**本门的裁决**必须与**真实 gofmt 的输出**一致。
  //    这条的意义：若哪天 Go 改了 doc comment 的格式化行为，
  //    上面那些「手写期望值」会变成过期期望 —— 而这条会**当场报出来**，
  //    提醒人来更新，而不是让本门继续拿一份过期清单自说自话。
  add('交叉核对·本门裁决与真实 gofmt 输出逐例一致', () => {
    const samples = [
      "package p\n// a <> '' b\nfunc F() {}\n",
      "package p\n//\t'' code\n//\t'' code2\nfunc F() {}\n",
      "package p\n//    '' code\n//    '' code2\nfunc F() {}\n",
      "package p\nfunc F() {\n// a <> '' b\n}\n",
      "package p\n/*\na <> '' b\n*/\nfunc F() {}\n",
      "package p\n/* a <> '' b */\nfunc F() {}\n",
      "package p\n/*\n * a <> '' b\n */\nfunc F() {}\n",
      "package p\n// prose\n//\t'' code\nfunc F() {}\n",
      "package p\n// a <> '' b\n/* c */\nfunc F() {}\n",
      "package p\nconst q = \"a <> '' b\"\nfunc F() {}\n",
      "package p\nconst q = `a <> '' b`\nfunc F() {}\n",
      "package p\ntype T struct {\n// X has '' for empty\nX int\n}\n",
      "package p\nfunc F() {} // trailing '' ok\n",
      "package p\n// a <> '' b\n\nfunc F() {}\n",
      "package p\n// x = '''\nfunc F() {}\n",
      "package p\n// x = ''''\nfunc F() {}\n",
      "// Package p handles ''.\npackage p\n",
      "/*\nPackage p a <> '' b\n*/\npackage p\n",
    ]
    for (const s of samples) {
      const direct = gofmtRun(s)
      if (direct.error) return false // gofmt 不可用 ⇒ 整条判据「无法判定」，不许假装通过
      const gofmtRewrites = countRightQuote(direct.out) > countRightQuote(s)
      const gateSays = judgeSource(s).hits.length > 0
      if (gofmtRewrites !== gateSays) return false
    }
    return true
  })

  let bad = 0
  for (const [name, fn] of cases) {
    let pass = false
    let err = ''
    try { pass = fn() === true } catch (e) { err = `（${e.message}）` }
    if (!pass) bad++
    console.log(`  ${pass ? 'PASS' : 'FAIL'}  ${name}${err}`)
  }
  // 条数下限闸（2026-10-07，docs/design §103.1）。本文件的夹具是**数组字面量**，
  //   不是 add()/push() —— 一条按调用形态做的普查会漏掉它（§103.2 的扫描器就漏了）。
  //   修前实测：把 18 个数组元素全删 ⇒ `selftest: 0/0 通过` + EXIT=0（假绿）。
  //   下限只能手工改这个常量，不接受命令行参数。
  // ★ 2026-10-20：下限**按当前实跑条数棘轮**到 51（46 → +5 条 oracle 失败分支）。
  //   语义是「只许增不许减」：删掉任何一条都必须**手改这个常量**才能过 ⇒
  //   「悄悄少测一条」从此必须是一个显式的决定，而不是一个没人注意到的 diff。
  const MIN_SELFTEST_CASES = 51
  if (cases.length < MIN_SELFTEST_CASES) {
    console.error(`[doc-empty-string] 自检只跑了 ${cases.length}/${MIN_SELFTEST_CASES} 例 —— cases 数组被改过。`)
    console.error('   「0/0 通过」不是通过：守卫空转时的读数和它要抓的病一模一样。')
    process.exit(2)
  }
  console.log(`\nselftest: 实跑 ${cases.length - bad} 例 / 声明 ${cases.length} 例，通过`)
  process.exit(bad ? 1 : 0)
}

// ------------------------------------------------------------------ 门禁主流程

const { hits, errors } = scan()

console.log(`oracle: ${RESOLVED_GOFMT}（go${ORACLE.version}，backend/go.mod 声明 Go ${ORACLE.declared}）`)

for (const e of errors) {
  console.error(`✗ ${e.file}：无法判定 —— ${e.error}`)
}
if (errors.length) {
  console.error('\n「无法判定」不是「通过」：本门需要 gofmt 才能裁决，先把上面的问题解决掉。')
  process.exit(1)
}

// 同一个 file:line 只报一次：'''' 这种会被拆成 2 处候选，但人只需要改那一行。
const locs = []
const seenLoc = new Set()
for (const h of hits) {
  const k = `${h.file}:${h.line}`
  if (seenLoc.has(k)) continue
  seenLoc.add(k)
  locs.push(h)
}

for (const h of locs) {
  console.log(`${h.file}:${h.line}  ${h.text}`)
}

if (process.argv.includes('--list')) {
  console.log(`\n${locs.length} 处注释里的 '' 会被 gofmt 改写成 U+201D ”`)
  process.exit(0)
}

if (locs.length) {
  console.error(`\n✗ ${locs.length} 处注释里的相邻单引号 '' 会被 gofmt 改写成 U+201D ”（右双引号）。`)
  console.error(`  go/format 会主动改写它，注释描述的 SQL/API 语义将不再与代码一致。\n`)
  console.error(`  写法选择（round41 §3.2 实测）：`)
  console.error(`    1. 字面量是这段注释的重点 ⇒ 放进 doc comment 内的缩进代码块（gofmt 稳定），`)
  console.error(`       例：`)
  console.error(`         // 探针：`)
  console.error(`         //    SELECT * FROM T WHERE user_id = '' AND workspace_id = 'default'`)
  console.error(`         //    ⇒ 返回零行`)
  console.error(`       代码块前后各留一条空 // 分隔线（gofmt 要求）。`)
  console.error(`    2. 只是顺带提到 ⇒ 改用中文措辞（「空串」「空字符串」），与该文件既有风格一致。`)
  process.exit(1)
}
console.log("OK：注释里没有相邻单引号 ''（gofmt 不会跑去改写）")
