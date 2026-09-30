/**
 * fix-i18n-nested-in-worktree.mjs — 把 BUG-S 的修复真正写进**工作区**。
 *
 * ## 为什么 HEAD 已修、真机还崩
 *
 * 上一轮我用 `stage-i18n-fix-brace.mjs` 把修复写进了 **git 索引 / HEAD**
 * （HEAD 的 en-US 是 `{count} results`，正确），但**工作区文件没动**
 * （工作区仍是 `{{count}} results`）。
 *
 * 而 `vite build` 读的是**工作区**的 locales，不是 HEAD。于是新 APK 打包进去的
 * 还是坏版本 —— 装机复验 appHTMLLen 371->371，错误一字不变。
 *
 * 这是本轮最该记的教训：**只提交不改工作区，等于没修**。
 * 验证环境的输入永远来自工作区，不是 git。
 *
 * ## 修哪些
 *
 * 用 `audit-i18n-compile.mjs`（带参调用）实测出每语言 10 个「带参调用必崩」的键：
 *   flashcards.review.clozeCount / edit.clozeCount
 *   flashcards.browser.resultCount / tagsSelected
 *   flashcards.stats.reviewsPerDay / lapsesPerDay / retentionHint
 *   flashcards.io.exportOk / importOk
 *   study.decks.dueShort
 *
 * **不动** `flashcards.edit.clozePlaceholder` / `clozeHint` 里的
 * `{{c1::H₂O::hydrogen dioxide}}` —— 那是 Cloze 语法的**字面展示**（教用户怎么
 * 写挖空），不是占位符。下面的正则 `\{\{\s*([A-Za-z_][\w.]*)\s*\}\}` 不匹配
 * `c1::H₂O`，所以天然不会碰它们。这是刻意设计，别改宽了。
 *
 * ## 为什么不直接覆盖文件
 *
 * 工作区的 locales 混着并发会话的未提交改动（en-US 有 47 行增量）。整体覆盖会
 * 把别人的工作弄丢。这里走 JSON.parse -> 改 -> JSON.stringify 往返：实测
 * `JSON.stringify(obj, null, 2)` 与本仓库格式**逐字节相同**，所以往返只改目标键，
 * 其他内容原样保留。往返后逐文件做 diff 校验，变化行数与预期不符就拒绝写入。
 */
import { readFileSync, writeFileSync, readdirSync } from 'node:fs'
import { join, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'
import { execFileSync } from 'node:child_process'

const ROOT = join(dirname(fileURLToPath(import.meta.url)), '..')
const LOCALES = join(ROOT, 'frontend/src/locales')

/** 只替换「纯变量名」形式的双花括号；`{{c1::x}}` 这类带 :: 的不动。 */
const NESTED = /\{\{\s*([A-Za-z_][\w.]*)\s*\}\}/g

const report = []
let touched = 0

for (const file of readdirSync(LOCALES).filter((f) => f.endsWith('.json'))) {
  const full = join(LOCALES, file)
  const before = readFileSync(full, 'utf8')
  const obj = JSON.parse(before)

  const changed = []
  ;(function walk(node, path = []) {
    for (const [k, v] of Object.entries(node)) {
      const p = [...path, k]
      if (v && typeof v === 'object') { walk(v, p); continue }
      if (typeof v !== 'string') continue
      if (!/\{\{\s*[A-Za-z_][\w.]*\s*\}\}/.test(v)) continue
      const next = v.replace(NESTED, '{$1}')
      if (next === v) continue
      node[k] = next
      changed.push(`${p.join('.')}: ${JSON.stringify(v)} -> ${JSON.stringify(next)}`)
    }
  })(obj)

  if (changed.length === 0) { report.push(`${file}: 无需改动`); continue }

  const body = JSON.stringify(obj, null, 2)
  const after = before.endsWith('\n') ? body + '\n' : body

  try { JSON.parse(after) } catch (e) {
    report.push(`${file}: 序列化非法，拒绝写入：${e.message}`)
    continue
  }

  // 往返自检：除目标键外不应有别的变化。统计实际变化行数与预期比对。
  const a = before.split('\n')
  const b = after.split('\n')
  let diffLines = 0
  for (let i = 0; i < Math.max(a.length, b.length); i++) if (a[i] !== b[i]) diffLines++
  // 每个改动键影响 1 行（末行加逗号的情况会多 1 行）
  if (diffLines > changed.length * 2 + 2) {
    report.push(`${file}: 变化行数 ${diffLines} 远超预期（改动 ${changed.length} 键），拒绝写入`)
    continue
  }

  writeFileSync(full, after)
  touched++
  report.push(`${file}: 修 ${changed.length} 键（diff ${diffLines} 行）`)
  for (const c of changed) report.push(`    ${c}`)
}

console.log(report.join('\n'))
console.log(`\n改写工作区文件 ${touched} 个`)

// 复核：改完立刻用带参审计确认不再有「必崩」键
try {
  execFileSync('node', [join(ROOT, 'scripts/audit-i18n-compile.mjs')], { stdio: 'inherit' })
} catch {
  console.log('\n注意：带参审计仍有失败项，见上。')
}
