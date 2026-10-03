// run-gates.mjs —— npm run gates 的执行者。名单在 gates.json，顺序即执行顺序，失败即停。
//
// 为什么要有这一层
// ----------------
// 过去 gates 是 package.json 里一条 1000+ 字符的单行长字符串：
//
//   "gates": "npm run typecheck && npm run build:gate && ... && npm run check:test-coverage"
//
// 代价在 2026-10-03 第一次真的结账：两个分支各自往**同一行末尾**追加一个门禁
//（round29 追加 check:callback-routes / check:edge-route-reach，
//  round28 追加 check:runtime-data），merge 时在同一行炸掉，冲突上下文长到没法读。
// 也就是说「每加一个门禁都要手改这一行」不只是难改，它是一个**必然发生的冲突源**。
//
// 改法不是「把冲突解掉」而是「让这类冲突不再存在」：
//   · package.json 里的 gates 值恒为 `node scripts/run-gates.mjs`（各分支一致 ⇒ 不可能冲突）；
//   · 名单独立成 gates.json 的数组，一行一个，git 能正常合并。
// 以后加门禁 = gates.json 加一行 + package.json 加一个 script 条目，两处都不与别人抢同一行。
//
// 这个 runner 自己也要防空跑
// ------------------------
// 「全绿」有两种来源：门禁真的过了，和门禁根本没跑。本脚本在开跑前把下面每一种
// 「静默什么都没做」的情况都变成非 0 退出码：
//   1. gates.json 读不到 / 不是数组 / 为空 / 条目少得不像话（低于 MIN_GATES）；
//   2. 名单里有 package.json 里不存在的 script 名（拼错、被改名、写了个从没接线的门禁）；
//   3. 名单里有重复项（同一门禁跑两遍，第二遍必然是绿的假象）；
//   4. package.json 里新增了 check:* 门禁，但既不在 gates 也不在 notGates 里
//      —— 也就是「加了门禁却忘了接进门槛」，本仓已因此让 114 个测试文件当过孤儿；
//   5. 任何一条门禁以非 0 退出（含护栏自用的 3/4 —— 那是「拒绝给结论」，不是「通过」）。
//
// 用法
// ----
//   node scripts/run-gates.mjs              跑全部门禁
//   node scripts/run-gates.mjs --list       只打印名单与接线核对结果，不执行
//   node scripts/run-gates.mjs --only <名>  只跑指定门禁（可重复），用于定位
//   node scripts/run-gates.mjs --ci          跑 gates.json 的 ciRuns 名单（CI 专用，见规则 5）
//   node scripts/run-gates.mjs --list       追加打印本地/CI 的分工核对结果

import { spawnSync } from 'node:child_process'
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

const FRONTEND = join(dirname(fileURLToPath(import.meta.url)), '..')
const PKG_PATH = join(FRONTEND, 'package.json')
const LIST_PATH = join(FRONTEND, 'gates.json')

// 名单被截断成个位数时，下面的核对几乎必然还会报别的错；但先在这里挡住，
// 免得「只跑 3 条」被当成「门槛变小了」而静默通过。
const MIN_GATES = 10

function die(msg, code = 2) {
  console.error(`[gates] ❌ ${msg}`)
  process.exit(code)
}

function loadPkg() {
  try {
    return JSON.parse(readFileSync(PKG_PATH, 'utf8'))
  } catch (e) {
    die(`无法解析 package.json：${e.message}`)
  }
}

function loadList() {
  let raw
  try {
    raw = readFileSync(LIST_PATH, 'utf8')
  } catch (e) {
    die(`读不到 gates.json（${LIST_PATH}）：${e.message}`)
  }
  let doc
  try {
    doc = JSON.parse(raw)
  } catch (e) {
    die(`gates.json 不是合法 JSON：${e.message}`)
  }
  if (!Array.isArray(doc.gates)) die('gates.json 里没有 gates 数组')
  return doc
}

const pkg = loadPkg()
const scripts = pkg.scripts || {}
const doc = loadList()
const list = doc.gates
const notGates = doc.notGates || {}

// ---- 防空跑第 1 类：名单本身可疑 ----------------------------------------
if (list.length === 0) die('gates 数组是空的 —— 这样「全绿」不证明任何事')
if (list.length < MIN_GATES)
  die(`gates 只有 ${list.length} 项，低于下限 ${MIN_GATES}：名单被截断了？`)

// ---- 防空跑第 3 类：重复项 ----------------------------------------------
const dup = list.filter((n, i) => list.indexOf(n) !== i)
if (dup.length) die(`gates 里有重复项：${[...new Set(dup)].join(', ')}（第二遍必然是假绿）`)

// ---- 防空跑第 2 类：名单指向不存在的 script ------------------------------
const missing = list.filter((n) => typeof scripts[n] !== 'string')
if (missing.length)
  die(
    `gates 里有 ${missing.length} 个名字在 package.json 的 scripts 里不存在：\n` +
      missing.map((n) => `  - ${n}`).join('\n') +
      '\n（拼错了，还是加了门禁却没接上 npm script？）',
  )

// ---- 防空跑第 4 类：新加的 check:* 忘了接线 -----------------------------
const CHECK_RE = /^check:/
const declaredChecks = Object.keys(scripts).filter((k) => CHECK_RE.test(k))
const unhooked = declaredChecks.filter((n) => !list.includes(n) && !(n in notGates))
if (unhooked.length)
  die(
    `package.json 里有 ${unhooked.length} 个 check:* 门禁既不在 gates、也不在 notGates：\n` +
      unhooked.map((n) => `  - ${n}`).join('\n') +
      '\n要接进门槛就加到 gates.json 的 gates；要刻意不接就在 notGates 里写明理由。',
  )

// ---- 防空跑第 5 类：新加的门禁没接进 CI -------------------------------------
// 2026-10-03 实测：CI 的 frontend.yml 是逐条手列命令，而 23 条 gates 里当时只有
// check:test-coverage 与 check:crlf-needles 进了 CI，其余只在开发者本机跑。
// 护栏存在但没人执行，等于没有护栏 —— 与「146 个测试文件只有 32 个被引用」同一类事故。
// 修法不是把 12 个名字硬写进 workflow（那样每加一条门禁又要人记得同步，漂移照旧），
// 而是让 CI 侧也变成**数据**：gates.json 的 ciRuns 是「本机之外由 CI 跑」的名单，
// ciCoveredElsewhere 是「已在 workflow 里手列、因此不重复跑」及其理由。
// 于是「新加一条 check:* 却没接进 CI」从静默变成退出码非 0。
const ciRuns = Array.isArray(doc.ciRuns) ? doc.ciRuns : []
const ciElsewhere = doc.ciCoveredElsewhere || {}
const ciUnknown = ciRuns.filter((n) => !list.includes(n))
if (ciUnknown.length)
  die(
    `ciRuns 里有 ${ciUnknown.length} 个名字不在 gates 里（改名或已删？）：\n` +
      ciUnknown.map((n) => `  - ${n}`).join('\n'),
  )
const ciDup = ciRuns.filter((n, i) => ciRuns.indexOf(n) !== i)
if (ciDup.length) die(`ciRuns 里有重复项：${[...new Set(ciDup)].join(', ')}`)
const ciOrphanElsewhere = Object.keys(ciElsewhere).filter((n) => !list.includes(n))
if (ciOrphanElsewhere.length)
  die(
    `ciCoveredElsewhere 里有 ${ciOrphanElsewhere.length} 个名字已不在 gates 里（应删掉该理由）：\n` +
      ciOrphanElsewhere.map((n) => `  - ${n}`).join('\n'),
  )
const unhookedCi = list.filter((n) => !ciRuns.includes(n) && !(n in ciElsewhere))
if (unhookedCi.length)
  die(
    `gates 里有 ${unhookedCi.length} 条既不在 ciRuns、也不在 ciCoveredElsewhere —— CI 不会跑它们：\n` +
      unhookedCi.map((n) => `  - ${n}`).join('\n') +
      '\n要 CI 跑就加进 ciRuns（CI 侧无需改 workflow）；已在 workflow 手列就写进 ciCoveredElsewhere 并说明理由。',
  )
const ciDupElsewhere = list.filter(
  (n) => ciRuns.includes(n) && n in ciElsewhere,
)
if (ciDupElsewhere.length)
  die(`这些门禁同时出现在 ciRuns 与 ciCoveredElsewhere，CI 会跑两遍：${ciDupElsewhere.join(', ')}`)

// ---- 命令行 --------------------------------------------------------------
const argv = process.argv.slice(2)
const wantsList = argv.includes('--list')
const only = []
for (let i = 0; i < argv.length; i += 1) if (argv[i] === '--only') only.push(argv[i + 1])

// --ci：跑 ciRuns 名单。它是 --only 的数据驱动版本，所以 CI 侧不需要同步任何名字。
const wantsCi = argv.includes('--ci')
if (wantsCi) for (const n of ciRuns) only.push(n)

const runList = only.length ? list.filter((n) => only.includes(n)) : list
if (only.length) {
  const unknown = only.filter((n) => !list.includes(n))
  if (unknown.length) die(`--only 给了不在 gates 里的名字：${unknown.join(', ')}`)
}

// npm 在 Windows 上是 .cmd 包一层，直接 spawnSync('npm') 会 ENOENT。
const NPM = process.platform === 'win32' ? 'npm.cmd' : 'npm'

if (wantsList) {
  console.log(`[gates] CI 分工：--ci 跑 ${ciRuns.length} 条，另有 ${Object.keys(ciElsewhere).length} 条在 workflow 里手列`)
  for (const n of ciRuns) console.log(`  ci        ${n}`)
  for (const n of Object.keys(ciElsewhere)) console.log(`  elsewhere ${n} —— ${ciElsewhere[n]}`)
  console.log(`[gates] 名单 ${list.length} 项，接线核对通过（无重复 / 无悬空 / 无未接线的 check:*）`)
  list.forEach((n, i) => console.log(`  ${String(i + 1).padStart(2)}. ${n}`))
  const excluded = Object.keys(notGates)
  if (excluded.length) {
    console.log(`\n[gates] 刻意不接进 gates 的 check:* ${excluded.length} 个：`)
    for (const n of excluded) console.log(`  - ${n}`)
  }
  process.exit(0)
}

console.log(`[gates] 开始执行 ${runList.length} 项门禁（顺序见 gates.json，失败即停）`)
const t0 = Date.now()
const passed = []
for (const [i, name] of runList.entries()) {
  const n = i + 1
  console.log(`\n[gates] ── [${n}/${runList.length}] ${name} ──`)
  const r = spawnSync(NPM, ['run', name], { cwd: FRONTEND, stdio: 'inherit', shell: process.platform === 'win32' })
  if (r.error) die(`${name} 启动失败：${r.error.message}`)
  if (r.status !== 0) {
    // 保留原始退出码：护栏常用 3/4 表示「拒绝给结论」（防空跑 / 提取不到被检查对象），
    // 那是比 1 更需要被人看见的信号，不该被抹平成 1。
    const code = typeof r.status === 'number' && r.status !== 0 ? r.status : 1
    console.error(
      `\n[gates] ❌ 第 ${n} 项 ${name} 失败，退出码 ${code}；` +
        `已通过 ${passed.length}/${runList.length} 项，用时 ${((Date.now() - t0) / 1000).toFixed(1)}s`,
    )
    if (code >= 3)
      console.error('[gates] 注意：退出码 ≥3 通常是护栏在「拒绝给结论」（没扫到被检查对象），不是普通断言失败。')
    process.exit(code)
  }
  passed.push(name)
}

console.log(
  `\n[gates] ✅ 全部 ${runList.length} 项通过，用时 ${((Date.now() - t0) / 1000).toFixed(1)}s：` +
    passed.join(' → '),
)
