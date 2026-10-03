/** 修正此前写错的 useApiError import 相对路径。 */
import { readFileSync, writeFileSync } from 'node:fs'
import { join, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'

const SRC = join(dirname(fileURLToPath(import.meta.url)), '..', 'src')
for (const t of process.argv.slice(2)) {
  const full = join(SRC, t)
  const src = readFileSync(full, 'utf8')
  const depth = t.split('/').length - 1
  const want = `${'../'.repeat(depth)}composables/useApiError`
  const fixed = src.replace(
    /import \{ useApiError \} from '[^']*composables\/useApiError'/,
    `import { useApiError } from '${want}'`,
  )
  if (fixed === src) { console.log(`${t}  未找到需修正的 import`); continue }
  writeFileSync(full, fixed, 'utf8')
  console.log(`${t}  → ${want}`)
}
