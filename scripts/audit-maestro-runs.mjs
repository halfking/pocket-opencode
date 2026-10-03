// audit-maestro-runs.mjs — 统计 ~/.maestro/tests 下每一次运行的真实成败。
//
// 为什么做这个：本轮 verifier 指控「真机 Maestro 从未成功执行一次（零运行产物）」，
// 我手上却有 85 个运行目录。但反过来，我自己的记忆里写过
// 「flashcards-write.yaml 首次全绿（连绿两次）」，而抽查 maestro.log 时
// 却看到 07:05~08:21 连续多次 CommandFailed。**两边都可能不准。**
// 所以不靠记忆、不靠抽样，直接扫全部运行产物，逐个判成败。
//
// 判据：maestro.log 里出现 `[ERROR] ... CommandFailed:` 即该次运行失败。
// 只统计含该 flow 关键字的运行，并单独标出通过的那些。
import { readdirSync, readFileSync, existsSync, statSync } from 'node:fs'
import { join } from 'node:path'

const ROOT = join(process.env.USERPROFILE || '', '.maestro', 'tests')
if (!existsSync(ROOT)) { console.log('NO_MAESTRO_TESTS_DIR'); process.exit(2) }

const runs = readdirSync(ROOT)
  .filter((n) => statSync(join(ROOT, n)).isDirectory())
  .sort()

const rows = []
for (const name of runs) {
  const logPath = join(ROOT, name, 'maestro.log')
  if (!existsSync(logPath)) continue
  let log
  try { log = readFileSync(logPath, 'utf8') } catch { continue }

  const failed = /CommandFailed:/.test(log)
  const errCount = (log.match(/\[ERROR\]/g) || []).length
  const isFlashcard = /回归正面|flashcards-write|今日待复习|开始复习/.test(log)
  const isNotes = /新建笔记|notes-crud/.test(log)
  // flow 名：Maestro 打印 "Running flow <name>" 或 "Flow <name>"
  const fm = log.match(/Running flow[:\s]+(\S+)/i) || log.match(/Flow[:\s]+([\w\-.]+)/i)
  rows.push({ name, failed, errCount, isFlashcard, isNotes, flow: fm ? fm[1] : '' })
}

const flash = rows.filter((r) => r.isFlashcard)
const notes = rows.filter((r) => r.isNotes)
const passAll = rows.filter((r) => !r.failed)
const failAll = rows.filter((r) => r.failed)

console.log(`=== 总览 ===`)
console.log(`有 maestro.log 的运行: ${rows.length}`)
console.log(`  判定通过（无 CommandFailed）: ${passAll.length}`)
console.log(`  判定失败（含 CommandFailed）: ${failAll.length}`)
console.log(`  含闪卡判据的运行: ${flash.length}（通过 ${flash.filter(r=>!r.failed).length} / 失败 ${flash.filter(r=>r.failed).length}）`)
console.log(`  含笔记判据的运行: ${notes.length}（通过 ${notes.filter(r=>!r.failed).length} / 失败 ${notes.filter(r=>r.failed).length}）`)

console.log(`\n=== 含闪卡判据的运行（按时间）===\n${'run'.padEnd(18)}${'结果'.padEnd(6)}errCount  flow`)
for (const r of flash) {
  console.log(`${r.name.padEnd(18)}${(r.failed ? '❌失败' : '✅通过').padEnd(8)}${String(r.errCount).padEnd(9)}${r.flow}`)
}

const fp = flash.filter((r) => !r.failed)
console.log(`\n=== 闪卡 flow 通过的运行 ===`)
if (fp.length) fp.forEach((r) => console.log('  ✅ ' + r.name))
else console.log('  ⚠️  没有任何一次含闪卡判据的运行通过')
