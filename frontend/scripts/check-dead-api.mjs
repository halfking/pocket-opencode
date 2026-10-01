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

// ── 分类必须分四类，不能只分「死/活」──
// 第二版把整个 api/ 排除出消费者，犯了个**严重假阳性**：
//   api/websocket.ts:2  import { nextReconnectDelay } from './reconnectPolicy'
//   api/websocket.ts:77 const delay = nextReconnectDelay(this.reconnectAttempts)
// 退避策略**确实接进了 App**，却被判成「仅测试引用」。
//   同理 RECONNECT_FACTOR/JITTER 在 reconnectPolicy.ts:15/18 被**本模块自己**使用，
//   也被判成「完全无引用」。
// ⇒ 同包内其它模块的引用、以及模块内部的引用，都是**真实使用**，必须计入。
//
// 四类：
//   wired          被本文件以外的**非测试**文件引用 ⇒ 已接进 App
//   testOnly       只被测试引用（能力被测过但没接进 App）
//   moduleInternal 只在本模块体内被引用（常量/内部 helper，外部拿不到）
//   dead           哪都没被引用 —— 这才是「写好了没人接线」，棘轮管的就是它
// ── 计数必须在**剥掉注释**的源码上做 ──
// 第三版又踩了同一个坑的变体：assets.ts 的文件头注释里写着「调 assetsApi.sync() 上传」，
// 于是 `assetsApi` 被算成「模块内部使用」——**注释不是使用**。
// 这和 i18n 卡口那个「统计未排除注释行」的坑是同一类，
// 说明「计数类判据」几乎一定要显式处理注释，否则基线从第一天就是错的。
function stripComments(src) {
  return src
    .replace(/\/\*[\s\S]*?\*\//g, ' ')   // 块注释
    .replace(/(^|[^:])\/\/[^\n]*/g, '$1') // 行注释（避开 http:// 里的 //）
}

function countRefs(sym, files) {
  const re = new RegExp(`\\b${sym}\\b`, 'g')
  let app = 0
  let test = 0
  for (const p of files) {
    const text = stripComments(readFileSync(p, 'utf8'))
    if (!re.test(text)) continue
    re.lastIndex = 0
    let m
    while ((m = re.exec(text))) {
      if (isTest(p)) test++
      else app++
    }
  }
  return { app, test }
}

const classified = []
for (const [, info] of exportsByFile) {
  const own = join(apiDir, info.file)
  const ownText = stripComments(readFileSync(own, 'utf8'))
  for (const name of info.names) {
    const re = new RegExp(`\\b${name}\\b`, 'g')
    // 模块内出现次数减去「导出声明」那一次
    let ownRefs = (ownText.match(re) || []).length
    if (new RegExp(`export\\s+(?:const|function|async\\s+function|class)\\s+${name}\\b`).test(ownText)) ownRefs--
    if (new RegExp(`export\\s*\\{[^}]*\\b${name}\\b`).test(ownText)) ownRefs = Math.max(0, ownRefs - 1)

    const others = all.filter((p) => p !== own)
    const { app, test } = countRefs(name, others)

    let kind
    if (app > 0) kind = 'wired'
    else if (test > 0) kind = 'testOnly'
    else if (ownRefs > 0) kind = 'moduleInternal'
    else kind = 'dead'
    classified.push({ file: info.file, symbol: name, kind, app, test, ownRefs })
  }
}

const of = (k) => classified.filter((c) => c.kind === k).sort((a, b) => (a.file + a.symbol).localeCompare(b.file + b.symbol))
const dead = of('dead')
const testOnly = of('testOnly')
const moduleInternal = of('moduleInternal')
const wired = of('wired')

const totalSyms = classified.length
console.log(`【死能力卡口】api/ 下 ${apiFiles.length} 个模块，导出 ${totalSyms} 个符号`)
console.log(
  `已接进 App ${wired.length} · 仅测试引用 ${testOnly.length} · 仅模块内部使用 ${moduleInternal.length} · ` +
    `**完全无人使用 ${dead.length}**\n`,
)

const group = (title, list, mark) => {
  if (!list.length) return
  const byFile = {}
  for (const d of list) (byFile[d.file] ||= []).push(d.symbol)
  console.log(`  ${mark} ${title}`)
  for (const [f, names] of Object.entries(byFile)) console.log(`      ${f.padEnd(22)} ${names.join(', ')}`)
}
group('完全无人使用（棘轮管的就是这批）', dead, '❌')
group('仅被 __tests__ 引用：能力被测过，但没接进 App', testOnly, '⚠️ ')
group('仅本模块内部使用：外部拿不到，常量/内部 helper', moduleInternal, 'ℹ️ ')

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
