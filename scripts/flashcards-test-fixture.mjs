// flashcards-test-fixture — 清空闪卡表 + 前端缓存，让 flashcards-write.yaml 每次都从**零卡组**起步。
//
// 为什么必需：
//   1. 这个 flow 测的正是「零状态建卡组」（BUG-U 修的就是零卡组时的死胡同）。
//      机器上有卡组时列表页走的是另一条分支（deck-toggle 展开式），零状态那条分支根本测不到。
//   2. 不清残留的话，上一轮的「回归卡组」「回归正面」会一直在，**功能彻底坏掉时断言照样绿**。
//      与 pkm-test-fixture.mjs 同一个道理。
//
// ⚠️ 必须**同时清前端 localStorage 缓存**（stores/flashcards.ts:88 的 flashcards:v1），
//    否则删了 PG 也进不了零状态。原因是 stores/flashcards.ts:291 的
//    `deckConfigs = mergeById(本地, 服务端)` 只做增量合并，删除只走
//    envelope.deletedIds 这条**增量**通道；夹具是绕开 API 的硬删，
//    客户端本来就无从知晓（**这是增量同步的正常行为，不是产品缺陷**）。
//
// 安全性：只删 test 用户的闪卡数据（本仓库测试用 user-admin）+ 该 App 的闪卡缓存键，
// 不碰其它表、不碰其它 localStorage 键。
//
// 用法：node scripts/flashcards-test-fixture.mjs [--dry]
import { execFileSync } from 'node:child_process'

const PSQL = 'C:/workspace/openpocket/logs/pg/dist2/pgsql/bin/psql.exe'
const DRY = process.argv.includes('--dry')
const USER = process.env.POCKET_PG_USER || 'user-admin'
const PKG = 'com.kaixuan.opencode.pocket'
const SERIAL = process.env.POCKET_SERIAL || '192.168.31.19:5555'
const PORT = process.env.POCKET_CDP_PORT || '9420'
const CACHE_KEYS = ['flashcards:v1', 'flashcards:v1:outbox']

// 全部 ASCII，避免 PowerShell/psql 兜底串编码问题
const TABLES = ['flashcard_deck_config', 'flashcard_notes', 'flashcard_cards', 'flashcard_revlog']
const q = (sql) => {
  const out = execFileSync(PSQL, ['-h', '127.0.0.1', '-p', '5432', '-U', 'postgres', '-d', 'postgres', '-t', '-A', '-c', sql], {
    encoding: 'utf8', timeout: 60000, maxBuffer: 33554432,
  })
  return String(out).trim()
}

const countSql = `SELECT ${TABLES.map((t) => `(SELECT count(*) FROM opencode_pocket.${t})`).join(" || '|' || ")}`

console.log(`before [decks|notes|cards|revlog] = ${q(countSql)}`)

if (DRY) {
  console.log('--dry：只读，不删。')
  console.log(`将删除 user_id='${USER}' 的 flashcard_revlog / flashcard_cards / flashcard_notes / flashcard_deck_config`)
  console.log(`并将清除 App localStorage 键：${CACHE_KEYS.join(', ')}`)
  process.exit(0)
}

/** 清 App 里闪卡那两个 localStorage 键。App 没跑就跳过（flow 会自己重启）。 */
function clearAppCache() {
  const adb = (a, t = 60000) => execFileSync('C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe', ['-s', SERIAL, ...a], { encoding: 'utf8', timeout: t, maxBuffer: 33554432 })
  const pid = adb(['shell', `pidof ${PKG}`]).trim().split(/\s+/)[0]
  if (!pid) { console.log('  (App 未运行，跳过缓存清理；flow 的启动器会重启它)'); return }
  const socks = adb(['shell', `cat /proc/net/unix | grep -o 'webview_devtools_remote_[0-9]*'`])
    .split(/\r?\n/).map((l) => l.trim().replace('@', '')).filter(Boolean)
  adb(['forward', `tcp:${PORT}`, `localabstract:${socks.find((s) => s.endsWith(`_${pid}`)) || socks[socks.length - 1]}`])
  return { adb, pid }
}

const conn = clearAppCache()
if (conn) {
  const { adb } = conn
  const page = (await (await fetch(`http://127.0.0.1:${PORT}/json/list`)).json()).find((t) => t.type === 'page')
  const ws = new WebSocket(page.webSocketDebuggerUrl.replace(/:\d+\//, `:${PORT}/`))
  let id = 0; const pending = new Map()
  ws.addEventListener('message', (e) => {
    const m = JSON.parse(e.data)
    if (m.id && pending.has(m.id)) { pending.get(m.id)(m.result); pending.delete(m.id) }
  })
  await new Promise((r) => ws.addEventListener('open', r))
  const ev = (x) => new Promise((r) => {
    const i = ++id
    const t = setTimeout(() => r({ __t: 1 }), 15000)
    pending.set(i, (y) => { clearTimeout(t); r(y) })
    ws.send(JSON.stringify({ id: i, method: 'Runtime.evaluate', params: { expression: x, returnByValue: true, awaitPromise: true } }))
  })
  const expr = `(() => { const k=${JSON.stringify(CACHE_KEYS)}; const had=k.map(x=>[x, localStorage.getItem(x)!==null]); k.forEach(x=>localStorage.removeItem(x)); return JSON.stringify(had) })()`
  const res = await ev(expr)
  console.log(`  localStorage 清理：${res?.__t ? '超时(未确认)' : JSON.stringify(res?.result?.value)}`)
  ws.close()
}

q(`DELETE FROM opencode_pocket.flashcard_revlog WHERE card_id IN (
     SELECT c.id FROM opencode_pocket.flashcard_cards c WHERE c.user_id = '${USER}')`)
q(`DELETE FROM opencode_pocket.flashcard_cards WHERE user_id = '${USER}'`)
q(`DELETE FROM opencode_pocket.flashcard_notes WHERE user_id = '${USER}'`)
q(`DELETE FROM opencode_pocket.flashcard_deck_config WHERE user_id = '${USER}'`)

console.log(`after  [decks|notes|cards|revlog] = ${q(countSql)}`)
const left = q(countSql).split('|').map(Number)
if (left.some((n) => n !== 0)) {
  console.log('⚠️ 仍有残留，flow 断言可能假绿，先别跑。')
  process.exit(1)
}
console.log('✅ 已清零，可以跑 flashcards-write.yaml（零卡组分支）')
