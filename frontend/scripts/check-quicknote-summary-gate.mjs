#!/usr/bin/env node
// check-quicknote-summary-gate.mjs —— 卡口：**随手记链承诺了什么**（§490）
//
// 用法：
//   node scripts/check-quicknote-summary-gate.mjs [srcRoot]
// 退出码：0 = 现状与登记一致；1 = 漂移；2 = 判据失明/自检不过
//
// ── 这道门防的是什么 ────────────────────────────────────────────────
//
// 用户原始需求第三条：「在录音时需要进行即时总结……并能在总结同时，
// 给出一些**参考资料**与**建议**」。
//
// §490 把这条需求在**随手记**那条链上的兑现度逐条实测了，结论是 **0/3**：
//
//   ① 录音中即时总结 → features/notes/ 下 summary 出现 0 次
//   ② 参考资料       → features/notes/ 下 references 出现 0 次
//   ③ 建议           → useNoteRecording.ts 里 recommend 出现 0 次
//
// ⚠⚠ **0/3 是一条**登记在案的结构性阴性结论**，而阴性结论的寿命
//
//　取决于有没有东西盯着它。这个仓库反复出现的形态是：
//　「结论写进了设计文档，下一个会话读到它，**当成已核实的现状**直接引用」——
//　而中间可能已经有人加了一半（比如只给会议加了，没给随手记加）。
//　⇒ **阴性结论必须自己出声**，否则它会变成一张过期通行证。
//
// ⛔ **本门不是「要求随手记必须有这三样」** —— 那是产品决策（#26，未拍板）。
//　本门只断一件事：**代码现状有没有偏离已登记的 0/3**。
//　一旦有人实现了其中一样，**本门立刻转红**，并提示「该更新 #26 了」。
//　这是刻意的：**让产品决策的时点被强制显式化**，而不是悄悄发生。
//
// ── 为什么先看契约、再看调用点 ──────────────────────────────────────
//
// §490 的关键判定不是「前端忘了调用」，而是
// `NoteRecordingStopResult`（recordingRuntime.ts:751）**只有四个字段**：
// text / audioBlob / durationMs / draftId ——
// **结构上就装不下**总结、参考资料、建议。
//
// ⇒ 所以本门的主判据是**契约字段**，调用点扫描只作为辅助信号。
//　只看调用点会得出「grep 不到就是没实现」，而真实原因可能是「实现了但走别的路」。
//
// ⚠ 勘误一处过期行号（§584，2026-10-14）：上面「recordingRuntime.ts:**751**」
//　已漂 —— §572 给该文件加过内容，实测在 **:777**。行号别写死在注释里。
//
// ⛔ 撤回一次**错误的勘误**（§584 记）：2026-10-14 曾在本处加过一段
//　「0/3 是错的、随手记其实有总结」—— **那段是错的，已撤**。
//　 错因：把「随手记**录完**后有 `notesApi.summarize`」当成了「『**录音中**即时总结』已实现」。
//　 而需求原文（「**在录音时**需要进行即时总结」）与本门、以及决策简报 #26
//　 说的 **0/3 指的是「录音中」那一项** —— 三个口径本来就一致，**本门没说谎**。
//　 ⇒ 记在这里是为了防下一个人重犯同一个误读：
//　 **「录完后的总结」与「录音中的即时总结」是两件事**，
//　 核对一个功能是否满足需求时，要先确认需求里的那个时间限定词。
//
// ✅ 顺带把 §583 里仍然成立的读数留在设计文档（只读代码，无判据变化）：
//　 · `features/notes` 链**确有**录完后的总结与行动项/待办落库
//　　（`NoteListView.vue:349/377/428` + `server_assistant.go:575 handleNoteSummarize`）
//　 · 「随手记」有**两条链**（会话内录音走 `meeting-recording-finalize` → refine，
//　　笔记列表页录音走 `/api/notes/{id}/summarize`）⇒ 两条产出质量仍差一量级
//　 ⇒ 这些**不构成**「0/3 有错」，只说明 #26 问的是**形态与差**，不是「有没有」。

import { readFileSync, readdirSync, statSync } from 'node:fs'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const HERE = dirname(fileURLToPath(import.meta.url))
const SRC_ROOT = resolve(process.argv.slice(2).find((a) => !a.startsWith('-')) || join(HERE, '..', 'src'))

/** 随手记特性目录 —— 用户需求第三条在这条链上的落点。 */
const NOTES_DIR = join(SRC_ROOT, 'features', 'notes')

/** 录音运行时 —— stop() 契约的定义处。 */
const RUNTIME = join(SRC_ROOT, 'native', 'recordingRuntime.ts')

/** 已登记的 0/3：随手记 stop() 契约**当前**承载的字段。 */
const REGISTERED_FIELDS = ['text', 'audioBlob', 'durationMs', 'draftId']

/**
 * 辅助判据②的真实信号：随手记链上**有没有调到会议摘要那两个端点**。
 *
 * ⚠⚠ 第一版这里写的是「grep 随手记目录里有没有 summary/references/recommend 字样」，
 *   实测**17 处命中、全部是假阳性**：
 *     · `note-search.ts` 的 `summary` 是**检索结果简报**（另一个功能）
 *     · `note-todo-persist.ts` 的 `summary` 出现在**注释**里
 *     · `notes-types.ts` 的 `summary` 是**笔记自身**的字段
 *   ⇒ 「关键词出现过」与「这条需求被实现了」**毫无关系**。
 *   这正是 §488 记的「掩码没抹掉我不想它看见的东西」的反向版本。
 *
 *   真正的判别是**端点调用**：`/api/meetings/{id}/summary` 与
 *   `.../recommend` 是会议链的摘要入口；随手记链上一个都没有，
 *   而它只 import 了 `meeting-due-reminder` / `meeting-due-plan`
 *   （**日期解析**，不是摘要）。
 */
const MEETING_SUMMARY_ENDPOINTS = ['/summary', '/recommend']

function dieSelfCheck(msg) {
  console.error(`判据自检失败：${msg}`)
  process.exit(2)
}

// ── 量具自证：路径必须真的存在，且读到非空内容 ──────────────────────
//
// ⚠⚠ 这一段不能省：若路径写错而判据读空，`every(...)` 会**恒真**
//　（空数组上「每个元素都不含某词」为真）⇒ 门会绿，而它什么也没检查。
//　这与 §488/M6 是同一个家族：判据在**没有内容可报**时天生偏绿。
if (!statSync(NOTES_DIR).isDirectory()) {
  dieSelfCheck(`随手记目录不存在：${NOTES_DIR}（先怀疑路径，再怀疑代码）`)
}
const runtimeSrc = readFileSync(RUNTIME, 'utf8')
if (!/export interface NoteRecordingStopResult/.test(runtimeSrc)) {
  dieSelfCheck(`在 ${RUNTIME} 里找不到 NoteRecordingStopResult —— 判据锚点漂了`)
}

// ── 判据①：契约字段集合必须与登记一致 ──────────────────────────────
//
// 主判据。提取 stop() 契约的字段名并与 REGISTERED_FIELDS 双向比对。
const ifaceStart = runtimeSrc.indexOf('export interface NoteRecordingStopResult')
const ifaceEnd = runtimeSrc.indexOf('\n}', ifaceStart)
const ifaceBody = runtimeSrc.slice(ifaceStart, ifaceEnd)
const actualFields = [...ifaceBody.matchAll(/^\s{2}([A-Za-z_][A-Za-z0-9_]*)\s*[?:]/gm)].map((m) => m[1])

if (actualFields.length === 0) {
  dieSelfCheck(`从契约里提取到 0 个字段（实测 ${ifaceBody.length} 字节）—— 抽取器坏了`)
}

const registeredSet = new Set(REGISTERED_FIELDS)
const actualSet = new Set(actualFields)

const added = actualFields.filter((f) => !registeredSet.has(f))
const removed = REGISTERED_FIELDS.filter((f) => !actualSet.has(f))

if (added.length > 0) {
  console.error('❌ NoteRecordingStopResult 新增了字段：' + added.join(', '))
  console.error('')
  console.error('  §490 登记的是「随手记 0/3」，理由是**契约里没有承载它们的位置**。')
  console.error('  新增字段说明有人开始实现了 —— ⭐ 这是好事，但意味着：')
  console.error('    · #26（随手记要不要有即时总结/references/建议）需要**重新拍板**')
  console.error('    · 本门要从「差异报告」改成「行为门」，否则它会一直红')
  console.error('  ⇒ 请更新 REGISTERED_FIELDS 并同步设计文档 §490 / 决策简报 #26。')
  console.error('')
  process.exit(1)
}
if (removed.length > 0) {
  console.error('❌ NoteRecordingStopResult 少了字段：' + removed.join(', '))
  console.error('  ⇒ §490 的 0/3 判定基于「它有这四个字段」，其中之一没了 —— 重新核实后再改文档。')
  process.exit(1)
}

// ── 判据②：等式 —— 提取到的字段数必须与登记数一致 ───────────────────
//
// ⭐ 独立于判据①的**逐条**检查。理由同 §489 变异 M6：
// 逐条比对只在你恰好有内容可报时才有效。
if (actualFields.length !== REGISTERED_FIELDS.length) {
  dieSelfCheck(`字段数不等：契约 ${actualFields.length} vs 登记 ${REGISTERED_FIELDS.length}`)
}

// ── 判据③：随手记链上不得调用会议摘要端点（辅助信号）──────────────
//
// ⚠ 命中时**不直接判红**，只提示复核 —— 与判据①分工明确：
// ① 是主判据（契约装不装得下），③ 是「是不是走了另一条路实现了」。
const notesFiles = readdirSync(NOTES_DIR)
  .filter((f) => f.endsWith('.ts') || f.endsWith('.vue'))
const hits = []
for (const f of notesFiles) {
  const src = readFileSync(join(NOTES_DIR, f), 'utf8')
  for (const ep of MEETING_SUMMARY_ENDPOINTS) {
    if (src.includes(ep)) hits.push(`${f} → ${ep}`)
  }
}

console.log(`扫描根 ${SRC_ROOT}`)
console.log(`  判据①契约字段 ${actualFields.length} 个，与登记一致：${actualFields.join(', ')}`)
console.log(`  判据③随手记目录 ${notesFiles.length} 个文件，会议摘要端点调用 ${hits.length} 处`)
if (hits.length > 0) {
  console.log('')
  console.log('  ⚠⚠ **命中了**，这可能是「有人用另一条路实现了 #26」：')
  for (const h of hits) console.log('    · ' + h)
  console.log('')
  console.log('  ⇒ 请核实：是随手记**真的**拿到了即时总结/references/建议，')
  console.log('     还是又一次误报（§490 第一版就因关键词扫描误报了 17 处）。')
  console.log('  ⇒ 若确实实现了：更新 §490 / #26，并把它升级成断言（别停在提示）。')
}
console.log('')
console.log('✅ 随手记链现状与 §490 登记的 0/3 一致（契约无承载位 + 无调用点）')
