/**
 * 核对：Pinia store 暂存的原始 error，消费视图到底有没有归一。
 *
 * 架构约定（本轮确立）：store 只保存后端原始错误（`error.value = e?.message`），
 * 由视图在渲染前经 toUserMessage / useApiError 归一。
 * 这样避免了「store 依赖全局 i18n 访问器」的架构决策，同时不让原文上屏。
 *
 * 风险点：只要有**任何一个**消费视图直接把 `{{ store.error }}` 渲染出去，
 * 约定就被打破，原始错误就会漏到界面上。这个脚本就是守住这条约定。
 *
 * Run: node scripts/audit-store-error-consumers.mjs
 */
import { readFileSync, readdirSync, statSync } from 'node:fs'
import { join, dirname, extname, relative } from 'node:path'
import { fileURLToPath } from 'node:url'

const SRC = join(dirname(fileURLToPath(import.meta.url)), '..', 'src')
const SKIP = new Set(['node_modules', '.git', 'dist', '__tests__', 'android', 'ios'])

/** 会把 store 原始 error 直接渲染到界面的写法。 */
const RENDERS_RAW = /\{\{\s*[A-Za-z_$][\w$]*(?:\.[A-Za-z_$][\w$]*)*\.error\s*\}\}|v-if\s*=\s*"[^"]*\.[A-Za-z_$][\w$]*\.error\b/
/** 已做归一的标志。 */
const NORMALIZED = /\b(apiError|toUserMessage)\s*\(/

const vueFiles = []
;(function walk(d) {
  for (const n of readdirSync(d)) {
    if (SKIP.has(n)) continue
    const full = join(d, n)
    if (statSync(full).isDirectory()) { walk(full); continue }
    if (extname(full) === '.vue') vueFiles.push(full)
  }
})(SRC)

const findings = []
for (const file of vueFiles) {
  const text = readFileSync(file, 'utf8')
  if (!RENDERS_RAW.test(text)) continue
  if (NORMALIZED.test(text)) continue
  const lines = text.split('\n')
  lines.forEach((l, i) => {
    if (/\{\{\s*[A-Za-z_$][\w$]*(?:\.[A-Za-z_$][\w$]*)*\.error\s*\}\}|v-if\s*=\s*"[^"]*\.[A-Za-z_$][\w$]*\.error\b/.test(l)) {
      findings.push(`${relative(SRC, file).replace(/\\/g, '/')}:${i + 1}  ${l.trim().slice(0, 90)}`)
    }
  })
}

console.log(`扫描 ${vueFiles.length} 个 .vue`)
if (!findings.length) {
  console.log('✅ 没有「直接渲染 store 原始 error 且未归一」的视图。')
} else {
  console.log(`\n❌ ${findings.length} 处直接渲染未归一的 store error：\n`)
  for (const f of findings) console.log('  ' + f)
  process.exitCode = 1
}
