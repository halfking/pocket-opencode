/**
 * authenticated-shell.spec.ts — 登录后外壳取证（2026-10-06 新增）。
 *
 * 此前本仓 e2e 的取样面只有登录页与服务器选择页，理由写在旧 spec 里：
 * 「靠 UI 登录 + 首次主密码创建，进不去无凭据环境」。那句话**现在不成立**
 * ——后端仓自带 `cmd/gen-jwt`，用与 pocketd 同一个 secret 签一枚真 token，
 * 配 `helpers/tokenAuth.ts` 即可进入任意受保护路由。
 *
 * 这条 spec 补上那一半取样面，断言必须**有牙**：
 *   1. 落地 URL 仍停在目标路由。**token 整体无效时最先红的是这一条**——
 *      2026-10-06 实测：喂一枚签名错误的 token，应用 boot 期的鉴权请求
 *      触发 `forceReauth()`，在守卫判定前就被弹去 /#/login。
 *   2. 整程**零 401**。它抓的是**上面那条抓不到的那一类**：URL 还没跳、
 *      但请求已经被拒（token 中途过期、`VITE_API_PROXY` 指错后端、
 *      某个模块的 401 不走 `forceReauth`——BUG-AX 记录的
 *      「`api/client.ts` 整个面绕过兜底」就是这种）。
 *      ⚠️ 这两条是**分工**不是重复：调换顺序会掩盖真实病因。
 *   3. `requiresLobster` 路由真的能进（守卫 Case C 的 redirectUnlock 会被抓）。
 *   4. 顶栏标题与底栏真的渲染。这是本轮 Hyper 重构的产物，值得在**真实登录态**
 *      下被看见，而不是只在登录页上推断。
 *
 * ⚠️ 视口固定在 390×844（compact）：底栏只在该档出现（`--z-bottom-nav` 那一档
 *    规则），在 1280 下断言底栏可见是**量具错了**，不是产品错了。
 *    选择器用 `.bottom-nav` 而非 `getByRole('navigation', {name:'主导航'})`：
 *    后者的 aria-label 走 i18n（`:aria-label="t('nav.mainNavigation')"`），
 *    本仓当前语言下渲染成英文，按中文字面量找必然找不到。
 *
 * 前置条件（不是本 spec 能自己解决的）：
 *   - 真后端在 :8088（POCKET_JWT_SECRET=test-secret-key-for-phase7-validation）；
 *   - vite 以 `VITE_API_PROXY=http://127.0.0.1:8088` 起。
 *
 * Run: E2E_BASE_URL=http://127.0.0.1:4190 npx playwright test authenticated-shell --reporter=list
 */
import { expect, test, type Page } from '@playwright/test'
import { assertTokenWorks, gotoAuthenticated, seedTokenAuth } from '../helpers/tokenAuth'

/** 收集整程的 401/5xx，供断言使用。 */
function watchApi(page: Page) {
  const api401: string[] = []
  const api5xx: string[] = []
  page.on('response', (r) => {
    if (!r.url().includes('/api/')) return
    const path = r.url().replace(/^https?:\/\/[^/]+/, '')
    if (r.status() === 401) api401.push(path)
    else if (r.status() >= 500) api5xx.push(`${r.status()} ${path}`)
  })
  return { api401, api5xx }
}

test.use({ viewport: { width: 390, height: 844 } })

test.beforeAll(async () => {
  // 先证明 token 被真后端接受。若这一步红了，后面全是噪音。
  await assertTokenWorks()
})

test('不带 requiresLobster 的受保护路由：渲染且整程零 401', async ({ page }) => {
  await seedTokenAuth(page)
  const { api401 } = watchApi(page)

  await page.goto('/#/ai')
  // 有牙 1：**最先红的就是这条**。2026-10-06 实测：喂一枚签名错误的 token，
  // 应用 boot 期的鉴权请求触发 forceReauth()，在守卫判定前就被弹去 /#/login。
  await expect(page).toHaveURL(/#\/ai/, { timeout: 15_000 })
  await page.waitForTimeout(3000)

  // 有牙 2：整程无 401。抓的是「上面那条抓不到」的那一类——URL 还对，
  // 但请求已经被拒（token 中途过期、VITE_API_PROXY 指错后端、某模块的 401
  // 走了不兜底的路径：BUG-AX 记录的「api/client.ts 整个面绕过兜底」）。
  expect(
    api401,
    `URL 仍在 ${page.url()} 但出现 401 —— token 中途失效 / 代理指错后端 / 某模块的 401 走了不兜底的路径：\n${api401.join('\n')}`,
  ).toEqual([])
  // 有牙 3：URL 不带 reason=expired（forceReauth 的签名）。
  expect(page.url()).not.toContain('reason=expired')
  // 有牙 3：底栏真在（compact 档 + 路由 meta.bottomNav=true）。
  await expect(page.locator('.bottom-nav').first()).toBeVisible()
  // 有牙 4：顶栏有真实标题，不是「加载中」或空。
  const title = (await page.locator('.top-bar').first().innerText()).trim()
  expect(title.length, `顶栏标题为空：${JSON.stringify(title)}`).toBeGreaterThan(0)
})

test('requiresLobster 路由：解锁后可进入，且不落在 unlock 流程', async ({ page }) => {
  const { api401 } = watchApi(page)

  await gotoAuthenticated(page, '/email')
  await page.waitForTimeout(2500)

  expect(page.url(), '仍停在 unlock 说明 initLobster 没生效').not.toContain('unlock=1')
  expect(page.url()).not.toContain('/login')
  // 邮箱页顶栏标题取自 route meta.title = '邮箱'，壳层由 TitleResolver 解析。
  const title = (await page.locator('.top-bar').first().innerText()).trim()
  expect(title.length, '顶栏标题为空').toBeGreaterThan(0)
  expect(title).toContain('邮箱')
  await expect(page.locator('.bottom-nav').first()).toBeVisible()
  expect(api401, `出现 401：\n${api401.join('\n')}`).toEqual([])
})

test('跨页往返后仍是登录态（守卫没有二次弹回）', async ({ page }) => {
  await gotoAuthenticated(page, '/email')
  await page.waitForTimeout(2000)

  await page.goto('/#/ai')
  await page.waitForTimeout(1200)
  await page.goto('/#/email')
  await page.waitForTimeout(1200)

  expect(page.url()).not.toContain('/login')
  expect(page.url()).not.toContain('unlock=1')
  await expect(page.locator('.bottom-nav').first()).toBeVisible()
})
