// 负控：证明 diag-flashcard-clear-nav-away.mjs 的结论里，「先离开闪卡路由」确实是关键变量。
//
// 只做正向对照是不够的 —— 上一轮那张卡组本来就是流程跑出来的，清完之后再清一次
// 很可能**恰好**没有触发写回，那这条判据就是失明的。
//
// 这里自己造出同样的前置：直接把一份带卡组的缓存塞进 localStorage，
// 导航到 #/flashcards 让 store 挂载（内存因此持有卡组），
// 然后执行与被测方案**完全相同**的 removeItem + force-stop + 重启。
// 若卡组回来 ⇒ store 挂载确实是写回来源，方案里的「先离开」不是多余的。
import { execFileSync } from 'node:child_process'
import { openCdp } from './lib/adb-cdp.mjs'

const PKG = 'com.kaixuan.opencode.pocket'
const S = process.env.POCKET_SERIAL || '192.168.31.19:5555'
const adbBin = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe'
const adb = (a, t = 20000) =>
  execFileSync(adbBin, ['-s', S, ...a], { encoding: 'utf8', timeout: t, maxBuffer: 33554432 })
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
const CACHE_KEY = 'flashcards:v1'

// 造一份与真实结构一致的缓存（字段名照 stores/flashcards.ts 的 CachedState）
const SEED = JSON.stringify({
  notes: [], cards: [],
  deckConfigs: [{ deckId: 'deck_negctl0001', userId: 'user-admin', name: '负控卡组', newPerDay: 20, reviewsPerDay: 200, createdAt: 1, updatedAt: 1 }],
  reviewLogs: [], lastSyncedAt: 0,
})

const readDecks = async () => {
  const cdp = await openCdp({ pkg: PKG })
  try {
    const raw = await cdp.ev(`localStorage.getItem(${JSON.stringify(CACHE_KEY)})`)
    if (raw == null) return { present: false, decks: 0, name: null }
    const j = JSON.parse(raw)
    return { present: true, decks: j.deckConfigs?.length ?? 0, name: j.deckConfigs?.[0]?.name ?? null }
  } finally { await cdp.close() }
}

const ev = async (expr) => {
  const cdp = await openCdp({ pkg: PKG })
  try { return await cdp.ev(expr) } finally { await cdp.close() }
}

// ① 塞入缓存
await ev(`localStorage.setItem(${JSON.stringify(CACHE_KEY)}, ${JSON.stringify(SEED)})`)
console.log(`① 已注入缓存：${JSON.stringify(await readDecks())}`)

// ② 导航到 #/flashcards，让 store 挂载并把这份缓存读进内存
await ev(`location.hash = '#/flashcards'`)
await sleep(3500)
console.log(`② 导航到 #/flashcards（store 挂载，读入内存）`)

// ③ 与被测方案**完全相同**的清理动作
await ev(`localStorage.removeItem(${JSON.stringify(CACHE_KEY)})`)
console.log(`③ removeItem 后（仍在 #/flashcards）：${JSON.stringify(await readDecks())}`)

// ④ force-stop → 重启 → 复查
adb(['shell', `am force-stop ${PKG}`])
await sleep(1200)
adb(['shell', `monkey -p ${PKG} -c android.intent.category.LAUNCHER 1`])
await sleep(4500)
const after = await readDecks()
console.log(`④ 重启后：${JSON.stringify(after)}`)

if (after.decks > 0) {
  console.log('\n✅ 负控成立：store 挂载时「先清再停」会被写回 ⇒ 方案里的「先离开闪卡路由」是必要的，不是多余的。')
} else {
  console.log('\n⚠️ 负控不成立：store 挂载时也没写回。说明上一轮卡组回来的机制**不是**这个，' +
    '「先离开」只是碰巧无害 —— 不能拿它当已定死的修法。')
  process.exitCode = 1
}
