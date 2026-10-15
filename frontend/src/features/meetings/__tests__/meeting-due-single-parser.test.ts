// meeting-due-single-parser.test.ts —— 门禁：**期限解析只允许有一份实现**（§391）。
//
// ── 为什么这道门存在 ───────────────────────────────────────────────
//
// `docs/design/2026-10-06-meeting-timeline-to-schedule.md` 记载：当时有**三处**
// 各自写 `parseDue`，都只做 `Date.parse` ⇒ 中文期限一律 NaN ⇒ `due_at = null`。
// 现在它们已收敛成 `parseDueAt` 一份（这是 §52 那条纪律的成果）。
//
// ⚠ 但「收敛」这件事**没有任何门在守**。一旦有人为了某个新入口
// 又写一份 `parseDue`，§390 刚修好的那两处（补零对齐 + 兜底 fail-closed）
// 会**静默地不作用于新副本** —— 而门禁全绿。
// ⇒ 本门把「只有一份」变成不变量。
//
// ── 三条断言 ───────────────────────────────────────────────────────
//
//   A7 `export function parseDueAt` 在全 src 下**恰好一处**定义
//   A8 真正决定期限的那两个函数里**不得再用 `Date.parse`**（原始病因）
//      ⚠ 两个前置：
//        ① 断言前必须**剥注释** —— 四个文件的头注释里**大量提到** `Date.parse`
//           作为历史说明（meeting-due.ts:7/196、meeting-due-plan.ts:31、
//           meeting-todo-persist.ts:64）。不剥就会恒红。
//        ② **不能按目录扫** —— `features/notes/notes-row.ts` 的 Date.parse
//           读的是资产时间戳，与期限无关；按目录扫会**报错报错的文件**。
//   A9 `resolveTodoDue` 必须**委托**给 `parseDueAt`（而不是自己再解一遍）

import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readdirSync, readFileSync, statSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const HERE = path.dirname(fileURLToPath(import.meta.url))
const SRC = path.resolve(HERE, '../../..')   // frontend/src

const WALK_EXT = new Set(['.ts', '.tsx', '.vue', '.js', '.mjs'])

function walk(dir, out = []) {
  for (const name of readdirSync(dir)) {
    if (name === 'node_modules' || name === 'dist') continue
    const p = path.join(dir, name)
    if (statSync(p).isDirectory()) walk(p, out)
    else if (WALK_EXT.has(path.extname(p)) && !/\.test\.[cm]?[jt]sx?$/.test(p)) out.push(p)
  }
  return out
}

/** 剥注释：判据量的是「代码有没有用」，不是「注释里有没有提过」。 */
function stripComments(src) {
  return src
    .replace(/\/\*[\s\S]*?\*\//g, ' ')
    .replace(/<!--[\s\S]*?-->/g, ' ')
    .replace(/^\s*\/\/.*$/gm, ' ')
}

const FILES = walk(SRC)

test('A7 · parseDueAt 全 src 恰好一处定义', () => {
  const defs = FILES.filter((f) => /export\s+function\s+parseDueAt\b/.test(stripComments(readFileSync(f, 'utf8'))))
  assert.equal(
    defs.length, 1,
    `期限解析器必须唯一，实测 ${defs.length} 处：\n` + defs.map((f) => '  ' + path.relative(SRC, f)).join('\n') +
      '\n⚠ 多出来的副本不会继承 §390 的「补零对齐 + 兜底 fail-closed」，且门禁不会红。',
  )
})

test('A8 · 期限解析的两个函数里不得再用 Date.parse', () => {
  // ⚠⚠ 首版按**目录**扫（features/meetings/ 与 features/notes/），结果把
  //   `features/notes/notes-row.ts` 报成违规 —— 而那里的 Date.parse 在
  //   `readAssetTimestamp` 里，读的是 createdAt/updatedAt 这类**资产时间戳**，
  //   **与期限无关**。⇒ 报错报的**不是真的**，会让人去改一个无辜的文件。
  //   ★ 这正是 §385.4 那条：「报『缺这一行』会把人引去改文档，而真因在别处」。
  // ⇒ 收窄到**真正决定期限的那两个函数**。A7 + A9 已保证所有期限解析都经过它们，
  //   所以这一条覆盖完整，只是判据更准。
  const DUE_FUNCS = [
    ['features/meetings/meeting-due.ts', /export\s+function\s+parseDueAt/],
    ['features/meetings/meeting-due-plan.ts', /export\s+function\s+resolveTodoDue/],
  ]
  for (const [rel, decl] of DUE_FUNCS) {
    const f = path.join(SRC, rel)
    const src = stripComments(readFileSync(f, 'utf8'))
    assert.match(src, decl, `${rel} 里找不到预期的那条声明，判据需要跟着改`)
    assert.equal(
      /Date\.parse\s*\(/.test(src), false,
      `${rel} 的期限解析实现里出现了 Date.parse ⇒ 中文期限会重新变成 NaN` +
        '（timeline 文档 §0 记载的原始病因）',
    )
  }
})

test('A9 · resolveTodoDue 必须委托给 parseDueAt，而不是自己再解一遍', () => {
  const f = path.join(SRC, 'features/meetings/meeting-due-plan.ts')
  const src = stripComments(readFileSync(f, 'utf8'))
  assert.match(src, /export\s+function\s+resolveTodoDue/, 'resolveTodoDue 不见了')
  assert.match(
    src, /return\s+parseDueAt\(/,
    'resolveTodoDue 必须在实现里调用 parseDueAt ⇒ 会议待办与随手记待办两个入口拿到的是同一套口径',
  )
})