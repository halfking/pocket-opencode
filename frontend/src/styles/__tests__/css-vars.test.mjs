/**
 * CSS 变量定义/引用一致性回归测试。
 *
 * 缺陷背景（真机 Redmi 14R 5G 实测）：
 *   RSS 页「新增源」按钮渲染成近白不可读——`.btn-primary { background: var(--accent) }`
 *   而 `--accent` 在整个前端从未定义过，声明失效后背景回退为透明，
 *   白字（color: white）落在浅色底上，对比度约 1.1:1，完全不可读。
 *   同类问题波及 RSS 三个组件与 AgentMarketView，共 13 处引用。
 *
 * 这类缺陷静态可见、影响面大、且不会被任何运行时错误暴露，
 * 因此用扫描守住：任何 var(--x) 都必须能在 CSS 或 setProperty 里找到定义。
 *
 * Run: node --test src/styles/__tests__/css-vars.test.mjs
 */
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { readFileSync, readdirSync, statSync } from 'node:fs'
import { join, dirname, extname, relative } from 'node:path'
import { fileURLToPath } from 'node:url'

const ROOT = join(dirname(fileURLToPath(import.meta.url)), '..', '..', '..')
const SRC = join(ROOT, 'src')
const SKIP_DIRS = new Set(['node_modules', '.git', 'dist', '__tests__', 'android', 'ios'])

/** 递归收集源码文件（.vue/.css/.ts）。 */
function collectFiles(dir, out = []) {
  for (const name of readdirSync(dir)) {
    if (SKIP_DIRS.has(name)) continue
    const full = join(dir, name)
    const st = statSync(full)
    if (st.isDirectory()) collectFiles(full, out)
    else if (['.vue', '.css', '.ts'].includes(extname(name))) out.push(full)
  }
  return out
}

const files = collectFiles(SRC)

/**
 * `--name:` 形式的定义。
 * 允许名字后跟引号/反引号，以覆盖 Vue style 绑定里的运行时注入：
 *   `{ '--fold-master-width': `${x}px` }`
 *   那种写法不是 CSS 声明，但同样让变量在运行时存在。
 */
const DEFINE_RE = /--([A-Za-z0-9_-]+)['"`]?\s*:/g
/**
 * `var(--name)` 形式的引用，同时捕获紧随其后的分隔符：
 *   `)` = 裸引用（未定义时整条声明失效 —— 真 bug）
 *   `,` = 带 fallback（未定义时用兜底值 —— 合法）
 */
const USE_RE = /var\(\s*--([A-Za-z0-9_-]+)\s*([,)])/g
/** JS 运行时注入的定义。 */
const SET_RE = /setProperty\(\s*['"](--[A-Za-z0-9_-]+)['"]/g

const defined = new Set()
/** 裸引用（无 fallback）的使用点 —— 这些才是会静默失效的。 */
const bare = new Map()
/** 带 fallback 的使用点，仅作统计。 */
const withFallback = new Set()

for (const file of files) {
  const text = readFileSync(file, 'utf8')
  for (const m of text.matchAll(DEFINE_RE)) defined.add(m[1])
  for (const m of text.matchAll(SET_RE)) defined.add(m[1].slice(2))
  for (const m of text.matchAll(USE_RE)) {
    const [, name, sep] = m
    if (sep === ',') {
      withFallback.add(name)
      continue
    }
    const rel = relative(ROOT, file).replace(/\\/g, '/')
    if (!bare.has(name)) bare.set(name, new Set())
    bare.get(name).add(rel)
  }
}

describe('CSS 变量一致性', () => {
  it('扫描到了足够的源码文件（防止路径写错导致空跑通过）', () => {
    assert.ok(files.length > 100, `只扫描到 ${files.length} 个文件，扫描范围可能失效`)
  })

  it('裸引用 var(--x)（无 fallback）的变量都有定义', () => {
    // 带 fallback 的未定义变量只是用兜底值，不会静默失效，不在此判据内。
    const missing = []
    for (const [name, where] of bare) {
      if (defined.has(name)) continue
      missing.push(`var(--${name}) 未定义，用在: ${[...where].slice(0, 3).join(', ')}`)
    }
    assert.equal(
      missing.length,
      0,
      `发现 ${missing.length} 处裸引用了未定义 CSS 变量（声明会整条失效）：\n  - ${missing.join('\n  - ')}`,
    )
  })

  it('回归护栏：--accent 不得被裸引用（RSS 不可读按钮的根因）', () => {
    const users = bare.get('accent')
    assert.equal(
      users ? [...users].length : 0,
      0,
      `--accent 又被裸引用了: ${[...(users ?? [])].join(', ')}；主色请用 --brand-primary，或补 fallback`,
    )
  })
})

/**
 * 硬编码等宽字体栈扫描。
 *
 * 背景：`--font-mono` 末尾补了 `var(--font-sans)` 作为 CJK 回退，
 * 等宽字体不含中文字形，一旦某处绕过 token 写死 `ui-monospace, monospace`，
 * 中文就会掉到浏览器默认字体，与正文风格不一致 —— 正是用户报的「字体不对」。
 *
 * 之前的手工扫描只查了裸 `monospace:` 声明，漏了两种高频写法：
 *   1. `font: 12px/1.5 ui-monospace, monospace` 简写
 *   2. `font-family: ui-monospace, SFMono-Regular, Menlo, monospace` 展开写法
 * 两者都真实存在于 ScheduledTaskDetailView 与 FlashcardEditView。
 */
const HARDCODE_MONO_RE = /font(?:-family)?\s*:[^;{}]*?\b(?:monospace|ui-monospace|SFMono-Regular|Menlo|Consolas)\b/g

describe('等宽字体必须走 --font-mono token', () => {
  it('没有硬编码的等宽字体栈', () => {
    const hits = []
    for (const file of files) {
      const text = readFileSync(file, 'utf8')
      for (const m of text.matchAll(HARDCODE_MONO_RE)) {
        hits.push(`${relative(ROOT, file).replace(/\\/g, '/')}: ${m[0].trim()}`)
      }
    }
    assert.equal(
      hits.length,
      0,
      `发现 ${hits.length} 处硬编码等宽字体栈（中文会掉回默认字体，与正文不一致）：\n  - ${hits.join('\n  - ')}`,
    )
  })
})
