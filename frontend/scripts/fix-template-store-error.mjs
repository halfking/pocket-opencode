/**
 * 把模板里 `{{ xxx.error }}` 这种「直接渲染未归一的 store 原始错误」改成经 apiError 归一。
 * 只做机械替换，兜底 key 由调用方通过 --fallback 指定或按默认 loadSettingsFailed。
 *
 * 用法: node scripts/fix-template-store-error.mjs --file <相对 src 路径> [--fallback errors.xxx]
 */
import { readFileSync, writeFileSync } from 'node:fs'
import { join, dirname, relative } from 'node:path'
import { fileURLToPath } from 'node:url'

const SRC = join(dirname(fileURLToPath(import.meta.url)), '..', 'src')
const args = process.argv.slice(2)
const fileArg = args.indexOf('--file')
const fbArg = args.indexOf('--fallback')
const target = fileArg === -1 ? null : args[fileArg + 1]
const fallback = fbArg === -1 ? 'errors.loadSettingsFailed' : args[fbArg + 1]
const apply = args.includes('--apply')

if (!target) { console.error('用法: node scripts/fix-template-store-error.mjs --file <path> [--fallback errors.x] [--apply]'); process.exit(2) }

const full = join(SRC, target)
const lines = readFileSync(full, 'utf8').split(/\r?\n/)
const changed = []

const out = lines.map((line, i) => {
  // {{ store.error }}  /  {{ accStore.error }}  /  {{ a.error }}
  const m = line.match(/^(\s*)(.*?)(\{\{\s*)([A-Za-z_$][\w$]*(?:\.[A-Za-z_$][\w$]*)*\.error)(\s*\}\})(.*)$/)
  if (!m) return line
  const [, indent, pre, open, expr, close, post] = m
  if (/apiError\(|toUserMessage\(/.test(line)) return line
  const next = `${indent}${pre}${open}apiError(${expr}, '${fallback}')${close}${post}`
  changed.push(`${i + 1}: ${line.trim()}\n   → ${next.trim()}`)
  return next
})

console.log(`${target}  将修改 ${changed.length} 行：`)
for (const c of changed) console.log('  ' + c)
if (apply && changed.length) {
  writeFileSync(full, out.join('\n'), 'utf8')
  console.log(`\n已写入 ${relative(process.cwd(), full)}`)
} else if (changed.length) {
  console.log('\n（干跑，加 --apply 生效）')
}
