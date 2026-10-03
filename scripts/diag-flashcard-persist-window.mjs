// 判定「卡组是在磁盘上一直没删掉」还是「App 启动后才被写回去」。
//
// 背景（2026-10-04）：夹具导航离开闪卡页、removeItem、force-stop、删 PG、重启自证，
// 自证却读到 decks=1。而同一时刻：
//   - 服务端 GET /api/flashcards 返回 {"cards":null,"decks":null}（空）
//   - PG opencode_pocket 是 0 行
//   - pinia 里**没有** flashcards store（本次会话没人写过它）
// ⇒ 只剩两种可能，必须分开：
//     A. removeItem 根本没落盘（WebView 被 force-stop 时丢了这次写）
//     B. 落盘了，但 App 启动过程中某处又写回
//
// 判据 = **重启后最早读**的那一次：
//   A ⇒ 一连上就 decks=1（还没等 App 做任何事）
//   B ⇒ 刚连上时是 0，稍后才变 1
import { execFileSync } from 'node:child_process'
import { openCdp } from './lib/adb-cdp.mjs'

const PKG = 'com.kaixuan.opencode.pocket'
const S = process.env.POCKET_SERIAL || '192.168.31.19:5555'
const adbBin = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe'
const adb = (a, t = 20000) =>
  execFileSync(adbBin, ['-s', S, ...a], { encoding: 'utf8', timeout: t, maxBuffer: 33554432 })
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
const KEY = 'flashcards:v1'

const read = async (label) => {
  const t0 = Date.now()
  // App 刚拉起时 devtools socket 还没注册，严格匹配会抛 CDP_SOCKET_PID_MISMATCH。
  // 这不是失败，是「还没到能读的时候」——重试到能连上为止，但**只重试连接**，
  // 一旦连上就立刻读（读的时刻才是判据，连接时刻不是）。
  let cdp = null
  for (let i = 0; i < 40; i++) {
    try { cdp = await openCdp({ pkg: PKG }); break } catch { await sleep(500) }
  }
  if (!cdp) { console.log(`  ${label.padEnd(26)} 连不上`); return -1 }
  try {
    const raw = await cdp.ev(`localStorage.getItem(${JSON.stringify(KEY)})`)
    const n = raw == null ? 0 : (JSON.parse(raw).deckConfigs || []).length
    console.log(`  ${label.padEnd(26)} +${String(Date.now() - t0).padStart(5)}ms  decks=${n}`)
    return n
  } finally { await cdp.close() }
}

console.log('① 当前状态：')
const now = await read('启动前(现状)')

console.log('② force-stop → 重启')
adb(['shell', `am force-stop ${PKG}`])
await sleep(1500)
adb(['shell', `monkey -p ${PKG} -c android.intent.category.LAUNCHER 1`])

// 连上 devtools 需要时间，连上**立刻**读一次 —— 这是关键的一次。
const first = await read('重启后第一次读')
await sleep(6000)
const second = await read('重启后 +6s')
await sleep(8000)
const third = await read('重启后 +14s')

console.log('')
if (first > 0) {
  console.log('⇒ A：**磁盘上一直没删掉**。App 还没做任何事，卡组就已经在了。')
  console.log('   ⇒ localStorage.removeItem 的写没有落盘（force-stop 把这次写丢了），')
  console.log('     或者写落到了别的地方。往「清内存 store」方向修是修错方向了。')
} else if (second > 0 || third > 0) {
  console.log('⇒ B：App 启动过程中把卡组写回去了。')
  console.log(`   first=${first} second=${second} third=${third}`)
} else {
  console.log(`⇒ 既不是 A 也不是 B：现在就没了（now=${now} first=${first} second=${second} third=${third}）。`)
  console.log('   若是这种，说明卡组本来就会被清掉——上一轮自证失败另有原因，别急着下结论。')
}
