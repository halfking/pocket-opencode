// unappend-handoff-last-section.mjs —— 回退**最后一段**追加的 §x.yy。
//
// 为什么需要：§4.104 的 part 写好了但 appender 还没建，§4.105 先进去了。
// 直接再追加 §4.104 会让文档出现「4.95 在前、4.94 在后」。
// 这里把尾部那一段按 part 文件的内容精确切掉，再由调用方按顺序重放。
//
// 安全措施（每条都断言，不满足就 exit 不写）：
//   1) 文档必须以该 part 的 CRLF 规范化内容结尾
//   2) 切完的字节数 = 原字节数 − part 字节数
//   3) MARK 在切完后的文档里必须**不存在**（否则说明不止一段）
import fs from 'node:fs'
import path from 'node:path'

const ROOT = path.resolve(path.dirname(new URL(import.meta.url).pathname).replace(/^\/([A-Za-z]:)/, '$1'), '..')
const DOC = path.join(ROOT, 'docs', 'handoff', '2026-09-30-android-e2e-bug-d-e-f.md')
const PART = process.argv[2]
const MARK = process.argv[3]

if (!PART || !MARK) { console.error('用法：node scripts/unappend-handoff-last-section.mjs <part.md> <MARK>'); process.exit(2) }

const partRaw = fs.readFileSync(PART, 'utf8').replace(/\r?\n/g, '\r\n').replace(/^\r\n/, '')
const doc = fs.readFileSync(DOC, 'utf8')

if (!doc.endsWith(partRaw)) {
  console.error('❌ 文档结尾与该 part 内容不一致 —— 不敢切。先人工核对再决定。')
  console.error(`   文档尾 60 字: ${JSON.stringify(doc.slice(-60))}`)
  process.exit(1)
}
const head = doc.slice(0, doc.length - partRaw.length)
if (head.includes(MARK)) {
  console.error('❌ 切完之后 MARK 仍在文档里 —— 说明这段之前还有同 MARK 的内容，不敢动。')
  process.exit(1)
}

const before = Buffer.byteLength(doc, 'utf8')
const after = Buffer.byteLength(head, 'utf8')
if (before - after !== Buffer.byteLength(partRaw, 'utf8')) {
  console.error(`❌ 字节数对不上：${before} - ${after} != ${Buffer.byteLength(partRaw, 'utf8')}`)
  process.exit(1)
}

fs.writeFileSync(DOC, Buffer.from(head, 'utf8'))
const buf = fs.readFileSync(DOC)
let crlf = 0, bareLF = 0
for (let i = 0; i < buf.length; i++) {
  if (buf[i] === 13 && buf[i + 1] === 10) { crlf++; i++ } else if (buf[i] === 10) bareLF++
}
console.log(`已回退。bytes ${before} -> ${after}  CRLF=${crlf} bareLF=${bareLF} BOM=${buf[0] === 0xef}`)
if (bareLF > 0 || buf[0] === 0xef) { console.error('❌ 行尾/BOM 被破坏'); process.exit(1) }
