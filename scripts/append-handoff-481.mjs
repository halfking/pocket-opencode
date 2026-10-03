// append-handoff-480.mjs —— 把 docs/handoff/_part-4.81.md 追加到主 handoff 末尾。
//
// 与 append-handoff-aw.mjs 同款，但路径指向本 worktree，且针对 §4.81。
// 主文档约定：CRLF、无 BOM。幂等：已包含就拒绝二次追加。
//
// ⚠️ 追加前必须重新读主文档。主文档 461KB，且**并发会话也在往里追加**，
//    读一次写一次之间别人插进去的内容会被覆盖掉。所以：
//      1) 写回前比对"我读到的末尾"是否仍等于"现在的末尾"；
//      2) 不一致就中止并提示重跑，不要硬写。
import fs from 'node:fs'
import path from 'node:path'

const ROOT = path.resolve(path.dirname(new URL(import.meta.url).pathname).replace(/^\/([A-Za-z]:)/, '$1'), '..')
const DOC = path.join(ROOT, 'docs', 'handoff', '2026-09-30-android-e2e-bug-d-e-f.md')
const PART = path.join(ROOT, 'docs', 'handoff', '_part-4.81.md')
const MARK = '§4.81 真跑 Maestro + 回应外部审计'

const doc = fs.readFileSync(DOC, 'utf8')
if (doc.includes(MARK)) {
  console.log('已包含 §4.81，跳过（幂等）')
  process.exit(0)
}

let part = fs.readFileSync(PART, 'utf8')
part = part.replace(/\r?\n/g, '\r\n')
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

// 字节级回读验收：BOM、行尾分布、末行
const buf = fs.readFileSync(DOC)
let crlf = 0, bareLF = 0
for (let i = 0; i < buf.length; i++) {
  if (buf[i] === 13 && buf[i + 1] === 10) { crlf++; i++ } else if (buf[i] === 10) bareLF++
}
const back = buf.toString('utf8')
console.log(`已追加 §4.81`)
console.log(`  bytes=${buf.length}  CRLF=${crlf}  bareLF=${bareLF}  BOM=${buf[0] === 0xef}`)
console.log(`  末行: ${JSON.stringify(back.slice(-70))}`)
if (bareLF > 0) { console.error('❌ 仍有 bare LF，行尾不纯'); process.exit(1) }
