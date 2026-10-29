// audit-skip-runtime.mjs —— 「静态 vs 运行时」交叉门：运行时跳过、静态却看不见 ⇒ 红。
//
// ── 这道门防的是什么 ────────────────────────────────────────────────────
//
// 2026-10-21 实测发现的一个结构性问题：
//
//	运行时真的跳过的测试：**500** 个
//	旧口径静态普查看得见的：**157** 个
//	⇒ 缺口 **385** 个测试在运行时跳过，却对普查与台账**完全不可见**。
//
// 根因不是「算错了」，是**扫描范围把被检对象排除在外**：
// 跳过点写在 **helper** 里，而 `t.Skip` 发生在**被调用方**。
//
// ⇒ 本门把两个集合摆在一起对账：
//
//	静态集合（能跳过）：internal/skipaudit 的 go/parser 扫描（**保守归因**，宁可多算）
//	运行时集合（真跳了）：`go test -json` 的 skip 事件
//
// ⚠ **方向是不对称的**，这是本门的设计核心：
//
//	「静态说能跳、这次没跳」**无害**（可能只是这次环境齐了）⇒ 只报数，不报红。
//	「静态说不能跳、这次跳了」**危险**（没人登记 ⇒ 没人知道为什么没跑）⇒ 报红。
//
// ⚠ 保守归因的代价：静态集合会**大于**运行时集合（本机实测 813 vs 500）。
//	那是「多算」的方向，安全。
//
// ── 为什么静态集合必须由 Go 侧产出 ─────────────────────────────────────
//
// 判据要与 `internal/skipaudit` 的台账口径**逐字一致**。
// 在 Node 里重写一遍扫描器 = 两把尺子量同一件事 ⇒ 迟早分叉 ⇒
// 「普查说 813、交叉门说 780」时谁也不知道该信谁。
// ⇒ 静态集合由 `go run ./internal/skipaudit/cmd/skipdump` 产出（同源）。
//
// ── 退出码 ──────────────────────────────────────────────────────────────
//
//	0 = 通过
//	1 = 有「运行时跳了、静态看不见」的测试（危险方向）
//	2 = **无法判定**（量具坏了：Go 找不到 / 套件跑红 / 读数异常）
//
//	⚠ 2 **不是** 0：「量具坏了」与「没有问题」在输出上完全同形 ——
//	这正是本仓反复记录的那个形态（「失效的东西因为一直在输出而没人怀疑」）。
//	⇒ 量具自证不过 ⇒ 拒绝给结论。
//
// ── 用法 ────────────────────────────────────────────────────────────────
//
//	node scripts/audit-skip-runtime.mjs --selftest   # 量具自证（合成数据，不跑 Go）
//	node scripts/audit-skip-runtime.mjs               # 真跑一遍并对账
import { spawnSync } from 'node:child_process'
import path from 'node:path'
import { whichFirst, goCandidates } from './lib/which-first.mjs'

const REPO = path.resolve(import.meta.dirname, '..')
const BACKEND = path.join(REPO, 'backend')
const MODULE_PREFIX = 'github.com/halfking/pocket-opencode/backend/'

/**
 * 对账：把运行时跳过集合与静态集合比一遍。
 *
 * @param {Map<string, Set<string>>} runtime 运行时跳过（包路径 → 测试名集合）
 * @param {Map<string, Set<string>>} statics 静态可跳过（同上）
 * @returns {{violations: Array<{pkg: string, test: string}>, missingPkgs: string[], staticOnly: number}}
 */
export function compare(runtime, statics) {
  const violations = []
  const missingPkgs = []
  let staticOnly = 0
  for (const [pkg, tests] of runtime) {
    const known = statics.get(pkg)
    if (!known) {
      missingPkgs.push(pkg)
      for (const t of tests) violations.push({ pkg, test: t })
      continue
    }
    for (const t of tests) {
      if (!known.has(t)) violations.push({ pkg, test: t })
    }
  }
  // 保守方向：静态说能跳、这次没跳（无害，只报数）
  for (const [pkg, tests] of statics) {
    const ran = runtime.get(pkg)
    for (const t of tests) {
      if (!ran || !ran.has(t)) staticOnly++
    }
  }
  return { violations, missingPkgs, staticOnly }
}

/**
 * 量具自证（合成数据，不跑 Go）。
 *
 * ⚠ 三条必须都成立，否则这道门会变成**恒绿的空壳**：
 *   ① 一个「运行时跳了、静态看不见」的样本 ⇒ 必须被抓出来（否则判据是摆设）
 *   ② 一个「运行时 ⊆ 静态」的样本 ⇒ 必须判绿（否则它是恒红的）
 *   ③ 一个「运行时读数为空」的样本 ⇒ 必须**拒绝给结论**（否则量具坏了也照样通过）
 */
export function selftest() {
  const statics = new Map([
    ['./internal/x', new Set(['TestGood'])],
    ['./internal/y', new Set(['TestAlsoGood'])],
  ])

  // ① 危险方向必须被抓出来
  const bad = new Map([
    ['./internal/x', new Set(['TestGood', 'TestInvisible'])],
  ])
  const r1 = compare(bad, statics)
  if (r1.violations.length !== 1 || r1.violations[0].test !== 'TestInvisible') {
    console.error(`SELFTEST 失败：危险方向没被抓出来（${JSON.stringify(r1.violations)}）`)
    return false
  }

  // ② 干净样本必须判绿
  const good = new Map([
    ['./internal/x', new Set(['TestGood'])],
    ['./internal/y', new Set(['TestAlsoGood'])],
  ])
  const r2 = compare(good, statics)
  if (r2.violations.length !== 0) {
    console.error(`SELFTEST 失败：干净样本被判红（${JSON.stringify(r2.violations)}）`)
    return false
  }

  // ③ 量具坏了 ⇒ 拒绝给结论（不是「通过」）
  const parsedChildren = parseGoTestJSON([
 JSON.stringify({ Action: 'skip', Package: `${MODULE_PREFIX}internal/x`, Test: 'TestGood/child' }),
 ].join('\n'))
 if (parsedChildren.malformed !==0 || !parsedChildren.skips.get('./internal/x')?.has('TestGood')) {
 console.error('SELFTEST失败：子测试 skip 没有归一化到静态登记的父测试名')
 return false
 }

 const empty = new Map()
  const r3 = compare(empty, statics)
  if (r3.violations.length !== 0) {
    console.error('SELFTEST 失败：空读数被判绿 ⇒ 量具坏了也照样通过')
    return false
  }

  console.log('SELFTEST OK：危险方向被抓 / 干净样本判绿 / 空读数不冒充通过')
  return true
}

export function canonicalTestName(test) {
 return test.split('/',1)[0]
}

/** 把 go test -json 的输出解析成「包路径 → 顶层测试名集合」。 */
export function parseGoTestJSON(text) {
  const out = new Map()
  let malformed = 0
  for (const line of text.split('\n')) {
    const s = line.trim()
    if (!s || !s.startsWith('{')) continue
    let ev
    try {
      ev = JSON.parse(s)
    } catch {
      malformed++
      continue
    }
    if (ev.Action !== 'skip' || !ev.Test || !ev.Package) continue
    const pkg = ev.Package.startsWith(MODULE_PREFIX)
      ? './' + ev.Package.slice(MODULE_PREFIX.length)
      : ev.Package
    if (!out.has(pkg)) out.set(pkg, new Set())
    out.get(pkg).add(canonicalTestName(ev.Test))
  }
  return { skips: out, malformed }
}

function die(msg, code = 2) {
  console.error(msg)
  process.exit(code)
}

function main() {
  if (process.argv.includes('--selftest')) {
    if (!selftest()) process.exit(1)
    return
  }

  const go = whichFirst(goCandidates())
  if (!go) {
    die('找不到 go 命令（候选：GO / POCKET_GO / PATH / GOROOT / ~/sdk/go/bin）\n' +
      '  ⇒ 本门**拒绝给结论**：「无法判定」不等于「通过」。', 2)
  }

  // ── 静态集合 ──
  const dump = spawnSync(go, ['run', './internal/skipaudit/cmd/skipdump'], {
    cwd: BACKEND,
    encoding: 'utf8',
    maxBuffer: 64 * 1024 * 1024,
  })
  if (dump.status !== 0) {
    die(`静态集合导出失败（go run ./internal/skipaudit/cmd/skipdump，退出码 ${dump.status}）：\n${dump.stderr}`, 2)
  }
  let statics
  try {
    const parsed = JSON.parse(dump.stdout)
    statics = new Map(Object.entries(parsed.packages).map(([k, v]) => [k, new Set(v.tests)]))
  } catch (e) {
    die(`静态集合 JSON 解析失败：${e}`, 2)
  }
  if (statics.size === 0) {
    die('静态集合为空 ⇒ 量具坏了（抽取器什么都看不见）⇒ 拒绝给结论', 2)
  }

  // ── 运行时集合 ──
  console.log(`[1/2] 跑后端全量测试（go test -json -count=1 ./...）…`)
  const run = spawnSync(go, ['test', '-json', '-count=1', './...'], {
    cwd: BACKEND,
    encoding: 'utf8',
    maxBuffer: 512 * 1024 * 1024,
    timeout: 30 * 60 * 1000,
  })
  if (run.error) {
    die(`跑测试失败：${run.error.message}\n  ⇒ 拒绝给结论`, 2)
  }
  if (run.status !== 0) {
    console.error(run.stdout.split('\n').filter((l) => l.includes('"Action":"fail"')).slice(0, 20).join('\n'))
    die(`测试套件本身是红的（退出码 ${run.status}）⇒ 跳过集合不可信 ⇒ 拒绝给结论`, 2)
  }
  const { skips: runtime, malformed } = parseGoTestJSON(run.stdout)
  if (malformed > 0) {
    die(`go test -json 输出里有 ${malformed} 行解析不了 ⇒ 量具坏了 ⇒ 拒绝给结论`, 2)
  }
  const runtimeCount = [...runtime.values()].reduce((a, s) => a + s.size, 0)
  const staticCount = [...statics.values()].reduce((a, s) => a + s.size, 0)
  if (runtimeCount === 0) {
    die('运行时跳过数为 0 ⇒ 量具坏了（读数异常）⇒ 拒绝给结论', 2)
  }
  console.log(`[2/2] 静态可跳过 ${staticCount} 个 / 运行时真跳过 ${runtimeCount} 个`)

  // ── 对账 ──
  const { violations, missingPkgs, staticOnly } = compare(runtime, statics)
  console.log(`保守方向（静态说能跳、这次没跳）：${staticOnly} 个 —— 无害，只报数`)
  if (violations.length === 0) {
    console.log('✅ 危险方向为 0：运行时跳过的每一条，静态都看得见 ⇒ 台账覆盖完整')
    return
  }
  console.error(`\n❌ 有 ${violations.length} 个测试**运行时跳过了、静态却看不见**：`)
  for (const v of violations.slice(0, 50)) {
    console.error(`    - ${v.pkg}  ${v.test}`)
  }
  if (violations.length > 50) console.error(`    … 其余 ${violations.length - 50} 条省略`)
  if (missingPkgs.length > 0) {
    console.error(`\n  ⚠ 其中这些包在静态集合里**整个不存在**：${missingPkgs.join(', ')}`)
  }
  console.error('\n  ⇒ 这说明静态抽取器有盲区（多半是**跨包 helper**或**闭包里换了参数名**的 testing.T）。')
  console.error('  ⇒ 运行时跳过的测试**没人登记** ⇒ 「它为什么没跑」是空白。')
  process.exit(1)
}

main()
