// audit-i18n-untranslated.mjs — 量化「未翻译」欠账，并给出**可用的例外规则**。
//
// 为什么要单独一个脚本：现有 check-i18n-keys.mjs 只校验「代码在用的 key 在每份语言文件里
// 都存在」，**完全不校验值是否翻译**。实测 7 种非中文语言各有 100~153 条与 en-US 完全同值，
// 卡口全绿 —— 也就是说这些是静默通过的。
//
// 本脚本先跑一次，产出两样东西：
//   1. 每种语言的同值 key 清单（人工判断哪些是「本来就该一样」）
//   2. 各类同值 key 的形态分布（用来推导例外规则，而不是拍脑袋定）
import { readFileSync, readdirSync } from 'node:fs'
import { join, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'

const here = dirname(fileURLToPath(import.meta.url))
const locDir = join(here, '..', 'src', 'locales')

function flatten(obj, prefix = '', out = {}) {
  for (const [k, v] of Object.entries(obj)) {
    const key = prefix ? `${prefix}.${k}` : k
    if (v && typeof v === 'object' && !Array.isArray(v)) flatten(v, key, out)
    else out[key] = v
  }
  return out
}

const files = readdirSync(locDir).filter((f) => f.endsWith('.json'))
const data = Object.fromEntries(files.map((f) => [f.replace('.json', ''), flatten(JSON.parse(readFileSync(join(locDir, f), 'utf8')))]))
const REF = 'en-US'
const ref = data[REF]
console.log(`${REF} 共 ${Object.keys(ref).length} 个 key\n`)

/** 值的「形态」：用于推导例外规则 */
function shape(v) {
  if (typeof v !== 'string') return 'non-string'
  if (/^https?:\/\//.test(v)) return 'url'
  if (/^[\d\s.,:%¥$€£+-]+$/.test(v)) return 'number-only'
  if (/^[{][a-zA-Z0-9_]+[}]$/.test(v)) return 'placeholder-only'
  if (/^[\s\p{P}\p{S}]+$/u.test(v)) return 'punct-only'
  return 'text'
}

const langs = Object.keys(data).filter((l) => l !== REF)
for (const L of langs) {
  const same = Object.keys(ref).filter((k) => data[L][k] === ref[k])
  const byShape = {}
  for (const k of same) {
    const s = shape(ref[k])
    ;(byShape[s] ||= []).push(k)
  }
  console.log(`--- ${L}: ${same.length} 条与 ${REF} 同值`)
  for (const [s, ks] of Object.entries(byShape).sort((a, b) => b[1].length - a[1].length)) {
    console.log(`    ${s.padEnd(17)} ${String(ks.length).padStart(3)}  例: ${ks.slice(0, 4).map((k) => `${k}="${ref[k]}"`).join('  ')}`)
  }
  if (L === 'zh-CN' || L === 'fr-FR') {
    console.log('    全部同值 key:')
    for (const k of same) console.log(`      ${k.padEnd(38)} = ${JSON.stringify(ref[k])}`)
  }
  console.log()
}
