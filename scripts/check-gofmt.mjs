// check-gofmt.mjs —— Go 格式化门禁（区分「行尾伪债」与「真实格式债」）
//
// 为什么需要这个门禁（2026-10-03 实测）
// ------------------------------------
// 本机 core.autocrlf=true 且仓库无 .gitattributes，于是 Windows 检出后
// **全部 884 个 .go 文件都是 CRLF**。而 gofmt 的规范输出是 LF，
// 于是 `gofmt -l` 会把 884/884 = 100% 的文件全部报出 —— 报出率 100%
// 本身就是一个信号：它量的不是代码格式，是行尾。
//
// 实测量化（backend/，884 个 .go 文件）：
//   · `gofmt -l` 直接跑            → 884 个（100%，全是噪声）
//   · 把内容归一化成 LF 后再 gofmt → 169 个（19.1%，真实格式债）
//   · 差额                          → 715 个（80.9%，纯行尾伪债）
//
// 那 169 个真债的成因（抽样 gofmt -d 实测）只有两类，且都可用 gofmt -w 机械修复：
//   · 结构体字段的 tab 对齐（gofmt tabwriter）
//   · 文件末尾缺换行（\ No newline at end of file）
//
// 因此门禁**不能**直接用 `gofmt -l`：那等于要求先把全仓行尾统一，
// 而行尾统一是另一件事（有 .gitattributes / renormalize / CI 差异的代价）。
// 本门禁的做法是：归一化行尾后再判 gofmt。这样 Windows 与 Linux 结论一致，
// 且信号直达真债。伪债与真债的数量在结尾一并报出，供决策。
//
// 判据自检（防止本门禁自己静默失效）
// ----------------------------------
// 上面的推理依赖一个前提：**gofmt 确实会把纯行尾差异报出来**。
// 如果哪天 gofmt 改了行为、对 CRLF 宽容了，那 `gofmt -l` 就不再是 100%，
// 而本脚本「归一化前后差集」这套算法的前提就悄悄没了。
// 所以下面 selfTest() 每次运行都实测一遍：对一个**格式完全正确**的
// CRLF 文件，gofmt -l 必须报出它。自检不过 → 直接 exit 1，而不是继续判定。

import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import { spawnSync } from 'node:child_process'

const REPO = path.resolve(import.meta.dirname, '..')
const GO_DIR = path.join(REPO, 'backend')
const GO_BIN = process.platform === 'win32' ? 'gofmt.exe' : 'gofmt'
// Windows 命令行长度上限约 8191 字符（.cmd 包装走 cmd.exe，不是 PowerShell 的 32767）。
// 每批 30 个绝对路径 ≈ 1.4 KB，留足余量。
const BATCH = 30

/** gofmt -l <dir> → 报出的文件绝对路径数组。 */
function gofmtList(dir) {
  const r = spawnSync(GO_BIN, ['-l', dir], { encoding: 'utf8' })
  if (r.error) {
    console.error(`✗ 调不动 gofmt：${r.error.message}`)
    console.error('  gofmt 随 Go 工具链分发，不在 PATH 时本门禁无法判定（不是「通过」）。')
    process.exit(1)
  }
  if (r.status !== 0) {
    console.error(`✗ gofmt -l 退出码 ${r.status}\n${r.stderr}`)
    process.exit(1)
  }
  return r.stdout.split('\n').map((s) => s.trim()).filter(Boolean)
}

function walkGo(dir, acc = []) {
  for (const e of fs.readdirSync(dir, { withFileTypes: true })) {
    if (e.name === 'vendor' || e.name === 'node_modules') continue
    const p = path.join(dir, e.name)
    if (e.isDirectory()) walkGo(p, acc)
    else if (e.name.endsWith('.go')) acc.push(p)
  }
  return acc
}

/**
 * 自检：gofmt 必须对「仅行尾不同」的 CRLF 文件报红。
 * 不过 → 前提不成立，本门禁的伪债/真债区分失去意义，判红而不是放行。
 */
function selfTest() {
  const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'gofmt-selfcheck-'))
  try {
    // 格式本身完全正确（gofmt 规范输出的样子），只是行尾是 CRLF。
    const clean = 'package p\n\nfunc F() int {\n\treturn 1\n}\n'
    const crlf = clean.replace(/\n/g, '\r\n')
    const p = path.join(tmp, 'selftest.go')
    fs.writeFileSync(p, crlf)
    const flagged = gofmtList(tmp).length > 0
    if (!flagged) {
      console.error('✗ 门禁自检失败：对格式正确、仅行尾为 CRLF 的 .go 文件，gofmt -l 没有报出。')
      console.error('  本门禁的整个前提（先报满再归一化 = 行尾噪声）已不成立，拒绝给出「通过」结论。')
      process.exit(1)
    }
    // 第二个方向：格式真的坏了（缩进错），LF 下也必须报出。
    const p2 = path.join(tmp, 'selftest_bad.go')
    fs.writeFileSync(p2, 'package p\n\nfunc F() int {\n  return 1\n}\n')
    if (gofmtList(tmp).filter((x) => x.endsWith('selftest_bad.go')).length === 0) {
      console.error('✗ 门禁自检失败：缩进错误的 LF 文件没有被 gofmt 报出，gofmt 行为异常。')
      process.exit(1)
    }
  } finally {
    fs.rmSync(tmp, { recursive: true, force: true })
  }
}

if (!fs.existsSync(GO_DIR)) {
  console.error(`✗ 找不到 backend 目录：${GO_DIR}`)
  process.exit(1)
}
selfTest()

const files = walkGo(GO_DIR)

// 阶段 1：原样（工作区行尾）跑一遍 —— 用来报出「有多少是纯行尾噪声」
const asIs = new Set(gofmtList(GO_DIR).map((p) => path.resolve(p)))

// 阶段 2：把每个文件归一化成 LF 写进影子树，再跑一遍 —— 得到真实格式债
const shadow = fs.mkdtempSync(path.join(os.tmpdir(), 'gofmt-shadow-'))
let real = []
try {
  for (const f of files) {
    const rel = path.relative(GO_DIR, f)
    const dst = path.join(shadow, rel)
    fs.mkdirSync(path.dirname(dst), { recursive: true })
    fs.writeFileSync(dst, fs.readFileSync(f, 'utf8').replace(/\r\n/g, '\n'))
  }
  real = gofmtList(shadow).map((p) => path.relative(shadow, p).split(path.sep).join('/'))
} finally {
  fs.rmSync(shadow, { recursive: true, force: true })
}

const eolOnly = files.filter((f) => {
  const rel = path.relative(GO_DIR, f).split(path.sep).join('/')
  return asIs.has(path.resolve(f)) && !real.includes(rel)
}).length

const pct = (n) => `${n} (${((n / files.length) * 100).toFixed(1)}%)`

console.log(`· backend 下 .go 文件        : ${files.length}`)
console.log(`· 原样 gofmt -l 报出        : ${asIs.size}  ← 含行尾噪声，不代表代码脏`)
console.log(`· 其中纯行尾伪债           : ${eolOnly}`)
console.log(`· 归一化后仍不 gofmt（真债）: ${real.length}  ${pct(real.length)}`)

if (process.argv.includes('--fix')) {
  // 反复 gofmt -w 直到不动点。单遍不够（见上），所以这里用循环 + 收敛判据，
  // 而不是「跑一次就宣布修完」。
  const realAbs = real.map((r) => path.join(GO_DIR, r))
  let round = 0
  let remaining = realAbs
  while (remaining.length && round < 5) {
    round++
    console.error(`  --fix 第 ${round} 轮：gofmt -w ${remaining.length} 个文件`)
    // 分批传参：一次给 gofmt 169 个绝对路径会超 Windows 命令行长度上限
    // （实测 156 个路径就报 "The syntax of the command is incorrect"）。
    //
    // 也不能图省事改成 `gofmt -w <backend 目录>`——那会把**全部 884 个**文件的
    // 行尾从 CRLF 刷成 LF，制造一个纯行尾的全仓 diff；而本轮的决定恰恰是
    // **不统一行尾**。只碰真债文件，伪债那 715 个一个都不动。
    for (let i = 0; i < remaining.length; i += BATCH) {
      const batch = remaining.slice(i, i + BATCH)
      const r = spawnSync(GO_BIN, ['-w', ...batch], { encoding: 'utf8' })
      if (r.error || r.status !== 0) {
        console.error(`✗ gofmt -w 失败：${r.error ? r.error.message : r.stderr}`)
        process.exit(1)
      }
    }    // 收敛判据：整批重算一次，而不是逐个文件调 gofmt（那会起 169 次进程）。
    const still = new Set(gofmtList(GO_DIR).map((p) => path.resolve(p)))
    remaining = remaining.filter((f) => still.has(path.resolve(f)))
  }
  console.error(`\n--fix 完成，共 ${round} 轮；请重跑本门禁确认。`)
  process.exit(remaining.length ? 1 : 0)
}

if (real.length) {
  // 控制台只列前 30 是为了不让 CI 日志爆掉，但那会让排在后面的违规文件
  // **永远不被点名** —— 实测 169 个违规里 139 个在输出中消失。
  // 「被判红但不知道自己该改哪个文件」等于没法修，所以完整清单必须落盘，
  // 且 --list 能全量打印。
  const listPath = path.join(REPO, 'logs', 'gofmt-debt.txt')
  fs.mkdirSync(path.dirname(listPath), { recursive: true })
  fs.writeFileSync(listPath, real.map((r) => `backend/${r}`).join('\n') + '\n')

  console.error(`\n✗ gofmt 门禁：${real.length} 个文件在忽略行尾后仍不符合 gofmt\n`)
  for (const r of real.slice(0, 30)) console.error(`  backend/${r}`)
  if (real.length > 30) {
    console.error(`  … 另有 ${real.length - 30} 个（完整清单见 ${path.relative(REPO, listPath)}，` +
      `或 node scripts/check-gofmt.mjs --list 全量打印）`)
  }
  console.error('\n  修法：反复 `gofmt -w <file>` 直到 `gofmt -l` 不再报它，或直接 `node scripts/check-gofmt.mjs --fix`')
  console.error('  ⚠ 单遍不够：工作区是 CRLF（core.autocrlf=true），实测一次 gofmt -w 后仍有一批')
  console.error('    文件不通过（单文件 diff 356 → 19 行），第二遍才收敛到 0。')
  console.error('    所以「gofmt -w 一次就干净了」在本机是错的，别据此判断已经修完。')
  if (process.argv.includes('--list')) {
    console.error('\n--- 全量清单 ---')
    for (const r of real) console.error(`backend/${r}`)
  }
  process.exit(1)
}

console.log('\n✓ gofmt 门禁：全部 .go 文件在忽略行尾后均符合 gofmt')
