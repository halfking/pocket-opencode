/**
 * modal-scroll-lock.spec.ts — 模态层必须真正锁住背景滚动宿主（2026-10-06）。
 *
 * 契约：`useBodyScrollLock` 的名字承诺「锁住背景滚动」。本仓的滚动宿主不是
 * `document.body`：
 *   - `AppLayout` 的 `.content` 是 `flex:1 1 auto; min-height:0; overflow-y:auto`；
 *   - 带 `PullToRefresh` 的页面里真正常滚的是 `.refresh-content`。
 * 只锁 body ⇒ 契约**没有**被兑现，只是碰巧不出事（见下）。
 *
 * ★★★ 本 spec 的量法教训（先读，否则会重犯）★★★：
 * **程序化写 `scrollTop` 不是这个修复的有效量具。** `overflow:hidden` 按 CSS
 * 规范只禁止*用户*滚动，不阻止脚本写 scrollTop。所以「弹层打开时
 * `scrollTop = 700` 仍生效」在**改前改后都是 700** —— 对修复是不变量，
 * 推不出缺陷。上一轮正是用它误报了一个用户可见缺陷。
 * 本 spec 因此断言两件可区分的事：
 *   ② **契约**：宿主 computed overflowY 不再是 auto/scroll（对修复有牙）；
 *   ④ **现状刻画**：真实滚轮打不动背景 —— 但这条对**本次修复无牙**，
 *     它守的是另一条不变量（见 ④ 的注释）。
 *
 * 变异验证：`useBodyScrollLock` 回退成只锁 body ⇒ ② 红（宿主 computed='auto'）。
 * 已实测（e2e 前后对照，真实滚轮）：
 *   ┌──────────────┬───────────────┬───────────────┐
 *   │ 观测量       │ 只锁 body(旧) │ 锁宿主(本次) │
 *   ├──────────────┼───────────────┼───────────────┤
 *   │ 宿主 computed│ auto          │ hidden        │ ← ② 抓这里
 *   │ 真实滚轮     │ 0（不滚）     │ 0（不滚）     │ ← 无差异
 *   └──────────────┴───────────────┴───────────────┘
 */
import { expect, test, type Page } from '@playwright/test'
import { seedTokenAuth, unlockLobster } from '../helpers/tokenAuth'
import { makeEmails, seedEmails } from '../helpers/emailSeed'

test.use({ viewport: { width: 390, height: 844 } })

/**
 * 找出 `#app` 内当前真正在滚的容器（与 composable 的 findScrollableHosts
 * 同一判据：溢出 > 20px 且 overflowY ∈ {auto,scroll}），并打上标记。
 * 返回宿主的类名与可滚余量——供断言区分「找不到」与「找到了但不滚」。
 */
async function markBackgroundScroller(page: Page) {
  return page.evaluate(() => {
    const el = [...document.querySelectorAll<HTMLElement>('#app *')].find((e) => {
      const s = getComputedStyle(e)
      return e.scrollHeight > e.clientHeight + 20 && /(auto|scroll)/.test(s.overflowY)
    })
    if (!el) return null
    el.setAttribute('data-bg-scroller', '1')
    return { cls: el.className, max: el.scrollHeight - el.clientHeight }
  })
}

const readBg = (page: Page) =>
  page.evaluate(() => document.querySelector<HTMLElement>('[data-bg-scroller]')?.scrollTop ?? null)

const openMoveSheet = (page: Page, v: boolean) =>
  page.evaluate((val) => {
    const el = document.querySelector('.inbox-page') as any
    el.__vueParentComponent.setupState.moveOpen = val
  }, v)

async function openInbox(page: Page) {
  await seedTokenAuth(page)
  await page.goto('/#/ai')
  await page.waitForTimeout(2000)
  expect(await unlockLobster(page), 'initLobster 失败').toBe(true)
  await seedEmails(page, makeEmails(75))
  await page.goto('/#/email')
  await page.waitForTimeout(3000)
}

test('① 负对照：弹层未打开时，真实滚轮能推动背景（否则后面全是空断言）', async ({ page }) => {
  await openInbox(page)
  const info = await markBackgroundScroller(page)
  expect(info, '找不到可滚动的背景宿主').not.toBeNull()
  expect(info!.max, '背景宿主不可滚 ⇒ 本 spec 会恒真').toBeGreaterThan(500)

  // 真实输入，不是程序化赋值——程序化对 overflow:hidden 无效，量不出东西
  await page.mouse.move(195, 200)
  await page.mouse.wheel(0, 500)
  await page.waitForTimeout(400)
  expect(await readBg(page), '真实滚轮竟推不动背景 ⇒ 前提不成立').toBeGreaterThan(0)
})

test('② 契约：弹层打开时，#app 内没有任何滚动宿主仍是 auto/scroll', async ({ page }) => {
  await openInbox(page)
  const info = await markBackgroundScroller(page)
  expect(info, '找不到背景宿主 ⇒ 本条无意义').not.toBeNull()

  await openMoveSheet(page, true)
  await page.waitForTimeout(1200)

  const r = await page.evaluate(() => {
    const stillScrollable = [...document.querySelectorAll<HTMLElement>('#app *')]
      .filter((e) => e.scrollHeight > e.clientHeight + 20)
      .map((e) => ({ cls: e.className, oy: getComputedStyle(e).overflowY }))
      .filter((e) => e.oy === 'auto' || e.oy === 'scroll')
    return {
      sheetFound: !!document.querySelector('.bottom-sheet-overlay, [role="dialog"]'),
      bodyOverflowY: document.body.style.overflowY,
      stillScrollable,
    }
  })

  expect(r.sheetFound, 'BottomSheet 没打开').toBe(true)
  expect(r.bodyOverflowY, 'body 应仍被锁（保留旧行为）').toBe('hidden')
  // ★ 这条对修复有牙：回退成只锁 body 时这里会是 auto ⇒ 红
  expect(
    r.stillScrollable,
    '弹层打开期间仍有可滚宿主，背景会动：' + JSON.stringify(r.stillScrollable),
  ).toEqual([])
})

test('③ 弹层关闭后逐项精确恢复原 inline 值（写 "" 会抹掉页面自己的样式）', async ({ page }) => {
  await openInbox(page)
  const info = await markBackgroundScroller(page)
  expect(info, '找不到背景宿主').not.toBeNull()

  // ⚠️ 量具自证（缺了这条，③ 会恒真）：本仓 `/email` 的 `.refresh-content`
  // **本来就没有 inline overflow**（值来自 CSS class），直接比对
  // before/after 得到的是 `'' === ''` —— 恒成立，验不出任何东西。
  // 所以先人为写入一个**只有本用例设过**的非空 inline 值，让「恢复」有得可失。
  const SENTINEL = 'auto'
  const setInline = () =>
    page.evaluate((v) => {
      document.querySelector<HTMLElement>('[data-bg-scroller]')!.style.overflowY = v
    }, SENTINEL)
  const readInline = () =>
    page.evaluate(
      () => document.querySelector<HTMLElement>('[data-bg-scroller]')!.style.overflowY,
    )

  await setInline()
  expect(await readInline(), '前置条件：inline 值已就位').toBe(SENTINEL)

  await openMoveSheet(page, true)
  await page.waitForTimeout(1200)
  // 前提自证：锁**确实覆盖过**这个值，否则第 ④ 步的恢复是空谈
  expect(await readInline(), '锁未生效 ⇒ 本用例后续断言没有意义').toBe('hidden')

  await openMoveSheet(page, false)
  await page.waitForTimeout(1000)

  // ★ 真正有牙的一步：必须精确回到 SENTINEL。粗暴写 '' 会红。
  expect(await readInline(), `关闭后应精确恢复为 ${SENTINEL}`).toBe(SENTINEL)
  expect(await readBg(page), '恢复后宿主应可滚').toBe(0)

  // 复原页面自身状态，别把测试残留留给同 worker 的后续用例
  await page.evaluate(
    () => (document.querySelector<HTMLElement>('[data-bg-scroller]')!.style.overflowY = ''),
  )
})

test('④ 现状刻画（对本次修复无牙）：真实滚轮打不动背景，是因为遮罩盖满视口', async ({ page }) => {
  // ⚠️ 这条**对 useBodyScrollLock 的改动无牙**——回退成只锁 body 它依然绿。
  // 它守的是另一条不变量：三个消费方（BottomSheet / Dialog / UnifiedComposer
  // 全屏）都必须是 `position:fixed; inset:0` 的全屏遮罩。手势落在遮罩上，
  // 滚动链沿 DOM 祖先走（body/html），到不了 `.content` 这个兄弟子树。
  // 若将来引入**非全屏**的浮层（例如只占底部的面板），这条会红——
  // 那时 ② 的宿主锁才真正开始起作用。
  await openInbox(page)
  const info = await markBackgroundScroller(page)
  expect(info, '找不到背景宿主').not.toBeNull()

  await openMoveSheet(page, true)
  await page.waitForTimeout(1200)

  const overlay = await page.evaluate(() => {
    const ov = document.querySelector<HTMLElement>('.bottom-sheet-overlay')!
    const r = ov.getBoundingClientRect()
    return { w: r.width, h: r.height, vw: window.innerWidth, vh: window.innerHeight }
  })
  expect(
    overlay.w >= overlay.vw && overlay.h >= overlay.vh,
    `BottomSheet 遮罩必须盖满视口，当前 ${overlay.w}×${overlay.h} / ${overlay.vw}×${overlay.vh}`,
  ).toBe(true)

  await page.mouse.move(195, 200)
  await page.mouse.wheel(0, 500)
  await page.waitForTimeout(500)
  expect(await readBg(page), '弹层期间真实滚轮竟推动了背景').toBe(0)
})
