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
/** t(`prefix.${expr}`) 里出现过的静态前缀 */
const usedPrefixes = new Set()
/** file -> Set(静态前缀)，来自 t(`prefix.${expr}`) 模板字面量 */
const templatePrefixes = new Map()
/** file -> Set(被赋给 *Key / *Key: 属性的字符串字面量) */
const keyFragments = new Map()

// 2026-10-03：模板 key 的第三条采集规则。
// 起因是真实白屏级缺陷：NotesHubView 写 t(`notesHub.filter.${row.filterKey}`)，
// 而 row.filterKey 的值是 'note'，语言包里只有 notesHub.filter.manual ——
// 界面每一行都渲染出字面量 "notesHub.filter.note"。
//
// 下面两条老规则都看不见它：
//   - STATIC 只认 t('x.y.z') 的字面量形式，模板字符串整条不匹配；
//   - KEY_TYPE 只从 `export type XxxKey = 'a.b' | ...` 联合类型里展开，
//     而这里的片段来自对象字面量 `filterKey: 'note'`。
// 而 ia-smoke 也没抓到：全新空库下行列表根本不渲染，"无字面量 key" 那条
// 断言在空数据上是空断言。**只有真机有数据时才会暴露。**
//
// 这里补的规则是「前缀 + 片段」两段拼起来校验：
//   1. 抽出 t(`prefix.${…}`) 的静态 prefix，要求每种语言在该 prefix 下至少有一个 key；
//   2. 抽出同文件里 `*Key: 'frag'` 形式的片段，要求 prefix.frag 在语言包里真实存在。
// 只在同一文件内组合，避免跨文件猜前缀造成误报。
const TPL_KEY = /\bt\(\s*`([a-zA-Z0-9_]+(?:\.[a-zA-Z0-9_-]+)*)\.\$\{/g
const KEY_PROP = /\b[A-Za-z_$][\w$]*Key\s*:\s*'([a-z][a-zA-Z0-9_-]*)'/g

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
  TPL_KEY.lastIndex = 0
  let tpl
  while ((tpl = TPL_KEY.exec(text))) {
    if (!templatePrefixes.has(f)) templatePrefixes.set(f, new Set())
    templatePrefixes.get(f).add(tpl[1])
  }
  KEY_PROP.lastIndex = 0
  let frag
  while ((frag = KEY_PROP.exec(text))) {
    if (!keyFragments.has(f)) keyFragments.set(f, new Set())
    keyFragments.get(f).add(frag[1])
  }
}

/**
 * 模板 key 的「前缀 + 片段」组合展开成完整 key 并入 used，
 * 让它走下面同一套「9 语言齐平」校验。
 *
 * 另外单独记下用到的前缀：要求每种语言在该前缀下**至少有一个子 key**。
 * 否则说明前缀本身拼错了（比如把 notesHub 写成 noteHub），
 * 那种错会让整组模板 key 一起静默失效，光校验片段是发现不了的。
 */
for (const [f, prefixes] of templatePrefixes) {
  const frags = keyFragments.get(f)
  for (const p of prefixes) {
    usedPrefixes.add(p)
    if (frags) for (const frag of frags) used.add(`${p}.${frag}`)
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

console.log(`【i18n key 卡口】代码在用 ${used.size}（静态）/ ${dynamic.size}（动态候选），模板前缀 ${usedPrefixes.size} 个，语言文件 ${localeFiles.length} 份`)

let failed = false

// 模板前缀必须在每种语言下都真实存在至少一个子 key。
// 前缀写错时，同一前缀下的所有片段校验会一起「碰巧通过」或一起报错，
// 单看片段定位不到根因，所以单独验一遍前缀。
for (const [loc, keys] of Object.entries(localeKeys)) {
  const dead = [...usedPrefixes].filter((p) => !keys.has(p) && ![...keys].some((k) => k.startsWith(p + '.'))).sort()
  if (dead.length) {
    failed = true
    console.error(`❌ ${loc} 的模板 key 前缀不存在任何子 key：${dead.join(', ')}`)
  }
}

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
