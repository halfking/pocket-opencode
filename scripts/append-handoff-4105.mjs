// append-handoff-4105.mjs —— 把 docs/handoff/_part-4.105.md 追加到主 handoff 末尾。
//
// 与 486-494 同款，带两条自证：
//   ① 追加前断言 MARK 真的在 part 文件里
//   ② 回读时断言标记只出现一次
import fs from 'node:fs'
import path from 'node:path'

const ROOT = path.resolve(path.dirname(new URL(import.meta.url).pathname).replace(/^\/([A-Za-z]:)/, '$1'), '..')
const DOC = path.join(ROOT, 'docs', 'handoff', '2026-09-30-android-e2e-bug-d-e-f.md')
const PART = path.join(ROOT, 'docs', 'handoff', '_part-4.105.md')
const MARK = 'BUG-V20（待定位）'

const rawPart = fs.readFileSync(PART, 'utf8')
if (!rawPart.includes(MARK)) {
  console.error(`❌ MARK 与 _part-4.105.md 的内容对不上（MARK=${JSON.stringify(MARK)}）。`)
  console.error('   追加后幂等检查会永远失效 —— 复跑会再追加一遍。先修 MARK。')
  process.exit(2)
}

const doc = fs.readFileSync(DOC, 'utf8')
if (doc.includes(MARK)) {
  console.log('已包含 §4.105，跳过（幂等）')
  process.exit(0)
}

let part = rawPart.replace(/\r?\n/g, '\r\n')
if (!doc.endsWith('\r\n')) doc += '\r\n'
const out = doc + part.replace(/^\r\n/, '')

const recheck = fs.readFileSync(DOC, 'utf8')
if (recheck !== doc) {
  console.error('❌ 主 handoff 在我读取之后被改动（并发会话追加）。')
  console.error(`   我读到的 ${doc.length} 字符，现在 ${recheck.length} 字符。`)
  console.error('   硬写会覆盖别人的内容 —— 中止，请重跑。')
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
console.log('已追加 §4.105')
console.log(`  bytes=${buf.length}  CRLF=${crlf}  bareLF=${bareLF}  BOM=${buf[0] === 0xef}`)
console.log(`  标记出现次数=${times}`)
if (times !== 1) { console.error(`❌ 标记出现了 ${times} 次，应为 1 次`); process.exit(1) }
if (bareLF > 0) { console.error('❌ 仍有 bare LF，行尾不纯'); process.exit(1) }
if (buf[0] === 0xef) { console.error('❌ 出现了 BOM'); process.exit(1) }
