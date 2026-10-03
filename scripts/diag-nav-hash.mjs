// 把 App 导航到指定的 hash 路由（只改 location.hash，不做任何点击）。
// 用途：对比「不同入口」下闪卡建卡页的布局是否一致。
//   node diag-nav-hash.mjs '#/flashcards/new'
//   node diag-nav-hash.mjs '#/flashcards/new?deckId=xxx'
import { openCdp } from './lib/adb-cdp.mjs'

const hash = process.argv[2]
if (!hash) {
  console.error("用法: node diag-nav-hash.mjs '#/flashcards/new'")
  process.exit(2)
}

const cdp = await openCdp({ pkg: 'com.kaixuan.opencode.pocket' })
try {
  await cdp.ev(`location.hash = ${JSON.stringify(hash)}`)
  await new Promise((r) => setTimeout(r, 2500))
  const now = await cdp.ev(`location.hash`)
  console.log(`当前 hash = ${JSON.stringify(now)}${now === hash ? '' : '  ⚠️ 与目标不一致'}`)
} finally {
  await cdp.close()
}
