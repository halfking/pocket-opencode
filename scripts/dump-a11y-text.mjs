// dump-a11y-text.mjs — 把一次 Maestro 失败现场的可访问性树压成「文本 + enabled + bounds」清单。
// 用法: node scripts/dump-a11y-text.mjs <screen-hierarchy 目录或单个 json>
// 为什么需要它: Maestro 的 assertVisible 是整串正则全匹配，而且 WebView 会把同一容器的
// 多个 span 合并成一个节点——只看命令日志分不清「元素不存在」和「元素在但文案不是这个」。
import { readFileSync, readdirSync, statSync, existsSync } from 'node:fs'
import { join, basename } from 'node:path'

const arg = process.argv[2]
if (!arg) { console.log('用法: node scripts/dump-a11y-text.mjs <目录或 json>'); process.exit(2) }

const files = []
if (statSync(arg).isDirectory()) {
  for (const n of readdirSync(arg)) if (n.endsWith('.json')) files.push(join(arg, n))
} else {
  files.push(arg)
}
files.sort()

for (const f of files) {
  const j = JSON.parse(readFileSync(f, 'utf8'))
  const nodes = []
  ;(function w(o) {
    if (Array.isArray(o)) return o.forEach(w)
    if (o && typeof o === 'object') {
      if (o.attributes) nodes.push(o.attributes)
      for (const k of Object.keys(o)) if (k !== 'attributes') w(o[k])
    }
  })(j)

  console.log(`\n=== ${basename(f)} ===  节点 ${nodes.length}`)
  // 只打印有文本的节点，按 bounds 的 y 排序（视觉顺序）
  const withText = nodes.filter((a) => a.text && a.text.trim())
  const yOf = (a) => {
    const m = /\[(\d+),(\d+)\]\[(\d+),(\d+)\]/.exec(a.bounds || '')
    return m ? Number(m[2]) : 0
  }
  withText.sort((a, b) => yOf(a) - yOf(b))
  for (const a of withText) {
    const t = a.text.replace(/\s+/g, ' ').trim().slice(0, 110)
    const cls = (a.clazz || '').split('.').pop()
    const en = a.enabled === false ? 'DISABLED' : 'enabled'
    // rect 为 0 的节点在 Maestro 里通常选不中（不可见的外屏节点）
    const zero = /\[0,0\]\[0,0\]/.test(a.bounds || '') ? 'ZERO-RECT' : ''
    console.log(`  [${a.bounds}] ${en} ${zero} <${cls}> ${t}`)
  }
}
