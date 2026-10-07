#!/usr/bin/env node
// check-viewmodel-gaps.mjs —— 卡口：视图直连 api/stores/services 且没有 ViewModel 兜住。
//
// 用法：
//   node scripts/check-viewmodel-gaps.mjs [srcRoot]
//   node scripts/check-viewmodel-gaps.mjs --write-baseline     # 有意接受新增存量时才用
//   node scripts/check-viewmodel-gaps.mjs --write-baseline --record-full-repayment "<理由>"
// 退出码：0 = 无新增缺口；1 = 有新增缺口；2 = 判据失明/自检不过；3 = 拒绝给结论
//
// ## 判据（2026-10-07 重写）
//
// 旧判据是 `hasComposable = /\buse[A-Z]\w*\s*\(/` —— **词级**。
// 实测两种假通过：
//   · `useRouter()`（vue-router 内建，几乎每个视图都有）单独就能把缺口"消掉"
//   · composable 名字只出现在 `// TODO: 抽成 useXxxViewModel(...)` 注释里也能消掉
// ⇒ 旧判据从来没在问「有没有 ViewModel」，只在问「有没有这个形状」，
//   于是本仓 25 个真·候选缺口被它清成 0（实测，见设计文档 §88）。
//
// 现在这样判：把 `useXxx` 解析到**它的定义**，再看那个定义是否**持有数据通路**。
// 解析不到 / 解析到了但不持有 ⇒ 视图算缺口。
// 判定实现见 scripts/lib/vm-gap-classify.mjs，与 audit-viewmodel-gaps.mjs **真共用**
// （旧版那句「共用判定逻辑」是假的：两份文件里 15 行正则逐字复制粘贴）。

import { mkdirSync, mkdtempSync, readdirSync, readFileSync, rmSync, statSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join, dirname, relative, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import {
  countKeysBy,
  diffRatchet,
  loadBaseline,
  writeBaseline,
  ratchetSelfTestCases,
} from '../../scripts/lib/baseline-ratchet.mjs'
import { buildDataPlane, classifyVue, SCAN_SKIP } from './lib/vm-gap-classify.mjs'

const HERE = dirname(fileURLToPath(import.meta.url))
// ★ 从 argv[2] 起找：`process.argv[0]` 是 **node 可执行文件路径**，不以 '-' 开头，
//   用 `find(a => !a.startsWith('-'))` 会把 `/usr/local/bin/node` 当成 srcRoot
//   ⇒ 扫到 0 个视图/0 个模块（第一版就栽在这，读数是「0」而不是报错，最容易骗人）。
const SRC_ROOT = resolve(process.argv.slice(2).find(a => !a.startsWith('-')) || join(HERE, '..', 'src'))
const SKIP = SCAN_SKIP
const BASELINE = join(HERE, 'baseline', 'vm-gaps-baseline.json')

/**
 * 扫到的 `.vue` 数量下限。**只能手工改**，不给命令行开关。
 *
 * 本仓当前 131 个 features 视图，取 100：正常增删组件不误报，
 * 而**系统性**枚举退化（SKIP 被加了名字、某个子目录 stat 失败、walk 写坏）会撞上。
 * 抓不住「只丢一两个文件」—— 那是它的边界，不假装能抓。
 */
const MIN_VUE_FILES = 100

/** 扫到的 `.ts`/`.js` 模块数量下限：composable 索引全靠它，扫空了等于判据失明。 */
const MIN_MODULES = 200

function walk(dir, out = []) {
  let entries
  try { entries = readdirSync(dir) } catch { return out }
  for (const name of entries) {
    if (SKIP.has(name)) continue
    const p = join(dir, name)
    let st
    try { st = statSync(p) } catch { continue }
    if (st.isDirectory()) walk(p, out)
    else out.push(p)
  }
  return out
}

// ───────────────────────── 自检 1：判定层（合成夹具走同一个 classifyVue） ─────────────────────────
//
// 为什么必须自检：这道门失明时的读数**长得和结论一模一样** ——「0 个缺口 + ✅ + EXIT=0」。
// 判据被改成永远「有 ViewModel」就是这样。门当下看不出自己坏了，
// 只有把判定函数本身拿真假已知的输入喂一遍才知道。
// 每一例都要给出一个**该判红**和**该判绿**的形状，两侧都得有 —— 只测一侧等于没测。
const VIEW_FIXTURES = [
  {
    name: '真缺口：直连 api + 只用 useRouter',
    src: `<script setup lang="ts">
import { useRouter } from 'vue-router'
import { api } from '../../api/client'
const rows = await api.list()
</script>`,
    want: 'gap',
  },
  {
    name: '假通过·注释里提到 composable',
    src: `<script setup lang="ts">
import { api } from '../../api/client'
// TODO: 抽成 useTaskListViewModel(...)
const rows = await api.list()
</script>`,
    want: 'gap',
  },
  {
    name: '假通过·useRouter 之外还有纯 UI composable',
    src: `<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { useToast } from '../../composables/useToast'
import { api } from '../../api/client'
const rows = await api.list()
</script>`,
    want: 'gap',
  },
  {
    name: '真 ViewModel：经持有数据通路的 composable（useTaskListVM）',
    src: `<script setup lang="ts">
import { useTaskListVM } from '../vm/useTaskListVM'
const rows = useTaskListVM()
</script>`,
    want: 'clean',
    composables: { useTaskListVM: true },
  },
  {
    // ★ 头一版这条例外写成「视图只 import 了它、并没有直连数据面」，期望 gap ——
    //   **夹具本身是错的**：视图压根没碰数据面，就不是这道门要抓的那种缺口。
    //   判据没错、夹具恒真的样子长得很像「判据修好了」（§82 记过一次）。
    name: 'composable 解析得到但不持有数据通路 ⇒ 仍是缺口',
    src: `<script setup lang="ts">
import { useTaskListVM } from '../vm/useTaskListVM'
import { api } from '../../api/client'
const vm = useTaskListVM()
const rows = api.list()
</script>`,
    want: 'gap',
    composables: { useTaskListVM: false },
  },
  {
    // 「import 了 store」≠「经由 ViewModel」：必须有**调用**，否则仍然算直连。
    name: '只 import 了 store、没有调用 ⇒ 算直连',
    src: `<script setup lang="ts">
import { useAuthStore } from '../../stores/auth'
</script>`,
    want: 'gap',
    composables: { useAuthStore: true },
  },
  {
    name: 'type-only import 不算数据面引用',
    src: `<script setup lang="ts">
import type { Task } from '../../api/client'
import { useToast } from '../../composables/useToast'
const t = ref<Task>()
</script>`,
    want: 'clean',
  },
  {
    name: '多行 import type 不算数据面引用（旧判据剔不掉 ⇒ 这里守着）',
    src: `<script setup lang="ts">
import type {
  Task,
  TaskFilter,
} from '../../api/client'
import { useToast } from '../../composables/useToast'
</script>`,
    want: 'clean',
  },
  {
    name: '内联 type 说明符不算数据面引用',
    src: `<script setup lang="ts">
import { type Task, useToast } from '../../composables/useToast'
</script>`,
    want: 'clean',
  },
  {
    name: 'services/ 是数据面（旧判据完全不认识这条通道）',
    src: `<script setup lang="ts">
import * as learningApi from '../../services/learning'
</script>`,
    want: 'gap',
  },
  {
    name: 'stores/ 是数据面，且经持有数据通路的 store composable 调过 ⇒ 干净',
    src: `<script setup lang="ts">
import { useAuthStore } from '../../stores/auth'
const s = useAuthStore()
</script>`,
    want: 'clean',
    composables: { useAuthStore: true },
  },
]

const selfTestBad = []
// ★ 计数必须来自**实际跑了多少条**，不能用 VIEW_FIXTURES.length。
//   第一版打印「11 例夹具全通过」是拿数组长度算的 ⇒ 把循环改成 `for (const f of [])`
//   之后，自检照样报绿（2026-10-07 实测，臂 C）。这正是本轮一路在找的那条恒真判据，
//   结果它长在我自己刚写的代码里。
let selfTestRan = 0
for (const f of VIEW_FIXTURES) {
  selfTestRan += 1
  const composables = new Map(Object.entries(f.composables || {}).map(([k, v]) => [k, { file: 'fixture', holdsData: v }]))
  let got
  try {
    got = classifyVue({ rel: 'fixture.vue', src: f.src, composables }).isGap ? 'gap' : 'clean'
  } catch (err) {
    got = '抛异常: ' + err.message
  }
  if (got !== f.want) selfTestBad.push({ ...f, got })
}
if (selfTestRan !== VIEW_FIXTURES.length) {
  console.error(`❌ 判定自检只跑了 ${selfTestRan}/${VIEW_FIXTURES.length} 例 —— 夹具循环本身被改过。`)
  process.exit(2)
}
if (selfTestBad.length > 0) {
  console.error('❌ 判定自检不过 —— 判据本身坏掉了，本次扫描结果一律不可信。')
  for (const f of selfTestBad) console.error(`   ${f.name}\n     期望 ${f.want}，实得 ${f.got}`)
  console.error('   注意：判据坏了时的读数长得和结论一模一样（0 个缺口 + ✅），所以必须在这里拦。')
  process.exit(2)
}
const wantGap = VIEW_FIXTURES.filter(f => f.want === 'gap').length
console.log(`  判定自检：实跑 ${selfTestRan}/${VIEW_FIXTURES.length} 例全通过（其中 ${wantGap} 例必须判红、${VIEW_FIXTURES.length - wantGap} 例必须判绿）`)

// ───────────────────────── 自检 2：活体正控（证明扫描器真能抓到东西） ─────────────────────────
//
// 为什么不能只靠上面那层（§78 的教训）：夹具走的是 classifyVue，而真实结论取决于
// 「枚举 → 建索引 → 逐个 classifyVue」这条链有没有接上。把扫描根写错、SKIP 写坏、
// 循环体被改，夹具照样全绿。
// ⇒ 往**临时目录**真放一个已知缺口文件、走**同一条**扫描与判定函数，要求必须被抓到。
const CONTROL_SRC = `<script setup lang="ts">
import { useRouter } from 'vue-router'
import { api } from '../../api/client'
const rows = await api.list()
</script>
`
function liveControl() {
  const base = mkdtempSync(join(tmpdir(), 'vm-gaps-control-'))
  try {
    const feat = join(base, 'features', 'ctrl')
    const mod = join(base, 'composables')
    mkdirSync(feat, { recursive: true }); mkdirSync(mod, { recursive: true })
    writeFileSync(join(feat, 'ControlView.vue'), CONTROL_SRC, 'utf8')
    writeFileSync(join(mod, 'useCtrlStore.ts'), "import { defineStore } from 'pinia'\nexport const useCtrlStore = defineStore('c', { state: () => ({}) })\n", 'utf8')
    const found = scan(base).gaps.map(g => g.rel)
    return found.some(r => r.endsWith('ControlView.vue'))
  } finally {
    rmSync(base, { recursive: true, force: true })
  }
}
// ───────────────────────── 扫描 ─────────────────────────
function scan(srcRoot) {
  const all = walk(srcRoot)
  const modules = all.filter(f => /\.(ts|js)$/.test(f)).map(f => ({
    rel: relative(srcRoot, f).replace(/\\/g, '/'),
    src: readFileSync(f, 'utf8'),
  }))
  const views = all.filter(f => f.endsWith('.vue') && relative(srcRoot, f).replace(/\\/g, '/').startsWith('features/'))
  const plane = buildDataPlane(modules)
  const gaps = []
  let reaching = 0
  for (const f of views) {
    const rel = relative(srcRoot, f).replace(/\\/g, '/')
    const r = classifyVue({ rel, src: readFileSync(f, 'utf8'), composables: plane.composables })
    if (r.apiCount + r.storeCount + r.serviceCount === 0) continue
    reaching += 1
    if (r.isGap) gaps.push(r)
  }
  return { gaps, modules: modules.length, views: views.length, reaching, composables: plane.composables }
}

if (!liveControl()) {
  console.error('❌ 活体正控失败 —— 已知缺口放进临时目录都没被抓到，扫描/枚举/判定这条链断了。')
  console.error('   在这条链修好之前，任何「0 个缺口」都不能当成通过。')
  process.exit(2)
}
console.log('  活体正控：临时目录里的已知缺口被抓到了（枚举→建索引→判定 整条链是通的）')

const result = scan(SRC_ROOT)

if (result.views < MIN_VUE_FILES || result.modules < MIN_MODULES) {
  console.error(`❌ 扫到 ${result.views} 个视图（下限 ${MIN_VUE_FILES}）／${result.modules} 个模块（下限 ${MIN_MODULES}）—— 枚举漏了东西，拒绝按「通过」处理。`)
  console.error('   下限只能手工改常量，本脚本不给命令行开关。')
  process.exit(2)
}

console.log()
console.log(`【ViewModel 缺口】扫了 ${result.views} 个视图 / ${result.modules} 个模块`)
console.log(`  composable 名字 ${result.composables.size} 个，其中持有数据通路 ${[...result.composables.values()].filter(v => v.holdsData).length} 个`)
console.log(`  有数据面引用的视图 ${result.reaching} 个，判为缺口 ${result.gaps.length} 个`)
console.log()

// ───────────────────────── 基线棘轮 ─────────────────────────
const keyOf = (h) => `${h.rel}|viewmodel-gap`
const hits = result.gaps

if (process.argv.includes('--write-baseline')) {
  const idx = process.argv.indexOf('--record-full-repayment')
  const declared = idx >= 0 ? process.argv[idx + 1] : undefined
  try {
    const sorted = writeBaseline(
      BASELINE,
      countKeysBy(hits, keyOf),
      'ViewModel 缺口基线：key = <相对路径>|viewmodel-gap（不含行号）。只对新增判红，存量可以少。',
      declared ? { recordFullRepayment: declared } : {},
    )
    console.log(`✅ 已写入基线 ${Object.keys(sorted).length} 条 → ${BASELINE}`)
    process.exit(0)
  } catch (err) {
    console.error('❌ 写基线被拒绝：' + err.message)
    process.exit(3)
  }
}

let baseline = null
try {
  baseline = loadBaseline(BASELINE)
} catch (err) {
  console.error(`❌ ${err.message}`)
  process.exit(3)
}
if (baseline === null) {
  console.error(`❌ 基线文件不存在：${BASELINE}`)
  console.error('   这是「存量不判红、只对新增判红」的门。首次建立请跑：')
  console.error('     node scripts/check-viewmodel-gaps.mjs --write-baseline')
  console.error('   跑之前请先看一眼上面那份缺口清单，确认这些存量你是接受的。')
  process.exit(1)
}

const ratchetFails = ratchetSelfTestCases({
  diff: (h, b) => diffRatchet(h, b, keyOf),
  keyFn: keyOf,
  inBaselineHit: { rel: 'a/A.vue', line: 951 },
  newHit: { rel: 'b/B.vue' },
  // ★ 本门的 key **不含行号**（§77 的教训：key 里带行号 ⇒ 行号一漂就误报），
  //   所以「同一文件、同一违规」在任何行都算同一条。
  //   这里用一个**遗留的行号式 key** 当基线里的旧条目，验的是：
  //   遗留键会被认成「已消失」而不是「新违规」—— 正是 ratchet 该做的事。
  driftKey: 'a/A.vue|viewmodel-gap|line=1',
  sameHit2: { rel: 'a/A.vue', line: 12 },
})
if (ratchetFails.length > 0) {
  console.error('❌ 棘轮自检不过：')
  for (const f of ratchetFails) console.error('   ' + f)
  process.exit(2)
}

const { newHits, removed } = diffRatchet(hits, baseline, keyOf)

// ★ 未人工确认的存量必须每次都印出来。
//   这 25 条是重写判据时**第一次**被看见的候选（见基线文件里的 `_unconfirmed`）。
//   把它们记进基线只是为了「门能接进来」——如果连这份名单都不印，
//   基线就等于替所有人做了「这些可以不改」的判断，而且不留任何痕迹。
let unconfirmed = []
try {
  const doc = JSON.parse(readFileSync(BASELINE, 'utf8'))
  if (doc && Array.isArray(doc._unconfirmed && doc._unconfirmed.keys)) unconfirmed = doc._unconfirmed.keys
} catch { /* 没有这个字段就是没有，不当错误 */ }
const unconfirmedLive = unconfirmed.filter(k => (baseline[k] || 0) > 0)
if (unconfirmedLive.length > 0) {
  console.log(`⚠️  基线里有 ${unconfirmedLive.length} 条**尚未人工确认**的存量（下列 ❗ 标记项），`)
  console.log(`   它们是 2026-10-07 重写判据时第一次被看见的候选：旧判据用 useRouter / useApiError /`)
  console.log(`   useToast 这类不持有数据通路的 composable 把它们清成了 0。逐条确认该重构还是该保留，`)
  console.log(`   确认完把 key 从基线文件的 _unconfirmed.keys 里删掉。`)
}

if (removed.length > 0) {
  console.log(`✅ 存量 ${removed.length} 条已消失（棘轮可以收紧了）：${[...new Set(removed)].slice(0, 8).join(', ')}${removed.length > 8 ? ' …' : ''}`)
}
console.log()
console.log(`新增缺口 ${newHits.length} 条（存量 ${hits.length - newHits.length} 条已在基线内；❗ = 尚未人工确认）`)
for (const h of hits) {
  const k = keyOf(h)
  const tag = !baseline[k] ? '❗' : unconfirmedLive.includes(k) ? '❗' : '  '
  console.log(`  ${tag} ${h.rel}`)
  console.log(`       ${h.why}`)
}
console.log()

if (newHits.length > 0) {
  console.error('❌ 新增 ViewModel 缺口：视图直连 api/stores/services，却没有 ViewModel 兜住。')
  console.error('修法：把这块逻辑抽成 useXxxViewModel（或 composable），视图只调它。')
  console.error('注意判定口径：useRouter / useI18n / useToast / useApiError 这类**不持有数据通路**，')
  console.error('     出现它们并不代表这个视图已经有 ViewModel。')
  process.exit(1)
}
console.log('✅ 无新增 ViewModel 缺口')
process.exit(0)