// 验证「先离开闪卡路由再清缓存」能否真正清掉（2026-10-04 夹具竞态的候选修法）。
//
// 背景：夹具 removeItem 之后紧跟 force-stop，PG 也清到 0，
// 但被测主 App 重启后 `flashcards:v1` 里**又出现了**那张卡组。
// store 代码里没有 watcher / pagehide / 防抖，只有 syncFromServer() 结束时的 persistCache()
// ⇒ 推测是「App 停在 #/flashcards，store 挂载、内存持有卡组，在途 sync 回来后写回」。
//
// 候选修法：清之前先把 App 导航到**不挂闪卡 store** 的路由（#/ai），
// 内存里就没有那份状态可写。判据必须落在**重启之后**读到的值上，
// 而不是 removeItem 的返回值 —— 后者只证明「我调了 removeItem」。
import { execFileSync } from 'node:child_process'
import { openCdp } from './lib/adb-cdp.mjs'

const PKG = 'com.kaixuan.opencode.pocket'
const S = process.env.POCKET_SERIAL || '192.168.31.19:5555'
const adbBin = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe'
const adb = (a, t = 20000) =>
  execFileSync(adbBin, ['-s', S, ...a], { encoding: 'utf8', timeout: t, maxBuffer: 33554432 })
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
const CACHE_KEY = 'flashcards:v1'

const readDecks = async () => {
  const cdp = await openCdp({ pkg: PKG })
  try {
    const raw = await cdp.ev(`localStorage.getItem(${JSON.stringify(CACHE_KEY)})`)
    if (raw == null) return { present: false, decks: 0 }
    const n = JSON.parse(raw).deckConfigs?.length ?? 0
    return { present: true, decks: n }
  } finally { await cdp.close() }
}

const relaunch = async () => {
  adb(['shell', `am force-stop ${PKG}`])
  await sleep(1200)
  adb(['shell', `monkey -p ${PKG} -c android.intent.category.LAUNCHER 1`])
  await sleep(4000)
}

const before = await readDecks()
console.log(`起点：${JSON.stringify(before)}`)
if (before.decks === 0) {
  console.log('（起点已经是零状态，本轮测不出东西 —— 先跑一次 flow 生成卡组再来）')
}

// ① 导航到不挂闪卡 store 的路由
{
  const cdp = await openCdp({ pkg: PKG })
  try {
    await cdp.ev(`location.hash = '#/ai'`)
    await sleep(2000)
    const h = await cdp.ev('location.hash')
    const mounted = await cdp.ev(`!!document.querySelector('main.content.scroll-shell')`)
    console.log(`① 导航到 ${h}（App 外壳挂载=${mounted}）`)
  } finally { await cdp.close() }
}

// ② 清缓存
{
  const cdp = await openCdp({ pkg: PKG })
  try {
    const r = await cdp.ev(`(() => { const had = localStorage.getItem(${JSON.stringify(CACHE_KEY)}) !== null
      localStorage.removeItem(${JSON.stringify(CACHE_KEY)}); return had })()`)
    console.log(`② removeItem：之前存在=${r}，现在=${JSON.stringify(await readDecks())}`)
  } finally { await cdp.close() }
}

// ③ force-stop → 重启 → **重启后**复查
adb(['shell', `am force-stop ${PKG}`])
await sleep(1200)
adb(['shell', `monkey -p ${PKG} -c android.intent.category.LAUNCHER 1`])
await sleep(4500)
const after = await readDecks()
console.log(`③ 重启后：${JSON.stringify(after)}`)

const pass = after.decks === 0
console.log(pass
  ? '\n✅ 成立：先离开闪卡路由再清，重启后确实为空。'
  : '\n❌ 不成立：卡组仍然回来了 —— 在途 sync 不是原因，或还有别的写回路径。')
if (!pass) process.exitCode = 1
