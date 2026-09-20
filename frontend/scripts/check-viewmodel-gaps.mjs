#!/usr/bin/env node
// CI gate：ViewModel 缺口硬门槛（2026-09-20）。
//
// 用法：
//   node scripts/check-viewmodel-gaps.mjs
//   HITS_ALLOWED=0 node scripts/check-viewmodel-gaps.mjs   # 严格门槛
//   npm run check:vm-gaps
//
// 行为：
//   - 运行审计 + 命中 > HITS_ALLOWED 时退出非零
//   - 命中 < 阈值时也提示"可收紧"
//   - 默认阈值 1（=ConfigList.vue，留到下次配置域大改）
//
// 设计：与 audit-viewmodel-gaps.mjs 共用判定逻辑，避免重复扫描实现漂移。

import { readdirSync, readFileSync, statSync } from 'node:fs'
import { join, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'

const HITS_ALLOWED = Number.parseInt(process.env.HITS_ALLOWED ?? '0', 10)
const HERE = dirname(fileURLToPath(import.meta.url))
const ROOT_FEATURES = join(HERE, '..', 'src', 'features')

function walk(dir, out = []) {
  for (const name of readdirSync(dir)) {
    const p = join(dir, name)
    const s = statSync(p)
    if (s.isDirectory()) walk(p, out)
    else if (p.endsWith('.vue')) out.push(p)
  }
  return out
}

const hits = []
for (const f of walk(ROOT_FEATURES)) {
  if (f.includes('__tests__') || f.includes('node_modules')) continue
  const rel = f.slice(ROOT_FEATURES.length + 1).replace(/\\/g, '/')
  const src = readFileSync(f, 'utf8')
  // 仅运行时 import；type-only 被剔除
  const nonType = src.replace(/^import\s+type\s+\{[^}]*\}\s+from\s+['"][^'"]*['"];?$/gm, '')
  const apiRefs = (nonType.match(/from\s+['"][^'"]*\/api\/[^'"]*['"]/g) || []).length
    + (nonType.match(/from\s+['"]\.\.\/api['"]/g) || []).length
  const storeRefs = (nonType.match(/from\s+['"][^'"]*\/stores\/[^'"]*['"]/g) || []).length
    + (nonType.match(/from\s+['"]\.\.\/stores['"]/g) || []).length
  const usesComposable = /\buse[A-Z][A-Za-z0-9_]*\s*\(/.test(src)
  const usesHookStyle = /import\s*\{?[^}]*?\buse-[a-z][a-z0-9-]+/.test(src)
  if ((apiRefs + storeRefs) > 0 && !usesComposable && !usesHookStyle) {
    hits.push({ file: rel, apiCount: apiRefs, storeCount: storeRefs })
  }
}
hits.sort((a, b) => (b.apiCount + b.storeCount) - (a.apiCount + a.storeCount))

console.log(`【ViewModel 缺口盘点】共 ${hits.length} 个 .vue 同时：直连 api/stores 且未使用 useXxx composable / hook`)
console.log()
for (const h of hits) {
  console.log(`  api=${h.apiCount} stores=${h.storeCount}  ${h.file}`)
}
console.log()

if (hits.length > HITS_ALLOWED) {
  console.error(`❌ 命中 ${hits.length} > 阈值 ${HITS_ALLOWED}`)
  console.error('处理方法：为该 .vue 抽出 useXxxViewModel / composable 后再提交')
  process.exit(1)
}

if (hits.length < HITS_ALLOWED) {
  console.log(`✅ 命中 ${hits.length} < 阈值 ${HITS_ALLOWED}（可收紧 HITS_ALLOWED=${hits.length}）`)
  process.exit(0)
}

console.log(`✅ 命中 ${hits.length} = 阈值（通过）`)
process.exit(0)
