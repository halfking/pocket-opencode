// parse-hier.mjs — 把 `maestro hierarchy` 的 JSON 树压成可读列表。
//
// 用途：写 Maestro flow 时选择器必须来自**真机实测的可访问性树**，
// 不能照着 Vue 模板猜（模板里写 placeholder 不代表 a11y 树里有 text）。
//
// 用法：node scripts/parse-hier.mjs <hierarchy.json> [过滤子串]
import { readFileSync } from 'node:fs'

const p = process.argv[2]
if (!p) { console.error('用法: node scripts/parse-hier.mjs <hierarchy.json> [过滤]'); process.exit(2) }
const filter = process.argv[3] || ''
// PowerShell 5.1 的 `Out-File -Encoding utf8` 会写 BOM，JSON.parse 不认。
const root = JSON.parse(readFileSync(p, 'utf8').replace(/^\uFEFF/, ''))

const out = []
function walk(n, d) {
  const a = n.attributes || {}
  const cls = String(a.clazz || a.class || '')
  const text = String(a.text ?? '')
  const desc = String(a['content-desc'] ?? '')
  const clickable = a.clickable === 'true'
  const focusable = a.focusable === 'true'
  const enabled = a.enabled === 'true'
  // 只留真的有语义的节点：有文字、有 content-desc、或可交互的控件。
  const interesting = text || desc || /EditText|Button|ImageButton|CheckBox|Switch|SeekBar/.test(cls) || clickable
  if (interesting) {
    const b = String(a.bounds || '')
    const line = [
      '  '.repeat(Math.min(d, 12)),
      `[${cls.replace('android.widget.', '').replace('android.view.', '')}]`,
      `t=${JSON.stringify(text.slice(0, 44))}`,
      `cd=${JSON.stringify(desc.slice(0, 30))}`,
      `en=${enabled ? 1 : 0}`,
      clickable ? 'CLICKABLE' : '',
      focusable ? 'FOCUS' : '',
      b,
    ].filter(Boolean).join(' ')
    if (!filter || line.includes(filter)) out.push(line)
  }
  for (const c of n.children || []) walk(c, d + 1)
}
walk(root, 0)
console.log(`节点 ${out.length} 条${filter ? '，过滤=' + filter : ''}\n`)
console.log(out.join('\n'))
