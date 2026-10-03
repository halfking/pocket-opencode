// read-maestro-hierarchy.mjs — 读 Maestro 落的 screen-hierarchy JSON，打印可访问性树。
// 用法：node scripts/read-maestro-hierarchy.mjs <json路径> [--all]
import { readFileSync } from 'node:fs'

const p = process.argv[2]
const showAll = process.argv.includes('--all')
if (!p) { console.error('用法: node scripts/read-maestro-hierarchy.mjs <json> [--all]'); process.exit(2) }
const j = JSON.parse(readFileSync(p, 'utf8'))

let wv = null
const find = (n) => {
  const c = (n.attributes?.clazz || n.attributes?.class || '').toString()
  if (c.includes('WebView')) wv = n
  ;(n.children || []).forEach(find)
}
find(j)
const root = wv || j
if (!wv) console.error('（未找到 WebView 节点，打印整棵树）')

const out = []
const walk = (n, d) => {
  const a = n.attributes || {}
  const c = (a.clazz || a.class || '?').toString()
  const t = String(a.text || '')
  const isWidget = /TextView|EditText|Button|View$/.test(c)
  if (showAll || t || /EditText|Button/.test(c)) {
    out.push('  '.repeat(Math.min(d, 10)) +
      `[${c.replace('android.widget.', '').replace('android.view.', '')}] ` +
      `t=${JSON.stringify(t.slice(0, 34))} en=${a.enabled} b=${JSON.stringify(a.bounds || '')}`)
  }
  ;(n.children || []).forEach((x) => walk(x, d + 1))
}
walk(root, 0)
console.log(`${p.split(/[\\/]/).pop()}\n`)
console.log(out.join('\n'))
