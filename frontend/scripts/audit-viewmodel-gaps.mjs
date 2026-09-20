// 审计 features/*.vue 是否 直连 api/stores 且未使用 useXxx composable。
// 周 5-6 UI 解耦盘点：列出"待补包 View"，给后续 PR 提供目标列表。
import { readdirSync, readFileSync, statSync } from 'node:fs'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'

const ROOT_FEATURES = join(fileURLToPath(import.meta.url), '..', '..', 'src', 'features')

function walk(dir, out = []) {
  for (const name of readdirSync(dir)) {
    const p = join(dir, name)
    const s = statSync(p)
    if (s.isDirectory()) walk(p, out)
    else if (p.endsWith('.vue')) out.push(p)
  }
  return out
}

const results = []
for (const f of walk(ROOT_FEATURES)) {
  if (f.includes('__tests__') || f.includes('node_modules')) continue
  const rel = f.slice(ROOT_FEATURES.length + 1).replace(/\\/g, '/')
  const src = readFileSync(f, 'utf8')

  // 仅"运行时" import — 排除 type-only（`import type { ... }`）
  const nonTypeApi = src.replace(/^import\s+type\s+\{[^}]*\}\s+from\s+['"][^'"]*['"];?$/gm, '')
  const apiRefs = (nonTypeApi.match(/from\s+['"][^'"]*\/api\/[^'"]*['"]/g) || []).length
    + (nonTypeApi.match(/from\s+['"]\.\.\/api['"]/g) || []).length
  const storeRefs = (nonTypeApi.match(/from\s+['"][^'"]*\/stores\/[^'"]*['"]/g) || []).length
    + (nonTypeApi.match(/from\s+['"]\.\.\/stores['"]/g) || []).length

  // "useXxx(" 任意 comopsable 调用
  const usesComposable = /\buse[A-Z][A-Za-z0-9_]*\s*\(/.test(src)

  // 有 use-* 命名（如 use-email-inbox）
  const usesHookStyle = /import\s*\{?[^}]*?\buse-[a-z][a-z0-9-]+/.test(src)

  if ((apiRefs + storeRefs) > 0 && !usesComposable && !usesHookStyle) {
    results.push({ file: rel, apiCount: apiRefs, storeCount: storeRefs })
  }
}

results.sort((a, b) => (b.apiCount + b.storeCount) - (a.apiCount + a.storeCount))
console.log(`【ViewModel 缺口盘点】共 ${results.length} 个 .vue 同时：直连 api/stores 且未使用 useXxx composable / hook`)
console.log()
for (const r of results) {
  console.log(`  api=${r.apiCount} stores=${r.storeCount}  ${r.file}`)
}
