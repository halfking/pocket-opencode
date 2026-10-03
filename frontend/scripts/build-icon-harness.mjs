// build-icon-harness.mjs —— 生成「图标目视验证页」。
//
// 用法：node scripts/build-icon-harness.mjs
// 产物：frontend/.icon-harness/index.html（自带一份字体，可直接用浏览器打开）
//
// ## 它补的是哪一类验证
//
// check-icon-font.mjs 用 harfbuzz 回答「这个名字在字体里合不合成得出来」——
// 能回答**有字形**，回答不了**字形画出来对不对**。后者只有真渲染才算数。
//
// 这页把工程引用到的全部图标名各渲染一格，用的是**真实的子集字体文件**，
// 于是肉眼（或截图）可以逐格核对。若某格显示成 LIGHT_MODE 这样的下划线大写
// 文本，就是真的缺字。
//
// 2026-09-30 实测：125 个名字在 Chromium 里逐格渲染，123 个是图形，
// 2 个（`name` / `starred`）显示为文本——这两个是**收集规则的假阳性**，
// 压根不是图标名（`starred` 来自 `item.status === 'starred'` 的比较值），
// 与 check-icon-font.mjs 的判定完全一致。两种独立方法互为佐证。
//
// 产物目录已在 .gitignore 里（含一份 3.5 MB 字体拷贝，不该进版本库）。
// 想在真机上看：把这个目录拷到设备，用浏览器打开 index.html 即可。

import { readFileSync, writeFileSync, mkdirSync, readdirSync, statSync } from 'node:fs'
import { join, relative } from 'node:path'
import { fileURLToPath } from 'node:url'

const __dirname = fileURLToPath(new URL('.', import.meta.url))
const ROOT = join(__dirname, '..')
const SRC = join(ROOT, 'src')
const OUT_DIR = join(ROOT, '.icon-harness')

// 与 check-icon-font.mjs 保持一致的四条采集规则。名单不手抄。
const LITERAL_RE = /material-symbols-outlined"[^>]*>\s*([a-z_]{3,})\s*</g
const INTERP_RE = /material-symbols-outlined"[^>]*>\s*\{\{([\s\S]{0,200}?)\}\}/g
const QUOTED_RE = /'([a-z_]{3,})'/g
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

const refs = new Map()
const want = (n, where) => {
  if (!/^[a-z][a-z0-9_]{2,}$/.test(n)) return
  if (!refs.has(n)) refs.set(n, where)
}

for (const file of walk(SRC)) {
  const text = readFileSync(file, 'utf8')
  let m
  LITERAL_RE.lastIndex = 0
  while ((m = LITERAL_RE.exec(text)) !== null) want(m[1], '字面量')
  INTERP_RE.lastIndex = 0
  while ((m = INTERP_RE.exec(text)) !== null) {
    QUOTED_RE.lastIndex = 0
    let q
    while ((q = QUOTED_RE.exec(m[1])) !== null) want(q[1], '插值')
  }
  DECL_RE.lastIndex = 0
  while ((m = DECL_RE.exec(text)) !== null) want(m[1], '数据表')
}

const registrySrc = readFileSync(join(SRC, 'constants', 'icons.ts'), 'utf8')
const block = /export const ICON = \{([\s\S]*?)\n\} as const/.exec(registrySrc)
if (!block) {
  console.error('[harness] 无法解析 constants/icons.ts 的 ICON 注册表')
  process.exit(2)
}
for (const m of block[1].matchAll(/:\s*'([a-z_]+)'/g)) want(m[1], '注册表')

const names = [...refs.keys()].sort()
mkdirSync(OUT_DIR, { recursive: true })
writeFileSync(
  join(OUT_DIR, 'material-symbols-outlined.woff2'),
  readFileSync(join(SRC, 'assets', 'fonts', 'material-symbols-outlined.woff2')),
)

const cells = names
  .map(
    (n) =>
      `<div class="cell"><span class="material-symbols-outlined">${n}</span><code>${n}</code><em>${refs.get(n)}</em></div>`,
  )
  .join('\n')

writeFileSync(
  join(OUT_DIR, 'index.html'),
  `<!doctype html>
<html lang="zh"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>图标目视验证 · ${names.length} 个</title>
<style>
@font-face {
  font-family: 'Material Symbols Outlined';
  src: url('./material-symbols-outlined.woff2') format('woff2');
  font-weight: normal; font-style: normal; font-display: block;
}
body { font-family: system-ui, sans-serif; background:#fff; color:#111; margin:16px; }
h1 { font-size:18px; }
.note { color:#555; font-size:13px; margin-bottom:12px; line-height:1.6; }
.grid { display:grid; grid-template-columns:repeat(auto-fill,minmax(190px,1fr)); gap:6px; }
.cell { border:1px solid #ddd; border-radius:6px; padding:6px 8px; display:flex; align-items:center; gap:8px; }
.material-symbols-outlined {
  font-family:'Material Symbols Outlined'; font-weight:normal; font-style:normal;
  font-size:26px; line-height:1; letter-spacing:normal; text-transform:none;
  display:inline-block; white-space:nowrap; word-wrap:normal; direction:ltr;
  font-feature-settings:'liga'; -webkit-font-feature-settings:'liga';
  -webkit-font-smoothing:antialiased;
}
code { font-size:11px; }
em { font-size:10px; color:#888; font-style:normal; margin-left:auto; text-align:right; }
</style></head>
<body>
<h1>图标目视验证 · 共 ${names.length} 个名字</h1>
<p class="note">
若某格显示成 LIGHT_MODE 这样的下划线大写文本，而不是图形，说明该连字在字体里缺失。<br>
标着「注册表」的来自 <code>src/constants/icons.ts</code>，是扫描器看不见的那一类动态引用。
</p>
<div class="grid" data-testid="harness" data-count="${names.length}">
${cells}
</div>
</body></html>
`,
)

console.log(`[harness] ${names.length} 个名字 → ${relative(ROOT, join(OUT_DIR, 'index.html'))}`)
console.log('[harness] 用浏览器打开该文件即可逐格核对；拷到手机上也能直接看。')
