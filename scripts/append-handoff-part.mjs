// append-handoff-part.mjs — 把 docs/handoff/_part-*.md 追加进主 handoff，保留 CRLF / 无 BOM。
//
// 为什么拆成两个文件：Markdown 正文里有大量反引号（含 ``` 代码块），
// 塞进 JS 模板字符串必然要转义，实测被 write 工具吃掉一层反斜杠后
// 直接语法报错。拆开后正文是纯 Markdown，一个反斜杠都不用碰。
//
// 为什么不用 PowerShell：Get-Content | Set-Content 会写 BOM 并改行尾，
// 而这个文件必须保持 **无 BOM + 纯 CRLF**（追加前 414798 字节 / 7042 CRLF / 0 裸 LF）。
//
// 用法：node scripts/append-handoff-part.mjs docs/handoff/_part-4.74.md
import { readFileSync, writeFileSync } from 'node:fs'

const DOC = 'docs/handoff/2026-09-30-android-e2e-bug-d-e-f.md'
const partPath = process.argv[2]
if (!partPath) { console.error('用法: node scripts/append-handoff-part.mjs <part.md>'); process.exit(2) }

const part = readFileSync(partPath, 'utf8').replace(/\r\n/g, '\n')
const head = part.split('\n')[0].trim()
if (!head.startsWith('#### ')) {
  console.error(`片段首行必须是 '#### ' 小节标题，实际是：${head.slice(0, 40)}`)
  process.exit(2)
}

const before = readFileSync(DOC, 'utf8')
if (before.includes(head)) {
  console.error(`❌ 主 handoff 里已经有「${head}」，不重复追加`)
  process.exit(2)
}

const after = before.replace(/\s*$/, '') + '\r\n' + part.replace(/\n/g, '\r\n')
writeFileSync(DOC, after, 'utf8')

// 自检：三项对不上就是写坏了，不要提交
const check = readFileSync(DOC, 'utf8')
const crlf = (check.match(/\r\n/g) || []).length
const bare = (check.match(/(?<!\r)\n/g) || []).length
const bom = check.charCodeAt(0) === 0xFEFF
console.log(`bytes ${Buffer.byteLength(before)} -> ${Buffer.byteLength(check)}`)
console.log(`CRLF=${crlf} bareLF=${bare} BOM=${bom} 已含「${head}」=${check.includes(head)}`)
if (bare > 0 || bom) { console.error('❌ 编码/行尾被写坏了，不要提交'); process.exit(1) }
