// 一次性验证：证明 `-Schema` 隔离**真的**生效，不是纸面参数。
//
// 做法：往隔离后端（18101 / opencode_pocket_verify）写一条 finance 行，
// 然后**分别**查两个 schema，确认：
//   1) 它出现在 opencode_pocket_verify
//   2) 它**没有**出现在 opencode_pocket（并发会话的数据在这里）
// 最后自清理：只从隔离 schema 删。
//
// ⚠️ 绝不对 opencode_pocket 做任何写操作 —— 那是另一会话的库。
//
// 判据纪律（§4.91，两次自伤换来的）：
//   a) 基线必须在 POST **之前**取。第一版取在 POST 之后却标成「播种前基线」，
//      `afterVerify === baseVerify` 于是永远差 1 —— 判据和它的标签一起说谎。
//   b) 「共享库里数到 0」只有在**共享 schema 确实有数据**时才有说服力。
//      共享的 finance 表本身就是空的，所以阳性对照改用 shared.tasks 证明
//      psql 真看得见 opencode_pocket，而不是「那张表压根不存在所以是 0」。
//   c) SQL 报错必须响亮退出，不能被吞成「结果是 0」。
import { execFileSync } from 'node:child_process'

const PSQL = 'C:/workspace/openpocket/logs/pg/dist2/pgsql/bin/psql.exe'
let cleanupNeeded = null   // 已播种但还没删的 id
let cleaned = false
async function cleanup(reason) {
  if (!cleanupNeeded || cleaned) return null
  cleaned = true
  try {
    const r = await fetch(`${BASE}/api/finance/${cleanupNeeded}`, {
      method: 'DELETE', headers: { authorization: `Bearer ${token}` }, signal: AbortSignal.timeout(15000),
    })
    console.log(`  [清理 ${reason}] DELETE ${cleanupNeeded} -> ${r.status}`)
    return r.status
  } catch (e) {
    console.error(`  [清理失败 ${reason}] ${cleanupNeeded}：${e.message} —— 需要手工清`)
    process.exitCode = 2
    return null
  }
}
for (const sig of ['unhandledRejection', 'uncaughtException']) {
  process.on(sig, async (e) => {
    await cleanup(sig)
    console.error(`${sig}: ${(e && e.stack) || e}`)
    process.exit(1)
  })
}

function Q(sql) {
  try {
    return String(execFileSync(PSQL,
      ['-h', '127.0.0.1', '-p', '5432', '-U', 'postgres', '-d', 'postgres', '-t', '-A', '-c', sql],
      { encoding: 'utf8', timeout: 30000 })).trim()
  } catch (e) {
    // 判据自杀通道：把「查错了」伪装成「没泄漏」。
    // 抛出去而不是 process.exit —— exit 不会跑 finally，会把刚播的行留在库里。
    throw new Error(`SQL 失败：${sql}\n${(e.stderr || e.message || '').toString().trim()}`)
  }
}

const BASE = process.env.POCKET_VERIFY_BASE || 'http://127.0.0.1:18101'
const PASS = process.env.POCKET_AUTH_PASS
if (!PASS) { console.error('缺 POCKET_AUTH_PASS'); process.exit(2) }

// ⚠️ 真表名是 **`finance_transactions`**，不是 `finance`。
//    第一版写成 `finance` 报 "relation does not exist" —— 那不是隔离失败，是断言查错了表。
const TBL = 'finance_transactions'
const V = 'opencode_pocket_verify'
const S = 'opencode_pocket'

const login = await fetch(`${BASE}/api/auth/login`, {
  method: 'POST',
  headers: { 'content-type': 'application/json' },
  body: JSON.stringify({ username: 'admin', password: PASS }),
  signal: AbortSignal.timeout(15000),
})
const { token } = await login.json()
console.log(`登录 ${login.status}，token ${String(token || '').length} 字符`)
if (!token) { console.error('登录失败，拿不到 token'); process.exit(2) }

const residue0 = Q(`SELECT count(*) FROM ${V}.${TBL} WHERE note LIKE 'ISOLATE-%'`)
console.log(`  [遗留] 隔离 schema 里历史 ISOLATE- 行：${residue0}`)

// 显式 opt-in 才清遗留。必须在取基线**之前**做完，否则「回到基线」那条判据会失真。
// 只动 opencode_pocket_verify，绝不碰 opencode_pocket。
if (process.argv.includes('--purge-residue') && residue0 !== '0') {
  console.log(`  [purge] 删除 ${V} 里 ${residue0} 条历史 ISOLATE- 遗留（逐条走 API，不裸 SQL 写库）`)
  const rows = Q(`SELECT id FROM ${V}.${TBL} WHERE note LIKE 'ISOLATE-%'`)
  for (const rid of rows.split('\n').map((s) => s.trim()).filter(Boolean)) {
    const r = await fetch(`${BASE}/api/finance/${rid}`, {
      method: 'DELETE', headers: { authorization: `Bearer ${token}` }, signal: AbortSignal.timeout(15000),
    })
    console.log(`  [purge] DELETE ${rid} -> ${r.status}`)
  }
  console.log(`  [purge] 剩余 ${Q(`SELECT count(*) FROM ${V}.${TBL} WHERE note LIKE 'ISOLATE-%'`)} 条`)
}

// ---- 基线：全部在 POST 之前取 ----
const baseVerify = Q(`SELECT count(*) FROM ${V}.${TBL}`)
const baseSharedFinance = Q(`SELECT count(*) FROM ${S}.${TBL}`)
const baseSharedTasks = Q(`SELECT count(*) FROM ${S}.tasks`)
console.log(`\n  [基线·POST 之前] verify.${TBL}=${baseVerify}  shared.${TBL}=${baseSharedFinance}  shared.tasks=${baseSharedTasks}`)

const STAMP = `ISOLATE-${Date.now().toString().slice(-6)}`
const post = await fetch(`${BASE}/api/finance`, {
  method: 'POST',
  headers: { authorization: `Bearer ${token}`, 'content-type': 'application/json' },
  body: JSON.stringify({ type: 'expense', amount: 13.37, category: 'ISOLATE', note: STAMP, source: 'manual' }),
  signal: AbortSignal.timeout(15000),
})
const created = await post.json()
const id = created && created.id
console.log(`  播种 ${STAMP} -> ${post.status} id=${id || '(无)'}`)
if (!id) { console.error('播种失败，无法验证隔离'); process.exit(1) }
cleanupNeeded = id        // 从这一刻起，任何异常都必须由 cleanup() 兜住

const inVerify = Q(`SELECT count(*) FROM ${V}.${TBL} WHERE id='${id}'`)
const inShared = Q(`SELECT count(*) FROM ${S}.${TBL} WHERE id='${id}'`)
const inPublic = Q(`SELECT count(*) FROM public.${TBL} WHERE id='${id}'`)

// 负控注入：在播种**之后**故意让一条 SQL 失败，验证异常路径真的走了 cleanup()。
// 不注入时这段是死代码；注入时若退出码非 0 且「[清理 unhandledRejection]」出现，
// 才说明异常清理是活的。
if (process.env.POCKET_FAULT === 'sql') {
  console.log('  [负控] POCKET_FAULT=sql —— 故意执行一条会报错的 SQL')
  Q(`SELECT count(*) FROM ${V}.no_such_table_xyz`)
}

console.log(`\n  ${V} 里出现？ ${inVerify} （期望 1）${inVerify === '1' ? ' OK' : ' FAIL'}`)
console.log(`  ${S}（共享）里出现？ ${inShared} （期望 0）${inShared === '0' ? ' OK' : ' FAIL 隔离失败'}`)
console.log(`  public 里出现？       ${inPublic} （期望 0）${inPublic === '0' ? ' OK' : ' WARN'}`)

// 自清理：走同一个幂等 cleanup()，正常路径和异常钩子不会删两次
const delStatus = await cleanup('normal')
console.log(`\n  清理 DELETE -> ${delStatus}（期望 204）`)

const leftVerify = Q(`SELECT count(*) FROM ${V}.${TBL} WHERE id='${id}'`)
const leftShared = Q(`SELECT count(*) FROM ${S}.${TBL} WHERE id='${id}'`)
const leftByNote = Q(`SELECT count(*) FROM ${V}.${TBL} WHERE note='${STAMP}'`)
const afterVerify = Q(`SELECT count(*) FROM ${V}.${TBL}`)
const afterSharedFinance = Q(`SELECT count(*) FROM ${S}.${TBL}`)
const afterSharedTasks = Q(`SELECT count(*) FROM ${S}.tasks`)

console.log(`  删后 verify=${leftVerify}（期望 0） shared=${leftShared}（期望 0）`)
console.log(`  按 note 反查 ${V} 里还有没有 ${STAMP}：${leftByNote}（期望 0）`)
console.log(`  [删后] verify 总行 ${afterVerify}（期望回到基线 ${baseVerify}）`)
console.log(`  [删后] shared 总行 ${afterSharedFinance}（期望回到基线 ${baseSharedFinance}）`)

const checks = [
  ['隔离 schema 收到了这一行（同一 SQL 打 verify 返回 1 = 判据有区分力）', inVerify === '1'],
  ['共享 schema 没收到这一行', inShared === '0'],
  ['public schema 没收到这一行', inPublic === '0'],
  ['DELETE 返回 204', String(delStatus) === '204'],
  ['按 id 复查隔离 schema 已空', leftVerify === '0'],
  ['按 id 复查共享 schema 为空', leftShared === '0'],
  ['按 note 复查隔离 schema 已空', leftByNote === '0'],
  ['隔离 schema 总行数回到 POST 前的基线', afterVerify === baseVerify],
  ['共享 finance 表总行数未变（证明没写共享库）', afterSharedFinance === baseSharedFinance],
  ['阳性对照：共享 schema 确实有数据（shared.tasks > 0），所以上面那个 0 不是「表不存在」', Number(baseSharedTasks) > 0],
  ['阳性对照：共享 tasks 总行数也未变', afterSharedTasks === baseSharedTasks],
]
let ok = true
for (const [label, pass] of checks) {
  if (!pass) ok = false
  console.log(`  ${pass ? 'PASS' : 'FAIL'}  ${label}`)
}
if (baseSharedFinance === '0') {
  console.log('  NOTE  共享 finance 表本身是空的，所以「共享里 0」这一条的强度有限；')
  console.log('        它靠「同一 SQL 在 verify 返回 1」和「shared.tasks>0」两条支撑。')
}
console.log(`\n结论：${ok ? '隔离确实生效' : '隔离未生效或判据不成立'}`)
process.exitCode = ok ? 0 : 1
