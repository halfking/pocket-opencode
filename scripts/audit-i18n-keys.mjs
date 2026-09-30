// 全量 i18n key 对账：代码里用到的 key vs 各语言文件里存在的 key。
// BUG-AM(study.reminder.*) / BUG-AN(study.due.*) 都是「代码在用、9 个语言文件全缺」，
// 逐个撞效率太低且容易漏——这里一次性扫出全部。
//
// 抽取范围：t('x.y.z') / t("x.y.z") / $t(`x.y.z`) / i18n.t(...)
// 基准：zh-CN.json（MessageSchema = typeof zhCN，由 i18n/types.ts 定义）
import { readFileSync, readdirSync, statSync } from 'node:fs'
import { join, extname, relative } from 'node:path'
import { fileURLToPath } from 'node:url'
import { dirname } from 'node:path'

const here = dirname(fileURLToPath(import.meta.url))
const srcRoot = join(here, '..', 'frontend', 'src')
const locDir = join(srcRoot, 'locales')

// ---- 1. 递归收集源码文件 ----
function walk(dir, out = []) {
  for (const name of readdirSync(dir)) {
    if (name === 'node_modules' || name === '__tests__') continue
    const p = join(dir, name)
    const st = statSync(p)
    if (st.isDirectory()) walk(p, out)
    else if (['.vue', '.ts', '.js'].includes(extname(name))) out.push(p)
  }
  return out
}
const files = walk(srcRoot)

// ---- 2. 抽取 key ----
const RE = /\bt\(\s*['"`]([a-zA-Z0-9_]+(?:\.[a-zA-Z0-9_-]+)+)['"`]/g
const used = new Map() // key -> Set(file)
for (const f of files) {
  if (f.includes(join('locales'))) continue
  const text = readFileSync(f, 'utf8')
  let m
  while ((m = RE.exec(text))) {
    const k = m[1]
    if (!used.has(k)) used.set(k, new Set())
    used.get(k).add(relative(srcRoot, f))
  }
}

// ---- 3. 读各语言文件，展开成扁平 key 集合 ----
function flatten(obj, prefix = '', out = new Set()) {
  for (const [k, v] of Object.entries(obj)) {
    const key = prefix ? `${prefix}.${k}` : k
    if (v && typeof v === 'object' && !Array.isArray(v)) flatten(v, key, out)
    else out.add(key)
  }
  return out
}
const localeFiles = readdirSync(locDir).filter((f) => f.endsWith('.json'))
const localeKeys = {}
for (const lf of localeFiles) {
  const obj = JSON.parse(readFileSync(join(locDir, lf), 'utf8'))
  localeKeys[lf.replace('.json', '')] = flatten(obj)
}

// ---- 4. 对账 ----
const base = 'zh-CN'
const baseKeys = localeKeys[base]

// ---- 4b. 动态 key 候选（BUG-AN 续的漏报原因）----
// 前一版只认静态 t('x.y.z')，漏掉了 study.due.allClear：
// 它不出现在 t(...) 调用里，而是 DueSummaryHeadlineKey 联合类型的字面量 +
// 函数返回值，界面用 t(dueSummaryHeadlineKey(...)) 动态取用。
//
// 这里刻意收窄到「导出类型名以 Key 结尾的联合/别名里的点分字面量」。
// 试过更宽的规则（所有首段命中命名空间的点分字面量），结果 23 条候选里
// 绝大多数是噪声——email.is_starred（数据库列名）、inbox.classifyHint.value
// （ref 属性路径）、settings.temperature（API 字段名）。
// 宽规则报出来的东西没人敢用，工具必须先做到零噪声才有价值。
// 关键：联合类型声明**没有分号**（`export type X = | 'a' | 'b'` 换行结束），
// 所以不能写成 `=([^;]+);`，那样一条都匹配不上、工具会「碰巧对」而不是真对。
// 这里只匹配 `| '字面量'` 连续链，避免把整个文件尾巴吞进来。
const KEY_TYPE = /export\s+type\s+\w*Key\w*\s*=((?:\s*\|\s*['"`][^'"`]+['"`])+)/g
const LIT = /['"`]([a-z][a-zA-Z0-9]*(?:\.[a-zA-Z0-9_-]+)+)['"`]/g
const candidates = new Map() // key -> Set(file)
for (const f of files) {
  if (f.includes(join('locales'))) continue
  const text = readFileSync(f, 'utf8')
  let tm
  KEY_TYPE.lastIndex = 0
  while ((tm = KEY_TYPE.exec(text))) {
    LIT.lastIndex = 0
    let m
    while ((m = LIT.exec(tm[1]))) {
      const k = m[1]
      if (!candidates.has(k)) candidates.set(k, new Set())
      candidates.get(k).add(relative(srcRoot, f))
    }
  }
}
const dynamicMissing = [...candidates.keys()].filter((k) => !baseKeys.has(k)).sort()

const missingInBase = [...used.keys()].filter((k) => !baseKeys.has(k)).sort()
for (const k of dynamicMissing) {
  if (!missingInBase.includes(k)) missingInBase.push(k)
}
missingInBase.sort()
const usedInBase = [...used.keys()].filter((k) => baseKeys.has(k))

// 对每个语言，统计「代码在用但该语言缺」
const perLocale = {}
for (const [loc, keys] of Object.entries(localeKeys)) {
  perLocale[loc] = usedInBase.filter((k) => !keys.has(k)).sort()
}

console.log(`源码文件数: ${files.length}   代码中静态可识别的 key: ${used.size}`)
console.log(`动态 key 候选: ${candidates.size}（导出 *Key 联合类型里的点分字面量）`)
console.log(`zh-CN 存在: ${usedInBase.length}   zh-CN 缺失（静态+动态）: ${missingInBase.length}\n`)

console.log('=== A. 代码在用、但 zh-CN（=类型基准）里就没有的 key ===')
if (!missingInBase.length) console.log('  （无）')
for (const k of missingInBase) {
  const via = used.has(k) ? '静态 t(...)' : '动态 *Key 联合类型'
  const where = used.has(k) ? [...used.get(k)].join(', ') : [...(candidates.get(k) || [])].join(', ')
  console.log(`  ${k}   [${via}]\n      ${where}`)
}

console.log('\n=== B. 各语言相对 zh-CN 缺失的 key（代码在用的范围内）===')
for (const [loc, miss] of Object.entries(perLocale)) {
  if (loc === base) continue
  console.log(`  ${loc.padEnd(7)} 缺 ${String(miss.length).padStart(3)} 个` +
    (miss.length ? `: ${miss.slice(0, 12).join(', ')}${miss.length > 12 ? ' ...' : ''}` : ''))
}
