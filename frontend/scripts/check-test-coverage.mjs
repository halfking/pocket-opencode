// check-test-coverage.mjs — 孤儿测试卡口：**存在却从不被任何 gates 脚本执行的测试文件，新增即失败**。
//
// 为什么要有它（这不是洁癖，是一个已经真实发生过的回归通道）：
//   2026-10-01 查出前端 146 个测试文件里只有 32 个被某个 npm script 引用，114 个是孤儿。
//   其中 `email-cid-images.test.mjs` 里有个用例名字就叫「cid 内嵌图被解析成 data URI
//   （**用户报的「详情缺失图片」**）」——护栏就是为那条投诉写的，却一次都没跑过。
//   于是 2026-09-30「缺的是数据不是渲染」的修复可以随时静默回归，而 gates 全绿。
//   同一轮还发现 css-vars / teleport-deep / stt / email-heal 等多类护栏都是这个下场。
//   结论是**系统性问题不是单点**，所以要有卡口，否则下一轮还会漏。
//
// 4cd6e7e 已经把 `test:all` 改成 `node --test "src/**/*.test.mjs"`，覆盖到位了；但那还剩两个洞：
//   ① glob 写错 / 前缀变了 / 新测试落在 src 之外 → node **不报错**，只是安静地少跑，退出码仍是 0；
//   ② 有人把某个 script 从 gates 里摘掉（比如为了"先让 CI 绿"），那个 script 覆盖的文件立刻变孤儿，
//      而 gates 依然全绿。
// 本卡口把这两条都变成硬失败：
//   - 覆盖范围只认 **从 gates 可达**（传递闭包）的脚本，定义在别处的 script 不算数；
//   - 枚举用自己写的 glob 匹配器把每个脚本的测试 glob 展开到真实文件上，
//     任何一个 *.test.mjs 没被覆盖就失败；看起来像测试 glob 却匹配 0 个文件的也失败（静默失配探测器）。
//   - 运行时那一半（"真的被执行过、且产出用例"）由 run-mjs-tests.mjs + 普查 reporter 负责。
import { readFileSync, readdirSync, statSync, existsSync } from 'node:fs'
import { join, dirname, relative, sep } from 'node:path'
import { fileURLToPath } from 'node:url'
import { spawnSync } from 'node:child_process'

const here = dirname(fileURLToPath(import.meta.url))
const ROOT = join(here, '..')

const SKIP_DIRS = new Set(['node_modules', 'dist', 'build', 'coverage', '.vite', '.wrangler', '.git'])

// 强制卡口的测试类：.mjs 与 .ts 都必须被执行。
// （曾经以为 .ts "跑不了"——那是 4cd6e7e 的结论。2026-10-02 实测：55 个 .test.ts 里
//   54 个共 333 个用例在 node 22 下全部通过，只有 flashcardIo.test.ts 因源码无扩展名
//   import 跑不起来。333 个从不执行的测试比 91 个 .mjs 更值得接进 gates。）
//
// ★ 这份清单必须与 runner 的 `SUFFIXES` 一致，而**一致性是验出来的、不是注释保证的**
//   （2026-10-07 修，见下面的 `--print-files` 对账）。原来两边各写一份、无人核对：
//   实测只给 runner 的枚举加一类 `.test.js` ⇒ runner 真的去跑它们，
//   而本门压根不知道这一类存在 ⇒ **这一类的孤儿永远不会被报出来**，
//   也就是这道门存在的目的被绕过，而且读数是全绿。
const ENFORCED_SUFFIXES = ['.test.mjs', '.test.ts']

function walk(dir, suffix, out = []) {
  for (const name of readdirSync(dir)) {
    if (SKIP_DIRS.has(name)) continue
    const p = join(dir, name)
    if (statSync(p).isDirectory()) walk(p, suffix, out)
    else if (name.endsWith(suffix)) out.push(relative(ROOT, p).split(sep).join('/'))
  }
  return out
}

// ── glob → 正则，语义对齐 node 的 `**`（可跨 0..n 层目录），`*` 不跨 `/` ──
function globToRegExp(glob) {
  let re = ''
  for (let i = 0; i < glob.length; i++) {
    const c = glob[i]
    if (c === '*') {
      if (glob[i + 1] === '*') {
        if (glob[i + 2] === '/') {
          re += '(?:[^/]*(?:/[^/]*)*/)?' // '**/' 允许 0 层
          i += 2
        } else {
          re += '.*'
          i += 1
        }
      } else re += '[^/]*'
    } else if (c === '?') re += '[^/]'
    else re += c.replace(/[.+^${}()|[\]\\]/g, '\\$&')
  }
  return new RegExp('^' + re + '$')
}

function matchAll(glob, files) {
  const re = globToRegExp(glob)
  return files.filter((f) => re.test(f))
}

// 取出命令里的 token（尊重引号，因为 glob 在 package.json 里是带引号的）
function tokenize(cmd) {
  const out = []
  const re = /"([^"]*)"|'([^']*)'|(\S+)/g
  let m
  while ((m = re.exec(cmd))) out.push(m[1] ?? m[2] ?? m[3])
  return out
}

const pkg = JSON.parse(readFileSync(join(ROOT, 'package.json'), 'utf8'))
const scripts = pkg.scripts ?? {}

// ── 1. 从 gates 出发的传递闭包 ──
// 2026-10-03：gates 的执行方式从 package.json 里的一条 `&&` 长串改成了
// `node scripts/run-gates.mjs` + 名单在 gates.json（见 run-gates.mjs 头注释）。
// 于是**不能再只靠正则从 gates 的命令串里抠 `npm run X`**：gates 自己那一条
// 已经不含任何 npm run，闭包会退化成「只有 gates 一个脚本」，
// 而它恰好一个测试文件都不覆盖 —— 判据不会报错，只会得出「191 个全是孤儿」。
// 这就是本次实测踩到的：改完 gates 结构，孤儿卡口立刻从「0 孤儿」变「191 孤儿」。
//
// 所以种子从两处取：gates.json 的名单（现行，权威）+ gates 命令串里的 npm run（兼容旧结构）。
const GATES_LIST = join(ROOT, 'gates.json')
let gateNames = []
if (existsSync(GATES_LIST)) {
  const doc = JSON.parse(readFileSync(GATES_LIST, 'utf8'))
  if (!Array.isArray(doc.gates)) {
    console.error('❌ gates.json 里没有 gates 数组 —— 孤儿卡口无法确定哪些脚本算「gates 可达」')
    process.exit(2)
  }
  gateNames = doc.gates
  // 名单里写了一个不存在的 script 名：run-gates.mjs 会挡住真跑，但这里的结论
  // 仍会基于一份残缺的可达集合算出来。宁可当场拒绝给结论。
  const ghost = gateNames.filter((n) => typeof scripts[n] !== 'string')
  if (ghost.length) {
    console.error(`❌ gates.json 里有 ${ghost.length} 个名字在 package.json 的 scripts 里不存在：${ghost.join(', ')}`)
    process.exit(2)
  }
}

const reachable = new Set()
const queue = ['gates', ...gateNames]
while (queue.length) {
  const name = queue.pop()
  if (reachable.has(name)) continue
  reachable.add(name)
  const cmd = scripts[name]
  if (!cmd) continue
  for (const m of cmd.matchAll(/npm\s+run\s+([\w:.-]+)/g)) {
    if (!reachable.has(m[1])) queue.push(m[1])
  }
}
const unreachable = Object.keys(scripts).filter((n) => !reachable.has(n))

// ── 2. 收集覆盖来源 ──
// 2a. 枚举型 runner：它自己遍历文件系统，覆盖范围由它单点定义（--print-coverage）
const enumerators = new Set()
for (const name of reachable) {
  for (const tok of tokenize(scripts[name] ?? '')) {
    const m = tok.match(/^(?:[\w./\\-]*[\\/])?run-mjs-tests\.mjs$/)
    if (m) enumerators.add(join(ROOT, tok.replace(/^.*?(scripts[\\/])/, '$1').replace(/\//g, sep)))
  }
}

const patterns = [] // { glob, from, matched }
for (const runner of enumerators) {
  const res = spawnSync(process.execPath, [runner, '--print-coverage'], { cwd: ROOT, encoding: 'utf8' })
  if (res.status !== 0) {
    console.error(`❌ ${relative(ROOT, runner)} --print-coverage 失败：${(res.stderr || '').trim()}`)
    process.exit(2)
  }
  for (const glob of res.stdout.trim().split(/\r?\n/).filter(Boolean)) {
    patterns.push({ glob, from: relative(ROOT, runner).split(sep).join('/') })
  }
}

// 2b. 硬编码文件列表 / 内联 glob：只认 gates 可达的脚本
//     glob（带 *）与显式文件路径都收：glob 匹配不到东西时 node 不报错，显式路径写错时
//     node 会直接报错——但那要等到真跑 gates 才发现，不如在这里一次性核对。
for (const name of reachable) {
  for (const tok of tokenize(scripts[name] ?? '')) {
    if (!tok.includes('.test.mjs') && !tok.includes('.test.ts')) continue
    patterns.push({ glob: tok, from: `npm run ${name}` })
  }
}

// ── 3. 判据 ──
// 豁免清单与运行时普查共用一份：只登记"确实跑不起来"或"确实零产出"的存量，
// 每条必须写理由。豁免指向**已不存在**的文件时硬失败——否则删掉文件就等于永久免检。
const WAIVERS = JSON.parse(readFileSync(join(here, 'test-coverage-waivers.json'), 'utf8'))
const waived = { ...(WAIVERS.unrunnable ?? {}), ...(WAIVERS.zeroCase ?? {}) }

/**
 * **冻结的豁免名册**：只允许下面这 2 个 key。
 *
 * ★ 为什么必须冻结（2026-10-07 实测，不是推断）：
 *   `staleWaivers` 那一段只挡住「豁免指向的文件被删了」，
 *   **完全挡不住「新增一条豁免」**。
 *   实测：造一个孤儿测试文件 ⇒ 本门 `EXIT=1`（正确）；
 *   往 `test-coverage-waivers.json` 里加一条 ⇒ 本门 **`EXIT=0`**，
 *   新条目与两条合法条目**并排打印**，读起来像完全正常。
 *   而那份 JSON 的 `note` 自己写着「只登记存量，**不许新增**」
 *   ⇒ **注释声称的契约当时并没有被实现。**
 *
 * 修法与 `z-index-ladder` 的 ALLOWLIST 同形：把「允许存在的豁免」钉在**代码里**。
 * 代价是加一条合法豁免要连这里一起改 —— 这正是想要的阻力：
 * 它让「多一条豁免」变成一次**在代码 diff 里看得见**的动作，
 * 而不是往 JSON 里塞一行就完事。
 */
const ALLOWED_WAIVERS = new Set([
  // 2026-10-17 起为空：两条豁免都已处置（详见 test-coverage-waivers.json 的 history）。
  // ⚠ 冻结机制**保留**：往 waivers JSON 里塞一条依然会让本门 EXIT=0，
  //    所以「多一条豁免」仍然必须先在这里手写一行才合法。
])
const unapprovedWaivers = Object.keys(waived).filter((f) => !ALLOWED_WAIVERS.has(f))

/**
 * 测试文件数下限。**只能手工改常量**，不给命令行开关。
 *
 * ★ 为什么需要（2026-10-07 实测）：下面那条 `!found.length` 只挡「**全空**」。
 *   而枚举退化往往是**部分**的 —— walk 里某层 statSync 抛错被 catch 吞掉、
 *   SKIP 集合被谁加了一个目录名、某个 __tests__ 被重命名 ——
 *   结果是「从 287 个悄悄变成 30 个」，orphans 照样是空，
 *   「无孤儿 ✅」照打。**覆盖率掉了，而门说全覆盖。**
 *
 *   取 200：本仓当前 287（并行会话仍在加测试）。正常增删不会误报，
 *   而**大比例**的枚举退化会撞上。抓不住「只掉两三个」—— 那是它的边界，不假装能抓。
 */
const MIN_TEST_FILES = 200

const found = ENFORCED_SUFFIXES.flatMap((s) => walk(ROOT, s)).sort()
if (!found.length) {
  console.error(`❌ 整个 frontend/ 下没找到任何测试文件（${ENFORCED_SUFFIXES.join(' / ')}）—— 判据本身失效了，别空转绿灯。`)
  process.exit(2)
}
if (found.length < MIN_TEST_FILES) {
  console.error(`❌ 只枚举到 ${found.length} 个测试文件 < 下限 ${MIN_TEST_FILES} —— 枚举漏了东西，拒绝按「通过」处理。`)
  console.error('   上一条只挡「全空」；**部分**退化（某层 statSync 失败被吞、SKIP 被加了目录名、')
  console.error('   某个 __tests__ 被改名）会让「覆盖率掉了」而门照样打「无孤儿 ✅」。')
  console.error('   下限只能手工改上面的 MIN_TEST_FILES 常量，本门不给命令行开关。')
  process.exit(2)
}
// ── 2c. 与 runner 的**实际枚举结果**对账（2026-10-07 新增）──
//
// 覆盖判定是「glob 展开 ∩ 本门自己的清单」。而 runner 真正遍历的是它自己那份 SUFFIXES。
// 两份清单只要不一致，**不一致的那一类**就成了孤儿卡的盲区：
// 本门不认为它是测试 ⇒ 不会报它；runner 却会真的去跑它。
//
// 实测（2026-10-07）：只给 runner 的枚举加一类 `.test.js` ⇒ runner 运行时那行
// 「枚举 287 个测试文件 · 实际执行 287 个」照常打，而本门 EXIT=0 且从不提这一类
// ⇒ **这一类的孤儿永远不会被报出来**，也就是这道门存在的目的被绕过。
//
// 只改 `COVERAGE_GLOBS` 也一样坏，只是坏得「好看」：本门会收到一个新 glob，
// 展开后匹配 0 个文件 ⇒ `deadGlobs` 报红，可它让人去查路径拼写，
// 而真实原因是**后缀清单不一致**。两种坏法都指向同一个缺失：对账。
//
// 修法：runner 加 `--print-files` 把真正枚举到的文件吐出来，这里逐条比。
const runnerFiles = new Set()
for (const runner of enumerators) {
  const res = spawnSync(process.execPath, [runner, '--print-files'], { cwd: ROOT, encoding: 'utf8' })
  if (res.status !== 0) {
    console.error(`❌ ${relative(ROOT, runner)} --print-files 失败：${(res.stderr || '').trim()}`)
    process.exit(2)
  }
  for (const line of res.stdout.trim().split(/\r?\n/).filter(Boolean)) runnerFiles.add(line)
}
if (runnerFiles.size === 0) {
  console.error('❌ runner 一个测试文件都没枚举到 —— 对账无从做起，拒绝按「通过」处理。')
  process.exit(2)
}
const runnerOnly = [...runnerFiles].filter((f) => !found.includes(f)).sort()
const gateOnly = found.filter((f) => !runnerFiles.has(f)).sort()
if (runnerOnly.length || gateOnly.length) {
  console.error('❌ 本门与 runner 的测试文件清单对不上 —— 有一类测试正在被绕过：')
  if (runnerOnly.length) {
    console.error(`   runner 认、本门不认（${runnerOnly.length} 个）—— **这些文件的孤儿永远不会被报出来**：` 
      )
    for (const f of runnerOnly.slice(0, 20)) console.error(`     ${f}`)
    if (runnerOnly.length > 20) console.error(`     …还有 ${runnerOnly.length - 20} 个`)
  }
  if (gateOnly.length) {
    console.error(`   本门认、runner 不认（${gateOnly.length} 个）—— 本门在守一批 runner 根本不跑的文件：`)
    for (const f of gateOnly.slice(0, 20)) console.error(`     ${f}`)
    if (gateOnly.length > 20) console.error(`     …还有 ${gateOnly.length - 20} 个`)
  }
  console.error('   修法：让两边的后缀清单一致（本门 ENFORCED_SUFFIXES 与 run-mjs-tests.mjs 的 COVERAGE_GLOBS），然后重跑。')
  console.error('        注意这不是路径拼写问题。')
  process.exit(2)
}
console.log(`  与 runner 实际枚举结果对账：${runnerFiles.size} 个文件逐条一致`)

const staleWaivers = Object.keys(waived).filter((f) => !found.includes(f))
const enforced = found.filter((f) => !waived[f])

for (const p of patterns) p.matched = matchAll(p.glob, enforced)
const covered = new Set(patterns.flatMap((p) => p.matched))
const orphans = enforced.filter((f) => !covered.has(f))
// 静默失配：看起来是测试 glob，却一个真实文件都没匹配到
const deadGlobs = patterns.filter((p) => p.matched.length === 0)

const byExt = (list, ext) => list.filter((f) => f.endsWith(ext)).length
console.log(
  `【孤儿测试卡口】gates 可达脚本 ${reachable.size} 个 · 测试文件 ${found.length} 个` +
    `（.mjs ${byExt(found, '.test.mjs')} / .ts ${byExt(found, '.test.ts')}，其中 ${Object.keys(waived).length} 个在豁免名单）`,
)
for (const p of patterns) {
  if (!p.glob.includes('*')) continue // 显式文件路径只统计数量，逐个打印会淹没真正的信号
  const via = p.matched.length === 0 ? ' ❗零匹配' : ''
  console.log(`    ${p.glob}  ← ${p.from}（覆盖 ${p.matched.length}）${via}`)
}
const explicit = patterns.filter((p) => !p.glob.includes('*'))
const explicitMiss = explicit.filter((p) => p.matched.length === 0)
// ★ 「全部命中」必须**按读数说**，不能无条件印（2026-10-07 实测，与 §102.4 同款）。
//   原来这行是常量字符串。实测把 test:native 的一个路径改成不存在的文件：
//   本门**正确地** EXIT=1 并点名那条路径，可**同一份输出里**前面一行还印着
//   「另有 15 条显式文件路径…**全部命中**」——而且「15」正是它自己在数的那批。
//   退出码没错，但人读输出会拿到一条**指向刚失败的那组脚本**的假肯定。
//   ⇒ 失败时印实际数与缺口数，让人一眼看出是哪几条没命中。
console.log(
  `    另有 ${explicit.length} 条显式文件路径（test:native / test:stt 等分组脚本），` +
    (explicitMiss.length
      ? `**${explicit.length - explicitMiss.length} 条命中 · ${explicitMiss.length} 条没命中**`
      : '全部命中'),
)
console.log(`    被覆盖 ${covered.size} / ${enforced.length}${orphans.length ? ` · **孤儿 ${orphans.length}**` : ''}\n`)

if (unapprovedWaivers.length) {
  console.error('❌ 豁免名单里有**名册之外**的条目——「只登记存量，不许新增」是可以绕过的：')
  for (const f of unapprovedWaivers) console.error(`   ${f}`)
  console.error('   （实测：加一条豁免就能让一个孤儿测试文件静默免检，而旧版本对此 EXIT=0）\n')
  console.error('   要新增合法豁免，必须同时在两处登记，让它在代码 diff 里看得见：')
  console.error('     ① 本文件 scripts/check-test-coverage.mjs 的 ALLOWED_WAIVERS')
  console.error('     ② scripts/test-coverage-waivers.json（附理由）\n')
  process.exit(1)
}

if (staleWaivers.length) {
  console.error('❌ 豁免名单里有文件已经不存在了——这会让"删掉测试"变成永久免检，请清理：')
  for (const f of staleWaivers) console.error(`   ${f}`)
  console.error('   （文件已删 → 从 test-coverage-waivers.json 移除；文件被改名 → 改成新名字）\n')
  process.exit(1)
}

if (deadGlobs.length || explicitMiss.length) {
  console.error('❌ 以下 gates 脚本里的测试 glob / 路径一个文件都没匹配到（静默失配，node 不会报错）：')
  // 一条路径可能同时落进 deadGlobs 与 explicitMiss（它既是「像 glob」又是「零匹配」
  // 时两个集合都会收它）⇒ 直接拼起来会把**同一条**印两遍。
  // 失败清单里出现重复项，会让人以为是两个不同的缺陷而多花一轮排查。
  const seenMiss = new Set()
  for (const p of [...deadGlobs, ...explicitMiss]) {
    const k = `${p.glob} :: ${p.from}`
    if (seenMiss.has(k)) continue
    seenMiss.add(k)
    console.error(`   ${p.glob}  ← ${p.from}`)
  }
  console.error('   路径/前缀写错时 node --test 会安静地什么都不跑，退出码仍是 0。\n')
  // ★ 这里原来只打印、不退出（2026-10-07 修）。实测那个后果是**整组门被废掉而全绿**：
  //   把 `test:styles` 的 glob 改成 `src/styles/__tests2/*.test.mjs`（目录不存在）⇒
  //     ① `npm run test:styles` 实测 `# pass 0 / # fail 0`、**EXIT=0**（node 不报错）
  //     ② 本卡口**算出了**「覆盖 0 ❗零匹配」并打印 ❌，却仍 **EXIT=0** 并打出绿色 ✅
  //     ③ `run-gates` 报 36/36 全通过
  //   ⇒ 一整组门彻底空跑，而整条门禁链没有任何一处会红。
  //   覆盖没掉是因为 `src/**` 枚举仍然覆盖着这些文件 —— **「无孤儿」与「这一组真的在跑」是两件事。**
  console.error('   要新增/改名测试文件后，请同步更新 gates 可达脚本里的路径或 glob。\n')
  process.exit(1)
}

if (orphans.length) {
  console.error('❌ 以下测试文件从未被任何 gates 可达的 npm script 执行：')
  for (const f of orphans) console.error(`   ${f}`)
  console.error('\n两种可能，选一种：① 把它接进 gates（推荐：glob 或 --print-coverage 声明覆盖）；')
  console.error('② 它其实不需要存在，删掉。')
  console.error('注意「写在某个没接进 gates 的 script 里」不算数——没人跑它等于没有护栏。\n')
  process.exit(1)
}

for (const [f, why] of Object.entries(waived)) {
  console.log(`   豁免 ${f}\n      ${why}`)
}
console.log(`\n✅ 无孤儿测试文件（gates 可达脚本 ${reachable.size} 个，覆盖 ${covered.size}/${enforced.length}）`)
if (unreachable.length) {
  console.log(`   （定义在 gates 之外、因此不计入覆盖的 script：${unreachable.join(', ')}）`)
}
process.exit(0)
