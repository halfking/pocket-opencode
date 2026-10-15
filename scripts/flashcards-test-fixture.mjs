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
import { existsSync } from 'node:fs'
import { join } from 'node:path'
import { openCdp, adb } from './lib/adb-cdp.mjs'

// ⚠️ 2026-10-07 修：原来写死 'C:/workspace/openpocket/logs/pg/dist2/pgsql/bin/psql.exe'，
// 表面是「一个本机有效的路径」，实际是**一份对所有机器都失效的跨平台状态**。
// 实测：2026-10-07 全量扫账扫到第 11 条 flow 的时候，它打断整批 flow（flashcards-write）——
// 报的是 **ENOENT: psql.exe**，不是「数据错了」：没打开的行上，flow 测成另一条分支。
//
// 为什么门禁放过它：check-no-hardcoded-abs-paths 的扫描名单是
// **LIVE_SCRIPTS + package.json 里点名的脚本**，而本文件是手工
// `node scripts/flashcards-test-fixture.mjs` 唤起来的，两者都不含它
// ⇒ **问题永远逃过门禁**。
// 调数据到 2026-10-07：scripts/ 下 407 个 .mjs，本门禁只扫了 40 个（9%），
// 名单外还有 173 个含硬编码 Windows 路径的文件。
//
// 改法搭现成「跑平台上水合」，所以照抄同一条项目现成做法（同目录 stt-error-fixture.mjs）。
const PSQL = (process.env.POCKET_PSQL
  || [
      '/opt/homebrew/opt/libpq/bin/psql',
      '/usr/local/opt/libpq/bin/psql',
      '/usr/bin/psql',
      '/opt/homebrew/bin/psql',
      join(process.env.LOCALAPPDATA || '', 'Programs/PostgreSQL/*/bin/psql.exe'),
    ].find((p) => p && !p.includes('*') && existsSync(p))
  || 'psql')
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
// ⚠️ 2026-10-07 修第二处：原先固定用 `-h 127.0.0.1 -U postgres`，**不带密码**。
// 本机 PG 开了 scram，psql 直接报 `fe_sendauth: no password supplied` ⇒ 又是 ENOENT 之外的另一种死法。
//
// 走 DSN 的坑：**不能写成 `DSN=x psql ...`**（那是给 psql 自己设环境变量，psql 根本不读它），
// 也不会走 `-d`；它会把 DSN 当**位置参数**（第一个非选项参数）——
//   POCKET_POSTGRES_DSN 必须放在 argv 的**位置参数位**，不是 -d 后面。
// ⚠️ DSN 里的 host 是 **host.docker.internal** —— 那是**容器内部**用的主机名。
// 从宿主 macOS 上跑时它根本解析不了：`could not translate host name "host.docker.internal"`。
// 而 PG 那边 Docker 已把 5432 映射到宿主 127.0.0.1（实测 lsof 有 com.docke LISTEN）。
// ⇒ 从宿主跑就把主机名换成 127.0.0.1，**凭据/库名原样保留**（口令在 DSN 里，不重写）。
function hostRunnableDsn(dsn) {
  if (!dsn) return ''
  try {
    const u = new URL(dsn)
    if (u.hostname === 'host.docker.internal') u.hostname = '127.0.0.1'
    return u.toString()
  } catch {
    return dsn   // 不是 URL 形态就原样用，交给 psql 自己报错
  }
}

const DSN = hostRunnableDsn(process.env.POCKET_POSTGRES_DSN || '')
const connArgs = DSN
  ? [DSN]
  : ['-h', '127.0.0.1', '-p', '5432', '-U', 'postgres', '-d', 'postgres']

const q = (sql) => {
  const out = execFileSync(PSQL, [...connArgs, '-t', '-A', '-c', sql], {
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

// adb 小工具：清缓存要在「App 活着」时做，自证要「重启后再读」，
// 两头都要驱动 App，所以 sh() 必须在 CDP 块之前就绪。
//
// ⚠️ 2026-10-07 修第三处：原来这里是
//   `const adbBin = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe'`
// ——与本文件上方 PSQL 那条**完全同型**的缺陷，上一轮只修了 psql、没修 adb。
// 后果实测：在 macOS 上首个 sh() 就是 ENOENT（`ENOENT spawnSync C:/Users/86133/...adb.exe`），
// 而本文件头注描述的失败形态是「打断整批 flow、报的不是数据错」——
// runner 会**跳过** flashcards-write，现象是「这条没跑」而不是「夹具坏了」。
//
// 修法不是再抄第三份候选表：本文件**已经 import 了 ./lib/adb-cdp.mjs**，
// 而那份 lib（:70-83）本来就是全仓 adb/serial 的单一来源
// （POCKET_ADB / POCKET_ADB_BIN / POCKET_SERIAL + 平台守卫候选 + PATH 兜底），
// 跑绿的全部 flow 都走它 ⇒ 直接复用，别在夹具里本地重写。
const sh = (cmd) => adb(['shell', cmd], 30000)
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
const launchApp = async () => {
  sh(`monkey -p ${PKG} -c android.intent.category.LAUNCHER 1`)
  // devtools socket 是 App 起来之后才注册的，等久一点，别在半路去连。
  for (let i = 0; i < 20; i++) {
    await sleep(700)
    try { const c = await openCdp({ pkg: PKG }); await c.close(); return true } catch { /* 还没起来 */ }
  }
  return false
}

// ⚠️ 2026-10-04 定死的关键一步：**清之前先把 App 导航到不挂闪卡 store 的路由。**
//
// 现象（01:13 那轮真机 flow）：本夹具报告「localStorage 清理成功」、PG 也清到 0|0|0|0，
// 但 App 重启后 `flashcards:v1` 里**又出现了**上一轮的「回归卡组」，
// flow 于是挂在第一条断言「暂无卡组不可见」—— 看起来像产品坏了，其实是前置没生效。
//
// 机制（双向对照实测，不是推测）：
//   停在 #/flashcards（store 已挂载、内存持有卡组）时 removeItem：
//     removeItem 后立刻读 = 空 ✅，但 force-stop + 重启后**卡组又回来了** ❌
//   先导航到 #/ai（store 未挂载、无内存可写）时 removeItem：
//     removeItem 后 = 空 ✅，force-stop + 重启后**仍然为空** ✅
//
// 对照脚本：scripts/diag-flashcard-clear-nav-away.mjs（正向）
//           scripts/diag-flashcard-clear-negctl.mjs （负控，store 挂载时确实写回）
//
// ⇒ 旧做法「removeItem 后立刻 force-stop」只是把窗口压小，窗口仍在。
//    真正管用的是**让 store 根本没挂载**，内存里没有可写回的东西。
// 之所以会写回：stores/flashcards.ts 没有 watcher / pagehide / 防抖，
// 但 syncFromServer() 结束时会 persistCache()，而闪卡页会触发它。
//
// ⚠️ 路由选择：必须是**已登录也能安全停留**的路由，且不能挂闪卡 store。
//    用 #/ai（首页）：未登录时会被弹回 #/login，那不影响——
//    我们只关心「离开 #/flashcards」，导航是否真的落地由下面的回读校验。

// CDP 通道走共享 helper（2026-10-02）：端口由 adb 分配（tcp:0），不再硬绑 9420。
// 硬编码端口是**同机所有会话共享**的状态——本机同时有别的会话在驱同一台设备，
// 撞上时 adb 抛 10048，而那句报错指向装置，看不出真问题是「上次没清干净」。
let cdp = null
try { cdp = await openCdp({ pkg: PKG }) }
catch (e) {
  const msg = String(e?.message || e)
  if (/APP_NOT_RUNNING|CDP_SOCKET_PID_MISMATCH|NO_DEVTOOLS_SOCKET/.test(msg)) {
    console.log(`  App 没在跑/通道未就绪，先拉起来再清（否则清不了磁盘上的 localStorage）`)
    if (await launchApp()) cdp = await openCdp({ pkg: PKG })
  }
  if (!cdp) {
    console.log(`  ❌ 无法连接 App 的 CDP：${msg.split('\n')[0]}`)
    console.log('   清不掉 localStorage 就等于没清场，这轮不能跑。')
    process.exit(1)
  }
}

let cacheErr = ''
try {
  // ① 先离开闪卡路由（关键，见上面的大段注释）
  const navHash = await cdp.ev(`(() => { location.hash = '#/ai'; return 1 })()`)
  void navHash
  await sleep(2000)
  const landed = await cdp.ev('location.hash')
  // 只要求「**离开了闪卡页**」。未登录时 #/ai 会被弹回 #/login，那也满足要求，
  // 没必要在这里把登录态也管起来（那是 preflight 的事）。
  if (/#\/flashcards/.test(String(landed))) {
    throw new Error(`导航未生效，仍停在 ${JSON.stringify(landed)}（闪卡 store 还挂着，清了也会被写回）`)
  }
  console.log(`  已导航到 ${landed}（已离开闪卡页）`)

  // ② 再清缓存
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
  // ⚠️ 关键：DOM Storage 是**异步**提交到 leveldb 的。removeItem 之后必须给提交留时间，
  //    否则紧接着的 force-stop 会把这次写直接丢掉（见文件末尾的实测说明）。
  await sleep(2000)
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

// ⚠️ 2026-10-04 第二次修：清完 localStorage 立刻 force-stop（保留），
//    但**真正的修法是上面那一步「先离开闪卡路由」**。
//
// 走过的四条弯路（都留档，避免再走）：
//   ① 先 force-stop 再 CDP 清 → App 不在运行，devtools socket 连不上，清不掉。
//   ② 先 CDP removeItem 再 force-stop → **两步之间 App 还活着**，store 已挂载、
//      内存里仍持有卡组，会在这个窗口里把缓存写回。01:13 那轮就是这么漏的。
//   ③ 删掉整个 app_webview/Default/Local Storage 目录 → 竞态是没了，
//      但把**主密码设置也一起清掉**了（leveldb 按 key 没法精确删）。
//      后果实测：下一次登录后 App 弹「创建主密码」对话框盖住路由，
//      maestro-run.mjs 等 hash 离开 #/login 超时 → **误报登录失败**
//      （后端 /api/auth/login 实测 200、token 291 字符都在）。
//      为了跑一条闪卡 flow 毁掉整个 App 登录态，不划算。
//   ④ 靠 `cdp` 连「随便哪个活着的 socket」→ 那是另一个包（…sttdev）的 WebView，
//      清的是它的存储。见 scripts/lib/adb-cdp.mjs 2026-10-04 的 pid 严格匹配。
//
// ⇒ 现在的顺序：**导航离开闪卡路由 → removeItem → force-stop → 删 PG → 重启回读自证**。
try {
  sh(`am force-stop ${PKG}`)
  console.log(`  已 force-stop ${PKG}`)
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

// ---------------------------------------------------------------------------
// 自证 + 重试：清场必须在**App 重启之后**成立才算数，而且不成立就**重来**。
//
// 为什么必须多这一步：上面所有「成功」的证据都来自 App **还在跑**的时候——
// removeItem 返回 true、PG 查到 0。而 01:13 那轮恰恰是：这些全都「成功」，
// App 一重启卡组就回来了，flow 挂在「暂无卡组不可见」，看起来像产品坏了。
// ⇒ 判据要钉在 flow 真正会看到的那个状态上（重启后的磁盘内容），
//    而不是钉在我自己刚刚做过的那个动作上。
//
// 为什么是「重试」而不是「小心一点」（2026-10-04 实测定死）：
//   removeItem 之后立刻 force-stop，**写不一定落盘**。
//   实测（diag-flashcard-persist-window.mjs）：重启后第一次读（App 还没做任何事）
//   就已经是 decks=1 ⇒ 卡组在磁盘上，从来没被删掉过。
//   Android WebView 的 DOM Storage 是**异步**提交到 leveldb 的，
//   `am force-stop` 立刻杀进程会丢掉未提交的写。
//   之前那次「删成功了」是巧合：removeItem 之后多开了一次 CDP 连接去回读，
//   那一次往返刚好给了提交时间。
//   ⇒ 写操作在这里**本质上不可靠**，所以只能「做完重启验、不行就重来」，
//      不能靠「这次应该来得及」。
// ---------------------------------------------------------------------------
const readCachedDecks = async () => {
  const c = await openCdp({ pkg: PKG })
  try {
    const raw = await c.ev(`localStorage.getItem(${JSON.stringify(CACHE_KEYS[0])})`)
    return raw == null ? 0 : (JSON.parse(raw).deckConfigs || []).length
  } finally { await c.close() }
}

const clearCacheOnce = async () => {
  const c = await openCdp({ pkg: PKG })
  try {
    await c.ev(`location.hash = '#/ai'`)
    await sleep(1500)
    await c.ev(`(() => { ${JSON.stringify(CACHE_KEYS)}.forEach(k => localStorage.removeItem(k)); return 1 })()`)
    // 给 DOM Storage 的异步提交留时间。缺了它，下一次 force-stop 会把这次写丢掉。
    await sleep(2000)
  } finally { await c.close() }
}

const MAX_ATTEMPTS = 4
let decks = -1
for (let attempt = 1; attempt <= MAX_ATTEMPTS; attempt++) {
  if (!(await launchApp())) {
    console.log('❌ 自证失败：App 拉不起来，读不到重启后的真实状态。没自证就不许说「已清零」。')
    process.exit(1)
  }
  try {
    decks = await readCachedDecks()
  } catch (e) {
    console.log(`❌ 自证失败：读不了重启后的 localStorage：${String(e?.message || e).split('\n')[0]}`)
    process.exit(1)
  }
  console.log(`自证 第 ${attempt}/${MAX_ATTEMPTS} 次（重启后）：decks=${decks}`)
  if (decks === 0) break
  console.log('   卡组还在（写没落盘 / 被写回）→ 重来一次，这次清完多等 2s 再停')
  await clearCacheOnce()
  await sleep(2000)
  sh(`am force-stop ${PKG}`)
  await sleep(1200)
}

if (decks !== 0) {
  console.log(`❌ 重试 ${MAX_ATTEMPTS} 次后重启后仍有 ${decks} 张卡组 —— 清场没生效，这轮 flow 会测成 deck-toggle 分支。`)
  console.log('   不要去改 flow 的断言来「适配」这个状态，前置没生效就是没生效。')
  process.exit(1)
}

console.log(`✅ 已清零并自证（PG=0 且重启后 ${CACHE_KEYS[0]} 无卡组），可以跑 flashcards-write.yaml（零卡组分支）`)
