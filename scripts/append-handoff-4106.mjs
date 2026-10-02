// append-handoff-4106.mjs —— 把 docs/handoff/_part-4.106.md 追加到主 handoff 末尾。
import fs from 'node:fs'
import path from 'node:path'

const ROOT = path.resolve(path.dirname(new URL(import.meta.url).pathname).replace(/^\/([A-Za-z]:)/, '$1'), '..')
const DOC = path.join(ROOT, 'docs', 'handoff', '2026-09-30-android-e2e-bug-d-e-f.md')
const PART = path.join(ROOT, 'docs', 'handoff', '_part-4.106.md')
const MARK = '建立在一个**空对照**上'

const rawPart = fs.readFileSync(PART, 'utf8')
if (!rawPart.includes(MARK)) {
  console.error(`❌ MARK 与 _part-4.106.md 的内容对不上（MARK=${JSON.stringify(MARK)}）。`)
  process.exit(2)
}

const doc = fs.readFileSync(DOC, 'utf8')
if (doc.includes(MARK)) { console.log('已包含 §4.106，跳过（幂等）'); process.exit(0) }

let part = rawPart.replace(/\r?\n/g, '\r\n')
if (!doc.endsWith('\r\n')) doc += '\r\n'
const out = doc + part.replace(/^\r\n/, '')

const recheck = fs.readFileSync(DOC, 'utf8')
if (recheck !== doc) {
  console.error('❌ 主 handoff 在我读取之后被改动（并发会话追加）。中止。')
  process.exit(1)
}

fs.writeFileSync(DOC, Buffer.from(out, 'utf8'))

const buf = fs.readFileSync(DOC)
let crlf = 0, bareLF = 0
for (let i = 0; i < buf.length; i++) {
  if (buf[i] === 13 && buf[i + 1] === 10) { crlf++; i++ } else if (buf[i] === 10) bareLF++
}
const back = buf.toString('utf8')
const times = back.split(MARK).length - 1
console.log('已追加 §4.106')
console.log(`  bytes=${buf.length}  CRLF=${crlf}  bareLF=${bareLF}  BOM=${buf[0] === 0xef}`)
console.log(`  标记出现次数=${times}`)
if (times !== 1) { console.error(`❌ 标记出现了 ${times} 次`); process.exit(1) }
if (bareLF > 0) { console.error('❌ 仍有 bare LF'); process.exit(1) }
if (buf[0] === 0xef) { console.error('❌ 出现了 BOM'); process.exit(1) }
