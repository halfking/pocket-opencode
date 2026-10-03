/**
 * useAutoGrowTextarea —— 多行输入「内容驱动高度」的行为测试。
 *
 * ## 这条修复对应用户诉求的哪一句
 *
 * 「需要检查所有有多行文本输入的区域，输入的内容展示要尽可能地完整，
 * 区域足够大，让人感觉舒服，不要太小或看不完整。」
 *
 * 修复前：UnifiedComposer 的 `onInput` 只发事件，**没有任何高度逻辑**，
 * 而组件文件头第 11 行白纸黑字写着「标准模式：自适应增高（上限 40vh 后滚动）」。
 * 文档承诺了、实现没做——多行正文写到第 4 行以后就藏进内部滚动条后面。
 *
 * ## 为什么这些断言是「行为」而不是「源码长相」
 *
 * 本文件不 grep 源码，直接给一个假 textarea 喂 scrollHeight、读回 style.height。
 * 判据是三件必须同时成立的事：
 *
 *   1. 长内容 → 高度跟上去（内容可见）
 *   2. 删内容 → 高度缩回来（不留大片空白）
 *   3. boxSizing 被锁成 border-box（否则每敲一次多长 padding，无限增长）
 *
 * 第 3 条是本实现最容易踩的坑：`* { box-sizing: border-box }` 此刻是
 * styles.css 的全局规则在**兜底**，而 autoGrow 的数学（height = scrollHeight）
 * 正确性完全依赖它。哪天有人把那条全局规则删掉或改窄，没有这行断言就是
 * 一个「每敲一个字框就长几像素」的诡异 bug —— 所以在实现里显式锁死并测住。
 */

import { JSDOM } from 'jsdom'
import test from 'node:test'
import assert from 'node:assert/strict'

/** 造一个 scrollHeight 可控的假 textarea。 */
function fakeTextarea(scrollHeight) {
  const el = {
    style: {},
    scrollHeight,
  }
  return el
}

test.afterEach(() => {
  delete globalThis.window
  delete globalThis.document
})

/** 每次都拿全新模块实例（模块级无状态，但保持隔离以便未来加缓存）。 */
let seq = 0
async function freshModule() {
  return await import(`../useAutoGrowTextarea.ts?n=${++seq}`)
}

test('内容变长时，高度跟到内容高度（多行内容不再藏在滚动条后面）', async () => {
  const { autoGrow } = await freshModule()
  const el = fakeTextarea(240) // 约 6 行

  autoGrow(el)

  assert.equal(el.style.height, '240px', '高度应等于内容真实高度')
})

test('内容变短时，高度缩回来（删字后不留大片空白）', async () => {
  const { autoGrow } = await freshModule()
  const el = fakeTextarea(240)

  autoGrow(el)
  assert.equal(el.style.height, '240px')

  // 用户把内容删到只剩一行
  el.scrollHeight = 48
  autoGrow(el)

  assert.equal(
    el.style.height,
    '48px',
    '必须缩回去。若实现漏了「先归零」这步，高度会卡在 240px 不动 —— ' +
      '用户删掉一大段后框里留着一片空白，看起来像没生效',
  )
})

test('连续多次增行时高度严格跟随，不累积（幂等）', async () => {
  const { autoGrow } = await freshModule()
  const el = fakeTextarea(100)

  autoGrow(el)
  el.scrollHeight = 150
  autoGrow(el)
  el.scrollHeight = 200
  autoGrow(el)

  assert.equal(el.style.height, '200px', '每步都以内容高度为准，不应出现 100+150+200 的累积')
})

test('boxSizing 被显式锁成 border-box（height=scrollHeight 的正确性前提）', async () => {
  const { autoGrow } = await freshModule()
  const el = fakeTextarea(120)

  autoGrow(el)

  assert.equal(
    el.style.boxSizing,
    'border-box',
    'content-box 下 height=scrollHeight 会把 padding+border 再加一遍，' +
      '每敲一次多长几像素、无限增长。本实现的数学依赖 border-box，必须由实现自己锁死，' +
      '不能只依赖 styles.css 的全局 * 规则',
  )
})

test('autoGrow 对空值静默返回（组件卸载后 watcher 仍可能触发，不该抛）', async () => {
  const { autoGrow } = await freshModule()

  assert.doesNotThrow(() => autoGrow(null), '传 null 不该抛')
  assert.doesNotThrow(() => autoGrow(undefined), '传 undefined 不该抛')
})

test('autoGrow 先归零再写回 —— 归零这一步是删字能缩回去的前提', async () => {
  const { autoGrow } = await freshModule()
  const el = fakeTextarea(200)
  const writes = []
  // 用 setter 记录写入顺序，验证 'auto' 确实在 px 之前
  let inner = {}
  Object.defineProperty(el, 'style', {
    get: () => inner,
    set: () => {},
    configurable: true,
  })
  inner = new Proxy(
    {},
    {
      set(target, prop, value) {
        writes.push([prop, value])
        target[prop] = value
        return true
      },
    },
  )

  autoGrow(el)

  const heightWrites = writes.filter(([p]) => p === 'height')
  assert.equal(heightWrites.length, 2, 'height 应被写两次：先 auto 再具体值')
  assert.equal(heightWrites[0][1], 'auto', '第一次必须是 auto（让浏览器按内容重排）')
  assert.equal(heightWrites[1][1], '200px', '第二次写内容真实高度')
})
