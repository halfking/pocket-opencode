// check-icon-subset.mjs —— 校验「运行时才决定的图标名」确实在字体子集里。
//
// 用法：node scripts/check-icon-subset.mjs
//
// 为什么需要它：
// build-material-symbols-subset.mjs 的扫描只认**字面量**图标名
// （`material-symbols-outlined"…>([a-z_]+)<`）。而下面这些名字是在 JS 表达式里
// 决定的——数据表里写 `icon: 'forum'`，模板里写 `{{ item.icon }}`：
//
//   features/settings/SettingsView.vue       主题三选项
//   features/more/MoreHubView.vue            导航网格
//   components/base/SettingsMenuDrawer.vue   设置菜单
//   features/flashcards/FlashcardEditView.vue 卡片模板
//   features/sessions/SessionComposer.vue     斜杠命令
//   features/study/StudyHubView.vue           dueRows / sourceIcon()
//
// 静态扫描原理上抓不到它们。它们今天能正常显示，**只是因为同样的名字在别处
// 恰好以字面量出现过**——那是巧合，不是保证。删掉那个巧合的用法，图标就变豆腐块。
//
// 这个脚本把巧合变成不变量：扫出所有 `icon: 'x'` 声明，与「字面量 + 兜底 +
// 动态白名单」求差集；有差集就退出码 1。
//
// 注意：它不重建字体，只报告。重建由 build-material-symbols-subset.mjs 负责。

import { readFileSync, readdirSync, statSync } from 'node:fs'
import { join, dirname, relative } from 'node:path'
import { fileURLToPath } from 'node:url'

const __dirname = dirname(fileURLToPath(import.meta.url))
const ROOT = join(__dirname, '..')
const SRC = join(ROOT, 'src')

// 与 build-material-symbols-subset.mjs 保持一致的三条采集规则。
const LITERAL_RE = /material-symbols-outlined"[^>]*>([a-z_]+)</g
const DYNAMIC_LINE_RE = /material-symbols-outlined"[^>]*>\s*\{\{([^}]*)\}\}/
const QUOTED_RE = /'([a-z_]{3,})'/g

// 兜底名单直接从构建脚本里读，而不是在这里抄一份。两份清单迟早会漂移，
// 而漂移的后果是「构建以为有、检查以为没有」（或者反过来）——那正是本脚本
// 要消灭的那类静默不一致。
const subsetSource = readFileSync(
  join(__dirname, 'build-material-symbols-subset.mjs'),
  'utf8',
)
const FALLBACK_BLOCK = /const FALLBACK = \[([\s\S]*?)\n\s*\]/.exec(subsetSource)
if (!FALLBACK_BLOCK) {
  console.error("[icons] 无法从 build-material-symbols-subset.mjs 解析 FALLBACK 名单")
  process.exit(2)
}
const FALLBACK = [
  ...FALLBACK_BLOCK[1].matchAll(/'([a-z_]+)'/g),
].map((m) => m[1])
if (FALLBACK.length < 10) {
  console.error(`[icons] FALLBACK 名单解析异常，只得到 ${FALLBACK.length} 个名字`)
  process.exit(2)
}

// 动态图标的权威名单在 src/constants/icons.ts。构建脚本也读它，
// 所以这里的 inSubset 必须把它算进去，否则会把注册表里的名字全报成「不在子集内」。
// 同样是从源文件正则解析，不另抄一份。
const registrySrc = readFileSync(join(SRC, 'constants', 'icons.ts'), 'utf8')
const ICON_BLOCK = /export const ICON = \{([\s\S]*?)\n\} as const/.exec(registrySrc)
if (!ICON_BLOCK) {
  console.error('[icons] 无法从 src/constants/icons.ts 解析 ICON 注册表')
  process.exit(2)
}
const REGISTRY = [...ICON_BLOCK[1].matchAll(/:\s*'([a-z_]+)'/g)].map((m) => m[1])
if (REGISTRY.length < 20) {
  console.error(`[icons] ICON 注册表解析异常，只得到 ${REGISTRY.length} 个名字`)
  process.exit(2)
}

// 声明式图标：`icon: 'x'` / `iconName: 'x'`。
const DECL_RE = /(?:^|[{,(\s])icon(?:Name)?\s*:\s*'([a-z_]{3,})'/g

function walk(dir, out = []) {
  for (const name of readdirSync(dir)) {
    if (name === 'node_modules' || name.startsWith('.')) continue
    const p = join(dir, name)
    if (statSync(p).isDirectory()) walk(p, out)
    else if (/\.(vue|ts|css|js|mjs)$/.test(name)) out.push(p)
  }
  return out
}

const files = walk(SRC)

const inSubset = new Set([...FALLBACK, ...REGISTRY])
const declared = new Map() // name -> first file:line that declares it
const usedLiterally = new Set()
const dynamicOnly = new Set()

for (const file of files) {
  const text = readFileSync(file, 'utf8')
  const lines = text.split('\n')

  let m
  LITERAL_RE.lastIndex = 0
  while ((m = LITERAL_RE.exec(text)) !== null) inSubset.add(m[1])

  lines.forEach((line, i) => {
    DECL_RE.lastIndex = 0
    let d
    while ((d = DECL_RE.exec(line)) !== null) {
      if (!declared.has(d[1])) declared.set(d[1], `${relative(ROOT, file)}:${i + 1}`)
    }
    const dyn = DYNAMIC_LINE_RE.exec(line)
    if (dyn) {
      let q
      QUOTED_RE.lastIndex = 0
      while ((q = QUOTED_RE.exec(dyn[1])) !== null) dynamicOnly.add(q[1])
    }
  })
}

// 声明过、且不是以字面量出现在模板里的 —— 这些正是需要子集包含的名字。
const missing = []
for (const [name, where] of declared) {
  if (!inSubset.has(name)) missing.push({ name, where, dynamic: dynamicOnly.has(name) })
}

console.log(`[icons] 扫描 ${files.length} 个文件`)
console.log(`[icons] 字面量 + 兜底 + ICON 注册表共 ${inSubset.size} 个名字在子集内`)
console.log(`[icons] 声明式图标（icon: 'x'）${declared.size} 个`)
console.log(`[icons] 其中仅经 JS 表达式渲染的 ${dynamicOnly.size} 个`)
console.log(`[icons] 注册表登记的动态名 ${REGISTRY.length} 个`)

if (missing.length === 0) {
  console.log('[icons] ✅ 所有声明式图标都在字体子集内')
  process.exit(0)
}

console.error(`[icons] ❌ ${missing.length} 个声明式图标不在子集内，真机会显示豆腐块：`)
for (const m of missing) {
  console.error(`  ${m.name.padEnd(24)} ${m.where}${m.dynamic ? '  (仅 JS 表达式引用)' : ''}`)
}
console.error('[icons] 修法：把它们加进 build-material-symbols-subset.mjs 的 FALLBACK，并重建字体。')
process.exit(1)
