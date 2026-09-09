/**
 * 登录辅助函数。
 *
 * 选择器依据（已核对 frontend/src/features/auth/LoginView.vue 源码）：
 * - 用户名输入框：placeholder「输入用户名」
 * - 密码输入框：  placeholder「输入密码」（type=password）
 * - 提交按钮：    role=button，文案恰为「登录」（加载中变「登录中...」）
 * - 登录失败提示：.error-message（401 时文案为「登录失败：用户名或密码错误」）
 * - 登录成功：    router.push('/ai') → URL 变为 /#/ai
 *
 * 注意分支：若后端尚未设置「主密码」（cryptoConfig.cfg.hasMasterPassword=false），
 * 登录成功后不会跳转，而是弹出 MasterPasswordDialog（mode=create，标题「创建主密码」，
 * 输入框 placeholder「主密码（至少 8 位）」/「再次输入主密码」，提交按钮文案「确认」），
 * 创建完成后才 router.replace 跳转。本 helper 两条路径都处理。
 *
 * 优化说明：当前每个用例前都走一遍 UI 登录（简单、可靠）。后续可用
 * Playwright 的 storageState（登录一次后保存 localStorage 的 pocket_token 等，
 * 在 playwright.config.ts 里配置 storageState）复用会话，缩短套件耗时。
 */
import { expect, type Page, type Locator } from '@playwright/test'

export const E2E_USERNAME = process.env.E2E_USERNAME ?? 'admin'
export const E2E_PASSWORD = process.env.E2E_PASSWORD ?? 'Veritrans&9527'
/** 首次登录时若后端要求创建主密码，用它完成创建（可被环境变量覆盖） */
export const E2E_MASTER_PASSWORD = process.env.E2E_MASTER_PASSWORD ?? 'e2e-master-pass-123'

/** 在登录页定位「创建主密码」对话框（role=dialog 且含标题文案） */
function masterPasswordDialog(page: Page): Locator {
  return page.locator('[role="dialog"]').filter({ hasText: '创建主密码' })
}

/**
 * 通过 UI 完成登录，最终落在主页 /#/ai。
 * 幂等：可在一个用例开头调用一次，之后该 page 已带登录态。
 */
export async function login(page: Page): Promise<void> {
  await page.goto('/#/login')
  await expect(page).toHaveURL(/#\/login/)

  await page.getByPlaceholder('输入用户名').fill(E2E_USERNAME)
  await page.getByPlaceholder('输入密码').fill(E2E_PASSWORD)
  // exact:true 避免误中「验证码登录」tab（其显式 role=tab，本身不会被
  // getByRole('button') 命中，exact 再排除「指纹登录」等相近文案）
  await page.getByRole('button', { name: '登录', exact: true }).click()

  // 竞速两条成功路径：直接跳 /#/ai，或先弹「创建主密码」对话框
  const dialog = masterPasswordDialog(page)
  const landedDirectly = await Promise.race([
    page.waitForURL(/#\/ai/, { timeout: 15_000 }).then(() => true),
    dialog
      .waitFor({ state: 'visible', timeout: 15_000 })
      .then(() => false),
  ])

  if (!landedDirectly) {
    await dialog.getByPlaceholder('主密码（至少 8 位）').fill(E2E_MASTER_PASSWORD)
    await dialog.getByPlaceholder('再次输入主密码').fill(E2E_MASTER_PASSWORD)
    await dialog.getByRole('button', { name: '确认' }).click()
  }

  // 创建主密码后 router.replace 跳 returnTo 或 '/'，而 '/' redirect 到 '/ai'
  await expect(page).toHaveURL(/#\/ai/, { timeout: 20_000 })
}
