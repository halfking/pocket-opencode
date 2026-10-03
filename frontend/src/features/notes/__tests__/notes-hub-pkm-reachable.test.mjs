// notes-hub-pkm-reachable.test.mjs
//
// 判据：笔记 hub 的「打开 PKM」入口**不能**被 counts.total 门控。
//
// 2026-10-03 真机实测（Xiaomi 2411DRN47C）挖出的可达性缺陷：
// 2026-10-03 全局 IA 重组把一级 tab 收敛为 首页 / 笔记 / 消息 / 更多，
// 「PKM笔记」从底部「更多」的宫格里被移除（MoreHubView 的 items 里已没有它），
// 于是 /pkm/today 只剩 NotesHubView 里一个「打开 PKM」按钮能进。
// 而那一整块 see-all nav 当时挂在 `v-if="counts.total"` 上：
//
//     <EmptyState v-else-if="counts.total === 0" … />   ← 空库走这里
//     <nav v-if="counts.total"> … 打开 PKM … </nav>       ← 空库不渲染
//
// ⇒ 新装 / 空库用户 `counts.total === 0`，界面上**没有任何入口**能到 PKM 工作台，
//   除非他先手写出一条笔记。PKM 是功能，不是「有数据才让你看」的列表。
//
// 为什么用源码结构判据而不是挂载渲染：本仓库的同类门禁（settings-helper-text-tier、
// version-config-unavailable-wiring 等）都是读 .vue 源码判形状，不引入 vue 测试栈。
// 这里要判的恰恰是**模板里的门控关系**，读源码比挂载更直接。
//
// 负控（文件末尾）：把 PKM 那个 nav 改回 v-if="counts.total"，本文件必须转红。

import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, it } from 'node:test'

const here = dirname(fileURLToPath(import.meta.url))
const SFC = join(here, '..', 'NotesHubView.vue')
const src = readFileSync(SFC, 'utf8')

/** 取某个 go('/xxx') 所在那一行**所属**的 <nav …> 开标签。 */
function owningNavTag(goCall) {
  const at = src.indexOf(goCall)
  assert.notEqual(at, -1, `NotesHubView.vue 里找不到 ${goCall}`)
  // 往上找最近的 <nav 开标签（本文件里 nav 不嵌套）。
  const before = src.slice(0, at)
  const open = before.lastIndexOf('<nav')
  assert.notEqual(open, -1, `${goCall} 上方没有 <nav>`)
  const close = src.indexOf('>', open)
  return src.slice(open, close + 1)
}

describe('NotesHubView 的 PKM 入口可达性', () => {
  it('「打开 PKM」确实渲染在某个 nav 里（判据自身的前提）', () => {
    assert.match(src, /notesHub\.link\.allPkm/, 'PKM 入口文案引用不见了，先确认这条判据还成立')
    assert.match(src, /go\('\/pkm\/today'\)/, 'PKM 入口的跳转不见了')
  })

  it('PKM 入口所在的 nav 没有被 counts.total 门控', () => {
    const navTag = owningNavTag("go('/pkm/today')")
    assert.doesNotMatch(
      navTag,
      /counts\.total/,
      `PKM 入口的 nav 被 counts 门控了：${navTag}\n`
        + '空库用户会因此在界面上找不到 PKM 工作台（2026-10-03 真机实测的缺陷）',
    )
  })

  it('「全部笔记 / 全部会议」仍保留 counts 门控（没数据时不该出现）', () => {
    for (const goCall of ["go('/notes/voice')", "go('/meetings')"]) {
      const navTag = owningNavTag(goCall)
      assert.match(
        navTag,
        /counts\.total/,
        `${goCall} 所在的 nav: ${navTag}\n`
          + '列表类入口在空库时不该渲染（与 PKM 的工具入口不同）',
      )
    }
  })
})
