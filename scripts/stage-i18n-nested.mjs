/**
 * stage-i18n-nested.mjs — 把 BUG-S 的修复同步进 **git 索引/HEAD**（以 HEAD 为基底）。
 *
 * ## 与前作的关系
 *
 * `stage-i18n-fix-brace.mjs` 只修了 5 个**已证实崩溃**的键，基底是 HEAD —— 写入
 * 索引/HEAD，但**没动工作区**。于是出现「HEAD 已修、工作区未修」的分裂：
 * vite build 读工作区，打进 APK 的还是 `{{count}}`，真机照旧崩。
 *
 * 本脚本取代它：
 *   - 修**全部 10 个**「带参调用必崩」的键（`audit-i18n-compile.mjs` 实测），
 *     而不是靠猜哪几个会崩；
 *   - 只动 git 索引/HEAD，基底是 HEAD 版本，**不碰工作区**
 *     （工作区混着并发会话的未提交改动，碰不得）；
 *   - 工作区的同步由 `fix-i18n-nested-in-worktree.mjs` 负责。
 *
 * 两个脚本配套：工作区改了 -> 索引/HEAD 也要改 -> APK 与主干一致。
 *
 * ## 不动的键
 *
 * 正则 `\{\{\s*([A-Za-z_][\w.]*)\s*\}\}` 只匹配**纯变量名**的双花括号。
 * `flashcards.edit.clozePlaceholder` / `clozeHint` 里的
 * `{{c1::answer}}` / `{{c1::H₂O::hydrogen dioxide}}` 是 **Cloze 语法的字面展示**，
 * 带 `::`，天然不匹配 —— 不会被误改。这是刻意设计，别把正则放宽。
 */
import { execFileSync } from 'node:child_process'

const LOCALES_DIR = 'frontend/src/locales'
const git = (...args) => execFileSync('git', args, { encoding: 'utf8', maxBuffer: 64 * 1024 * 1024 })
const NESTED = /\{\{\s*([A-Za-z_][\w.]*)\s*\}\}/g

let staged = 0
const report = []

const files = git('ls-files', LOCALES_DIR).trim().split('\n').filter((f) => f.endsWith('.json'))
for (const file of files) {
  const before = git('show', `HEAD:${file}`)
  const obj = JSON.parse(before)
  const changed = []
  ;(function walk(node, path = []) {
    for (const [k, v] of Object.entries(node)) {
      const p = [...path, k]
      if (v && typeof v === 'object') { walk(v, p); continue }
      if (typeof v !== 'string' || !/\{\{\s*[A-Za-z_][\w.]*\s*\}\}/.test(v)) continue
      const next = v.replace(NESTED, '{$1}')
      if (next === v) continue
      node[k] = next
      changed.push(`${p.join('.')}: ${JSON.stringify(v)} -> ${JSON.stringify(next)}`)
    }
  })(obj)

  if (changed.length === 0) { report.push(`${file}: 无需改动`); continue }

  const body = JSON.stringify(obj, null, 2)
  const text = before.endsWith('\n') ? body + '\n' : body
  try { JSON.parse(text) } catch (e) {
    report.push(`${file}: 序列化非法，已放弃：${e.message}`)
    continue
  }
  const hash = execFileSync('git', ['hash-object', '-w', '--stdin'], { input: text, encoding: 'utf8' }).trim()
  git('update-index', '--cacheinfo', `100644,${hash},${file}`)
  staged++
  report.push(`${file}: 修 ${changed.length} 键`)
  for (const c of changed) report.push(`    ${c}`)
}

console.log(report.join('\n'))
console.log(`\nstaged ${staged} locale file(s)`)
