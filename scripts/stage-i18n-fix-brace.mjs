/**
 * stage-i18n-fix-brace.mjs — 修 BUG-S 已**证实崩溃**的 5 个 i18n 键。
 *
 * ## 只改这 5 个，不全量改
 *
 * 扫描器 `audit-i18n-placeholder.mjs` 在每种语言里找到 12 处 `{{...}}`，9 种
 * 语言共 108 处。但它们**不是同一类东西**，盲目全改会改坏 Cloze：
 *
 *   - `flashcards.edit.clozePlaceholder` / `clozeHint` 里的 `{{c1::H₂O::hydrogen
 *     dioxide}}` 是 **Cloze 语法的字面展示**（教用户怎么写挖空），把它改成
 *     `{c1::...}` 会让用户看不到正确的语法示例。**必须保留。**
 *   - `clozeCount` / `io.exportOk` / `io.importOk` / `study.decks.dueShort`
 *     同样是 `{{name}}` 写法，但所在页面本次没渲染到它们，**未证实会崩**。
 *
 * 只有这 5 个在真机上**已证实**抛 `Not allowed nest placeholder` 导致整页白屏：
 *   flashcards.browser.resultCount    {{count}}      (CardBrowserView 渲染即崩)
 *   flashcards.browser.tagsSelected   {{count}}
 *   flashcards.stats.reviewsPerDay    {{days}}
 *   flashcards.stats.lapsesPerDay     {{days}}
 *   flashcards.stats.retentionHint    {{again}}…4 个
 *
 * 改法：`{{name}}` -> `{name}`（vue-i18n v9+ 的命名插值语法）。
 *
 * 做法与 stage-i18n-bugk.mjs 一致：HEAD 版本 -> parse -> 改 -> stringify ->
 * hash-object -w -> update-index，不碰工作区、不夹带并发会话的 locales 改动。
 */
import { execFileSync } from 'node:child_process'

const LOCALES_DIR = 'frontend/src/locales'
const git = (...args) => execFileSync('git', args, { encoding: 'utf8', maxBuffer: 64 * 1024 * 1024 })

/** 只列出已证实崩溃的键。 */
const TARGETS = [
  'flashcards.browser.resultCount',
  'flashcards.browser.tagsSelected',
  'flashcards.stats.reviewsPerDay',
  'flashcards.stats.lapsesPerDay',
  'flashcards.stats.retentionHint',
]

/** 按路径取/设值。 */
const getPath = (obj, path) => path.split('.').reduce((o, k) => (o == null ? o : o[k]), obj)

let staged = 0
const report = []

for (const file of execFileSync('git', ['ls-files', LOCALES_DIR], { encoding: 'utf8' })
  .trim().split('\n').filter((f) => f.endsWith('.json'))) {
  const before = git('show', `HEAD:${file}`)
  const obj = JSON.parse(before)
  const changed = []
  for (const path of TARGETS) {
    const cur = getPath(obj, path)
    if (typeof cur !== 'string' || !cur.includes('{{')) continue
    // {{name}} -> {name}
    const next = cur.replace(/\{\{\s*([A-Za-z_][\w.]*)\s*\}\}/g, '{$1}')
    if (next === cur) continue
    const keys = path.split('.')
    const last = keys.pop()
    keys.reduce((o, k) => o[k], obj)[last] = next
    changed.push(`${path}: ${JSON.stringify(cur)} -> ${JSON.stringify(next)}`)
  }
  if (changed.length === 0) { report.push(`${file}: 无需改动`); continue }

  const body = JSON.stringify(obj, null, 2)
  const text = before.endsWith('\n') ? body + '\n' : body
  try { JSON.parse(text) } catch (e) {
    report.push(`${file}: 序列化后非法，已放弃：${e.message}`)
    continue
  }
  const hash = execFileSync('git', ['hash-object', '-w', '--stdin'], { input: text, encoding: 'utf8' }).trim()
  git('update-index', '--cacheinfo', `100644,${hash},${file}`)
  staged++
  report.push(`${file}:\n${changed.map((c) => '    ' + c).join('\n')}`)
}

console.log(report.join('\n'))
console.log(`\nstaged ${staged} locale file(s)`)
