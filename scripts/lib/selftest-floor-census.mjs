#!/usr/bin/env node
/**
 * 自检层「用例被删空 ⇒ 假绿」这一族的**成员普查**。
 *
 * ⚠ 为什么不能用 `grep -l -- '--selftest'` 当普查入口（这是本工具存在的全部理由）：
 *   那个字符串出现在文件里，**不等于这个文件有自检**。
 *   实测 `scripts/verify-marketplace-fix.mjs` 在 HEAD 里 `--selftest` 只出现在两行
 *   帮助文本里（`console.log('node scripts/check-hide-app-header.mjs --selftest（11/11）')`），
 *   它**根本没有 selftest 函数**。按标记筛会把它算成成员。
 *   同理，未跟踪文件 `git show HEAD:` 返回空串，若把空串当「无下限闸」，
 *   分母就会凭空多出来几个（§154 那次 21 里混了 4 个未跟踪文件，真分母是 17）。
 *
 * 判定口径（三步，缺一不可）：
 *   ① 有**真的**自检 —— 有 `function selftest` 或 `process.argv.includes('--selftest')`。
 *   ② 自检把用例收进**可数的集合** —— `const cases = [` / `const add =` / `results.push`。
 *   ③ 判决**只**由这个集合上的失败数决定，且**没有下限闸**。
 *
 * ★ 关键修正（§157 实测推翻的）：第 ③ 步的判据**不是**「收尾行印的是不是 `x/y` 自比值」。
 *   `check-fixed-cdp-ports` 的收尾只印 `✅ 判据自测通过`，没有比值，
 *   但它的 `shouldHit`/`shouldMiss` 被抽空后 `fails` 必为空 ⇒ 照样打印通过。
 *   **`x/y` 自比读数只是这一族病里「看得见」的那一小部分，不是病的前提。**
 *   前提是「判决只依赖一个可以被抽空的集合上的失败数」。
 *
 * 只报事实 + 打印原始证据行，分类由读表的人复核。本工具不自己下结论。
 *
 * 跑法：
 *   node scripts/lib/selftest-floor-census.mjs            # 量 HEAD（不可变，推荐）
 *   node scripts/lib/selftest-floor-census.mjs --worktree # 量工作区（在制品，会动）
 */
import { execFileSync } from 'node:child_process'
import { readFileSync } from 'node:fs'
import { basename, join, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'

const ROOT = join(dirname(fileURLToPath(import.meta.url)), '..', '..')
const ROOTS = ['scripts', 'frontend/scripts']

const argv = process.argv.slice(2)
const USE_WORKTREE = argv.includes('--worktree')

/** 取源码：默认 HEAD（稳定基线），--worktree 才读工作区。 */
function read(f) {
  if (USE_WORKTREE) {
    try { return readFileSync(join(ROOT, f), 'utf8') } catch { return null }
  }
  try {
    // ⚠ stderr 必须丢掉：`git show` 读未跟踪文件会打「致命错误：…不在 'HEAD' 中」，
    //   那些行会混进本工具自己的输出，让人以为普查报了什么。
    return execFileSync('git', ['show', `HEAD:${f}`],
      { cwd: ROOT, encoding: 'utf8', maxBuffer: 1 << 24, stdio: ['ignore', 'pipe', 'ignore'] })
  } catch { return null }
}

/** 列候选文件：工作树里所有含 `--selftest` 字面量的 .mjs。 */
function candidates() {
  const out = []
  for (const r of ROOTS) {
    let names = []
    try {
      names = execFileSync('git', ['ls-files', '--others', '--cached', '--exclude-standard', '--', `${r}/*.mjs`],
        { cwd: ROOT, encoding: 'utf8' }).split('\n').filter(Boolean)
    } catch { continue }
    for (const n of names) {
      if (n.endsWith('.md5bak')) continue
      const src = USE_WORKTREE ? (() => { try { return readFileSync(join(ROOT, n), 'utf8') } catch { return null } })() : null
      const s = src ?? null
      if (s && !s.includes('--selftest')) continue
      if (!s && !candidateInHead(n)) continue
      out.push(n)
    }
  }
  return out.sort()
}

/** 工作树没命中时，退回 HEAD 判定（HEAD 未跟踪的文件一律排除）。 */
function candidateInHead(f) {
  try { return execFileSync('git', ['show', `HEAD:${f}`], { cwd: ROOT, encoding: 'utf8', maxBuffer: 1 << 24 }).includes('--selftest') } catch { return false }
}

/** 找出「名字里含 selftest」的函数定义。
 *  ⚠ 别写成 `[A-Za-z_$][\w$]*[Ss]elftest` —— 那要求名字**至少 9 个字符**，
 *  而最常见的那一个就叫 `selftest`（8 个），整批门会被挤到 fallback 分支、
 *  然后因为找不到 `const cases = [` 而被误判成「无可数集合」。
 *  本轮就是这么错的；是锚点自证把它挡下来的（脚本 exit 2）。 */
function findSelftestFn(s) {
  const re = /function\s+([A-Za-z_$][\w$]*)\s*\(/g
  let m
  while ((m = re.exec(s))) {
    if (/selftest/i.test(m[1])) return { index: m.index, name: m[1] }
  }
  return null
}

const hasRealSelftest = (s) => {
  if (findSelftestFn(s)) return true
  // ⚠ 不能只认 `process.argv.includes('--selftest')` —— 实测 device-matrix 用的是
  //   `if ((process.argv[2] || '').toLowerCase() === '--selftest') await runSelectorSelftest()`，
  //   同一个 dispatch 的**另一种形态**，按前一种写法筛会把它整条漏掉（这是第三次栽在这上面）。
  if (/['"`]--selftest['"`]\s*\)?\s*===/.test(s)) return true
  // 兜底：`--selftest` 出现在**非 console.log 行**上（注释也算线索）。
  // 反例就在 HEAD 里：verify-marketplace-fix 的两处 `--selftest` 全在
  // `console.log('node scripts/check-hide-app-header.mjs --selftest（11/11）')` 里 ⇒ 必须排除。
  const nonLog = s.split('\n').filter((l) => !/console\.(log|error)\s*\(/.test(l)).join('\n')
  return /--selftest/.test(nonLog)
}

/** 从某处的大括号开始，做**深度匹配**取到函数结束。
 *  ⚠ 绝不能一刀切到文件末 —— 那会把主流程一起吞进来。
 *  实测 verify-card-deck-labels 的 selftest 在 111–129 行，
 *  而 `vueFiles.push` 在 153 行（主流程）；切到 EOF 会把它**误判成有可数集合的真成员**。
 *
 *  ⚠⚠ 也绝不能**不跳字符串**地数括号（本工具第 5 个错，2026-10-10 §165）：
 *  `check-router-runtime-parity` 的自检里有一条夹具
 *  `add(..., 'hideAppHeader: true }')` —— **单引号字符串里一个不成对的 `}`**，
 *  裸计数会提前归零 ⇒ 自检体在第 267 行截断 ⇒ 第 273-275 行的 `MIN_SELFTEST_CASES`
 *  被切掉 ⇒ 把一个**有下限闸**的门报成「（无）」。
 *  ★ 那条夹具是我自己在 §152.5 写的 ⇒ **我自己的测试数据反过来骗了我自己的普查器。**
 *  ⇒ 模板串（反引号）里的裸花括号仍是已知残留风险，未处理。
 */
function balanced(s, start) {
  let depth = 0
  let i = start
  while (i < s.length) {
    const c = s[i]
    const n = s[i + 1]
    if (c === '/' && n === '/') { while (i < s.length && s[i] !== '\n') i++; continue }
    if (c === '/' && n === '*') { i += 2; while (i < s.length && !(s[i] === '*' && s[i + 1] === '/')) i++; i += 2; continue }
    if (c === "'" || c === '"') {                       // ★ 跳过字符串字面量
      const q = c
      i++
      while (i < s.length && s[i] !== q) { if (s[i] === '\\') { i += 2; continue } i++ }
      i++
      continue
    }
    if (c === '{') depth++
    else if (c === '}') { depth--; if (depth === 0) return i + 1 }
    i++
  }
  return s.length
}

function selftestBody(s) {
  const fn = findSelftestFn(s)
  if (fn) {
    const brace = s.indexOf('{', fn.index)
    return brace >= 0 ? s.slice(fn.index, balanced(s, brace)) : s.slice(fn.index)
  }
  // 没有 selftest 函数的形态：找 argv 分派处，取它**后面第一个函数**的主体。
  const b = s.search(/['"`]--selftest['"`]\s*\)?\s*===/) >= 0
    ? s.search(/['"`]--selftest['"`]\s*\)?\s*===/)
    : s.search(/process\.argv\.includes\(\s*['"`]--selftest/)
  if (b < 0) return ''
  const rest = s.slice(b)
  const mm = /function\s+([A-Za-z_$][\w$]*)\s*\([^)]*\)\s*\{/.exec(rest)
  if (!mm) return rest
  const brace = b + mm.index + mm[0].length - 1
  return s.slice(b, balanced(s, brace))
}

/** ② 可数集合：字面量数组 / add 函数 / push 收集。 */
function collections(body) {
  const found = new Set()
  for (const m of body.matchAll(/const\s+([A-Za-z_$][\w$]*)\s*=\s*\[/g)) found.add(m[1])
  for (const m of body.matchAll(/const\s+add\s*=/g)) found.add('add()')
  for (const m of body.matchAll(/([A-Za-z_$][\w$]*)\.push\(/g)) found.add(m[1] + '.push()')
  return [...found]
}

/** ③ 下限闸：拿「集合长度」和「一个常量」比较，且比较方向是 `<`。 */
function floorOf(body) {
  const m = body.match(/([A-Za-z_$][\w$]*)\.length\s*<\s*([A-Z_][A-Z0-9_]*)/)
  return m ? `${m[1]}.length < ${m[2]}` : null
}

/** 收尾读数行（给人看的原始证据，不参与判定）。 */
function readout(body) {
  const lines = body.split('\n')
  const hits = []
  for (const l of lines) {
    if (!/console\.(log|error)/.test(l)) continue
    if (/\.length|通过|失败|自测|自检/.test(l)) hits.push(l.trim())
    if (hits.length >= 2) break
  }
  return hits
}

const rows = []
for (const f of candidates()) {
  const src = read(f)
  if (src === null) continue                       // HEAD 里不存在 ⇒ 不是基线成员
  const real = hasRealSelftest(src)
  const body = real ? selftestBody(src) : ''
  const cols = real ? collections(body) : []
  const fl = real ? floorOf(body) : null
  rows.push({
    file: basename(f).replace(/\.mjs$/, ''),
    real,
    cols,
    floor: fl,
    readout: real ? readout(body) : [],
    markerOnly: !real,                             // 只有字面量、没有真自检
  })
}

const members = rows.filter((r) => r.real && r.cols.length > 0 && !r.floor)
const floored = rows.filter((r) => r.real && r.floor)
const realSelftests = rows.filter((r) => r.real)
const markerOnly = rows.filter((r) => r.markerOnly)
const nothingAtAll = rows.filter((r) => !r.real && !r.markerOnly)

const pad = (s, n) => String(s).padEnd(n)
console.log(`\n自检下限闸成员普查 · 读数来源：${USE_WORKTREE ? '工作区（在制品）' : 'HEAD（稳定基线）'}`)
console.log('='.repeat(96))
console.log(`${pad('门', 40)} ${pad('真自检', 8)} ${pad('可数集合', 30)} 下限闸`)
console.log('-'.repeat(96))
for (const r of rows) {
  const tag = r.markerOnly ? '★仅标记' : r.real ? '是' : '否'
  console.log(`${pad(r.file, 40)} ${pad(tag, 8)} ${pad(r.cols.join(',') || '—', 30)} ${r.floor || '（无）'}`)
}
console.log('-'.repeat(96))
// ⚠ 汇总不能重复扣减：markerOnly ⊆ !real，两者相加会多减。
console.log(`候选 ${rows.length} 个 → **有真自检 ${realSelftests.length}**`
  + `（其中无下限闸 ${members.length} / 有下限闸 ${floored.length}）`)
console.log(`★ 只因出现「--selftest」字面量被筛进来、实际没有自检：${markerOnly.length}`
  + (markerOnly.length ? `（${markerOnly.map((r) => r.file).join(', ')}）` : ''))
console.log(`★ 连标记都没有（不该出现在候选里，是本工具的筛选 bug）：${nothingAtAll.length}`
  + (nothingAtAll.length ? `（${nothingAtAll.map((r) => r.file).join(', ')}）` : ''))

if (members.length) {
  console.log(`\n无下限闸的 ${members.length} 个（判决只依赖一个可被抽空的集合上的失败数）：`)
  for (const m of members) {
    console.log(`  · ${m.file}  集合=[${m.cols.join(',')}]`)
    for (const l of m.readout) console.log(`      ${l}`)
  }
}

/* ── 量具自证：三个已知锚点必须落在正确的桶里，否则本表不可信 ──
   锚点 1 `check-runtime-data-tracked`：`cases = [...]` 字面量 + 判决只看失败数 ⇒ 无下限闸成员。
   锚点 2 `verify-marketplace-fix`：只有帮助文本提到别人的 --selftest ⇒ 仅标记，不是成员。
   锚点 3 `device-matrix`：dispatch 写成 `(process.argv[2]||'').toLowerCase() === '--selftest'`，
           只认 `process.argv.includes('--selftest')` 的普查器会把它整条漏掉 —— 这是回归守卫。 */
const bad = []
// 锚点 1 必须**按模式分支**：
//   HEAD 里 check-runtime-data-tracked 没有下限闸 ⇒ 应是「成员」；
//   工作区里我已于 §156 给它补了 MIN_SELFTEST_CASES=11 ⇒ 应是「有下限闸」。
// ★ 锚点照着**某个时点的状态**写死，读数一变它就失效 —— 这是本工具第 5 个错暴露出来的，
//   不修的话以后每加一道闸都要来改一次锚点。
const rdt = rows.find((r) => r.file === 'check-runtime-data-tracked')
if (!rdt) bad.push('锚点1 失效：check-runtime-data-tracked 根本没被枚举到')
else if (USE_WORKTREE) {
  if (!rdt.floor) bad.push('锚点1 失效（工作区）：check-runtime-data-tracked 已有 MIN_SELFTEST_CASES=11，应判成「有下限闸」')
} else if (!members.some((m) => m.file === 'check-runtime-data-tracked')) {
  bad.push('锚点1 失效（HEAD）：check-runtime-data-tracked 应是无下限闸成员')
}
if (!markerOnly.some((m) => m.file === 'verify-marketplace-fix')) bad.push('锚点2 失效：verify-marketplace-fix 应被判为「仅标记」')
if (!USE_WORKTREE && !members.some((m) => m.file === 'device-matrix')) bad.push('锚点3 失效：device-matrix 应是无下限闸成员（它的 --selftest 分派是 argv[2].toLowerCase() 形态）')
if (members.some((m) => m.file === 'verify-card-deck-labels')) bad.push('锚点4 失效：verify-card-deck-labels 不该算成员（§156 已用变异证明它没有可数用例数组）')
if (bad.length) { console.error(`\n❌ ${bad.join('\n❌ ')}`); process.exit(2) }
console.log('\n✅ 量具自证：四个锚点均落在预期桶里'
  + '（check-runtime-data-tracked / device-matrix = 成员；verify-marketplace-fix = 仅标记；'
  + 'verify-card-deck-labels ≠ 成员）')

/* ── 锚点 5（本工具第 5 个错才逼出来的）：「有下限闸」这个桶也要有断言 ──
   之前四个锚点只断言「是不是成员」「是不是仅标记」，
   于是「把一个有闸的门误判成无闸」**不会让任何锚点失败** ——
   实测正是如此：check-router-runtime-parity 有 MIN_SELFTEST_CASES=3，
   工具却报「（无）」，而 EXIT=0。
   ⇒ ★★★ **锚点只覆盖它们被写下的那个维度；没写进锚点的维度坏了没人知道。**
   只有 --worktree 模式能断言这个（HEAD 里这些闸还没落地）。 */
if (USE_WORKTREE) {
  const bad2 = []
  for (const f of ['check-hide-app-header', 'check-router-runtime-parity', 'check-runtime-data-tracked', 'check-pg-schema-scope']) {
    if (!floored.some((r) => r.file === f)) bad2.push(`锚点5 失效：${f} 在工作区里确实有 MIN_SELFTEST_CASES，却没被判成「有下限闸」`)
  }
  if (bad2.length) { console.error(`\n❌ ${bad2.join('\n❌ ')}`); process.exit(2) }
  console.log('✅ 锚点 5：四个已知「有下限闸」的门都被正确判成有闸')
}