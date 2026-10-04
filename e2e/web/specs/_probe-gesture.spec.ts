/**
 * 临时探针 —— 测「模态打开时，真实滚轮能否推动背景滚动宿主」。
 * 用真实输入（page.mouse.wheel），不是程序化 scrollTop。
 */
import { expect, test } from '@playwright/test'
import { seedTokenAuth, unlockLobster } from '../helpers/tokenAuth'
import { makeEmails, seedEmails } from '../helpers/emailSeed'

test.use({ viewport: { width: 390, height: 844 } })

test('probe', async ({ page }) => {
  const out: Record<string, unknown> = {}

  await seedTokenAuth(page)
  await page.goto('/#/ai')
  await page.waitForTimeout(2000)
  expect(await unlockLobster(page)).toBe(true)
  await seedEmails(page, makeEmails(75))
  await page.goto('/#/email')
  await page.waitForTimeout(3000)

  // 标出背景滚动宿主
  out.host = await page.evaluate(() => {
    const el = [...document.querySelectorAll<HTMLElement>('*')].find((e) => {
      const s = getComputedStyle(e)
      return e.scrollHeight > e.clientHeight + 20 && /(auto|scroll)/.test(s.overflowY) && e.clientHeight > 150
    })
    if (!el) return null
    el.setAttribute('data-bg', '1')
    return {
      cls: el.className,
      max: el.scrollHeight - el.clientHeight,
      overflowY: getComputedStyle(el).overflowY,
      rect: (({ x, y, width, height }) => ({ x, y, width, height }))(el.getBoundingClientRect()),
    }
  })

  // ── 阶段 A：弹层未打开，真实滚轮 ──
  await page.mouse.move(195, 200)
  await page.mouse.wheel(0, 500)
  await page.waitForTimeout(400)
  out.wheelNoModal = await page.evaluate(
    () => document.querySelector<HTMLElement>('[data-bg]')!.scrollTop,
  )

  // 复位
  await page.evaluate(() => (document.querySelector<HTMLElement>('[data-bg]')!.scrollTop = 0))
  await page.waitForTimeout(200)

  // ── 阶段 B：打开 BottomSheet ──
  await page.evaluate(() => {
    const el = document.querySelector('.inbox-page') as any
    el.__vueParentComponent.setupState.moveOpen = true
  })
  await page.waitForTimeout(1200)

  out.overlay = await page.evaluate(() => {
    const ov = document.querySelector<HTMLElement>('.bottom-sheet-overlay')
    if (!ov) return null
    const r = ov.getBoundingClientRect()
    return {
      rect: { x: r.x, y: r.y, w: r.width, h: r.height },
      coversViewport: r.width >= window.innerWidth && r.height >= window.innerHeight,
      viewport: { w: window.innerWidth, h: window.innerHeight },
    }
  })

  out.elementAtBackgroundPoint = await page.evaluate(() => {
    const e = document.elementFromPoint(195, 200)
    return e ? `${e.tagName}.${(e.className || '').toString().split(' ').slice(0, 2).join('.')}` : null
  })

  out.hostWhileOpen = await page.evaluate(() => {
    const h = document.querySelector<HTMLElement>('[data-bg]')!
    return { inline: h.style.overflowY, computed: getComputedStyle(h).overflowY, scrollTop: h.scrollTop }
  })

  // 真实滚轮落在背景点（现在被遮罩覆盖）
  await page.mouse.move(195, 200)
  await page.mouse.wheel(0, 500)
  await page.waitForTimeout(500)
  out.wheelModal = await page.evaluate(
    () => document.querySelector<HTMLElement>('[data-bg]')!.scrollTop,
  )

  // 程序化对照
  await page.evaluate(() => {
    document.querySelector<HTMLElement>('[data-bg]')!.scrollTop = 700
  })
  out.programmatic = await page.evaluate(
    () => document.querySelector<HTMLElement>('[data-bg]')!.scrollTop,
  )

  console.log('PROBE_REPORT=' + JSON.stringify(out, null, 2))
  expect(true).toBe(true)
})
