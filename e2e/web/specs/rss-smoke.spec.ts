/**
 * rss-smoke.spec.ts — RSS 冒烟 E2E（web 端）。
 *
 * 覆盖点：
 *   1. 登录 → /rss 列表页可达 + 页面骨架（requiresAuth + requiresLobster：
 *      web 下需先经「创建主密码 / 解锁」让本地加密库就绪（jeep-sqlite web，
 *      见 frontend/src/native/sqlite-web-init.ts）；初始化失败时显式 skip，不挂）。
 *   2. 添加订阅源入口可见（RssListView 头部「新增源」按钮）→ 点击进入
 *      /rss/add，断言「添加订阅源」表单骨架；不实际提交 —— 「发现候选」
 *      与「添加」会请求目标站点（外部网络依赖），本期不自动执行（见文末 TODO）。
 *   3. 数据驱动（两种结果都是合法通过路径，不会 flaky）：
 *      GET /api/rss/sources、GET /api/rss/items 探测后端数据 ——
 *      有数据断言列表渲染，无数据断言组件真实空态文案。
 *
 * 路由来源（frontend/src/app/router-mobile.ts，createWebHashHistory）：
 *   /rss       → frontend/src/features/rss/RssListView.vue（title 'RSS 订阅'）
 *   /rss/add   → frontend/src/features/rss/RssAddSource.vue（title '添加订阅'）
 *   /rss/items/:id → frontend/src/features/rss/RssItemDetail.vue（title '条目详情'）
 *
 * 选择器来源（全部出自组件源码，非猜测）：
 *   - RssListView.vue：.rss-header h2 'RSS 订阅'、.btn-secondary '刷新'、
 *     .btn-primary '新增源'、.tabs button '信息流' / '源 (N)'、.filters select
 *     （option ''=全部）、.filters input placeholder '搜索标题/摘要'、
 *     空态 '暂无信息。' + button.link '添加第一个订阅源'、
 *     源空态 '还没有订阅源。' + button.link '添加一个'
 *   - RssAddSource.vue：.bar h3 '添加订阅源'、label '粘贴网站或 feed URL'、
 *     input placeholder 'https://example.com 或 https://example.com/feed'、
 *     button '发现候选'（输入为空时 disabled）、divider '或者从内置种子开始'
 *
 * TODO（真实添加订阅源，待接入）：
 *   fill 订阅输入框 → 点「发现候选」→ 从候选列表（.candidates li）点选 →
 *   自动回到 /rss 且「源 (N)」计数 +1。因 discover/add 会访问外部 feed 站点
 *   （CI 网络不稳定），本期只覆盖入口与表单骨架；接入时建议先对
 *   POST /api/rss/sources/discover 做 page.route mock 再跑全流程。
 */
import { expect, test } from '@playwright/test'
import {
  apiLoginToken,
  login,
  navigateGuarded,
  probeRssCounts,
} from '../helpers/session'

test.describe('RSS 冒烟', () => {
  // ---------------------------------------------------------------------
  // 1. 列表页可达 + 骨架
  // ---------------------------------------------------------------------
  test('登录后 /rss 列表页可达，头部与入口骨架渲染', async ({ page }) => {
    await login(page) // helpers/auth.ts：落达 /#/ai，主密码已就绪

    // /rss requiresLobster：navigateGuarded 处理「解锁屏」分支；失败则显式跳过
    const nav = await navigateGuarded(page, '/rss')
    if (!nav.landed) {
      test.skip(true, `无法进入 /rss：${nav.reason}（web 运行时本地加密库 jeep-sqlite 初始化失败时会如此）`)
    }

    // 页面骨架（RssListView.vue 模板原文案）
    await expect(page.locator('.rss-header h2')).toHaveText('RSS 订阅')
    await expect(page.locator('.rss-header .btn-secondary')).toContainText('刷新')
    await expect(page.locator('.rss-header .btn-primary')).toContainText('新增源')
    await expect(page.locator('.tabs button', { hasText: '信息流' })).toBeVisible()
    await expect(page.locator('.tabs button', { hasText: /^源 \(/ })).toBeVisible()
  })

  // ---------------------------------------------------------------------
  // 2. 添加订阅源入口可见 → /rss/add 表单骨架（不实际提交）
  // ---------------------------------------------------------------------
  test('「新增源」入口可见，点击进入添加订阅源表单', async ({ page }) => {
    await login(page)
    const nav = await navigateGuarded(page, '/rss')
    if (!nav.landed) {
      test.skip(true, `无法进入 /rss：${nav.reason}`)
    }

    // 添加入口（RssListView 头部主按钮「新增源」，点击 router.push 到 rss-add）
    const addEntry = page.locator('.rss-header .btn-primary')
    await expect(addEntry).toContainText('新增源')
    await addEntry.click()
    await expect(page).toHaveURL(/#\/rss\/add$/)

    // 表单骨架（RssAddSource.vue step=input 的真实文案）
    await expect(page.locator('.rss-add .bar h3')).toHaveText('添加订阅源')
    await expect(page.getByText('粘贴网站或 feed URL')).toBeVisible()
    const urlInput = page.getByPlaceholder('https://example.com 或 https://example.com/feed')
    await expect(urlInput).toBeVisible()
    // 「发现候选」在输入为空时禁用（:disabled="!inputUrl.trim() || busy"）
    const discoverBtn = page.getByRole('button', { name: '发现候选' })
    await expect(discoverBtn).toBeVisible()
    await expect(discoverBtn).toBeDisabled()
    // 内置种子分区标题（GET /api/rss/sources/seeds 成功与否不影响骨架渲染）
    await expect(page.getByText('或者从内置种子开始')).toBeVisible()

    // TODO(真实添加订阅)：见文件头注释 —— fill(urlInput, …) → discoverBtn.click()
    // → 候选选择 → 回到 /rss。外部网络依赖，本期不自动执行。
  })

  // ---------------------------------------------------------------------
  // 3. 数据驱动：有数据断言列表，无数据断言真实空态文案
  // ---------------------------------------------------------------------
  test('信息流与源列表：有数据显示条目，无数据显示空态文案', async ({ request, page }) => {
    const token = await apiLoginToken(request)
    const counts = await probeRssCounts(request, token)
    if (!counts) {
      test.skip(true, '无法探测 GET /api/rss/sources、GET /api/rss/items（后端 RSS 模块不可用）')
    }

    await login(page)
    const nav = await navigateGuarded(page, '/rss')
    if (!nav.landed) {
      test.skip(true, `无法进入 /rss：${nav.reason}`)
    }

    // 信息流默认 tab=items、statusFilter='unread'（RssListView 初始 state）；
    // 为与 API 全量计数对齐，先把筛选切到「全部」（option value=''）再点「刷新」
    // （筛选变更不自动触发请求，需手动 refresh —— 源码 refresh() 的真实交互流）。
    await page.locator('.filters select').selectOption('')
    await page.locator('.rss-header .btn-secondary').click()

    if (counts!.items === 0) {
      // 真实空态文案（RssListView.vue：'暂无信息。' + 链接按钮「添加第一个订阅源」）
      await expect(page.getByText('暂无信息。')).toBeVisible({ timeout: 10_000 })
      await expect(page.getByRole('button', { name: '添加第一个订阅源' })).toBeVisible()
    } else {
      await expect(page.locator('.item-list li').first()).toBeVisible({ timeout: 10_000 })
    }

    // 源 tab（.tabs button 文案 '源 (N)'，N = sources.length）
    await page.locator('.tabs button', { hasText: /^源 \(/ }).click()
    if (counts!.sources === 0) {
      await expect(page.getByText('还没有订阅源。')).toBeVisible({ timeout: 10_000 })
      await expect(page.getByRole('button', { name: '添加一个' })).toBeVisible()
    } else {
      await expect(page.locator('.source-list li').first()).toBeVisible({ timeout: 10_000 })
    }
  })
})
