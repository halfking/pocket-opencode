// append-handoff-498.mjs —— 把 docs/handoff/_part-4.98.md 追加到主 handoff 末尾。
// 沿用 490/491/494/496/497 那一套：MARK 自证 + 幂等 + 落盘后重读比对 + 换行/BOM 体检。
import fs from 'node:fs'
import path from 'node:path'

const ROOT = path.resolve(path.dirname(new URL(import.meta.url).pathname).replace(/^\/([A-Za-z]:)/, '$1'), '..')
const DOC = path.join(ROOT, 'docs', 'handoff', '2026-09-30-android-e2e-bug-d-e-f.md')
const PART = path.join(ROOT, 'docs', 'handoff', '_part-4.98.md')
// ⚠️ MARK 必须逐字出现在 part 里，写错就 exit 2，绝不盲写。
const MARK = '探针锚点子串碰撞'

const rawPart = fs.readFileSync(PART, 'utf8')
if (!rawPart.includes(MARK)) {
  console.error(`❌ MARK 与 _part-4.98.md 的内容对不上（MARK=${JSON.stringify(MARK)}）`)
  process.exit(2)
}
const doc = fs.readFileSync(DOC, 'utf8')
if (doc.includes(MARK)) { console.log('已包含 §4.98，跳过（幂等）'); process.exit(0) }

let part = rawPart.replace(/\r?\n/g, '\r\n')
if (!doc.endsWith('\r\n')) doc += '\r\n'
const out = doc + part.replace(/^\r\n/, '')
const recheck = fs.readFileSync(DOC, 'utf8')
if (recheck !== doc) { console.error('❌ 主 handoff 被并发改动，中止'); process.exit(1) }

fs.writeFileSync(DOC, Buffer.from(out, 'utf8'))
const buf = fs.readFileSync(DOC)
let crlf = 0, bareLF = 0
for (let i = 0; i < buf.length; i++) {
  if (buf[i] === 13 && buf[i + 1] === 10) { crlf++; i++ } else if (buf[i] === 10) bareLF++
}
const times = buf.toString('utf8').split(MARK).length - 1
console.log('已追加 §4.98')
console.log(`  bytes=${buf.length}  CRLF=${crlf}  bareLF=${bareLF}  BOM=${buf[0] === 0xef}`)
console.log(`  标记出现次数=${times}`)
if (times !== 1) { console.error('❌ 标记次数不对'); process.exit(1) }
if (bareLF > 0) { console.error('❌ 仍有 bare LF'); process.exit(1) }
if (buf[0] === 0xef) { console.error('❌ 出现 BOM'); process.exit(1) }
