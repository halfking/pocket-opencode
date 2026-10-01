// verify-dueclock-wiring.mjs — 断言 stores/flashcards.ts 的时间来源接线**分布正确**。
//
// 为什么要有这个脚本：wire-dueclock.mjs 的锚点用的是 `\n`，而本仓库文件是 CRLF，
// 三处到期判据的锚点全部未命中，脚本只打了三行提示就照样写盘，
// 结果 3 个 computed 全被接到 liveNowSec()（不响应式）⇒ 修复等于没做。
// 这类「静默部分失败」正是判据要挡住的东西，所以这里逐个核对：
// 每个到期判据 computed 内的第一处时间调用必须是 dueNowSec()，
// 每处记录时间戳必须是 liveNowSec()，位置与数量都要对上。
//
// 判据自身的两个陷阱（第一版就踩了，说明必须写进注释）：
//   1. 统计/匹配必须**排除注释行**——本文件的说明块里就写着 `dueNowSec()` 字样。
//   2. 1-based 行号喂给数组要减 1；从 computed 锚点找时间调用不能只看紧邻几行
//      （deckSummaries 的 `now` 在锚点后 15 行），要截到 computed 块结束为止。
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'

// 可传 argv[2] 覆盖被检文件路径 —— 负控用：对缺陷副本跑本判据，必须报红。
// 默认路径由本脚本位置推出，不能写死某个 worktree 绝对路径（提交后别人 clone 就会失效）。
const DEFAULT_P = fileURLToPath(new URL('../frontend/src/stores/flashcards.ts', import.meta.url))
const P = process.argv[2] ?? DEFAULT_P
const src = readFileSync(P, 'utf8')
const lines = src.split(/\r?\n/)
console.log(`被检文件：${P}`)

/** 是否注释行（说明块 / 行注释 / 尾部注释） */
const isComment = (l) => /^\s*(\/\/|\/\*|\*)/.test(l)

function lineOf(needle, from = 0) {
  for (let i = from; i < lines.length; i++) if (lines[i].includes(needle)) return i + 1
  return -1
}

/** 在 [startIdx, endIdx) 内找第一处 dueNowSec/liveNowSec 调用（跳过注释行） */
function firstTimeCall(startIdx, endIdx = lines.length) {
  for (let i = Math.max(0, startIdx); i < Math.min(endIdx, lines.length); i++) {
    if (isComment(lines[i])) continue
    const m = lines[i].match(/\b(dueNowSec|liveNowSec)\(\)/)
    if (m) return { line: i + 1, call: m[1] }
  }
  return null
}

/** 定位一个 computed 块的结束行：锚点之后第一个 `\n  })` */
function computedEndIdx(anchorLine) {
  for (let i = anchorLine; i < lines.length; i++) if (/^\s{2}\}\)/.test(lines[i])) return i
  return lines.length
}

const fails = []
function expect(ok, label, detail) {
  console.log(`${ok ? '  OK  ' : ' FAIL '} ${label}${detail ? ' :: ' + detail : ''}`)
  if (!ok) fails.push(label)
}

// 1) import 存在，且包含需要的三个符号
const importLine = lineOf("from './flashcardDueClock'")
expect(importLine > 0, '导入 flashcardDueClock', importLine > 0 ? lines[importLine - 1].trim() : '没找到')
expect(
  importLine > 0 && /dueNowSec/.test(lines[importLine - 1]) && /liveNowSec/.test(lines[importLine - 1]) && /startDueClock/.test(lines[importLine - 1]),
  '导入包含 dueNowSec/liveNowSec/startDueClock',
  importLine > 0 ? lines[importLine - 1].trim() : '',
)

// 2) 三个到期判据 computed —— 块内第一处时间调用必须是 dueNowSec
for (const anchor of [
  'const dueByDeck = computed(',
  'const deckSummaries = computed',
  'const dueCardsForDeck = computed(',
]) {
  const ln = lineOf(anchor)
  const hit = ln > 0 ? firstTimeCall(ln - 1, computedEndIdx(ln)) : null
  expect(ln > 0 && hit && hit.call === 'dueNowSec', `到期判据用响应式时间: ${anchor}`, hit ? `第 ${hit.line} 行 ${hit.call}()` : '块内未找到时间调用')
}

// 3) 记录时间戳的位置 —— 所在行本身必须是 liveNowSec
//    注意锚点不能只按字段名找：文件里同名字段在类型定义（`enqueuedAt: number`）
//    和别的分支（`reviewedAt: item.reviewedAt`）都出现过，必须要求同一行含时间调用。
for (const [label, expectLine] of [
  ['enqueue 的 enqueuedAt', 'enqueuedAt: liveNowSec()'],
  ['review 的 reviewedAt', 'reviewedAt: liveNowSec()'],
  ['fetchDueCount 的服务端 now 参数', 'fetchDueCountSvc(deckIdArg, liveNowSec())'],
  ['updatedAt', 'updatedAt: liveNowSec()'],
]) {
  const ln = lineOf(expectLine)
  expect(ln > 0, `记录时间戳用真实时间: ${label}`, ln > 0 ? `第 ${ln} 行 ${lines[ln - 1].trim()}` : `没有形如 \`${expectLine}\` 的行`)
}

// 3b) applyReviewLocally 的 `const now` 声明行本身也要是 liveNowSec
{
  const ln = lineOf('const updated = useFsrs().applyReview(prev, rating, now)')
  const decl = ln > 0 ? firstTimeCall(ln - 2, ln - 1) : null // 上一行就是 const now = ...
  expect(ln > 0 && decl && decl.call === 'liveNowSec', 'applyReviewLocally 的 const now 声明是 liveNowSec', decl ? `第 ${decl.line} 行 ${decl.call}()` : '未找到声明行')
}

// 4) startDueClock 在 store 创建路径上，且全文件只有一处
const allStart = lines.map((l, i) => (l.includes('startDueClock(') ? i + 1 : 0)).filter(Boolean)
expect(allStart.length === 1, 'startDueClock 恰好一处', `实际 ${allStart.length} 处: ${allStart}`)
const storeLn = lineOf("defineStore('flashcards'")
expect(allStart.length === 1 && allStart[0] > storeLn && allStart[0] - storeLn <= 3, 'startDueClock 紧跟在 store 定义后', `store@${storeLn} start@${allStart[0]}`)

// 5) 代码行里不再有裸 Date.now()（注释里的说明文字不算）
const rawNow = lines.map((l, i) => (!isComment(l) && /Date\.now\(\)/.test(l) ? i + 1 : 0)).filter(Boolean)
expect(rawNow.length === 0, '代码行内不再直接读 Date.now()', rawNow.length ? `行 ${rawNow.join(',')}` : '')

// 6) 全量分布统计（排除注释行与 import 行）
const codeLines = lines.filter((l) => !isComment(l) && !/^\s*import\b/.test(l))
const dueCount = codeLines.filter((l) => /\bdueNowSec\(\)/.test(l)).length
const liveCount = codeLines.filter((l) => /\bliveNowSec\(\)/.test(l)).length
console.log(`\n分布（排除注释/import）：dueNowSec() ${dueCount} 处（应 3）/ liveNowSec() ${liveCount} 处（应 5）`)
expect(dueCount === 3, 'dueNowSec 出现 3 次', `实际 ${dueCount}`)
expect(liveCount === 5, 'liveNowSec 出现 5 次', `实际 ${liveCount}`)

console.log('\n--- 时间调用点全表（含注释，仅供人工核对） ---')
lines.forEach((l, i) => {
  if (/\b(dueNowSec|liveNowSec)\(\)|startDueClock\(/.test(l)) {
    console.log(`  ${String(i + 1).padStart(4)}: ${l.trim()}`)
  }
})

if (fails.length) {
  console.log(`\n❌ 接线校验失败 ${fails.length} 项：\n  - ${fails.join('\n  - ')}`)
  process.exit(1)
}
console.log('\n✅ 接线校验全部通过')
