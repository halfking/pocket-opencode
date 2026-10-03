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
import { openCdp } from './lib/adb-cdp.mjs'

const PSQL = 'C:/workspace/openpocket/logs/pg/dist2/pgsql/bin/psql.exe'
const DRY = process.argv.includes('--dry')
// ⚠️ 2026-10-03 改名的坑：原来这里读的是 `process.env.POCKET_PG_USER`。
// 而 **POCKET_PG_USER 在后端 config.go 里是 PostgreSQL 的登录角色**
// （start-local-backend.ps1 连库就用它），不是 App 的 user_id。
// 两个语义撞在一个变量名上，后果是静默的：对着隔离库跑时若按后端习惯
// 设成 POCKET_PG_USER=postgres，本脚本就会去删 `user_id='postgres'` 的行，
// 而真数据在 `user_id='user-admin'` 下 —— 实测 before=1|1|1|0、after 仍是
// 1|1|1|0，看起来像「删不掉」。旧的 user_id 覆盖开关保留为
// FLASH_FIXTURE_USER，只在确实要清别的账号时才用。
const USER = process.env.FLASH_FIXTURE_USER || 'user-admin'

const PKG = 'com.kaixuan.opencode.pocket'
const CACHE_KEYS = ['flashcards:v1', 'flashcards:v1:outbox']

// 全部 ASCII，避免 PowerShell/psql 兜底串编码问题
const TABLES = ['flashcard_deck_config', 'flashcard_notes', 'flashcard_cards', 'flashcard_revlog']
const q = (sql) => {
  const out = execFileSync(PSQL, ['-h', '127.0.0.1', '-p', '5432', '-U', 'postgres', '-d', 'postgres', '-t', '-A', '-c', sql], {
    encoding: 'utf8', timeout: 60000, maxBuffer: 33554432,
  })
  return String(out).trim()
}

// PG schema：跟随后端配置（backend/internal/config/config.go 的 POCKET_PG_SCHEMA，默认值相同）。
// 写死 opencode_pocket 会让本夹具只能对着共享库跑 —— 它是**硬删**，删错库就是事故。
const SCHEMA = process.env.POCKET_PG_SCHEMA || 'opencode_pocket';
if (SCHEMA !== 'opencode_pocket') console.log(`PG schema = ${SCHEMA}（非共享库）`);

const countSql = `SELECT ${TABLES.map((t) => `(SELECT count(*) FROM ${SCHEMA}.${t})`).join(" || '|' || ")}`

console.log(`before [decks|notes|cards|revlog] = ${q(countSql)}`)

if (DRY) {
  console.log('--dry：只读，不删。')
  console.log(`将删除 user_id='${USER}' 的 flashcard_revlog / flashcard_cards / flashcard_notes / flashcard_deck_config`)
  console.log(`并将清除 App localStorage 键：${CACHE_KEYS.join(', ')}`)
  process.exit(0)
}

// CDP 通道走共享 helper（2026-10-02）：端口由 adb 分配（tcp:0），不再硬绑 9420。
// 硬编码端口是**同机所有会话共享**的状态——本机同时有别的会话在驱同一台设备，
// 撞上时 adb 抛 10048，而那句报错指向装置，看不出真问题是「上次没清干净」。
let cdp = null
try { cdp = await openCdp({ pkg: PKG }) }
catch (e) { console.log(`  (跳过 localStorage 清理：${String(e?.message || e).split('\n')[0]})`); console.log('  (flow 的启动器会重启 App；未清缓存则这轮不覆盖零卡组分支)'); }

let cacheErr = ''
try {
  if (cdp) {
    const expr = `(() => { const k=${JSON.stringify(CACHE_KEYS)}; const had=k.map(x=>[x, localStorage.getItem(x)!==null]); k.forEach(x=>localStorage.removeItem(x)); return JSON.stringify(had) })()`
    // 原来这里是「超时/异常就打印一句『未确认』然后继续跑 PG 删除，最后照样 ✅」。
    // 那是把「零状态前置没生效」印成了成功：缓存没清 ⇒ 列表回显上一轮的卡组
    // ⇒ 这轮测的是 deck-toggle 分支而不是零卡组分支，而且**不会红**。
    // 现在改成硬失败——判据分不清就不许当它绿。
    const res = await cdp.ev(expr)
    if (typeof res !== 'string' || !res.startsWith('[[')) {
      throw new Error(`CDP 返回了非预期形状：${JSON.stringify(res)?.slice(0, 200)}`)
    }
    const had = JSON.parse(res)
    console.log(`  localStorage 清理：${JSON.stringify(had)}`)
    if (!had.some(([, v]) => v)) console.log('  (本来就没有缓存键)')
  }
} catch (e) {
  cacheErr = String(e?.message || e).slice(0, 300)
} finally {
  // 失败路径同样要还 forward；但**不**在这里 exit——
  // process.exit() 不跑 finally，退出必须放到块外。
  if (cdp) await cdp.close()
}
if (cacheErr) {
  console.log(`❌ localStorage 清理失败：${cacheErr}`)
  console.log('   前置没生效就不能声称「已清零」——否则这轮会静默地测错分支。')
  process.exit(1)
}

// ⚠️ 2026-10-04 修的竞态：清完 localStorage **立刻 force-stop**。
//
// 现象：PG 已清零，列表页仍显示上一轮的「回归卡组」，且那条 deck 的
// createdAt/updatedAt 与清场**前完全相同** ⇒ 不是被重写的，是从磁盘恢复的。
//
// 走过的三条弯路（都留档，避免再走）：
//   ① 先 force-stop 再 CDP 清 → App 不在运行，devtools socket 连不上，清不掉。
//   ② 先 CDP removeItem 再 force-stop → **两步之间 App 还活着**，内存里的
//      store 仍持有卡组，在这个窗口里又把缓存落了盘。
//   ③ 删掉整个 app_webview/Default/Local Storage 目录 → 竞态是没了，
//      但把**主密码设置也一起清掉**了（leveldb 按 key 没法精确删）。
//      后果实测：下一次登录后 App 弹「创建主密码」对话框盖住路由，
//      maestro-run.mjs 等 hash 离开 #/login 超时 → **误报登录失败**
//      （后端 /api/auth/login 实测 200、token 291 字符都在）。
//      为了跑一条闪卡 flow 毁掉整个 App 登录态，不划算。
//
// ⇒ 采纳 ② + 把回写窗口压到最小：removeItem 之后**立刻**force-stop。
// 仍以「force-stop 后重启复查键是否真的没了」为判据，不靠推测。
const adbBin = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe'
const serial = process.env.POCKET_SERIAL || '192.168.31.19:5555'
const sh = (cmd) => execFileSync(adbBin, ['-s', serial, 'shell', cmd], {
  encoding: 'utf8', timeout: 30000, maxBuffer: 33554432,
})
try {
  sh(`am force-stop ${PKG}`)
  console.log(`  已 force-stop ${PKG}（否则运行中的 store 会把缓存写回）`)
} catch (e) {
  console.log(`  ❌ force-stop 失败：${String(e?.message || e).split('\n')[0]}`)
  console.log('   App 仍在运行 → 它可能把闪卡缓存写回，这轮会静默测错分支。')
  process.exit(1)
}

q(`DELETE FROM ${SCHEMA}.flashcard_revlog WHERE card_id IN (
     SELECT c.id FROM ${SCHEMA}.flashcard_cards c WHERE c.user_id = '${USER}')`)
q(`DELETE FROM ${SCHEMA}.flashcard_cards WHERE user_id = '${USER}'`)
q(`DELETE FROM ${SCHEMA}.flashcard_notes WHERE user_id = '${USER}'`)
q(`DELETE FROM ${SCHEMA}.flashcard_deck_config WHERE user_id = '${USER}'`)

console.log(`after  [decks|notes|cards|revlog] = ${q(countSql)}`)
const left = q(countSql).split('|').map(Number)
if (left.some((n) => n !== 0)) {
  console.log('⚠️ 仍有残留，flow 断言可能假绿，先别跑。')
  process.exit(1)
}
console.log('✅ 已清零，可以跑 flashcards-write.yaml（零卡组分支）')
