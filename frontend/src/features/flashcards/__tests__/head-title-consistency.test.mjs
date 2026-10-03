// flashcards-head-title-consistency.test.mjs
//
// 同一模块内、同一语义角色（`.head h1` 页头）的字号必须一致。
//
// 2026-10-02 实测：features/flashcards 里 8 个视图都有 `.head h1`，其中
// 6 个是 18px、2 个 `.outer` 紧凑变体是 15px —— 而 FlashcardListView 是
// 内层 20px / 外层 17px，**两个都不是**。用户在模块之间来回切会看到页头
// 一会儿大一会儿小，这就是"字体不对"投诉的一部分。
//
// 判据只锁「同角色必须同值」，不锁具体像素：将来整体调档时改一处即可，
// 不会出现"护栏要求写死 18"这种一改就红、久了就没人维护的规则。
//
// 负控（见文件末尾）：把 FlashcardListView 的 20/17 塞回去，必须转红。

import assert from 'node:assert/strict'
import { readFileSync, readdirSync, statSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, it } from 'node:test'

const here = dirname(fileURLToPath(import.meta.url))
const moduleDir = join(here, '..')

/**
 * token 名 → 像素值。
 *
 * 2026-10-03：本模块的字号已统一走 token（18px 那一批整体换成
 * `var(--text-xl)`），于是原先只认 `font-size: Npx` 的检测器**全部失明**——
 * `.head h1` 一处都扫不到，`inner`/`outer` 变空集合。
 *
 * 好在防空跑用例（`finds enough instances`）当场报红而不是静默放过。
 * 这就是那条断言存在的意义：判据的失败面是「什么都没扫到 → 集合为空 → 绿」。
 *
 * 所以现在把两种写法都解析成**实际像素**再比，判据问的仍然是
 * 「同角色是否同字号」，而不是「源码里写的是不是同一种字面形式」。
 */
function loadTokenScale() {
  const css = readFileSync(join(moduleDir, '..', '..', 'styles', 'tokens.css'), 'utf8')
  const scale = {}
  for (const m of css.matchAll(/(--text-[a-z0-9-]+)\s*:\s*(\d+(?:\.\d+)?)px/g)) {
    scale[m[1]] = m[2]
  }
  return scale
}

const TOKEN_SCALE = loadTokenScale()

/** 收集该模块下所有 .vue（含 components/ 子目录）。 */
function vueFiles(dir) {
  const out = []
  for (const name of readdirSync(dir)) {
    const p = join(dir, name)
    if (statSync(p).isDirectory()) out.push(...vueFiles(p))
    else if (name.endsWith('.vue')) out.push(p)
  }
  return out
}

/**
 * 抽出 `.head h1` 的**实际字号（px）**。
 * compact=true 表示 `.outer .head h1`（紧凑变体），与内层分开统计。
 */
function headTitleSizes(files) {
  const inner = []
  const outer = []
  // 选择器里 `.outer` 是可选前缀。必须从选择器**起点**匹配：若先匹配内层的
  // `.head h1`，它也会命中 `.outer .head h1` 的子串（第一版就是这么错的，
  // 而且错法恰好让 outer 集合变空——被防空跑断言抓住才没变成永远绿）。
  //
  // 两种写法都收：写死 px，或 var(--text-*)。未知 token 一律跳过而不是当 0，
  // 否则一个拼错的 token 名会伪装成「0px」参与比较。
  const re = /(?:(\.outer)\s+)?(\.head)\s+h1\s*\{[^}]*font-size\s*:\s*(?:(\d+(?:\.\d+)?)px|var\((--text-[a-z0-9-]+)\))/g
  for (const f of files) {
    const src = readFileSync(f, 'utf8')
    for (const m of src.matchAll(re)) {
      const px = m[3] ?? TOKEN_SCALE[m[4]]
      if (px === undefined) continue
      ;(m[1] ? outer : inner).push({ file: f, size: px, via: m[3] ? `${m[3]}px` : m[4] })
    }
  }
  return { inner, outer }
}

const files = vueFiles(moduleDir)
const { inner, outer } = headTitleSizes(files)

describe('flashcards module page-title consistency', () => {
  it('finds enough instances for the check to be non-vacuous', () => {
    // 防空跑：判据若因为路径写错而扫不到东西，下面两条会「因为集合为空」而绿。
    assert.ok(files.length >= 8, `只扫到 ${files.length} 个 .vue，路径可能不对`)
    assert.ok(inner.length >= 5, `只扫到 ${inner.length} 处内层 .head h1，判据可能失明`)
    assert.ok(outer.length >= 2, `只扫到 ${outer.length} 处 .outer .head h1`)
  })

  it('all inner .head h1 share one font-size', () => {
    const sizes = [...new Set(inner.map((x) => x.size))]
    assert.equal(
      sizes.length, 1,
      `内层 .head h1 出现 ${sizes.length} 个不同字号 ${sizes.join('/')}：\n` +
        inner.map((x) => `  ${x.size}px  ${x.file.split(/[\\/]/).pop()}`).join('\n'),
    )
  })

  it('all compact .outer .head h1 share one font-size', () => {
    const sizes = [...new Set(outer.map((x) => x.size))]
    assert.equal(
      sizes.length, 1,
      `.outer .head h1 出现 ${sizes.length} 个不同字号 ${sizes.join('/')}：\n` +
        outer.map((x) => `  ${x.size}px  ${x.file.split(/[\\/]/).pop()}`).join('\n'),
    )
  })

  it('the compact variant is strictly smaller than the inner one', () => {
    // 两套并存是设计（外壳 16px / 模块内 18px / 紧凑 15px），
    // 但「紧凑 > 内层」一定是错的。
    if (inner.length === 0 || outer.length === 0) return // 防空跑用例已报，不重复制造噪声
    const i = Number(inner[0].size)
    const o = Number(outer[0].size)
    assert.ok(o < i, `紧凑头部 ${o}px 不应大于内层 ${i}px`)
  })
})
