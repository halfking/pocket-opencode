/**
 * Playwright 配置 — openpocket web E2E。
 *
 * - baseURL：默认本地 vite dev server（npm run dev，端口 4174）；
 *   vite 已把 /api、/ws 同源代理到后端 8090，无需跨域处理。
 * - 可用 E2E_BASE_URL 覆盖，指向其他环境。
 * - app 使用 hash history（/#/login、/#/ai-chat），所有导航走 hash 路由。
 */
import { defineConfig } from '@playwright/test'

const baseURL = process.env.E2E_BASE_URL ?? 'http://127.0.0.1:4174'

export default defineConfig({
  testDir: './specs',
  /* 单测默认 60s；chat-stream 流式用例内单独放宽到 120s（上游 90s 才报错） */
  timeout: 60_000,
  expect: { timeout: 10_000 },
  /* 流式用例依赖真实后端/上游，串行执行避免互相争抢 */
  fullyParallel: false,
  workers: 1,
  retries: 1,
  reporter: [
    ['list'],
    ['html', { outputFolder: 'test-results/html-report', open: 'never' }],
  ],
  outputDir: 'test-results/artifacts',
  use: {
    baseURL,
    trace: 'on-first-retry',
    screenshot: 'only-on-failure',
  },
  projects: [
    {
      name: 'chromium',
      use: { browserName: 'chromium' },
    },
  ],
})
