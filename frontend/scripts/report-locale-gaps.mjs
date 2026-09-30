/**
 * 列出所有语言包相对 en-US 缺失的 key（不写文件，只报告）。
 * locale-parity 测试一次只暴露第一个失败语言，改动后要来回跑好几轮；
 * 这个脚本一次看全，避免「修一个又冒出下一个」。
 *
 * Run: node scripts/report-locale-gaps.mjs
 */
import { readFileSync, readdirSync } from 'node:fs'
import { join, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'

const DIR = join(dirname(fileURLToPath(import.meta.url)), '..', 'src', 'locales')
const flat = (o, p = '') =>
  Object.entries(o).flatMap(([k, v]) =>
    v && typeof v === 'object' && !Array.isArray(v) ? flat(v, `${p}${k}.`) : [`${p}${k}`])

const base = flat(JSON.parse(readFileSync(join(DIR, 'en-US.json'), 'utf8')))
const baseSet = new Set(base)
let total = 0

for (const name of readdirSync(DIR).filter((f) => f.endsWith('.json') && f !== 'en-US.json')) {
  const keys = new Set(flat(JSON.parse(readFileSync(join(DIR, name), 'utf8'))))
  const missing = base.filter((k) => !keys.has(k))
  const extra = [...keys].filter((k) => !baseSet.has(k))
  total += missing.length
  console.log(`${name.padEnd(13)} 缺 ${String(missing.length).padStart(3)}  多 ${String(extra.length).padStart(3)}` +
    (missing.length ? `  → ${missing.slice(0, 8).join(', ')}${missing.length > 8 ? ' …' : ''}` : '') +
    (extra.length ? `  | 多余: ${extra.slice(0, 5).join(', ')}` : ''))
}
console.log(`\nen-US 共 ${base.length} 个 key，全部语言合计缺口 ${total} 个`)
