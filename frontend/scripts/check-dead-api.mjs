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
import { classifyRef, CLASS_BUCKETS } from './dead-api-classify.mjs'

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

/** 剥注释后的源码里，符号出现了几次。 */
function refsInText(sym, text) {
  return (stripComments(text).match(new RegExp(`\\b${sym}\\b`, 'g')) || []).length
}

/**
 * 单个符号的完整判定。抽出来是为了让**自检能走同一条路径**——
 * 自检若另写一份简化判定，它验证的就不是主流程真正在用的那段代码。
 *
 * ⚠️ `others` 收的是**路径**，不是「是不是测试」的布尔值（2026-10-08 变异 D4 抓到的自检缺陷）：
 *   第一版让调用方先算好 `isTest(p)` 再传进来，结果合成夹具**永远喂不进 isTest 函数**——
 *   而 isTest 正是本门最容易被放宽的一环（`p.includes('__tests__')` 写错一个字就全算测试）。
 *   那样自检看起来 8/8 通过，真实把 isTest 放宽成 `() => true` 时门照样绿：
 *   `app` 恒为 0 ⇒ 所有已接进 App 的符号被标成「⚠️ 仅被 __tests__ 引用：没接进 App」，
 *   正是 dead-api-classify.mjs 文件头记的「**说谎**，而且带 ⚠️，会让人去接线一个早就接好的东西」。
 *   ⇒ 必须把**路径**传进来，让 isTest 在函数内部被真正调用。
 *
 * @param {{name:string, ownText:string, others:{path:string, text:string}[]}} a
 */
function classifySymbol({ name, ownText, others }) {
  // 模块内出现次数减去「导出声明」那一次
  let ownRefs = refsInText(name, ownText)
  const viaDecl = new RegExp(`export\\s+(?:const|function|async\\s+function|class)\\s+${name}\\b`).test(ownText)
  const viaList = new RegExp(`export\\s*\\{[^}]*\\b${name}\\b`).test(ownText)
  if (viaDecl) ownRefs--
  if (viaList) {
    ownRefs = Math.max(0, ownRefs - 1)
    // ★ 裸声明也要扣（2026-10-08 自检 ② 当场抓到的漏报）。
    //   `function FOO(){}` + `export { FOO }` 这种写法里，「导出条目」被扣了，
    //   但**声明本身**不是使用——它只是把符号定义出来，没人调它。
    //   漏掉这一扣 ⇒ ownRefs 恒 ≥ 1 ⇒ 一个纯死能力被判成 moduleInternal，
    //   **棘轮就看不见它**（moduleInternal 不在棘轮管的那一桶里）。
    //   而且那个桶的本义是「外部拿不到」，`export { FOO }` 恰恰说明外部拿得到。
    //   已在 api/ 实测：4 处 `export { ... }` 共 5 个符号当前全是 wired，
    //   所以这是**潜伏漏报**，修它不动基线（8 条不变）。
    //   viaDecl 为真时不能再扣——那一次已经扣过了（export const FOO = 1 是同一次出现）。
    if (!viaDecl) ownRefs = Math.max(0, ownRefs - 1)
  }

  let app = 0
  let test = 0
  for (const o of others) {
    const n = refsInText(name, o.text)
    if (isTest(o.path)) test += n
    else app += n
  }
  return { app, test, ownRefs, kind: classifyRef({ app, test, ownRefs }) }
}

const classified = []
for (const [, info] of exportsByFile) {
  const own = join(apiDir, info.file)
  const ownText = stripComments(readFileSync(own, 'utf8'))
  const others = all.filter((p) => p !== own).map((p) => ({ path: p, text: readFileSync(p, 'utf8') }))
  for (const name of info.names) {
    classified.push({ file: info.file, symbol: name, ...classifySymbol({ name, ownText, others }) })
  }
}

// ---- 检测链自检 ----
//
// ★ 为什么要加（2026-10-08 实测，不是推断）：
//   本门原先只有一个「检出为空」的闸，它只挡**全塌成 0**。
//   实测**部分塌陷**照样绿灯：把剥块注释的正则一个字符写错
//   （`[\s\S]` → `[\sS]`，极常见的手误）⇒ 注释里提到的符号被算成使用
//   ⇒ 「完全无人使用」从 **8 条掉到 7 条** ⇒ 本门 EXIT=0 并打出
//   「⤵️ 基线里有、本次未检出：assets.ts:assetsApi —— 若确已清理，跑 --update-baseline 落盘」。
//   照着这句话跑 --update-baseline，实测基线从 8 条写成 7 条，
//   `assetsApi` 从此**不再被棘轮追踪** —— 记录动作把债抹了。
//   ⇒ 8→7 这种塌陷必须由**合成夹具**来抓：它不需要基线非空，也不需要塌到 0。
//
// 八条用例逐条对应本文件头里记下的一个真实踩坑。期望值都是**逐条手算**出来的，
// 不是跑一遍抄输出（那会变成「断言现状」，代码怎么错都对）。
// `others[].path` 是**真路径**：isTest 在 classifySymbol 内部按它判定，
// 所以「__tests__ 目录」与「.test 后缀」两个分支各自都有用例钉住（⑤⑥⑨⑩）。
const OWN_DECL = 'export const FOO = 1\n'
const TEST_DIR = 'src/api/__tests__/x.test.mjs' // isTest 的「目录」分支
const TEST_SUF = 'src/features/notes/n.test.ts' // isTest 的「后缀」分支（目录名不含 __tests__）
const APP_FILE = 'src/views/HomeView.vue'
const USE = "import {FOO} from './x'\nFOO()"
const SELF_TEST = [
  {
    name: '① 只有导出声明 ⇒ 死（§70：漏掉这次扣减就全塌成 moduleInternal）',
    a: { name: 'FOO', ownText: OWN_DECL, others: [] },
    want: 'dead',
  },
  {
    name: '② export { FOO } 形式，只有声明本身 ⇒ 也应是死',
    a: { name: 'FOO', ownText: 'const FOO = 1\nexport { FOO }\n', others: [] },
    want: 'dead',
  },
  {
    name: '③ 模块体内真的用了 ⇒ moduleInternal',
    a: { name: 'FOO', ownText: OWN_DECL + 'export const BAR = () => FOO\n', others: [] },
    want: 'moduleInternal',
  },
  {
    name: '④ 模块内用了 + 测试也用了 ⇒ moduleInternal（不是 testOnly）',
    a: { name: 'FOO', ownText: OWN_DECL + 'export const BAR = () => FOO\n', others: [{ path: TEST_DIR, text: USE }] },
    want: 'moduleInternal',
  },
  {
    name: '⑤ 只被 __tests__ 目录下的文件引用 ⇒ testOnly',
    a: { name: 'FOO', ownText: OWN_DECL, others: [{ path: TEST_DIR, text: USE }] },
    want: 'testOnly',
  },
  {
    name: '⑥ 被非测试文件引用 ⇒ wired',
    a: { name: 'FOO', ownText: OWN_DECL, others: [{ path: APP_FILE, text: USE }] },
    want: 'wired',
  },
  {
    name: '⑦ 本模块注释里提到 FOO **不算使用** ⇒ 仍是死',
    a: { name: 'FOO', ownText: '// TODO: 调用 FOO 上传\n' + OWN_DECL, others: [] },
    want: 'dead',
  },
  {
    name: '⑧ 别的文件的**块注释**里提到 FOO 不算使用 ⇒ 仍是死',
    // ⚠️ 第一版这里写的是「注释里提到 assetsApi」，而符号是 FOO——
    //   符号压根没出现在注释里，所以**无论剥不剥注释都恒过**，
    //   于是一条变异（D1：块注释正则 `[\s\S]` 写成 `[\sS]`）打穿了 8/8 通过的自检。
    //   这就是「夹具里被测对象压根没出现」⇒ 判据恒真。
    a: {
      name: 'FOO',
      ownText: OWN_DECL,
      others: [{ path: APP_FILE, text: '/** TODO: 在 store 里调用 FOO() 上传 */\nconst z = 1\n' }],
    },
    want: 'dead',
  },
  {
    name: '⑨ isTest 的「.test 后缀」分支：目录名不含 __tests__ 也算测试 ⇒ testOnly',
    a: { name: 'FOO', ownText: OWN_DECL, others: [{ path: TEST_SUF, text: USE }] },
    want: 'testOnly',
  },
  {
    name: '⑩ 本模块用了 + App 也用了 ⇒ wired（app 优先于 moduleInternal）',
    a: {
      name: 'FOO',
      ownText: OWN_DECL + 'export const BAR = () => FOO\n',
      others: [{ path: APP_FILE, text: USE }],
    },
    want: 'wired',
  },
]
{
  const fails = []
  for (const c of SELF_TEST) {
    const got = classifySymbol(c.a).kind
    if (got !== c.want) fails.push(`检测链自检「${c.name}」不符：期望 ${c.want}，实际 ${got}`)
    else console.log(`  检测链自检：${c.name} ✅（${got}）`)
  }
  if (fails.length) {
    console.error('\n❌ 检测链自检未通过——本门此刻对「死能力」的任何结论都不可信：')
    for (const f of fails) console.error(`   ${f}`)
    console.error('\n  典型成因：剥注释逻辑回归、导出声明扣减被删、isTest 过滤放宽、分类顺序被换。')
    console.error('  修探测链，别去动基线。')
    process.exit(3)
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
group(CLASS_BUCKETS.find((b) => b.kind === 'dead').title, dead, CLASS_BUCKETS.find((b) => b.kind === 'dead').mark)
group(CLASS_BUCKETS.find((b) => b.kind === 'testOnly').title, testOnly, CLASS_BUCKETS.find((b) => b.kind === 'testOnly').mark)
group(CLASS_BUCKETS.find((b) => b.kind === 'moduleInternal').title, moduleInternal, CLASS_BUCKETS.find((b) => b.kind === 'moduleInternal').mark)

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

/**
 * 基线非空、检出为空 ⇒ 拒绝给结论。
 *
 * ★ 2026-10-07 实测（与死能力卡口 check-dead-features 同族，同一次排查发现）：
 *   探测逻辑里有一行「减去导出声明那一次」的修正
 *   （`if (new RegExp(\`export\\s+…\`).test(ownText)) ownRefs--`）。
 *   把这一行删掉 ⇒ 每个符号自己的 `export` 行就算成自引用 ⇒ ownRefs ≥ 1
 *   ⇒ classifyRef 全部判成 `moduleInternal` ⇒ **「完全无人使用」塌成 0**。
 *   实测那种情况下本门 **EXIT=0**，并打出 8 行「⤵️ 已清掉的死能力」+ 一行绿色 ✅
 *   —— 绿灯配**假进展**（实际是检测整体失灵）。
 *
 *   与 check-dead-features 不同的是**本门没有自检可调**（两个文件里 0 处 selftest），
 *   所以这里只能加这个退化形态的闸。
 *   ⚠ 残留缺口（已知、未做）：**部分**塌陷（例如 8 条只剩 3 条）本闸抓不到，
 *     要覆盖它得给整条探测链补一个合成夹具自检 —— 见设计文档对应章节的「没做的事」。
 */
if (baseSet.size > 0 && cur.length === 0) {
  console.error(`✗ 基线里有 ${baseSet.size} 条死能力，探测却一条都没检出。`)
  console.error('  两种可能，本门不替你选：')
  console.error('    ① 探测器坏了 —— 上面那种「已清掉」全是假的（实测删一行修正就会这样）；')
  console.error('    ② 你确实把它们全清了 —— 跑 --update-baseline 落盘，棘轮会认新基线。')
  process.exit(3)
}

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
  // ⚠ 措辞刻意**不用**「已清掉」：那三个字读起来像进展，而实际有两种可能
  //   （真清理了 / 探测漏报了），本门区分不了就别替它下结论。
  for (const r of removed) console.log(`⤵️ 基线里有、本次未检出：${r} —— 若确已清理，跑 --update-baseline 落盘。`)
}
console.log(`\n✅ 死能力未新增（棘轮通过，基线 ${baseSet.size} 条）`)
process.exit(0)
