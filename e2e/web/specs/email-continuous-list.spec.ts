/**
 * email-continuous-list.spec.ts — 收件箱连续加载回归（2026-10-06）。
 *
 * 这条 spec 记录一个**真实缺陷**，它的发现过程本身就是教训：
 *
 * 缺陷：`EmailInboxView` 挂载后，后台流程会调最多 6 次 `showLocal(false)`
 *   （L485/496/501/505），而 `showLocal(false)` 走的是
 *   `advanceInboxPage(pageState, page.length, addedCount, INBOX_PAGE_SIZE)`。
 *   `advanceInboxPage` 是**翻页**的状态机：`nextOffset += fetchedCount`、
 *   `noProgressStreak += (addedCount === 0)`。于是「这次刷新没有新邮件」
 *   被当成「这页没有新行」——游标凭空推进、收敛计数凭空累加。
 *   3 次之后 `noProgressStreak >= MAX_NO_PROGRESS_PAGES(3)` ⇒ `hasMore=false`
 *   ⇒ 哨兵显示「已到最早一封」⇒ **用户再也翻不出更早的邮件**。
 *
 *   实测（seed 75 封）：`pageState = {nextOffset:210, noProgressStreak:6, hasMore:false}`，
 *   而库里 `listEmails` 分页为 30/30/15 —— **还有 45 封拿不到**。
 *   210 = 30×7，6 = 刷新次数，与 `advanceInboxPage` 的推演逐位吻合。
 *
 * 为什么它当时没被发现：`email-inbox-pagination.test.mjs` 测的是**函数**，
 * 而这个缺陷在**调用点**——同一个函数被用在了「翻页」和「刷新」两种语义上。
 * 函数本身没写错，**用错的地方也没判据**。
 *
 * ⚠️ 断言的分工：
 *   - 「哨兵文案」抓的是**症状**（用户看到的「已到最早一封」）；
 *   - 「滚动后行数增加」抓的是**能力**（能不能真的翻页）。
 *   两条缺一：只看文案会漏掉「文案说还有、但翻不动」；只看行数会在
 *   数据量恰为整页倍数时误判。
 */
import { expect, test } from '@playwright/test'
import { seedTokenAuth, unlockLobster } from '../helpers/tokenAuth'
import { makeEmails, seedEmails } from '../helpers/emailSeed'

test.use({ viewport: { width: 390, height: 844 } })

/** 灌数据 + 解锁 + 打开收件箱。返回渲染行数。 */
async function openInbox(page: import('@playwright/test').Page, count: number) {
  await seedTokenAuth(page)
  await page.goto('/#/ai')
  await page.waitForTimeout(2000)
  expect(await unlockLobster(page), 'initLobster 失败').toBe(true)
  const total = await seedEmails(page, makeEmails(count))
  await page.goto('/#/email')
  await page.waitForTimeout(3500)
  return total
}

/** 滚到底并等哨兵补页。 */
async function scrollToEnd(page: import('@playwright/test').Page, rounds = 3) {
  await page.evaluate(() => {
    const cands = [...document.querySelectorAll<HTMLElement>('*')].filter((e) => {
      const s = getComputedStyle(e)
      return e.scrollHeight > e.clientHeight + 20 && /(auto|scroll)/.test(s.overflowY) && e.clientHeight > 200
    })
    if (!cands.length) return
    let best = cands[0]
    for (const c of cands) if (c.scrollHeight > best.scrollHeight) best = c
    best.setAttribute('data-e2e-scroll-host', '1')
  })
  for (let i = 0; i < rounds; i++) {
    await page.evaluate(() => {
      const h = document.querySelector<HTMLElement>('[data-e2e-scroll-host]')
      if (h) h.scrollTop = h.scrollHeight
    })
    await page.waitForTimeout(1200)
  }
}

test('本地库确实能翻出更多（先证数据够，避免把「没数据」当「翻不动」）', async ({ page }) => {
  const total = await openInbox(page, 75)
  expect(total).toBeGreaterThanOrEqual(75)

  const pages = await page.evaluate(async () => {
    const s = (await import(/* @vite-ignore */ '/src/features/email/emails-store.ts')) as any
    return {
      p0: (await s.listEmails({ limit: 30, offset: 0 })).length,
      p1: (await s.listEmails({ limit: 30, offset: 30 })).length,
      p2: (await s.listEmails({ limit: 30, offset: 60 })).length,
    }
  })
  expect(pages.p1, '第二页必须有行').toBeGreaterThan(0)
  expect(pages.p2, '第三页必须有行').toBeGreaterThan(0)
})

test('哨兵不得谎报「已到最早一封」——库里还有 45 封', async ({ page }) => {
  await openInbox(page, 75)
  const more = await page.locator('.more').innerText()
  expect(more.trim(), `哨兵文案是「${more.trim()}」，但本地库还有 45 封没翻出来`).not.toContain('已到最早')
})

test('滚动到底真的能补出后续页（不是只改文案）', async ({ page }) => {
  await openInbox(page, 75)
  const before = await page.locator('.email-card').count()
  await scrollToEnd(page)
  const after = await page.locator('.email-card').count()
  expect(after, `滚动后行数 ${after} 未多于首屏 ${before} —— 连续加载失效`).toBeGreaterThan(before)
})
