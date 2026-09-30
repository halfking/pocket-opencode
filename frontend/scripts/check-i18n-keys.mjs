// i18n key 卡口：缺 key 直接 exit 1，挡在 gates 里。
//
// 为什么需要它（BUG-AM/AN 的教训）：
// `study.due.allClear` 等 13 个 key 长期「代码在用、9 个语言文件全缺」，
// vue-i18n 只是把 key 字符串回显，运行时**没有任何告警**，
// 只能靠人肉看截图偶然发现。静态对账是唯一能在提交前拦住它的手段。
//
// 覆盖两轮踩过的坑：
//  1) 静态 t('x.y.z') 抓不到动态 key —— study.due.allClear 只出现在
//     DueSummaryHeadlineKey 联合类型 + 函数返回值里，界面用
//     t(dueSummaryHeadlineKey(...)) 动态取用。故并入 audit 的第二条规则。
//  2) 联合类型声明**没有分号**（export type X = | 'a' | 'b' 换行结束），
//     正则若要求分号会一条都匹配不上，工具「碰巧对」而不是真对。
//
// 与 audit-viewmodel-gaps / check-viewmodel-gaps 同一套约定：
// audit-* 负责报告，check-* 负责卡口。
import { readFileSync, readdirSync, statSync } from 'node:fs'
import { join, dirname, extname } from 'node:path'
import { fileURLToPath } from 'node:url'

const here = dirname(fileURLToPath(import.meta.url))
const srcRoot = join(here, '..', 'src')
const locDir = join(srcRoot, 'locales')

function walk(dir, out = []) {
  for (const name of readdirSync(dir)) {
    if (name === 'node_modules' || name === '__tests__') continue
    const p = join(dir, name)
    if (statSync(p).isDirectory()) walk(p, out)
    else if (['.vue', '.ts', '.js'].includes(extname(name))) out.push(p)
  }
  return out
}

// 静态：t('x.y.z')
const STATIC = /\bt\(\s*['"`]([a-zA-Z0-9_]+(?:\.[a-zA-Z0-9_-]+)+)['"`]/g
// 动态：export type XxxKey = | 'a' | 'b'（无分号，不能写成 =([^;]+);）
const KEY_TYPE = /export\s+type\s+\w*Key\w*\s*=((?:\s*\|\s*['"`][^'"`]+['"`])+)/g
const LIT = /['"`]([a-z][a-zA-Z0-9]*(?:\.[a-zA-Z0-9_-]+)+)['"`]/g

const used = new Set()
const dynamic = new Set()
for (const f of walk(srcRoot)) {
  if (f.includes(join('locales'))) continue
  const text = readFileSync(f, 'utf8')
  let m
  STATIC.lastIndex = 0
  while ((m = STATIC.exec(text))) used.add(m[1])
  KEY_TYPE.lastIndex = 0
  let tm
  while ((tm = KEY_TYPE.exec(text))) {
    LIT.lastIndex = 0
    let lm
    while ((lm = LIT.exec(tm[1]))) dynamic.add(lm[1])
  }
}

function flatten(obj, prefix = '', out = new Set()) {
  for (const [k, v] of Object.entries(obj)) {
    const key = prefix ? `${prefix}.${k}` : k
    if (v && typeof v === 'object' && !Array.isArray(v)) flatten(v, key, out)
    else out.add(key)
  }
  return out
}

const all = [...new Set([...used, ...dynamic])]
const localeFiles = readdirSync(locDir).filter((f) => f.endsWith('.json'))
const localeKeys = {}
for (const lf of localeFiles) {
  localeKeys[lf.replace('.json', '')] = flatten(JSON.parse(readFileSync(join(locDir, lf), 'utf8')))
}

console.log(`【i18n key 卡口】代码在用 ${used.size}（静态）/ ${dynamic.size}（动态候选），语言文件 ${localeFiles.length} 份`)

let failed = false
for (const [loc, keys] of Object.entries(localeKeys)) {
  const miss = all.filter((k) => !keys.has(k)).sort()
  if (miss.length) {
    failed = true
    console.error(`❌ ${loc} 缺 ${miss.length} 个 key：${miss.join(', ')}`)
  } else {
    console.log(`✅ ${loc.padEnd(7)} 覆盖全部 ${all.length} 个 key`)
  }
}

if (failed) {
  console.error()
  console.error('处理方法：在 frontend/src/locales/*.json 补齐这些 key。')
  console.error('注意 zh-CN.json 是 MessageSchema 的类型基准（i18n/types.ts: typeof zhCN），必须先补它。')
  process.exit(1)
}
console.log('✅ 全部语言文件 key 齐平')
process.exit(0)
