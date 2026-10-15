// selfanchor-ambiguity-census.mjs —— 普查「读自己源码做自证」的判据里，
// **锚点到底靠不靠得住**。
//
// 只报事实，不下结论，不自动改任何东西。
//
// ── 它比 selfmatch-tautology-census 多管一层 ─────────────────────────────
// 已有普查（§127）只抓**一种**：模式是纯字面量 ⇒ 必然匹配自身 ⇒ 恒真。
// 但实测（2026-10-07，openpocket §127.8）在同一个文件里又挖出**两层**它管不到的：
//
//   层 2「锚点太短」：判据找 `死子流` 三个字，而那三个字在**注释**与
//     **本用例自己的名字**里都出现 ⇒ 实测删掉真守卫那一行，判据照样通过。
//     ⇒ 模式本身没有自匹配问题（它确实不含自己的正则字面量），
//       问题是**别人也能满足它**。
//   层 3「锚点只在插值后存在」：判据找 `声明的 fixture`，
//     而源码里真实写的是模板 `声明的 ${key}` ⇒ 静态源码里永远找不到那串字面量。
//     ⇒ 它之所以一直「通过」，仍然是因为匹配到了自己（层 1），
//       但**只按层 1 修**（改成长句锚点）会直接把它从「恒真」变成「恒假」。
//
// ── 判据（可判定，不含猜测）────────────────────────────────────────────────
// 对每一处自读判据，先把它的**有效模式** P 求出来（相邻字符串拼接要先拼起来），
// 再数 P 在**判据自身定义处之外**能命中本文件的多少行：
//
//   命中 1 行  → ANCHOR_UNIQUE       锚点唯一，真守卫写在别处 ⇒ 健康
//   命中 0 行  → 看原始模式文本是否出现在定义处内：
//                 在  → ANCHOR_TAUTOLOGY 恒真（层 1）
//                 不在 → ANCHOR_NO_STATIC 静态无落点（层 3，通常是模板）
//   命中 ≥2 行 → ANCHOR_AMBIGUOUS    有顶替者（层 2）
//
// ⚠ 已知边界（不掩盖）：
//   · 只看「读自己这个文件」+ `/re/.test(src)` / `new RegExp(..).test(src)` /
//     `src.includes(..)` 三种形态；换变量名、间接存进别的容器不在覆盖内。
//   · 命中行数是**上界**：同两行文本可能重复出现，本工具不去重，
//     所以 ANCHOR_AMBIGUOUS 可能偏保守（报多）。
//   · ANCHOR_UNIQUE 只说明「锚点只有一个落点」，**不说明**那个落点就是
//     真正被守护的代码 —— 这一点仍然只能靠变异回答，本工具不越权。

import { readFileSync, readdirSync, existsSync, statSync } from 'node:fs'
import { dirname, join, relative } from 'node:path'
import { fileURLToPath } from 'node:url'

const here = dirname(fileURLToPath(import.meta.url))
const REPO = join(here, '..', '..')
// 扫描根可以覆盖（**仅用于正控/负控**：把样本放 /tmp，不必往共享工作树里塞临时文件）。
// 与 §126 记的 SRC_ROOT 是同一回事：普查器的扫描根是参数，不是缺陷。
const DEFAULT_SCAN_DIRS = ['scripts', 'frontend/scripts', 'frontend/scripts/lib']
// ⚠ v3 在这里栽过一次（**最坏的一种**）：SCAN_DIRS 已经是绝对路径，
//   下面的循环却又 `join(REPO, d)` ⇒ `/tmp/x` 变成 `<repo>/tmp/x`
//   ⇒ existsSync 假 ⇒ continue ⇒ 扫了 0 个文件，**照样打印「✅ 没有」**。
//   即：量具失明时报的是绿。⇒ 扫描根在这里一次性定死，循环里不再拼。
const SCAN_DIRS = process.argv[2]
  ? [process.argv[2].startsWith('/') ? process.argv[2] : join(REPO, process.argv[2])]
  : DEFAULT_SCAN_DIRS

/** 找出「把自己这个文件读进来」的变量名。 */
function ownSourceVars(text, selfName) {
  const vars = new Set()
  for (const m of text.matchAll(/(?:const|let|var)\s+(\w+)\s*=\s*readFileSync\(([\s\S]{0,300}?)\)\s*(?:\n|$)/g)) {
    const parts = [...m[2].matchAll(/'([^']+)'|"([^"]+)"/g)].map((x) => x[1] ?? x[2])
    if (!parts.includes(selfName)) continue
    vars.add(m[1])
  }
  return vars
}

/** 把 `'a' + 'b' + "c"` 这类表达式还原成拼接后的值；遇到非字面量返回 null。 */
function foldStringExpr(src) {
  const trimmed = src.trim()
  if (!trimmed) return null
  // 允许：字面量 之间只用 + 连接。出现别的运算符就认不出来（返回 null，不猜）。
  if (!/^(['"][^'"]*['"])(\s*\+\s*(['"][^'"]*['"]))*$/.test(trimmed)) return null
  const out = []
  for (const m of trimmed.matchAll(/'([^']*)'|"([^"]*)"/g)) out.push(m[1] ?? m[2])
  // ⚠ v6 在这里栽过一次：上面取到的是**源码原文**，不是 JS 字符串的**求值结果**。
  //   源码里写 `new RegExp('runFlow: ' + '\\$\\{base\\}')` 时，正则源应当是
  //   `\$\{base\}`（`\\` 求值成一个 `\`），而本工具原样拿 `\\$\\{base\\}` 去编译
  //   ⇒ `\\` 变成「匹配一个字面反斜杠」⇒ 永远匹配不到真守卫
  //   ⇒ 把一条**运行时正确、且已实测有牙**的锚点误报成 ANCHOR_NO_STATIC。
  //   ⇒ 必须先做一层 JS 字符串反转义（`\\x` → `\x`），再当正则源/子串用。
  return out.join('').replace(/\\(.)/g, '$1')
}

function compilePattern(p) {
  try {
    return new RegExp(p)
  } catch {
    return null
  }
}

const findings = []
const healthy = []
let scanned = 0
let selfReading = 0

for (const dir of SCAN_DIRS) {
  if (!existsSync(dir)) {
    console.error(`❌ 扫描根不存在：${dir}`)
    process.exit(2)
  }
  for (const name of readdirSync(dir)) {
    const p = join(dir, name)
    if (!statSync(p).isFile() || !name.endsWith('.mjs')) continue
    scanned += 1
    const text = readFileSync(p, 'utf8')
    const selfVars = ownSourceVars(text, name)
    if (!selfVars.size) continue
    selfReading += 1

    const judges = []
    // 形态 1/2：/re/.test(v) 与 new RegExp(…).test(v)
    for (const m of text.matchAll(/(\/((?:\\.|[^/\\])+)\/\.test|new RegExp\(([\s\S]{0,200}?)\)\.test)\(\s*(\w+)\s*\)/g)) {
      if (!selfVars.has(m[4])) continue
      const whole = m[1]
      const regexRaw = m[2]
      const exprRaw = m[3]
      const pattern = regexRaw !== undefined ? regexRaw : foldStringExpr(exprRaw)
      if (pattern === null) continue
      judges.push({
        // 正则字面量：必须**按原文**编译。v1 在这里栽过一次 ——
        //   它照抄了 selfmatch 普查的「剥掉转义」那一步，于是
        //   `/if \(k\.startsWith\('_'\)\) continue/` 被编译成
        //   `if (k.startsWith('_')) continue`，而 `(` `)` 在正则里是**分组**，
        //   实际要匹配的是 `if k.startsWith('_') continue`（没有括号）
        //   ⇒ 真守卫那行永远不命中 ⇒ **假报 ANCHOR_NO_STATIC**。
        //   正则字面量里的 `\(` 恰恰是必需的，剥掉就等于改了判据。
        pattern,
        // 仅用于「判据自身是否逐字包含这个模式」那一问（层 1 的自匹配检测）。
        textPattern: regexRaw !== undefined ? regexRaw.replace(/\\(.)/g, '$1') : pattern,
        form: regexRaw !== undefined ? 'regex-literal' : 'new RegExp(...)',
        // 定义处 = 从这次匹配的开头到 `test(v)` 收尾的整段。
        // 正则字面量会**逐字**出现在这段里；拼接式不会。
        defStart: m.index,
        defEnd: m.index + whole.length + String(m[4]).length + 1,
        raw: whole,
      })
    }
    // 形态 3：v.includes(…)
    for (const m of text.matchAll(/(\w+)\.includes\(([\s\S]{0,200}?)\)/g)) {
      if (!selfVars.has(m[1])) continue
      const pattern = foldStringExpr(m[2])
      if (pattern === null) continue
      judges.push({
        pattern,
        textPattern: pattern,
        form: 'src.includes(...)',
        // ⚠ v4 在这里栽过一次：`includes` 是**精确子串**语义，不是正则。
        //   本普查当时一律 `compilePattern` ⇒ `没有对应脚本 ${fixtureScript`
        //   里的 `$` 被当成行尾锚点 ⇒ 整条锚点**永远匹配不到**真守卫那一行
        //   ⇒ 把一条**已经修好、并且实测有牙**的锚点误报成 ANCHOR_NO_STATIC。
        //   教训：判据的**匹配语义**本身也是量具的一部分，不能默认成正则。
        matchMode: 'substring',
        defStart: m.index,
        defEnd: m.index + m[0].length,
        raw: m[0],
      })
    }

    for (const j of judges) {
      const re = j.matchMode === 'substring' ? null : compilePattern(j.pattern)
      if (j.matchMode !== 'substring' && !re) continue
      const lineOf = (i) => text.slice(0, i).split('\n').length
      // 定义处按**行区间**排除。
      // ⚠ v2 在这里栽过一次：它按字符偏移判「这行是不是定义处」，
      //   而正则字面量的 `defStart` 落在**行中**（`if (!/re/.test(src)` 的 `/` 那一列）
      //   ⇒ 行首 < defStart ⇒ 定义行永远排不掉 ⇒ 判据把自己算成落点
      //   ⇒ 每次都凭空多报 1 个落点（实测 725 就是这样混进去的）。
      const defLo = lineOf(j.defStart)
      const defHi = lineOf(Math.max(j.defStart, j.defEnd - 1))
      const hits = []
      let idx = -1
      while ((idx = text.indexOf('\n', idx + 1)) !== -1) {
        const start = idx + 1
        const end = text.indexOf('\n', start)
        const lineEnd = end === -1 ? text.length : end
        const ln = lineOf(start)
        if (ln >= defLo && ln <= defHi) continue
        const line = text.slice(start, lineEnd)
        if (j.matchMode === 'substring' ? line.includes(j.pattern) : re.test(line)) hits.push(ln)
      }
      // ★ v5 在这里栽过：**自匹配必须用判据自己的语义去问，不能用文本比较。**
      //   裸正则字面量 `/runFlow: \$\{base\}/` 里带转义，文本上**不含**
      //   `runFlow: ${base}` ⇒ 旧的 `defSpan.includes(textPattern)` 判 false；
      //   但它作为**正则**去匹配自己那一行是**成立**的 ⇒ 判据仍然恒真。
      //   这是 §127 那条规律（纯字面量必然自匹配）的**第六种形态**：
      //   「转义过的裸正则字面量，正则语义上自匹配」。
      // ⇒ 自匹配 = 用同一套匹配语义去问「判据自己那一行满足不满足这个判据」。
      const defText = text.slice(text.lastIndexOf('\n', j.defStart) + 1, text.indexOf('\n', j.defEnd) === -1 ? text.length : text.indexOf('\n', j.defEnd))
      const selfMatch = j.matchMode === 'substring' ? defText.includes(j.pattern) : re.test(defText)
      const rec = {
        file: relative(REPO, p).split('\\').join('/'),
        line: lineOf(j.defStart),
        form: j.form,
        pattern: j.pattern,
        hits,
        selfMatch,
      }
      if (selfMatch) findings.push({ ...rec, kind: 'ANCHOR_TAUTOLOGY' })
      else if (hits.length === 1) healthy.push(rec)
      else if (hits.length === 0) findings.push({ ...rec, kind: 'ANCHOR_NO_STATIC' })
      else findings.push({ ...rec, kind: 'ANCHOR_AMBIGUOUS' })
    }
  }
}

console.log(`【自读判据锚点普查】扫了 ${scanned} 个 .mjs，其中 ${selfReading} 个读自己源码`)
console.log(`判据共 ${healthy.length + findings.length} 处：健康 ${healthy.length}，有问题 ${findings.length}`)

if (!findings.length) {
  console.log('✅ 没有「锚点不靠得住」的自读判据。')
  process.exit(0)
}

const order = { ANCHOR_TAUTOLOGY: 0, ANCHOR_NO_STATIC: 1, ANCHOR_AMBIGUOUS: 2 }
findings.sort((a, b) => order[a.kind] - order[b.kind] || a.file.localeCompare(b.file) || a.line - b.line)

console.log('')
for (const f of findings) {
  const why = {
    ANCHOR_TAUTOLOGY: '锚点只匹配到判据自己 ⇒ 恒真，删掉被守护的代码也不会红',
    ANCHOR_NO_STATIC: '锚点在静态源码里没有任何落点（多半是只在插值后存在的模板）',
    ANCHOR_AMBIGUOUS: `锚点有 ${f.hits.length} 个落点 ⇒ 注释/用例名就能顶替真守卫`,
  }[f.kind]
  console.log(`❌ ${f.kind}  ${f.file}:${f.line}`)
  console.log(`   形态: ${f.form}`)
  console.log(`   锚点: /${f.pattern}/`)
  console.log(`   含义: ${why}`)
  if (f.hits.length) console.log(`   落点行: ${f.hits.join(', ')}`)
  console.log('')
}
console.log('本工具只报**可判定**的锚点形态；「那个唯一落点就是真守卫吗」只能靠变异回答。')
