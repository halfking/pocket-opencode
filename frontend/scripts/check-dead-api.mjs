// check-dead-api.mjs — 「死能力」卡口：导出了但**从无调用方**的 API 客户端。
//
// 为什么要有它：本轮查「本地资产为什么不同步到云端」时发现——
//   - 后端 POST /api/assets/sync **已注册**（server.go:770，server_lobster.go 有完整 handler）
//   - 本地 assetStore 有 listDirty() / markSynced()
//   - API 客户端 assetsApi.sync() **也写好了**（api/assets.ts:47）
//   - 但 assetsApi 除了定义处**没有任何调用方**；编排函数 syncAssets() 连定义都没有
//     （只存在于注释里，还标着「待 F0.4 接入」）
// ⇒ 整条同步链路是**已知未实现**，不是回归。
//
// 这类「能力写好了却没人接线」的问题最难被发现：编译通过、类型通过、gates 全绿，
// 运行时也完全正常——只是那件事**从来没发生过**。
// 静态审计（audit-*) 负责报，check-* 负责卡口，职责与 audit/check-viewmodel-gaps 一致。
//
// 做法是**棘轮**而不是要求清零：api/vault.ts 这类历史死代码已经存在，
// 一次删干净属于另一个 PR 的事；本卡口保证**不再新增**，并把存量钉成可核对的基线。
import { readFileSync, readdirSync, statSync, existsSync, writeFileSync } from 'node:fs'
import { join, dirname, relative } from 'node:path'
import { fileURLToPath } from 'node:url'

const here = dirname(fileURLToPath(import.meta.url))
const srcRoot = join(here, '..', 'src')
const apiDir = join(srcRoot, 'api')
const BASELINE = join(here, 'dead-api-baseline.json')

function walk(dir, out = []) {
  for (const n of readdirSync(dir)) {
    const p = join(dir, n)
    if (statSync(p).isDirectory()) walk(p, out)
    else if (['.ts', '.vue', '.js', '.mjs'].includes(p.slice(p.lastIndexOf('.')))) out.push(p)
  }
  return out
}

const all = walk(srcRoot)

// 1) 收集 api/ 下每个模块导出的符号
const apiFiles = readdirSync(apiDir).filter((f) => f.endsWith('.ts') && !f.includes('__tests__'))
/** @type {Map<string, {file: string, names: string[]}>} */
const exportsByFile = new Map()
for (const f of apiFiles) {
  const text = readFileSync(join(apiDir, f), 'utf8')
  const names = new Set()
  for (const m of text.matchAll(/export\s+(?:const|function|async\s+function|class)\s+([A-Za-z_$][\w$]*)/g)) names.add(m[1])
  for (const m of text.matchAll(/export\s*\{([^}]+)\}/g)) {
    for (const part of m[1].split(',')) {
      const nm = part.trim().split(/\s+as\s+/).pop()?.trim()
      if (nm && /^[A-Za-z_$][\w$]*$/.test(nm)) names.add(nm)
    }
  }
  if (names.size) exportsByFile.set(f, { file: f, names: [...names] })
}

// 2) 在 api/ 之外的所有文件里找这些符号的使用（import 或直接引用）
//    区分两种「没人用」：只被 __tests__ 引用 vs 连测试都没有。
//    前者说明能力被测过但**没接进 App**，后者是彻底的死代码——两者处置方式不同。
const isTest = (p) => p.includes('__tests__') || /\.(test|spec)\.[tj]s$/.test(p)
// api/ 内的非测试文件不能算「使用自己的导出」；但 api/ 内的**测试文件**要算，
// 否则会把「仅被测试引用」误判成「完全无引用」——第一版就栽在这：
// api/__tests__/reconnectPolicy.test.ts 明明 import 了 nextReconnectDelay /
// RECONNECT_BASE_MS / RECONNECT_MAX_MS，却被过滤掉、连带产出假阳性基线。
const consumers = all.filter((p) => (p.startsWith(apiDir) ? isTest(p) : true))
/** @type {Set<string>} */
const used = new Set()
/** @type {Set<string>} */
const usedByTestOnly = new Set()
for (const p of consumers) {
  const text = readFileSync(p, 'utf8')
  for (const [, sym] of exportsByFile) {
    for (const name of sym.names) {
      if (!new RegExp(`\\b${name}\\b`).test(text)) continue
      const key = `${sym.file}:${name}`
      if (isTest(p)) usedByTestOnly.add(key)
      else used.add(key)
    }
  }
}

const dead = []
const testOnly = []
for (const [, info] of exportsByFile) {
  for (const name of info.names) {
    const key = `${info.file}:${name}`
    if (used.has(key)) continue
    if (usedByTestOnly.has(key)) testOnly.push({ file: info.file, symbol: name })
    else dead.push({ file: info.file, symbol: name })
  }
}
dead.sort((a, b) => (a.file + a.symbol).localeCompare(b.file + b.symbol))
testOnly.sort((a, b) => (a.file + a.symbol).localeCompare(b.file + b.symbol))

const totalSyms = [...exportsByFile.values()].reduce((n, s) => n + s.names.length, 0)
console.log(`【死能力卡口】api/ 下 ${apiFiles.length} 个模块，导出 ${totalSyms} 个符号`)
console.log(`完全无引用 ${dead.length} 个；仅被 __tests__ 引用（有能力但没接进 App）${testOnly.length} 个\n`)

const byFile = {}
for (const d of dead) (byFile[d.file] ||= []).push(d.symbol)
for (const [f, names] of Object.entries(byFile)) {
  console.log(`  ❌ ${f.padEnd(22)} ${names.join(', ')}`)
}
if (testOnly.length) {
  const byFile2 = {}
  for (const d of testOnly) (byFile2[d.file] ||= []).push(d.symbol)
  for (const [f, names] of Object.entries(byFile2)) {
    console.log(`  ⚠️  ${f.padEnd(22)} ${names.join(', ')}   (仅测试引用)`)
  }
}
if (!dead.length && !testOnly.length) console.log('  （无）')

// ---- 棘轮 ----
let baseline = { note: '允许存在的「导出了但无人调用」符号；只许减少不许增加。', dead: [] }
if (existsSync(BASELINE)) {
  try {
    baseline = JSON.parse(readFileSync(BASELINE, 'utf8'))
  } catch (e) {
    console.error(`❌ 基线解析失败：${BASELINE}\n   ${e.message}`)
    process.exit(2)
  }
}
const cur = dead.map((d) => `${d.file}:${d.symbol}`).sort()

if (process.argv.includes('--update-baseline')) {
  writeFileSync(BASELINE, JSON.stringify({ ...baseline, dead: cur }, null, 2) + '\n', 'utf8')
  console.log(`\n✅ 基线已更新（${cur.length} 条）`)
  process.exit(0)
}

const baseSet = new Set(baseline.dead || [])
const added = cur.filter((x) => !baseSet.has(x))
const removed = (baseline.dead || []).filter((x) => !cur.includes(x))

if (added.length) {
  console.error()
  for (const a of added) console.error(`❌ 新增死能力：${a} —— 导出了却没有任何调用方。`)
  console.error()
  console.error('两种可能，选一种：① 真接上去（写调用方）；② 确实是死代码，删掉导出。')
  console.error('若是有意保留的历史债，跑 --update-baseline 把新基线落盘，并在注释里写明为什么不接。')
  process.exit(1)
}
if (removed.length) {
  console.log()
  for (const r of removed) console.log(`⤵️ 已清掉的死能力：${r} —— 确认后跑 --update-baseline 落盘。`)
}
console.log(`\n✅ 死能力未新增（棘轮通过，基线 ${baseSet.size} 条）`)
process.exit(0)
