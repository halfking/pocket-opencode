/**
 * teleport 内容的样式不得用 :deep()。
 *
 * 缺陷背景（真机 Redmi 14R 5G 实测，CDP 逐条核对构建产物 + computed style）：
 *   AIChatView 的会话切换胶囊和 NoteListView 的两个圆形按钮，都经
 *   HeaderActionsPortal **teleport** 到 AppLayout 的 .header-actions。
 *   两个文件都用 `:deep(.xxx)` 给它们写样式，编译结果是
 *     [data-v-x] .chat-convo-btn { ... }
 *   —— 要求**祖先**带 scope 属性。但 teleport 出去的元素，scope 属性只挂在
 *   它自己身上，祖先链上一个都没有，于是这条规则永远不会命中。
 *
 *   真机表现：胶囊完全没有样式（computed 显示 border-radius 落到 8px、
 *   background 透明），而作者以为它渲染成了设计稿里的灰色胶囊。
 *   更隐蔽的是配套的 `@media (max-width: 380px)` 隐藏文字标签的规则也一并失效，
 *   导致顶栏被撑到 413px、「对话参数」「新建对话」两个按钮掉出屏幕 53px。
 *
 * 正确写法：scoped 块里直接写 `.chat-convo-btn`，
 *   编译为 `.chat-convo-btn[data-v-x]`，作用域属性在元素自身，实测可命中。
 *   `:deep()` 只适用于「祖先带 scope、后代不带」的元素。
 *
 * Run: node --test src/styles/__tests__/teleport-deep.test.mjs
 */
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { readFileSync, readdirSync, statSync } from 'node:fs'
import { join, dirname, extname, relative } from 'node:path'
import { fileURLToPath } from 'node:url'

const ROOT = join(dirname(fileURLToPath(import.meta.url)), '..', '..', '..')
const SRC = join(ROOT, 'src')
const SKIP_DIRS = new Set(['node_modules', '.git', 'dist', '__tests__', 'android', 'ios'])
const esc = (s) => s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')

function collectVue(dir, out = []) {
  for (const name of readdirSync(dir)) {
    if (SKIP_DIRS.has(name)) continue
    const full = join(dir, name)
    if (statSync(full).isDirectory()) collectVue(full, out)
    else if (extname(name) === '.vue') out.push(full)
  }
  return out
}

const files = collectVue(SRC)

/**
 * 找出「teleport 出去、且样式只靠 :deep()」的 class。
 * 判定：teleport 片段里出现该 class，文件里有 `:deep(... .cls ...)`，
 * 但没有 scoped 自身选择器 `.cls`。
 */
function findDeadTeleportStyles() {
  const out = []
  for (const file of files) {
    const text = readFileSync(file, 'utf8')
    if (!text.includes('HeaderActionsPortal')) continue
    const teleported = [...text.matchAll(/<HeaderActionsPortal>([\s\S]*?)<\/HeaderActionsPortal>/g)]
      .map((m) => m[1]).join('\n')
    if (!teleported.trim()) continue
    const classes = [...new Set(
      [...teleported.matchAll(/class="([^"]+)"/g)]
        .flatMap((m) => m[1].split(/\s+/))
        .filter((c) => c && !c.includes('{') && !c.includes('"')),
    )]
    const dead = classes.filter((c) => {
      if (!new RegExp(`:deep\\([^)]*\\.${esc(c)}\\b`).test(text)) return false
      return !new RegExp(`(^|[^\\w-])\\.${esc(c)}\\s*[{,:\\s>~+]`, 'm').test(text)
    })
    if (dead.length) out.push({ file: relative(ROOT, file).replace(/\\/g, '/'), classes: dead })
  }
  return out
}

describe('teleport 内容的样式作用域', () => {
  it('扫描到了足够的 .vue 文件（防止路径写错导致空跑通过）', () => {
    assert.ok(files.length > 40, `只扫描到 ${files.length} 个 .vue 文件，扫描范围可能失效`)
  })

  it('teleport 出去的元素不使用 :deep() 写样式（否则永不命中）', () => {
    const dead = findDeadTeleportStyles()
    const detail = dead.map((d) => `${d.file}: ${d.classes.join(', ')}`).join('\n  - ')
    assert.equal(
      dead.length,
      0,
      `发现 ${dead.length} 处「:deep() 样式 teleport 内容」的永不生效规则：\n  - ${detail}\n` +
      '  teleport 元素的 scope 属性在自身，请写 scoped 自身选择器（.cls）而不是 :deep(.cls)',
    )
  })
})
