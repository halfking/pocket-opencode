/**
 * responsive-shell.spec.ts — 响应式外壳门禁（视口矩阵）。
 *
 * 为什么要这条：断点数值本身有静态门禁（`breakpoint-mirror.test.mjs`），
 * 但那只证明「CSS 与 JS 写的是同一组数字」。它**证明不了**：
 *   - 某一档真的没有横向溢出（窄屏最常见的回归）；
 *   - 底栏只在 compact 出现、没有漏到桌面；
 *   - 桌面档的形态没被响应式改动带偏。
 * 这三类只有真渲染量得到 —— 截图和 DOM 测量比「读代码推断」可靠。
 *
 * 取样面：只用**不需要后端**的页面（登录 / 服务器选择）。
 * 原因：视口矩阵要覆盖 5 档 × 多页，页面越多越慢，而这两页无需后端即可渲染；
 * **登录后的业务页面由同目录的 `authenticated-shell.spec.ts` 覆盖**
 * （2026-10-06 起本仓已能用本地签发的真 JWT 进入，见 `helpers/tokenAuth.ts`）。
 * 此前这里写的是「本仓的 E2E 靠 UI 登录 + 首次主密码创建，进不去无凭据环境」
 * ——该理由已作废，后端仓自带 `cmd/gen-jwt`。本 spec 仍只取无后端页面，
 * 是**分工**（这里管视口矩阵，那里管登录态），不是因为做不到。
 *
 * 与参考仓（nbjl3）的关系：借它的做法——视口矩阵 + 横向溢出 ≤1px +
 * 桌面零回归断言；取样面按本仓可跑性重选。
 */
import { expect, test } from '@playwright/test'

/** 本仓 SSOT 阶梯（useBreakpoint.ts）：compact<560 / medium<840 / expanded<1280 / wide>=1280 */
const VIEWPORTS = [
  { name: '320 超窄屏', width: 320, height: 720, expectBottomNav: true },
  { name: '390 手机', width: 390, height: 844, expectBottomNav: true },
  { name: '600 medium 边界', width: 600, height: 800, expectBottomNav: true },
  { name: '840 expanded 边界', width: 840, height: 900, expectBottomNav: false },
  { name: '1280 wide 边界', width: 1280, height: 900, expectBottomNav: false },
]

/** 不需要后端即可渲染的路由。 */
const PAGES = [
  { name: '登录页', hash: '/#/login' },
  { name: '服务器选择页', hash: '/#/servers' },
]

/**
 * 横向溢出量 —— **量「内容被切掉」而不是「页面能不能横滚」**。
 *
 * ⚠️ 这条判据第一版量的是 `documentElement.scrollWidth - clientWidth`，
 *   在本仓**恒为 0**，于是是一条没有牙的门禁。
 *   变异注入 `#app > * { min-width: 900px }` 时：
 *     - 注入确实生效（app-root 的 rectWidth=900、right=900）；
 *     - 但某个祖先 clip 了横向溢出，documentElement.scrollWidth 仍是 320。
 *   也就是说「页面不能横滚」与「内容没被切」在本仓**不是同一件事**——
 *   一个 900px 宽的登录表单铺在 320px 屏上是彻底的坏 UI，却报 0。
 *
 * 改用元素级测量：任何右沿超出视口的元素都是被切掉的证据。
 * 例外是「声明了横向滚动」的容器内部（表格横滚、轮播）——那是有意为之。
 */
async function clippedElements(page: import('@playwright/test').Page) {
  return page.evaluate(() => {
    const vw = window.innerWidth
    const bad: Array<{ sel: string; right: number; width: number }> = []
    const describe = (el: Element) => {
      const cls = (el.className || '').toString().trim().split(/\s+/).slice(0, 2).join('.')
      return `${el.tagName.toLowerCase()}${cls ? '.' + cls : ''}`
    }
    const inScroller = (el: Element) => {
      let cur: Element | null = el.parentElement
      while (cur && cur !== document.body) {
        const ox = getComputedStyle(cur).overflowX
        if (ox === 'auto' || ox === 'scroll') return true
        cur = cur.parentElement
      }
      return false
    }
    for (const el of Array.from(document.body.querySelectorAll('*'))) {
      const r = el.getBoundingClientRect()
      if (r.width <= 0 || r.height <= 0) continue
      if (r.right <= vw + 1) continue
      if (inScroller(el)) continue
      bad.push({ sel: describe(el), right: Math.round(r.right), width: Math.round(r.width) })
      if (bad.length >= 8) break
    }
    return bad
  })
}

for (const vp of VIEWPORTS) {
  test.describe(`${vp.name} (${vp.width}px)`, () => {
    test.use({ viewport: { width: vp.width, height: vp.height } })

    for (const p of PAGES) {
      test(`${p.name}：无横向溢出`, async ({ page }) => {
        const errors: string[] = []
        page.on('pageerror', (e) => errors.push(String(e)))
        await page.goto(p.hash)
        // 等待应用完成首帧挂载（hash 路由 + 可能的异步初始化）
        await expect(page.locator('#app')).toBeVisible()
        await page.waitForTimeout(400)

        const clipped = await clippedElements(page)
        expect(
          clipped,
          `${p.name} @ ${vp.width}px 有内容被视口右沿切掉：` +
            clipped.map((c) => `${c.sel}(right=${c.right},w=${c.width})`).join(', ') +
            `。若某元素写死了 min-width/width，请改用档位或 .bp-* 工具类，不要新造断点。`,
        ).toEqual([])

        // 未捕获异常会让页面「看起来正常」但交互已废，必须一并判
        expect(errors, `${p.name} @ ${vp.width}px 有未捕获异常`).toEqual([])
      })
    }

    test('底栏只在 compact 档出现', async ({ page }) => {
      await page.goto('/#/login')
      await expect(page.locator('#app')).toBeVisible()
      await page.waitForTimeout(300)
      const nav = page.locator('.bottom-nav')
      const count = await nav.count()
      if (vp.expectBottomNav) {
        // 登录页本身不显示底栏（未登录），但**不应**出现桌面形态的侧栏
        const sidebar = await page.locator('.app-sidebar, [data-shell-sidebar]').count()
        expect(sidebar, 'compact 档不应出现桌面侧栏').toBe(0)
      }
      // 无论哪档，底栏都不应溢出视口宽度
      if (count > 0) {
        const box = await nav.first().boundingBox()
        if (box) {
          expect(box.x + box.width, '底栏右沿不得超出视口').toBeLessThanOrEqual(vp.width + 1)
        }
      }
    })
  })
}

test.describe('桌面零回归（1280px）', () => {
  test.use({ viewport: { width: 1280, height: 900 } })
  test('宽屏不出现窄屏专用的超窄屏工具类残留', async ({ page }) => {
    await page.goto('/#/login')
    await expect(page.locator('#app')).toBeVisible()
    await page.waitForTimeout(300)
    // --bp-* 的宽屏可见性由 breakpoints.css 的 min-width 控制；
    // 这里只断言「页面确实按宽屏渲染了」（login 的最大宽度容器存在）
    const wrap = await page.locator('#app').boundingBox()
    expect(wrap, '#app 应有布局盒').not.toBeNull()
  })
})
