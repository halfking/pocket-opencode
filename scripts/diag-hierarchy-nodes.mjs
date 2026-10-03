// 一次性诊断脚本：解析 Maestro 层级快照，打印所有 EditText / 按钮的
// class、bounds、enabled、focused，用来判断「保存按钮未解禁」是
// 文本没打进去（textarea 没被点到）还是按钮状态判定错了。
//
// 用法：node scripts/diag-hierarchy-nodes.mjs <hierarchy.json> [classFilter]
import { readFileSync } from 'node:fs'

const file = process.argv[2]
const filter = process.argv[3] || ''
const root = JSON.parse(readFileSync(file, 'utf8'))

const rows = []
const walk = (n) => {
  if (!n || typeof n !== 'object') return
  const a = n.attributes || {}
  if (a.class) {
    const t = a.text || ''
    const cd = a['content-desc'] || ''
    if (!filter || (a.class + ' ' + t + ' ' + cd).includes(filter)) {
      rows.push({
        class: a.class,
        text: t,
        desc: cd,
        bounds: a.bounds || '',
        enabled: a.enabled,
        focused: a.focused,
        clickable: a.clickable,
      })
    }
  }
  for (const c of n.children || []) walk(c)
}
walk(root)

for (const r of rows) {
  console.log(
    `${r.class.padEnd(28)} en=${String(r.enabled).padEnd(5)} foc=${String(r.focused).padEnd(5)} clk=${String(r.clickable).padEnd(5)} ${String(r.bounds).padEnd(24)} t='${r.text}' cd='${r.desc}'`,
  )
}
console.log(`\n共 ${rows.length} 个节点`)
