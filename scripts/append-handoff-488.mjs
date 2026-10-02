// append-handoff-488.mjs —— 把 docs/handoff/_part-4.88.md 追加到主 handoff 末尾。
//
// 与 486/487 同款：幂等 + CRLF + 无 BOM + 并发保护。
// ⚠️ MARK 必须与 _part-4.88.md 的**实际标题**一致。
//    487 那次我机械地把 486 的 MARK 复制过来（§4.87 真机 CDP 通道统一），
//    而 §4.87 的真实标题是「32 个探针在刮一个」⇒ 幂等检查失效 ⇒ **追加了两次**。
//    这次先核对标题再写。追加完必须复跑一次确认「已包含，跳过」。
import fs from 'node:fs'
import path from 'node:path'

const ROOT = path.resolve(path.dirname(new URL(import.meta.url).pathname).replace(/^\/([A-Za-z]:)/, '$1'), '..')
const DOC = path.join(ROOT, 'docs', 'handoff', '2026-09-30-android-e2e-bug-d-e-f.md')
const PART = path.join(ROOT, 'docs', 'handoff', '_part-4.88.md')
const MARK = '§4.88 BUG-V12 收口'

// 追加前先自证：MARK 必须真的在 part 里，否则幂等永远失效（487 踩过）
const rawPart = fs.readFileSync(PART, 'utf8')
if (!rawPart.includes(MARK)) {
  console.error(`❌ MARK 与 _part-4.88.md 的标题对不上（MARK=${JSON.stringify(MARK)}）。`)
  console.error('   追加上去之后幂等检查会永远失效 —— 复跑会再追加一遍。先修 MARK。')
  process.exit(2)
}

const doc = fs.readFileSync(DOC, 'utf8')
if (doc.includes(MARK)) {
  console.log('已包含 §4.88，跳过（幂等）')
  process.exit(0)
}

let part = rawPart.replace(/\r?\n/g, '\r\n')
if (!doc.endsWith('\r\n')) doc += '\r\n'
const out = doc + part.replace(/^\r\n/, '')

// ── 写回前再读一次，确认没有并发追加 ────────────────────────────────
const recheck = fs.readFileSync(DOC, 'utf8')
if (recheck !== doc) {
  console.error('❌ 主 handoff 在我读取之后被改动（并发会话追加）。')
  console.error(`   我读到的 ${doc.length} 字符，现在 ${recheck.length} 字符。`)
  console.error('   硬写会覆盖别人的内容 —— 中止，请重跑。')
  process.exit(1)
}

fs.writeFileSync(DOC, Buffer.from(out, 'utf8'))

// ── 字节级回读验收：BOM、行尾分布、标记、**只出现一次** ──────────────
const buf = fs.readFileSync(DOC)
let crlf = 0, bareLF = 0
for (let i = 0; i < buf.length; i++) {
  if (buf[i] === 13 && buf[i + 1] === 10) { crlf++; i++ } else if (buf[i] === 10) bareLF++
}
const back = buf.toString('utf8')
const times = back.split(MARK).length - 1
console.log('已追加 §4.88')
console.log(`  bytes=${buf.length}  CRLF=${crlf}  bareLF=${bareLF}  BOM=${buf[0] === 0xef}`)
console.log(`  标记出现次数=${times}  末行: ${JSON.stringify(back.slice(-70))}`)
if (!back.includes(MARK)) { console.error('❌ 回读没找到标记'); process.exit(1) }
if (times !== 1) { console.error(`❌ 标记出现了 ${times} 次，应为 1 次`); process.exit(1) }
if (bareLF > 0) { console.error('❌ 仍有 bare LF，行尾不纯'); process.exit(1) }
if (buf[0] === 0xef) { console.error('❌ 出现了 BOM'); process.exit(1) }
