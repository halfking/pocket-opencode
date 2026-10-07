// 审计：features/*.vue 里哪些还在**直连数据面**（api / stores / services）而没有 ViewModel 兜住。
//
// 用途：给后续 PR 提供"待补包 View"的目标列表 —— 它是给人看的清单，**不参与门禁**。
// 门禁是同目录的 check-viewmodel-gaps.mjs（棘轮形态，只对新增判红）。
//
// ★ 2026-10-07 之前，这个文件与那道门是**两份各自复制的扫描实现**（15 行正则逐字相同），
//   而这里那句"共用判定逻辑，避免重复扫描实现漂移"写在**门**的文件头上 —— 实测根本没共用。
//   现在两份都从 lib/vm-gap-classify.mjs 取判定，注释才变成事实。
//   口径见该文件的注释：**判据问的是「有没有 ViewModel」，不是「有没有 useXxx( 这个形状」**。
import { readdirSync, readFileSync, statSync } from 'node:fs'
import { dirname, join, relative } from 'node:path'
import { fileURLToPath } from 'node:url'
import { buildDataPlane, classifyVue, SCAN_SKIP } from './lib/vm-gap-classify.mjs'

// ★ 用 dirname(fileURLToPath(…))，不是把**文件路径本身**当目录去 join。
//   原写法 `join(fileURLToPath(import.meta.url), '..', '..', 'src', 'features')`
//   靠「文件名那一段正好被第一个 .. 吃掉」而**碰巧**是对的 —— 少一个 .. 或多一个都
//   会静默指到别处，而这类错误不报错。
const ROOT_FEATURES = join(dirname(fileURLToPath(import.meta.url)), '..', 'src', 'features')
const SRC_ROOT = join(ROOT_FEATURES, '..') // features 的上一级就是 src
const SKIP = SCAN_SKIP

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

// ★ 下限闸：这份脚本第一版（我写的）把 SRC_ROOT 多退了一级 ⇒ 扫到 frontend/ 而不是 src/，
//   而它**没有任何下限** ⇒ 输出「扫了 0 个视图」+ 退出码 0，静默通过。
//   「0」在这里是危险方向（不是「没有缺口」），必须当场拒。
const MIN_VUE_FILES = 100
const MIN_MODULES = 200

const all = walk(SRC_ROOT)
const modules = all
  .filter(f => /\.(ts|js)$/.test(f))
  .map(f => ({ rel: relative(SRC_ROOT, f).replace(/\\/g, '/'), src: readFileSync(f, 'utf8') }))
const views = all.filter(f => f.endsWith('.vue') && relative(SRC_ROOT, f).replace(/\\/g, '/').startsWith('features/'))
const plane = buildDataPlane(modules)

const gaps = []
for (const f of views) {
  const rel = relative(SRC_ROOT, f).replace(/\\/g, '/')
  const r = classifyVue({ rel, src: readFileSync(f, 'utf8'), composables: plane.composables })
  if (r.apiCount + r.storeCount + r.serviceCount === 0) continue
  if (r.isGap) gaps.push(r)
}
gaps.sort((a, b) => b.apiCount + b.storeCount + b.serviceCount - (a.apiCount + a.storeCount + a.serviceCount))

if (views.length < MIN_VUE_FILES || modules.length < MIN_MODULES) {
  console.error(`❌ 只扫到 ${views.length} 个视图（下限 ${MIN_VUE_FILES}）／${modules.length} 个模块（下限 ${MIN_MODULES}）。`)
  console.error('   路径写错或枚举退化 —— 「0 个缺口」在这种情况下是危险方向，不是通过。')
  process.exit(2)
}

console.log(
  `【ViewModel 缺口盘点】扫了 ${views.length} 个视图 / ${modules.length} 个模块；` +
    `${plane.composables.size} 个 composable 名字，其中持有数据通路 ` +
    `${[...plane.composables.values()].filter(v => v.holdsData).length} 个`,
)
console.log(`直连数据面且无 ViewModel 兜住的：${gaps.length} 个`)
console.log()
for (const g of gaps) {
  console.log(`  ${g.rel}`)
  console.log(`      ${g.why}`)
}
console.log()
console.log('（这份清单只是盘点，不是门禁结论。门禁见 check-viewmodel-gaps.mjs —— 它是棘轮形态，')
console.log('  只对**新增**判红，所以存量在这份清单里是正常的。）')