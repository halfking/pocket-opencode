/**
 * 给指定文件补 useApiError 的 import 与声明（供批量接入使用）。
 * 用法：node scripts/add-use-api-error.mjs <相对 src 的路径...>
 */
import { readFileSync, writeFileSync } from 'node:fs'
import { join, dirname, relative } from 'node:path'
import { fileURLToPath } from 'node:url'

const SRC = join(dirname(fileURLToPath(import.meta.url)), '..', 'src')
const targets = process.argv.slice(2)
if (!targets.length) {
  console.error('用法: node scripts/add-use-api-error.mjs <相对 src 的路径...>')
  process.exit(2)
}

for (const t of targets) {
  const full = join(SRC, t)
  const src = readFileSync(full, 'utf8')
  if (/const apiError = useApiError\(\)/.test(src)) { console.log(`${t} 已有声明`); continue }

  let out = src

  // 1) import：放在最后一条相对路径 import 之后
  const importRe = /^import .*from\s+'[^']+'\r?\n/gm
  const imports = [...out.matchAll(importRe)]
  if (!imports.length) { console.log(`${t} 找不到 import，跳过`); continue }
  // 目录深度 = 路径分段数 - 1（src/features/agents/X.vue → 2 → ../../）
  const depth = t.split('/').length - 1
  const rel = depth > 0 ? '../'.repeat(depth) : './'
  const imp = `import { useApiError } from '${rel}composables/useApiError'`
  const last = imports[imports.length - 1]
  out = out.slice(0, last.index + last[0].length) + imp + '\n' + out.slice(last.index + last[0].length)

  // 2) 声明：插到最后一个顶层 const 之前（setup 顶层即可）
  const declAnchor = out.match(/^const \w+ = /m) || out.match(/^const \w+\s*=/m)
  if (!declAnchor) { console.log(`${t} 找不到 const 锚点，跳过`); continue }
  out = out.slice(0, declAnchor.index) + 'const apiError = useApiError()\n' + out.slice(declAnchor.index)

  writeFileSync(full, out, 'utf8')
  console.log(`${t}  已补 useApiError（${rel}composables/useApiError）`)
}
