// audit-i18n-placeholder.mjs — 扫描 vue-i18n 语法迁移不彻底导致的崩溃点。
//
// ## BUG-S（2026-09-30 真机空白页）
//
// `/flashcards/browser` 与 `/flashcards/stats` 在真机上整页空白，console 报
//   SyntaxError: Not allowed nest placeholder
//
// 根因：项目已升级到 vue-i18n v9+，命名插值语法是 `{count}`；但这批文案还是
// Vue 2 的**字面量**插值 `{{count}}`。vue-i18n v9 解析 `{{count}}` 时把外层
// `{...}` 当成一个占位符、内容又是 `{count}`，于是判定为「嵌套占位符」并抛错。
// 抛错发生在渲染期 -> 整个组件渲染中断 -> 页面白屏。
//
// 关键点：**与语言无关**。9 种语言都写成 `{{count}}`，所以切换语言救不了。
//
// 本脚本扫全部 locale，找出所有 `{{...}}` 残留键。判据用「值里出现双花括号」，
// 足够精确 —— 正常文案里不该出现 `{{`。
//
// 用法：node scripts/audit-i18n-placeholder.mjs [--fix]
//   --fix  把 {{name}} 改写为 {name}（就地改工作区文件，**不入库**，需人工 review）
import { readdirSync, readFileSync, writeFileSync } from 'node:fs'
import { join, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'

const ROOT = join(dirname(fileURLToPath(import.meta.url)), '..')
const DIR = join(ROOT, 'frontend/src/locales')
const FIX = process.argv.includes('--fix')

/** 深度遍历，收集 path -> value。 */
function walk(node, path = '', out = []) {
  if (node === null || typeof node !== 'object') return out
  for (const [k, v] of Object.entries(node)) {
    const p = path ? `${path}.${k}` : k
    if (v && typeof v === 'object') walk(v, p, out)
    else if (typeof v === 'string' && v.includes('{{')) out.push([p, v])
  }
  return out
}

let total = 0
const report = []
for (const file of readdirSync(DIR).filter((f) => f.endsWith('.json'))) {
  const full = join(DIR, file)
  const raw = readFileSync(full, 'utf8')
  let obj
  try { obj = JSON.parse(raw) } catch (e) { report.push(`${file}: JSON 解析失败 ${e.message}`); continue }

  const hits = walk(obj)
  if (hits.length === 0) continue
  total += hits.length
  report.push(`\n=== ${file} (${hits.length}) ===`)
  for (const [p, v] of hits) report.push(`  ${p}\n      ${JSON.stringify(v)}`)

  if (FIX) {
    // {{name}} -> {name}
    const replaced = raw.replace(/\{\{\s*([A-Za-z_][\w.]*)\s*\}\}/g, '{$1}')
    // 反向自查：不能留下任何 {{ 或 }}
    const leftover = [...replaced.matchAll(/\{\{|\}\}/g)].length
    if (leftover > 0) {
      report.push(`  !! 改写后仍残留 ${leftover} 个花括号，人工处理，未写入`)
    } else {
      JSON.parse(replaced) // 合法性闸门
      writeFileSync(full, replaced)
      report.push(`  -> 已改写并写回（仅工作区，未入库）`)
    }
  }
}

console.log(report.join('\n'))
console.log(`\n=== 合计 ${total} 处双花括号残留 ===`)
if (total > 0 && !FIX) {
  console.log('这些键在 vue-i18n v9+ 下会导致**渲染期抛错**，页面直接白屏。')
  console.log('加 --fix 可就地改写工作区文件（改完请 review 再提交）。')
  process.exitCode = 1
}
