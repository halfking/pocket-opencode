/**
 * rss-digest.spec.ts — 每日摘要 / 内置推荐源 / 内置学习库 的 web 端 E2E。
 *
 * 覆盖的是本轮新增的三个用户可见入口（此前只有 rss-smoke.spec.ts 覆盖列表页骨架）：
 *   1. /rss 的一键导入推荐源（POST /api/rss/sources/import-starter，幂等）
 *   2. /rss/digest 每日摘要页：真实标题 + 真实链接 + 三个分类 + **真实分享** + 重新生成
 *   3. /flashcards 的内置学习库入口（AI / 智能体与大模型 / 英语单词+发音 / 常用句）
 *
 * 与既有 spec 的差别：**先探后断言**。报告类内容依赖当天是否抓到东西，
 * 所以每条断言都以 probe* 的真实返回为判据，而不是写死"必须 8 条"——
 * 否则源被限流那天这条用例会假红，而假红会让人以为功能坏了。
 *
 * 运行：
 *   E2E_BASE_URL=http://127.0.0.1:4174 E2E_PASSWORD=<本地实例口令> \
 *     npx playwright test rss-digest.spec.ts
 */
import { expect, test } from '@playwright/test'
import { apiLoginToken, login, navigateGuarded, probeRssCounts } from '../helpers/session'

/** 探一次日报：拿不到就显式 skip，不挂。 */
async function probeDigest(request: Parameters<typeof apiLoginToken>[0]) {
  const token = await apiLoginToken(request)
  const res = await request.get('/api/rss/digest', {
    headers: { Authorization: `Bearer ${token}` },
  })
  if (!res.ok()) return null
  const body = (await res.json()) as {
    digest?: { itemCount: number; headline?: string; sections?: unknown[] }
  }
  return body.digest ?? null
}

test.describe('每日摘要 / 内置推荐源 / 内置学习库', () => {
  test('/rss：内置推荐源入口存在，重复导入不产生重复订阅', async ({ request, page }) => {
    const before = await probeRssCounts(request, await apiLoginToken(request))
    if (!before) test.skip(true, '后端 RSS 模块不可用')

    await login(page)
    const nav = await navigateGuarded(page, '/rss')
    if (!nav.landed) test.skip(true, `无法进入 /rss：${nav.reason}`)

    // 「一键导入推荐源」在零订阅时是主按钮，已有订阅时是「再导入推荐源」
    // （RssListView.vue 的 starter-hint / starter-row 两处渲染）。
    const starter = page
      .getByRole('button', { name: /一键导入推荐源|再导入推荐源/ })
      .first()
    await expect(starter).toBeVisible({ timeout: 15_000 })

    // 点一次：后端是幂等的，订阅数量不应增加
    await starter.click()
    const after = await probeRssCounts(request, await apiLoginToken(request))
    if (!after) test.skip(true, '导入后无法再探测 RSS 接口')
    expect(after.sources, '重复导入推荐源后订阅数不应增加').toBe(before.sources)

    // 「今日摘要」入口（RssListView 头部 btn-secondary）
    await expect(page.getByRole('button', { name: '今日摘要' })).toBeVisible()
  })

  test('/rss/digest：日报页渲染真实条目，能复制分享文本，能重新生成', async ({ request, page }) => {
    const digest = await probeDigest(request)
    if (!digest) test.skip(true, '后端未提供 /api/rss/digest')

    await login(page)
    const nav = await navigateGuarded(page, '/rss/digest')
    if (!nav.landed) test.skip(true, `无法进入 /rss/digest：${nav.reason}`)

    // 页头（RssDigestView.vue 模板原文案）
    await expect(page.locator('.bar h3')).toHaveText('每日摘要')
    await expect(page.locator('.headline-card .date')).toBeVisible()
    await expect(page.locator('.headline-card h4')).toBeVisible()

    if (digest.itemCount > 0) {
      // 分类分组 + 真实标题/链接：判据取「有 li 且带 http 链接」，
      // 不写死条数（每天抓到多少条不由测试决定）。
      await expect(page.locator('.section h5')).not.toHaveCount(0)
      const firstItem = page.locator('.section li').first()
      await expect(firstItem).toBeVisible()
      await expect(firstItem.locator('a.item-link')).toHaveAttribute('href', /^https?:\/\//)

      // 分享：用户的第 3 项需求（「一键分享到微博/朋友圈」），必须真点。
      // 之前这条只断言按钮 enabled 就收工，等于「分享功能可用」从没被验证过 ——
      // enabled 只说明 hasItems 为真，与点击后是否真的产出可粘贴内容无关。
      // 判据取**剪贴板里的真实文本**（比断言提示文案硬：文案对、内容烂也发现不了）。
      await page.context().grantPermissions(['clipboard-read', 'clipboard-write'])
      await page.getByRole('button', { name: '分享到微博/朋友圈' }).click()
      await expect(page.locator('.notice')).toContainText('已复制', { timeout: 15_000 })
      const clip = await page.evaluate(() => navigator.clipboard.readText())
      // 拿页面上**看到的**标题去比对，而不是拿接口字段：判据钉的是
      // 「复制出去的正是用户眼前这份日报」，接口字段与渲染脱节时也会红。
      const headline = ((await page.locator('.headline-card h4').textContent()) ?? '').trim()
      expect(headline, '日报标题不应为空').not.toBe('')
      expect(clip, '分享文本应以页面上看到的日报标题开头').toContain(headline)
      expect(clip, '分享文本应带可点的原文链接').toMatch(/https?:\/\/\S+/)

      // 卡片图：web 端没有 files 能力，shareCard 渲染完 1080×1350 canvas 后回退成
      // 分享文本。它自己的「不支持分享图片」提示会被 shareText 的复制提示立刻覆盖，
      // 而两条路径复制的内容又完全一样（都走 shareDigestText 默认 12 条）——
      // 所以这里**没有**能区分「点了」和「没点」的判据，只有「跑一遍没炸」这一条：
      // 中文字体缺失 / canvas 抛错都会落到 .error。判据弱，但比不点强。
      await page.getByRole('button', { name: '分享卡片图' }).click()
      await expect(page.locator('.error')).toHaveCount(0)
    } else {
      await expect(page.getByText('今天还没有内容。')).toBeVisible()
      await expect(page.getByRole('button', { name: '分享到微博/朋友圈' })).toBeDisabled()
      await expect(page.getByRole('button', { name: '分享卡片图' })).toBeDisabled()
    }

    // 「重新生成」：应给出明确反馈，而不是静默
    await page.getByRole('button', { name: '重新生成' }).click()
    await expect(page.locator('.notice')).toBeVisible({ timeout: 30_000 })
    await expect(page.locator('.notice')).toContainText('已重新生成')
  })

  test('/flashcards：内置学习库入口存在，导入后四套牌组可见', async ({ request, page }) => {
    // FoldAwareLayout 把同一份内容渲染两遍：#outer（折叠/手机布局）与 #inner
    // （≥8 寸展开布局），同一时刻只有一份 display 得住，而 **#outer 在 DOM 里排在
    // 前面**。所以裸 getByText(x).first() 在宽视口下会选中那份隐藏副本，报
    // "hidden"——牌组其实正在屏幕上。下面所有断言都显式 filter({ visible: true })。
    // 视口也写死成展开布局：外层布局只显示第一个卡组，「四套牌组可见」在它那里
    // 本来就不成立，不固定视口等于让这条断言靠默认值碰运气。
    await page.setViewportSize({ width: 1280, height: 800 })

    const token = await apiLoginToken(request)
    const res = await request.get('/api/flashcards/starter', {
      headers: { Authorization: `Bearer ${token}` },
    })
    if (!res.ok()) test.skip(true, '后端未提供 /api/flashcards/starter')
    const body = (await res.json()) as { decks?: { deckId: string; name: string; cardCount: number }[] }
    const decks = body.decks ?? []
    if (decks.length === 0) test.skip(true, '内置学习库为空')

    await login(page)
    const nav = await navigateGuarded(page, '/flashcards')
    if (!nav.landed) test.skip(true, `无法进入 /flashcards：${nav.reason}`)

    // 入口按钮：空态是「一键导入内置学习库」，有卡组后是「导入内置学习库」
    const entry = page
      .getByRole('button', { name: /一键导入内置学习库|导入内置学习库/ })
      .filter({ visible: true })
      .first()
    await expect(entry).toBeVisible({ timeout: 15_000 })

    // 点一次（后端幂等）：这样无论环境里有没有卡组，四套牌组都必然落到列表里，
    // 断言才不依赖「上一个会话导入过」。
    if (await entry.isEnabled()) await entry.click()

    // 四套牌组（用户点名要的：AI / 智能体与大模型 / 英语单词+发音 / 常用句）
    for (const d of decks) {
      await expect(
        page.getByText(d.name).filter({ visible: true }),
        `牌组「${d.name}」应出现在 /flashcards 列表`,
      ).toBeVisible({ timeout: 30_000 })
    }
  })
})
