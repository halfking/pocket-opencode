/**
 * auth.spec.ts — 认证流程 E2E。
 *
 * 覆盖点：
 * 1. 未登录访问受保护路由（/#/ai，路由 meta.requiresAuth）→ 守卫重定向到登录页
 *    （frontend/src/app/routeGuards.ts：next({ path: '/login', query: { returnTo } })，
 *    hash history 下 URL 变为 /#/login?returnTo=%2Fai）。
 * 2. 错误密码登录 → 页面显示错误提示（.error-message，401 时文案为
 *    「登录失败：用户名或密码错误」，此处只断言可见且非空，不锁死服务端文案）。
 * 3. 正确密码登录（用户名/密码可由 E2E_USERNAME / E2E_PASSWORD 覆盖）→
 *    成功进入主页 /#/ai（LoginView.doLogin 成功后 router.push('/ai')；
 *    若后端未设置主密码会先弹「创建主密码」对话框，helper 已处理该分支）。
 */
import { expect, test } from '@playwright/test'
import { E2E_PASSWORD, E2E_USERNAME, login } from '../helpers/auth'

test.describe('认证流程', () => {
  test('未登录访问受保护路由跳转登录页', async ({ page }) => {
    // 全新 context 无 localStorage（pocket_token 为空）→ isAuthenticated=false
    await page.goto('/#/ai')
    await expect(page).toHaveURL(/#\/login/, { timeout: 15_000 })
  })

  test('错误密码登录显示错误提示', async ({ page }) => {
    await page.goto('/#/login')
    await expect(page).toHaveURL(/#\/login/)

    await page.getByPlaceholder('输入用户名').fill(E2E_USERNAME)
    await page.getByPlaceholder('输入密码').fill('definitely-wrong-password!')
    await page.getByRole('button', { name: '登录', exact: true }).click()

    // LoginView：<div v-if="error" class="error-message">{{ error }}</div>
    const errorMessage = page.locator('.error-message')
    await expect(errorMessage).toBeVisible({ timeout: 15_000 })
    await expect(errorMessage).not.toBeEmpty()
    // 仍在登录页
    await expect(page).toHaveURL(/#\/login/)
  })

  test('正确密码登录成功进入主页', async ({ page }) => {
    await login(page) // helper 内部已断言最终 URL 为 /#/ai
    await expect(page).toHaveURL(/#\/ai/)
  })
})
