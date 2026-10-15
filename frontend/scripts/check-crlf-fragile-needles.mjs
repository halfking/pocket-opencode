// check-crlf-fragile-needles.mjs
//
// 护栏：禁止测试里出现「跨行字符串针」去匹配工作区里的源文件。
//
// 背景（2026-10-02 实测事故）
// ----------------------------
// email-refresh-trigger.test.mjs 里有这样一处负控：
//
//     hostSrc.replace(
//       'state.lastAttemptAt = now\n    state.inFlight = true',
//       'state.inFlight = true',
//     )
//
// 仓库里存的是 LF，但本机 core.autocrlf=true，Windows 上检出的 .ts 是 CRLF。
// 于是 `...\n    state...` 这个针**在磁盘上根本不存在**，`.replace()` 静默
// 不命中，负控样本等于没变异过 —— 判据从此静默失效。
//
// 危险之处在于它**只在 Windows 上炸**：.github/workflows/*.yml 全部
// runs-on ubuntu-latest，Linux 检出是 LF，针必然命中，CI 永远全绿。
// 也就是说一个判据可以在 CI 里"有效"几个月，本机一跑就红，或者反过来：
// 谁在 Windows 上把 needle 改成永远匹配不到的样子，CI 也不会拦。
//
// 为什么只查 needle 不查 replacement
// --------------------------------
// `.replace(needle, replacement)` 里：
//   · needle（第 1 参数）必须匹配磁盘内容 → 对行尾敏感，会静默失效；
//   · replacement（第 2 参数）是**插入**的文本，它里面写 \n 还是 \r\n
//     只影响新插入内容的换行风格，不影响判据是否命中。
// 所以只有 needle 是风险面。实测 email-job-runtime-singleton.test.mjs 的
// 两处跨行串都是 replacement，故不受影响，本护栏也不该误报它们。
//
// 判定方式：静态扫 src 下的测试文件，找出「作为 .replace()/.includes()/
// .indexOf() 第 1 参数、且字面量里含 \n」的字符串，再看它读的那个源文件在
// 工作区里是不是 CRLF。宁可漏报也不误报：只报有实据的组合。

import fs from 'node:fs'
import path from 'node:path'
import { tmpdir } from 'node:os'
import { fileURLToPath } from 'node:url'

const FRONTEND = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const SRC = path.join(FRONTEND, 'src')

function walk(dir, acc = []) {
  for (const e of fs.readdirSync(dir, { withFileTypes: true })) {
    if (e.name === 'node_modules' || e.name === 'dist' || e.name === '.git') continue
    const p = path.join(dir, e.name)
    if (e.isDirectory()) walk(p, acc)
    else if (/\.(test|spec)\.(mjs|ts|js)$/.test(e.name)) acc.push(p)
  }
  return acc
}

/**
 * 该文件的两种行尾变体：**LF 版**与 **CRLF 版**，都从磁盘内容**合成**。
 *
 * ★ 为什么必须合成、而不是只看磁盘上那份（2026-10-07 实测，这是本门最大的洞）：
 *   老版本只把「工作区里确实是 CRLF」的目标纳入检查，其余**整条测试文件跳过**。
 *   实测本机（LF 检出）：286 个测试文件、7 个跨行 needle、**实际检验 0 个** ——
 *   而收尾那行照印「跨行 needle 全部**实测命中**」。
 *   换句话说：**这道门在 Linux/macOS 上（包括全部 ubuntu CI）从来没有执行过判定**，
 *   它能一直绿是因为什么都没测。
 *
 *   事故形态（本文件头写的 2026-10-02 那次）恰恰是 `core.autocrlf=true` 的
 *   Windows 检出才暴露的 —— 所以「按当前检出测」这个设计，等于把唯一的取证窗口
 *   留给了一台没人跑 CI 的机器。
 *
 *   ⇒ 判定改成**与检出无关**：needle 对着目标的 LF 变体和 CRLF 变体**各试一次**。
 */
function eolVariants(p) {
  let raw
  try { raw = fs.readFileSync(p, 'utf8') } catch { return null }
  const lf = raw.replace(/\r\n/g, '\n')
  const crlf = lf.replace(/\n/g, '\r\n')
  return { lf, crlf }
}

/** 抽出所有"跨行字面量"，并标注它是 needle 还是 replacement。 */
function multilineLiterals(src) {
  const out = []
  // 逐行扫：needle 一定紧跟在 .replace( / .includes( / .indexOf( 之后（可跨行）
  const re = /\.replace\(\s*(['"`])((?:[^'"`\\]|\\.)*?\\n(?:[^'"`\\]|\\.)*?)\1/g
  let m
  while ((m = re.exec(src)) !== null) out.push({ kind: 'needle(replace)', lit: m[2], index: m.index })
  const re2 = /\.includes\(\s*(['"`])((?:[^'"`\\]|\\.)*?\\n(?:[^'"`\\]|\\.)*?)\1/g
  while ((m = re2.exec(src)) !== null) out.push({ kind: 'needle(includes)', lit: m[2], index: m.index })
  const re3 = /\.indexOf\(\s*(['"`])((?:[^'"`\\]|\\.)*?\\n(?:[^'"`\\]|\\.)*?)\1/g
  while ((m = re3.exec(src)) !== null) out.push({ kind: 'needle(indexOf)', lit: m[2], index: m.index })
  return out
}

/**
 * 该测试读到的、真实存在的源文件。
 *
 * 关键：不能只拿「裸字面量」去猜路径。真实测试几乎都这么写：
 *
 *     const SRC  = path.resolve(HERE, '..', '..', '..')      // frontend/src
 *     const HOST = path.join(SRC, 'features', 'email', 'email-fetch-host.ts')
 *
 * 末段 'email-fetch-host.ts' 单独拿去 resolve 是不存在的（它不在测试文件旁边），
 * 必须把 path.join/path.resolve 的实参**拼起来**再解析。
 * —— 护栏第一版就是漏了这一步：对 email-refresh-trigger.test.mjs 报 0 处违规，
 *   而那个文件恰恰是本次事故本体。护栏不报警 = 护栏是废的。
 */
function readTargets(testFile, src) {
  const out = []
  const hereDir = path.dirname(testFile)

  const dirConsts = new Map()
  const constRe = /(?:const|let)\s+([A-Za-z_$][\w$]*)\s*=\s*path\.(?:join|resolve)\(([^)]*)\)/g
  let cm
  while ((cm = constRe.exec(src)) !== null) dirConsts.set(cm[1], cm[2])

  function evalCall(argsStr) {
    const parts = [...argsStr.matchAll(/'([^']*)'|\b([A-Za-z_$][\w$]*)\b/g)].map((m) =>
      m[1] !== undefined ? m[1] : m[2],
    )
    if (!parts.length) return null
    // ★ 第 0 个实参也要走同一套展开（2026-10-07 修）。
    //   老代码写的是 `acc = parts[0] === 'HERE' ? hereDir : parts[0]`，然后 `for (i = 1; …)`
    //   ⇒ `path.join(API_DIR, 'stt-settings.ts')` 里的 `API_DIR` 被当成**字面目录名**，
    //   解析出一个不存在的路径，`fs.existsSync` 判假、目标被丢进黑洞。
    //   而 `const API_DIR = path.resolve(HERE, '..')` 正是本仓测试里最常见的写法
    //   （30 个测试文件用了 `const X = path.dirname(`，间接依赖 'HERE' 这个名字）。
    //   它的后果不是「报红」而是**整条跳过**，所以完全静默。
    let acc = null
    for (let i = 0; i < parts.length; i++) {
      const p = parts[i]
      if (i === 0) {
        if (p === 'HERE') acc = hereDir
        else if (dirConsts.has(p)) {
          const inner = evalCall(dirConsts.get(p))
          acc = inner ? path.resolve(hereDir, inner) : null
        } else acc = p
        if (acc === null) return null
        continue
      }
      if (p === '..' || p === '.') acc = path.resolve(acc, p)
      else if (dirConsts.has(p)) {
        const inner = evalCall(dirConsts.get(p))
        acc = inner ? path.resolve(acc, inner) : acc
      } else acc = path.resolve(acc, p)
    }
    return acc
  }

  const callRe = /path\.(?:join|resolve)\(([^)]*)\)/g
  let m
  while ((m = callRe.exec(src)) !== null) {
    const resolved = evalCall(m[1])
    if (!resolved) continue
    if (fs.existsSync(resolved) && fs.statSync(resolved).isFile() && /\.(ts|vue|css|go|mjs|html)$/.test(resolved)) {
      out.push(resolved)
    }
  }

  const lits = [...src.matchAll(/'([^'\n]+)'/g)].map((x) => x[1])
  for (const s of lits) {
    if (!/\.(ts|vue|css|go|mjs|html)$/.test(s) || s.includes(' ')) continue
    for (const base of [path.dirname(testFile), FRONTEND, path.join(FRONTEND, 'src')]) {
      const p = path.resolve(base, s)
      try {
        if (fs.existsSync(p) && fs.statSync(p).isFile()) { out.push(p); break }
      } catch { /* ignore */ }
    }
  }

  return [...new Set(out)]
}

/**
 * 这个 needle 现在还**真的**匹配吗？—— 实测，不靠猜；且**与检出无关**。
 *
 * 这是本护栏最重要的一段。第一版只做静态检查，结果对着**已经修好的**
 * email-refresh-trigger.test.mjs 报警：那个测试确实有一个跨行 needle，
 * 但它在读文件时写了 .replace(/\r\n/g, '\n')，所以 needle 实际是安全的。
 * 静态扫描看不见运行时归一化 —— 它只会说「这里有个跨行 needle」，
 * 看不见「这个 needle 用的是归一化后的文本」。
 *
 * 两种可能的成因，静态上完全一样：
 *   (a) 归一化过了，needle 安全            -> 误报
 *   (b) 没归一化，needle 静默失效          -> 真缺陷
 * 光看代码分不出来，只能**拿 needle 去真实匹配一遍**。
 *
 * ★ 判定顺序（2026-10-07 改）：**先试 CRLF 变体，再试 LF 变体**。
 *   老顺序是「先试磁盘上那份」—— 在 LF 检出上，LF needle 必然命中磁盘文本，
 *   于是直接判「与行尾无关，安全」，而它在 CRLF 检出上恰恰会失效。
 *   现在：
 *     · CRLF 变体命中              -> LF/CRLF 两种检出都安全
 *     · 只在 LF 变体上命中          -> 依赖 LF；测试若不归一化就是缺陷
 *     · 两边都匹配不上              -> 针根本就是坏的（改名/失效），同样该报
 */
function needleStatus(needle, targets) {
  const variants = targets.map(eolVariants).filter(Boolean)
  return statusFromVariants(needle.replace(/\\n/g, '\n').replace(/\\r/g, '\r'), variants)
}

/**
 * 判定的**纯函数**部分：拿 needle 文本去试一组 {lf, crlf} 变体。
 *
 * 拆出来是为了让自检不必碰文件系统（上一版用 monkey-patch 换 `fs.readFileSync`，
 * 又丑又脆，而且 `finally` 里还写了一句 `fs.readFileSync = fs.readFileSync` 的空操作）。
 */
function statusFromVariants(asText, variants) {
  let lfHit = false
  for (const v of variants) {
    if (v.crlf.includes(asText)) return { ok: true, why: 'needle 在 CRLF 变体上即命中（两种行尾检出都安全）' }
    if (v.lf.includes(asText)) lfHit = true
  }
  if (lfHit) return { ok: false, needsLf: true, why: 'needle 只在 LF 变体上命中 —— CRLF 检出下会静默失效' }
  return { ok: false, why: 'needle 在 LF 与 CRLF 两种变体上都匹配不上 —— 判据已失效' }
}

/**
 * 这个测试自己有没有把读到的文本归一化成 LF？
 *
 * 这是判定的最后一环，也是护栏对不对的关键。上一版只看
 * 「needle 能否在 LF 文本上命中」，于是对着**已修好**的
 * email-refresh-trigger.test.mjs 仍然放行——因为那个测试确实归一化了，
 * 但护栏并不知道它归一化了；而把归一化删掉之后，护栏还是放行，
 * 因为 needle 在 LF 文本上照样命中。两种状态在护栏眼里一模一样 = 废护栏。
 *
 * 所以必须同时问两个问题：
 *   1. 这个 needle 是不是只在 LF 文本上命中？（needsLf）
 *   2. 这个测试有没有对读到的文本做 CRLF→LF 归一化？（normalizes）
 * 只有 (1) 且非 (2) 才是缺陷 —— 那正是 2026-10-02 的事故形态。
 */
function testNormalizesEol(src) {
  // 形如 .replace(/\r\n/g, '\n') 或 replace(/\r\n/g,"\n") 的归一化
  return /\.replace\(\s*\/\s*\\r\\n\s*\/[gimsuy]*\s*,\s*(['"`])\\\\?n\1\s*\)/.test(src)
}

// ───────────────────────── 自检：needleStatus 的判定必须与检出无关 ─────────────────────────
//
// 为什么必须有：这道门在 LF 检出上「什么都没测」却照样报「全部实测命中」。
// 夹具走的是**同一个** needleStatus，输入是合成的 LF/CRLF 变体，
// 所以「CRLF 变体优先」这条顺序一旦被改回去（比如先试 raw），
// 下面第 2、3 例会立刻变红 —— 而在真实仓上那个改动是**完全静默**的。
const NEEDLE_SELFTEST = [
  {
    name: 'LF needle + CRLF 检出 ⇒ 危险（这正是 2026-10-02 事故的形状）',
    targets: { lf: 'a = 1\nb = 2\n', crlf: 'a = 1\r\nb = 2\r\n' },
    lit: 'a = 1\\nb = 2\\n',
    want: 'needsLf',
  },
  {
    name: 'CRLF needle（字面里带 \\r\\n）⇒ 两种检出都安全',
    targets: { lf: 'a = 1\nb = 2\n', crlf: 'a = 1\r\nb = 2\r\n' },
    lit: 'a = 1\\r\\nb = 2\\r\\n',
    want: 'ok',
  },
  {
    name: '单行 needle（不含 \\n）⇒ 与行尾无关，安全',
    targets: { lf: 'a = 1\nb = 2\n', crlf: 'a = 1\r\nb = 2\r\n' },
    lit: 'b = 2',
    want: 'ok',
  },
  {
    name: '针已经坏了（两边都匹配不上）⇒ 必须报出来',
    targets: { lf: 'a = 1\nb = 2\n', crlf: 'a = 1\r\nb = 2\r\n' },
    lit: 'z = 9\\nw = 8\\n',
    want: 'broken',
  },
  {
    name: '目标磁盘上是 CRLF，但 needle 只吃 LF ⇒ 照样判危险（不能因为「磁盘已经是 CRLF」就放过）',
    targets: { lf: 'x\ny\n', crlf: 'x\r\ny\r\n' },
    lit: 'x\ny\n',
    want: 'needsLf',
  },
]

{
  const variants = [{ lf: 'a = 1\nb = 2\n', crlf: 'a = 1\r\nb = 2\r\n' }]
  void variants
}

const needleTestBad = []
for (const c of NEEDLE_SELFTEST) {
  let got
  try {
    const st = statusFromVariants(c.lit.replace(/\\n/g, '\n').replace(/\\r/g, '\r'), [c.targets])
    got = st.ok ? 'ok' : st.needsLf ? 'needsLf' : 'broken'
  } catch (err) {
    got = '抛异常: ' + err.message
  }
  if (got !== c.want) needleTestBad.push({ ...c, got })
}
if (needleTestBad.length > 0) {
  console.error('❌ needleStatus 判定自检不过 —— 判据本身坏掉了，下面所有结论不可信。')
  for (const c of needleTestBad) console.error(`   ${c.name}\n     期望 ${c.want}，实得 ${c.got}`)
  console.error('   注意：判据坏了时的读数长得和结论一模一样（0 处违规 + ✓），所以必须在这里拦。')
  process.exit(2)
}
console.log(`  needleStatus 自检：实跑 ${NEEDLE_SELFTEST.length}/${NEEDLE_SELFTEST.length} 例全通过（含 2 例必须判红）`)

/**
 * 剥掉注释后再扫。
 *
 * 这不是洁癖，是实测踩到的：第一版护栏把「我在注释里写的例子」
 * （`// 字符串针（'state.lastAttemptAt = now\n    state.inFlight = true'）`）
 * 当成了真 needle，于是修好代码之后护栏反而报警——护栏开始指着自己的
 * 注释要人改。判据必须只认代码，否则它会被注释满足/被注释污染。
 *
 * 用 TypeScript 的扫描器剥（与本仓库其他护栏一致），保证字符串字面量
 * 里的内容不受影响，也不会被 URL 里的 // 骗到。
 */
function stripComments(src) {
  // 轻量但够用：处理 // 与 /* */，并跳过字符串/模板字面量内部的同类符号。
  let out = ''
  let i = 0
  const n = src.length
  while (i < n) {
    const c = src[i]
    const c2 = src[i + 1]
    if (c === '/' && c2 === '/') {
      while (i < n && src[i] !== '\n') i++
      continue
    }
    if (c === '/' && c2 === '*') {
      i += 2
      while (i < n && !(src[i] === '*' && src[i + 1] === '/')) i++
      i += 2
      // 用空格占位，保持后续 indexOf/切片语义不变
      out += ' '
      continue
    }
    if (c === '"' || c === "'" || c === '`') {
      const q = c
      out += c
      i++
      while (i < n && src[i] !== q) {
        if (src[i] === '\\') { out += src[i] + (src[i + 1] ?? ''); i += 2; continue }
        out += src[i]
        i++
      }
      out += q
      i++
      continue
    }
    out += c
    i++
  }
  return out
}

/**
 * 扫到的测试文件数下限。**只能手工改常量**，不给命令行开关。
 * 本仓当前 286，取 200：正常增删测试不误报，而系统性枚举退化会撞上。
 * 它抓不住「只丢一两个文件」—— 那是它的边界，不假装能抓。
 */
const MIN_TEST_FILES = 200

/**
 * 扫描一次，返回四元组。**主路径与活体正控走的是同一个函数** ——
 * 否则正控证明的是「正控那条路是通的」，不是「真实扫描是通的」。
 */
function scanNeedles(root) {
  const testFiles = walk(root)
  const out = { files: testFiles.length, needles: 0, evaluated: 0, violations: [], unresolved: [] }
  for (const f of testFiles) {
    const raw = fs.readFileSync(f, 'utf8')
    if (!/readFileSync\([^)]*['"]utf8['"]\)/.test(raw)) continue
    const src = stripComments(raw)
    const needles = multilineLiterals(src).filter((x) => x.kind.startsWith('needle'))
    if (!needles.length) continue
    out.needles += needles.length
    // ★ 不再 `.filter(isCrlf)`：目标是按**两种行尾合成变体**去试的，与检出无关。
    const targets = readTargets(f, raw)
    if (!targets.length) {
      out.unresolved.push({ test: path.relative(FRONTEND, f), needles: needles.length })
      continue
    }
    // 该测试有没有自己做 CRLF→LF 归一化？（判定的另一半，见 testNormalizesEol）
    const normalizes = testNormalizesEol(src)
    for (const n of needles) {
      out.evaluated += 1
      const st = needleStatus(n.lit, targets)
      if (st.ok) continue
      // 只在 LF 变体上命中：测试自己归一化了就安全，没归一化就是缺陷。
      if (st.needsLf && normalizes) continue
      out.violations.push({
        test: path.relative(FRONTEND, f),
        kind: n.kind,
        lit: n.lit,
        targets: targets.map((t) => path.relative(FRONTEND, t)),
        why: st.needsLf && !normalizes
          ? '跨行 needle 依赖 LF 文本，但该测试没有做 CRLF→LF 归一化（Windows 检出时会静默失效）'
          : st.why,
      })
    }
  }
  return out
}

// ───────────────────────── 活体正控：种一个已知脆弱针，必须被抓到 ─────────────────────────
//
// 为什么上面那 5 例夹具不够：它们验的是 needleStatus 的**判定**，验不到
// 「枚举 → readTargets → stripComments → multilineLiterals → 判定」这条链。
// 而本门的历史恰恰是这条链**整条断掉却照样报绿**（7 个 needle、0 个被检验、
// 收尾行还写「全部实测命中」）。所以必须有一个「放进去就会红」的样本。
const CONTROL_ROOT = fs.mkdtempSync(path.join(tmpdir(), 'crlf-control-'))
try {
  const testDir = path.join(CONTROL_ROOT, 'ctl')
  fs.mkdirSync(testDir, { recursive: true })
  // 目标文件是 LF 检出的（本机就是这样），needle 也只吃 LF —— 正是事故形态。
  fs.writeFileSync(path.join(CONTROL_ROOT, 'host.ts'), 'a = 1\nb = 2\n', 'utf8')
  fs.writeFileSync(
    path.join(testDir, 'control.test.mjs'),
    [
      "import fs from 'node:fs'",
      "import path from 'node:path'",
      // ★ 用**单引号**字面量：readTargets 的实参解析正则只认 `'…'`，
      //   双引号会被当成标识符逐段匹配、解析出垃圾路径（它自己的限制，别绕过去）。
      "const HOST = path.resolve('" + testDir + "', '..', 'host.ts')",
      "const src = fs.readFileSync(HOST, 'utf8')",
      "const mutated = src.replace('a = 1\\nb = 2', 'b = 2')",
      'export default mutated',
      '',
    ].join('\n'),
    'utf8',
  )
  const ctrl = scanNeedles(CONTROL_ROOT)
  if (ctrl.violations.length === 0) {
    console.error('❌ 活体正控失败 —— 种进去的已知 CRLF 脆弱针**没被抓到**。')
    console.error(`   正控语料：${ctrl.files} 个测试文件 / ${ctrl.needles} 个 needle / 实际检验 ${ctrl.evaluated} 个。`)
    console.error('   判定或扫描链坏了。这道门失明时的读数是「0 处违规 + ✓」，和真通过一模一样。')
    process.exit(2)
  }
  console.log(
    `  活体正控：种进去的已知脆弱针被抓到了（${ctrl.files} 个测试文件 / ${ctrl.needles} 个 needle / ` +
      `实测 ${ctrl.evaluated} 个 / ${ctrl.violations.length} 处违规）`,
  )
} finally {
  fs.rmSync(CONTROL_ROOT, { recursive: true, force: true })
}

const result = scanNeedles(SRC)
const { files, needles: NEEDLES, evaluated: EVALUATED, unresolved: UNRESOLVED } = {
  files: result.files, needles: result.needles, evaluated: result.evaluated, unresolved: result.unresolved,
}

if (files < MIN_TEST_FILES) {
  console.error(`❌ 只扫到 ${files} 个测试文件 < 下限 ${MIN_TEST_FILES} —— 枚举漏了东西，拒绝按「通过」处理。`)
  console.error('   抓 CRLF 脆弱针的门在「一个 needle 都没扫到」时必须报错，而不是报通过。')
  process.exit(2)
}

console.log(
  `【CRLF 脆弱针】扫了 ${files} 个测试文件，跨行 needle ${NEEDLES} 个，实际检验 ${EVALUATED} 个`,
)
if (NEEDLES === 0) {
  console.error('❌ 一个跨行 needle 都没扫到 —— 这是**危险方向**，不是「没有脆弱针」的证据。')
  console.error('   要么 multilineLiterals 的正则被改坏了、要么扫描根没扫到。这道门一旦失明，')
  console.error('   2026-10-02 那种事故会完全无声地回来。')
  process.exit(2)
}
if (EVALUATED === 0) {
  console.error('❌ 扫到 needle 却一个都没检验 —— 目标解析（readTargets）整体失明了，拒绝按「通过」处理。')
  process.exit(2)
}
if (UNRESOLVED.length > 0) {
  console.error(
    `❌ ${UNRESOLVED.length} 个测试文件有跨行 needle，但**一个目标文件都没解析出来** ⇒ 这部分 needle 未被检验：`,
  )
  for (const u of UNRESOLVED) console.error(`     ${u.test}（${u.needles} 个 needle）`)
  console.error('   这不是通过，也不是违规 —— 是**读不到该判的样本**。')
  console.error('   放过去就等于把「静默跳过」这个老病根留在这里：readTargets 一旦退化，')
  console.error('   门会重新变成「什么都没测 + 报通过」。补 readTargets 的解析支持，或人肉核完再放。')
  process.exit(2)
}

const violations = result.violations
if (violations.length) {
  console.error(`✗ CRLF 脆弱针护栏：${violations.length} 处「跨行 needle」已失效\n`)
  for (const v of violations) {
    console.error(`  ${v.test}`)
    console.error(`    ${v.kind}: ${JSON.stringify(v.lit).slice(0, 120)}`)
    console.error(`    目标(按 LF/CRLF 两种行尾实测): ${v.targets.join(', ')}`)
    console.error(`    实测: ${v.why}`)
    console.error(`    修法：读文件后 .replace(/\\r\\n/g, '\\n') 归一化，再拿归一化后的文本当 needle\n`)
  }
  process.exit(1)
}

// ★ 这行**不许**再写「全部实测命中」：实测中 UNRESOLVED 可能非空，
//   而「全部」正是这句话最贵的部分 —— 它替没读到样本的部分作了保证。
const unexamined = NEEDLES - EVALUATED
console.log(
  `✓ CRLF 脆弱针护栏：${violations.length} 处违规；${NEEDLES} 个跨行 needle 中实测 ${EVALUATED} 个` +
    (unexamined > 0 ? `，${unexamined} 个因目标没解析出来而**未检验**（见上方 ⚠️）` : '，全部检验完毕'),
)
