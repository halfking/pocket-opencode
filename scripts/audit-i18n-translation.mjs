// 译文质量对账：非英文语言里，值与 en-US **逐字节相同**的 key。
//
// 这与「缺 key」是两码事（缺 key = 用户看到 study.due.allClear 这种机器串，
// 已由 check-i18n-keys 卡住）；这里是 key 存在、但值还是英文，
// 用户看到的是 "My decks" 而不是「我的牌组」——只是不好看，不会误导。
//
// 之所以单独量化：之前 handoff 里记的「42 条 / 20 处」是手数的，
// 每次改完 locale 都会过期。这个脚本让数字可复现。
import { readFileSync, readdirSync } from 'node:fs'
import { join, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'

const here = dirname(fileURLToPath(import.meta.url))
const locDir = join(here, '..', 'frontend', 'src', 'locales')

function flatten(obj, prefix = '', out = {}) {
  for (const [k, v] of Object.entries(obj)) {
    const key = prefix ? `${prefix}.${k}` : k
    if (v && typeof v === 'object' && !Array.isArray(v)) flatten(v, key, out)
    else out[key] = v
  }
  return out
}

const base = flatten(JSON.parse(readFileSync(join(locDir, 'en-US.json'), 'utf8')))
const locales = readdirSync(locDir).filter((f) => f.endsWith('.json') && f !== 'en-US.json')

console.log(`en-US 共 ${Object.keys(base).length} 个 key\n`)
const grandTotal = {}
for (const lf of locales) {
  const loc = lf.replace('.json', '')
  const cur = flatten(JSON.parse(readFileSync(join(locDir, lf), 'utf8')))
  // 值与英文完全一致 => 未翻译。zh-CN/zh-TW 单独看（繁中可能与英文同形但语义不同，
  // 仍按逐字节相同计，判定口径统一，不给例外）
  const same = Object.keys(base).filter((k) => cur[k] !== undefined && cur[k] === base[k])
  const missing = Object.keys(base).filter((k) => cur[k] === undefined)
  grandTotal[loc] = same.length
  // 按顶层命名空间归类，便于分批处理
  const byNs = {}
  for (const k of same) {
    const ns = k.split('.')[0]
    byNs[ns] = (byNs[ns] || 0) + 1
  }
  const top = Object.entries(byNs).sort((a, b) => b[1] - a[1]).slice(0, 6)
    .map(([ns, n]) => `${ns}=${n}`).join(' ')
  console.log(
    `${loc.padEnd(7)} 未翻译 ${String(same.length).padStart(3)} / ${String(Object.keys(base).length).padStart(3)}` +
    (missing.length ? `  缺 key ${missing.length}` : '') +
    (top ? `   主要：${top}` : ''),
  )
}
const avg = Math.round(Object.values(grandTotal).reduce((a, b) => a + b, 0) / locales.length)
console.log(`\n合计：每种语言平均 ${avg} 条未翻译（共 ${locales.length} 种）`)

// zh-CN 作为 MessageSchema 基准，单独看它自己的未翻译数最有意义
const zh = flatten(JSON.parse(readFileSync(join(locDir, 'zh-CN.json'), 'utf8')))
const zhSame = Object.keys(base).filter((k) => zh[k] === base[k])
console.log(`zh-CN 未翻译（值与英文同形）：${zhSame.length} 条`)
if (zhSame.length) console.log('  ' + zhSame.slice(0, 30).join(', ') + (zhSame.length > 30 ? ' ...' : ''))
