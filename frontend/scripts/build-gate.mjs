#!/usr/bin/env node
// gates 链里的冒烟构建（BUG-W 配套）。
//
// 为什么需要它：BUG-D 把「VITE_API_BASE 为空就拒绝构建」的守卫下沉到了
// vite.config.ts，对所有 mode 生效。这道守卫本身是对的 —— 移动端 bundle
// 缺基址会静默回落到 WebView 同源，所有 /api 返回 index.html。
//
// 但它顺带把 `npm run gates` 这条既定验证命令堵死了：gates 的第二步是
// `vite build`，在一台没有 .env.production 的干净机器上必然抛错，于是
// typecheck 之后的 test:native / check:vm-gaps 永远跑不到。
// 「gates 全绿」这句话在 BUG-D 之后对任何人都无法复现 —— 这比守卫本身更糟，
// 因为它让人不再相信这条门槛真的被跑过。
//
// 这里做的事很窄：只给**冒烟构建**打开逃生舱，且产物是丢弃的。
// `npm run build:fast`（BUG-D 要防的那条路）保持原样受守卫保护 ——
// 如果把逃生舱写进 build:fast，守卫就恰好对 BUG-D 的原始场景失效了。
import { spawnSync } from 'node:child_process'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const frontendRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')

const result = spawnSync(
  process.platform === 'win32' ? 'npx.cmd' : 'npx',
  ['vite', 'build'],
  {
    cwd: frontendRoot,
    stdio: 'inherit',
    shell: process.platform === 'win32',
    env: { ...process.env, MOBILE_ALLOW_EMPTY_API_BASE: '1' },
  },
)

process.exit(result.status ?? 1)
