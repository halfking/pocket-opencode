/**
 * chat-stream.spec.ts — AI 对话（/#/ai-chat）流式链路 E2E。
 *
 * 覆盖点（断言目标是「流式机制工作」，不要求上游回答成功——服务端上游
 * 经常失败并出现模型 retry chip，最长 90s 后报 context deadline exceeded）：
 * 1. 登录后进入 AI 对话页，composer（placeholder「输入消息，Enter 发送，
 *    Shift+Enter 换行」）可见；
 * 2. 输入并发送消息后，10s 内出现至少一种流式证据：
 *    a) 全局进程级流 singleton：window.__openpocket_aiStreamRuntime__
 *       .getStats().activeCount >= 1（frontend/src/native/aiStreamRuntime.ts）；
 *    b) 流式 typing 指示器 .typing（isStreaming=true 时的三点动画）；
 *    c) 模型 retry chip .msg-retry（auto 回退重试进度灰字）。
 * 3. 流结束进入终态（容忍服务端 error frame）：停止生成按钮消失，且气泡出现
 *    终态证据之一（.msg-retry 重试提示 / .msg-error 错误帧 / .usage-row token
 *    统计，或 ai 气泡有非空正文）。
 *
 * 超时预算：整体 120s（上游 90s 才报错），停止按钮消失最多等 110s。
 */
import { expect, test, type Page } from '@playwright/test'
import { login } from '../helpers/auth'

test.setTimeout(120_000)

const PROMPT = 'poem of the night city'
const COMPOSER_PLACEHOLDER = '输入消息，Enter 发送，Shift+Enter 换行'

/** 读取进程级流 singleton 的统计；全局不存在时返回 null（不抛错） */
function readRuntimeStats(page: Page) {
  return page.evaluate(() => {
    const rt = (window as unknown as Record<string, any>).__openpocket_aiStreamRuntime__
    return rt && typeof rt.getStats === 'function' ? rt.getStats() : null
  })
}

test.describe('AI 对话流式输出', () => {
  test('发送消息后 10s 内出现流式证据，结束后气泡有终态', async ({ page }) => {
    await login(page)

    // loadModels 与发送存在竞态：store.models 为空时发送是无操作（仅弹
    // 「请先在设置 → AI 网关配置网关密钥」toast）。先等 /api/llm/models 200；
    // 后端没配网关时该接口会失败 → 显式 skip 而不是误报失败。
    const modelsReady = page
      .waitForResponse((r) => r.url().includes('/api/llm/models') && r.status() === 200, {
        timeout: 15_000,
      })
      .catch(() => null)

    await page.goto('/#/ai-chat')

    // composer 可见（UnifiedComposer 内的 <textarea :placeholder="...">）
    const composer = page.getByPlaceholder(COMPOSER_PLACEHOLDER)
    await expect(composer).toBeVisible({ timeout: 20_000 })

    const ready = await modelsReady
    test.skip(!ready, '后端未配置 LLM 网关（/api/llm/models 未就绪），流式用例需要网关')

    await composer.fill(PROMPT)
    // 发送按钮：.send-btn，aria-label="发送"（流式中会替换为 aria-label="停止生成"）
    const sendButton = page.locator('button[aria-label="发送"]')
    await expect(sendButton).toBeEnabled()

    // 竞态兜底：极少数情况下点击瞬间 models 仍未就绪 → 发送 no-op（composer
    // 保持原文）。此时等 2s 重试点击，最多 6 次；始终无效则按网关未配置 skip。
    let sent = false
    for (let i = 0; i < 6 && !sent; i++) {
      await sendButton.click()
      sent = await page
        .locator('.user-bubble')
        .filter({ hasText: PROMPT })
        .waitFor({ state: 'visible', timeout: 2500 })
        .then(() => true)
        .catch(() => false)
      if (!sent) {
        if (await page.getByText('模型列表获取失败').isVisible().catch(() => false)) {
          test.skip(true, '后端网关未配置：模型列表获取失败')
        }
        await page.waitForTimeout(2_000)
      }
    }
    test.skip(!sent, '发送始终未生效（模型列表未就绪），视为网关未配置环境')

    // ---- 核心断言：10s 内出现任一流式证据（runtime / typing / retry chip）----
    const evidence = await Promise.race([
      page
        .waitForSelector('.typing', { timeout: 10_000 })
        .then(() => 'typing-indicator' as const)
        .catch(() => null),
      page
        .waitForSelector('.msg-retry', { timeout: 10_000 })
        .then(() => 'retry-chip' as const)
        .catch(() => null),
      (async () => {
        const deadline = Date.now() + 10_000
        while (Date.now() < deadline) {
          const stats = await readRuntimeStats(page).catch(() => null)
          if (stats && stats.activeCount >= 1) return 'aiStreamRuntime.activeCount>=1' as const
          await page.waitForTimeout(200)
        }
        return null
      })(),
    ])
    expect(
      evidence,
      '发送后 10s 内应出现流式证据：__openpocket_aiStreamRuntime__.activeCount>=1、.typing 或 .msg-retry 之一',
    ).toBeTruthy()

    // ---- 终态断言（容忍 error frame，不要求成功回答）----
    // 流结束：停止生成按钮消失（错误/完成都会让 isStreaming 回落 false）
    await expect(page.locator('button[aria-label="停止生成"]')).toBeHidden({
      timeout: 110_000,
    })

    // 气泡终态证据之一：retry 提示 / 错误帧 / usage 统计，或 ai 气泡有非空正文
    const frameEvidence = page.locator('.msg-retry, .msg-error, .usage-row')
    await expect
      .poll(
        async () => {
          if ((await frameEvidence.count()) > 0) return 'frame-evidence'
          const text = await page
            .locator('.ai-bubble')
            .last()
            .innerText()
            .catch(() => '')
          return text.trim().length > 0 ? 'bubble-text' : ''
        },
        { timeout: 15_000, intervals: [500, 1_000, 2_000] },
      )
      .not.toBe('')

    // runtime singleton 存在时，本次发送应至少注册过一个流（进程级累计）
    const stats = await readRuntimeStats(page)
    if (stats) {
      expect(stats.totalRegistered).toBeGreaterThanOrEqual(1)
    }
  })
})
