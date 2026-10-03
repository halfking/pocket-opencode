/**
 * 扫描「teleport 内容用 :deep() 写样式」的死规则。
 *
 * 背景（真机 Redmi 14R 5G 实测，CDP 逐条比对构建产物与 computed style）：
 *   AIChatView 的会话切换胶囊 `.chat-convo-btn` 用了 6 条 `:deep()` 规则，
 *   编译出来是 `[data-v-xxx] .chat-convo-btn{...}` —— 要求**祖先**带 scope 属性。
 *   但这个按钮被 HeaderActionsPortal teleport 到 AppLayout 的 .header-actions 里，
 *   scope 属性只挂在按钮**自己**身上，祖先链上一个都没有，于是永远不匹配。
 *   真机表现：胶囊完全没有样式（透明底、无边框、radius 落到 8px），
 *   开发者却以为它渲染成了设计稿里的灰色胶囊。
 *
 * 正确写法：scoped 块里直接写 `.chat-convo-btn`，
 *   编译为 `.chat-convo-btn[data-v-xxx]`，挂在元素自身，实测可匹配。
 *   `:deep()` 只适用于「祖先带 scope、后代不带」的元素。
 *
 * Run: node scripts/audit-teleport-deep.mjs
 */
import { readFileSync, readdirSync, statSync } from 'node:fs'
import { join, extname, relative, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'

const SRC = join(dirname(fileURLToPath(import.meta.url)), '..', 'src')
const SKIP = new Set(['node_modules', '.git', 'dist', '__tests__', 'android', 'ios'])
const esc = (s) => s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')

const findings = []
let scanned = 0

function walk(dir) {
  for (const name of readdirSync(dir)) {
    if (SKIP.has(name)) continue
    const full = join(dir, name)
    if (statSync(full).isDirectory()) { walk(full); continue }
    if (extname(name) !== '.vue') continue
    const text = readFileSync(full, 'utf8')
    if (!text.includes('HeaderActionsPortal')) continue
    scanned++

    // teleport 出去的模板片段
    const teleported = [...text.matchAll(/<HeaderActionsPortal>([\s\S]*?)<\/HeaderActionsPortal>/g)]
      .map((m) => m[1]).join('\n')
    if (!teleported.trim()) continue

    // 片段里出现的 class（排除 Vue 指令插值）
    const classes = [...new Set(
      [...teleported.matchAll(/class="([^"]+)"/g)]
        .flatMap((m) => m[1].split(/\s+/))
        .filter((c) => c && !c.includes('{') && !c.includes('"')),
    )]

    const dead = []
    for (const c of classes) {
      // 是否存在 :deep(... .c ...) 形式
      const deepRe = new RegExp(`:deep\\([^)]*\\.${esc(c)}\\b`)
      if (!deepRe.test(text)) continue
      // 是否同时存在普通（scoped 自身）选择器 .c
      const plainRe = new RegExp(`(^|[^\\w-])\\.${esc(c)}\\s*[{,:\\s>~+]`, 'm')
      if (plainRe.test(text)) continue
      dead.push(c)
    }
    if (dead.length) {
      findings.push(`${relative(SRC, full).replace(/\\/g, '/')}  →  ${dead.join(', ')}`)
    }
  }
}

walk(SRC)

console.log(`扫描了 ${scanned} 个使用 HeaderActionsPortal 的 .vue 文件`)
if (!findings.length) {
  console.log('未发现「只用 :deep() 样式化 teleport 内容」的死规则。')
} else {
  console.log(`\n发现 ${findings.length} 个文件的 teleport 内容样式从未生效：`)
  for (const f of findings) console.log('  - ' + f)
  process.exitCode = 1
}
