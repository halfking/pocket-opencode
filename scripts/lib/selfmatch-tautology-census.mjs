// selfmatch-tautology-census.mjs —— 普查「读自己源码做自证」的判据里，
// **哪些是正则纯字面量因而必然匹配自身**的恒真判据。
//
// 只报事实，不下结论，不自动改任何东西。
//
// ── 为什么这类判据值得单列 ────────────────────────────────────────────────
// 本仓的门禁里有一类自证写法：「读自己的源码，grep 某段文本还在不在」，
// 用来证明「规则没被删」。但当 grep 的**模式本身是纯字面量**（不含任何正则元字符）时，
// 那段字面量会**以自身正则字面量的形式出现在同一份源码里**
// ⇒ `src.includes(pattern)` 恒为真 ⇒ 该判据**永远不可能返回 false**。
//
// 实测（2026-10-07，openpocket §127）：`scripts/check-maestro-flows.mjs`
// 的 `unknownTopKeyIsRejected()` 用 `/FAIL 配置顶层出现未知字段/` 去 grep 自己，
// 而那个字符串就写在它下面那行正则里。
// ⇒ 实测把整道守卫的报错与 `process.exit` **全部删掉**（只留一个空 `if`），
//   自检仍报「实跑 14 例，通过 14 例 · 结果: OK」，EXIT=0。
//   这比「没有用例」更坏：它**看起来**是个用例。
//
// ── 判据 ────────────────────────────────────────────────────────────────
// 1. 文件里同时出现「读自己源码」与 `/pattern/.test(src)` 形态；
// 2. 该 pattern 去掉转义反斜杠后**不含任何正则元字符** ⇒ 纯字面量；
// 3. 用「去掉转义反斜杠后的 pattern」去 grep **整份文件** ⇒ 命中即自匹配。
//
// ⚠ 已知的判定边界（不掩盖）：
//   · 本普查只看 `readFileSync(<自身路径>)` + `.test(src)` 这一种形态；
//     换了个变量名、或把源码存进别的容器再匹配，都不在覆盖内。
//   · 「含元字符 ⇒ 不会自匹配」是**不充分**的判据：像 `/AB+C/` 这种
//     仍可能匹配文件里别处的 `ABBB`。所以本普查**只报纯字面量那一类**
//     —— 那一类是**可证的**恒真；含元字符的**可能**恒真，但本工具证明不了，不报。

import { readFileSync, readdirSync, existsSync, statSync } from 'node:fs'
import { dirname, join, relative } from 'node:path'
import { fileURLToPath } from 'node:url'

const here = dirname(fileURLToPath(import.meta.url))
const REPO = join(here, '..', '..')
const SCAN_DIRS = ['scripts', 'frontend/scripts', 'frontend/scripts/lib']

const META = /[\^$.[\]()+?*|{}]/
// `readFileSync(<某个本文件路径>)` 与 `/pattern/.test(<同一变量>)` 的配对
const READ_OWN = /readFileSync\(\s*(?:[^)]*?)(scripts['"]\s*,\s*['"][^'"]*['"]|__filename|fileURLToPath\(import\.meta\.url\))[^)]*\)/
const TEST_SRC = /\/((?:\\.|[^/\\])+)\/\.(?:test|exec)\(\s*(\w+)\s*\)/g

/**
 * 找出「把自己这个文件读进来」的变量名。
 *
 * ⚠ v1 在这里**撒了谎**：它把任何 `readFileSync(join(ROOT,'scripts', X))` 都当成自读，
 * 于是 `const harness = readFileSync('…/scripts/maestro-run.mjs')` 也被算成自读
 * ⇒ 把一条「grep 的是**另一个文件**」的判据误报进来（实测多报 1 处）。
 * ⇒ 现在必须把读到的路径**解析出来、确认等于被扫描的那个文件**才算自读。
 */
function ownSourceVars(text, selfName) {
  const vars = new Set()
  for (const m of text.matchAll(/(?:const|let|var)\s+(\w+)\s*=\s*readFileSync\(([\s\S]{0,300}?)\)\s*(?:\n|$)/g)) {
    const args = m[2]
    // 从参数里抽出所有字符串字面量
    // ⚠ v3 的 bug：把 `'utf8'`（encoding 参数）也当成路径的一部分拼进去，
    // 于是 `…,'check-maestro-flows.mjs'), 'utf8'` 拼出来以 `/utf8` 结尾
    // ⇒ 连**真正**的自读都认不出来 ⇒ 假阴性。判据改成「字面量里出现本文件名」。
    const parts = [...args.matchAll(/'([^']+)'|"([^"]+)"/g)].map((x) => x[1] ?? x[2])
    if (!parts.includes(selfName)) continue
    vars.add(m[1])
  }
  return vars
}

const findings = []
let scanned = 0

for (const d of SCAN_DIRS) {
  const dir = join(REPO, d)
  if (!existsSync(dir)) continue
  for (const name of readdirSync(dir)) {
    const p = join(dir, name)
    if (!statSync(p).isFile() || !name.endsWith('.mjs')) continue
    scanned += 1
    const text = readFileSync(p, 'utf8')
    const selfName = name
    const selfVars = ownSourceVars(text, selfName)
    if (!selfVars.size) continue

    for (const m of text.matchAll(TEST_SRC)) {
      const [, pattern, varName] = m
      if (!selfVars.has(varName)) continue
      const bare = pattern.replace(/\\(.)/g, '$1') // 去掉转义反斜杠
      if (META.test(bare)) continue // 含元字符：可能恒真但本工具证不了，不报
      // 纯字面量：它在文件里必然以自身正则字面量的形式出现
      const selfMatches = text.includes(bare)
      if (!selfMatches) continue
      findings.push({
        file: relative(REPO, p).split('\\').join('/'),
        line: text.slice(0, m.index).split('\n').length,
        pattern: bare,
      })
    }
  }
}

console.log(`【自匹配恒真判据普查】扫了 ${scanned} 个 .mjs`)
if (!findings.length) {
  console.log('✅ 没发现「纯字面量 grep 自己源码」这一类的恒真判据。')
  process.exit(0)
}

const byFile = new Map()
for (const f of findings) {
  if (!byFile.has(f.file)) byFile.set(f.file, [])
  byFile.get(f.file).push(f)
}
console.log(`⚠️ 发现 ${findings.length} 处必然匹配自身的判据，散在 ${byFile.size} 个文件里：\n`)
for (const [file, list] of byFile) {
  console.log(`  ${file}（${list.length} 处）`)
  for (const f of list) console.log(`    行 ${String(f.line).padStart(4)}  /${f.pattern}/`)
}
console.log('\n这类的失败形态：**判据看起来是个用例，但它永远返回真。**')
console.log('删掉被它守护的代码也不会红。修法见设计文档对应章节。')
console.log('（本工具只报**可证**的一类：模式是纯字面量。含元字符的可能恒真但证不了，不报。）')
