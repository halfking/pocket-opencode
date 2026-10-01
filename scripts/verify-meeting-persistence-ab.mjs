// verify-meeting-persistence-ab.mjs
//
// BUG-AW 黑盒 A/B 对照：**在两个真实进程上**证明「会议数据重启即丢」这个缺陷
// 真实存在、以及本次修复真的有效。
//
// 为什么必须做 A/B 而不是只跑新代码：
//   只跑新二进制，"重启后数据还在"是预期结果，但它**无法排除**"这套判据
//   压根测不出丢失"这种假绿。老二进制在**同一套判据**下必须红，且红的原因
//   必须是数据真的没了 —— 只有两边对上，结论才立得住。
//
// 两侧各自独立 PG schema，互不污染；老侧二进制来自 11b7da5 的干净 worktree。
//
// 用法：node scripts/verify-meeting-persistence-ab.mjs
import { execFileSync, spawn } from 'node:child_process'
import fs from 'node:fs'
import path from 'node:path'

const WT_NEW = 'C:/workspace/openpocket/wt3'
const WT_OLD = 'C:/workspace/openpocket-wt-baseline'
const LOGS = 'C:/workspace/openpocket/logs'
const DSN = 'postgresql://postgres@127.0.0.1:5432/postgres?sslmode=disable'
// 固定 secret：随机 secret 会在重启后作废 token，把"持久化"问题伪装成
// "登录态没了"，判据就废了（start-local-backend.ps1 里记过这个坑）。
const JWT = 'pocket-ab-jwt-secret-fixed-not-for-shared-env'

// dev 口令从 Go 源码取，不落明文
const goSrc = fs.readFileSync(`${WT_NEW}/backend/internal/server/server_assistant.go`, 'utf8')
const m = goSrc.match(/devPass\s*=\s*"([^"]+)"/)
if (!m) { console.error('未能从 Go 源码取 dev 口令'); process.exit(2) }
const PASS = m[1]

const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

function build(worktree, out) {
  if (fs.existsSync(out)) { console.log(`[build] 复用 ${path.basename(out)}`); return }
  console.log(`[build] ${worktree} -> ${out}`)
  execFileSync('go', ['build', '-o', out, './cmd/pocketd'], { cwd: `${worktree}/backend`, stdio: 'inherit' })
}

async function waitHealthz(base, timeoutMs = 40000) {
  const t0 = Date.now()
  while (Date.now() - t0 < timeoutMs) {
    try {
      const r = await fetch(`${base}/healthz`, { signal: AbortSignal.timeout(3000) })
      if (r.status === 200) return true
    } catch { /* 还没起来 */ }
    await sleep(400)
  }
  return false
}

function startProc(bin, port, schema, tag) {
  const stamp = new Date().toISOString().replace(/[:.]/g, '-')
  const outLog = `${LOGS}/ab-${tag}-${port}-${stamp}.out.log`
  const errLog = `${LOGS}/ab-${tag}-${port}-${stamp}.err.log`
  const env = {
    ...process.env,
    POCKET_POSTGRES_DSN: DSN,
    POCKET_PG_SCHEMA: schema,
    POCKET_HTTP_PORT: String(port),
    POCKET_DATA_DIR: `${LOGS}/ab-data-${tag}-${port}`,
    POCKET_DEV_AUTH: 'true',
    POCKET_AUTH_LEGACY_ONLY: 'true',
    POCKET_AUTH_USER: 'admin',
    POCKET_AUTH_PASS: PASS,
    POCKET_JWT_SECRET: JWT,
    POCKET_LLM_GATEWAY_ALLOW_PRIVATE: 'true',
  }
  fs.mkdirSync(env.POCKET_DATA_DIR, { recursive: true })
  const p = spawn(bin, [], { env, cwd: `${WT_NEW}/backend`, stdio: ['ignore', fs.openSync(outLog, 'a'), fs.openSync(errLog, 'a')], windowsHide: true })
  p._logs = { outLog, errLog }
  return p
}

function killProc(p) {
  return new Promise((resolve) => {
    if (!p || p.exitCode !== null) return resolve('already-exited')
    p.on('exit', () => resolve('exited'))
    p.kill()
    setTimeout(() => { try { p.kill('SIGKILL') } catch { /* 已经没了 */ } resolve('killed') }, 4000)
  })
}

async function login(base) {
  const r = await fetch(`${base}/api/auth/login`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ username: 'admin', password: PASS }),
    signal: AbortSignal.timeout(20000),
  })
  const t = await r.text()
  let j = null
  try { j = JSON.parse(t) } catch { /* 非 JSON */ }
  return { status: r.status, token: j?.token || j?.access_token || null, body: JSON.stringify(j).slice(0, 160) }
}

async function listMeetings(base, token) {
  const r = await fetch(`${base}/api/meetings`, {
    headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' },
    signal: AbortSignal.timeout(20000),
  })
  const t = await r.text()
  let j = null
  try { j = JSON.parse(t) } catch { /* 非 JSON */ }
  return { status: r.status, total: j?.total ?? null, count: Array.isArray(j?.meetings) ? j.meetings.length : null, raw: t.slice(0, 120) }
}

async function createMeeting(base, token, title) {
  const r = await fetch(`${base}/api/meetings`, {
    method: 'POST',
    headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' },
    body: JSON.stringify({ title }),
    signal: AbortSignal.timeout(20000),
  })
  const t = await r.text()
  let j = null
  try { j = JSON.parse(t) } catch { /* 非 JSON */ }
  return { status: r.status, id: j?.id || j?.meeting?.id || null, raw: t.slice(0, 120) }
}

// 跑一整侧：起进程 → 登录 → 建 3 条 → 列举 → 真杀 → 再起 → 再列举
async function runSide({ tag, bin, port, schema, wantAfterRestart, expectByIdOk }) {
  console.log(`\n${'═'.repeat(72)}\n【${tag}】binary=${path.basename(bin)} port=${port} schema=${schema}\n${'═'.repeat(72)}`)
  const base = `http://127.0.0.1:${port}`
  const out = { tag }

  // ── 第一次运行 ──
  let p = startProc(bin, port, schema, tag)
  if (!(await waitHealthz(base))) {
    console.log(`❌ ${tag}: /healthz 30s 未应答，stderr 尾：`)
    console.log(fs.readFileSync(p._logs.errLog, 'utf8').split('\n').slice(-15).join('\n'))
    await killProc(p)
    out.error = 'healthz-timeout'
    return out
  }
  console.log(`[1] 进程已就绪 pid=${p.pid}`)

  const lg = await login(base)
  if (lg.status !== 200 || !lg.token) {
    console.log(`❌ ${tag}: 登录失败 status=${lg.status} body=${lg.body}`)
    await killProc(p)
    out.error = 'login-failed'
    return out
  }
  console.log(`[2] 登录成功`)

  const created = []
  for (let i = 1; i <= 3; i++) {
    const c = await createMeeting(base, lg.token, `A/B 会议 ${i}`)
    created.push(c)
    console.log(`[3.${i}] 创建 status=${c.status} id=${c.id}`)
  }
  const before = await listMeetings(base, lg.token)
  console.log(`[4] 重启前 GET /api/meetings → status=${before.status} total=${before.total} count=${before.count}`)

  // ── 真的杀进程（模拟后端重启），再起一个新的 ──
  console.log(`[5] 杀掉进程 pid=${p.pid} …`)
  const how = await killProc(p)
  console.log(`[5] ${how}；再起一个同配置的新进程 …`)
  await sleep(1500)
  p = startProc(bin, port, schema, tag)
  if (!(await waitHealthz(base))) {
    console.log(`❌ ${tag}: 重启后 /healthz 未应答`)
    await killProc(p)
    out.error = 'healthz-timeout-restart'
    return out
  }
  console.log(`[5] 新进程就绪 pid=${p.pid}`)

  // JWT secret 固定 ⇒ token 应当仍然有效。若此处 401，说明是登录态问题
  // 而不是持久化问题，必须分开报，不能混成一条结论。
  const lg2 = await login(base)
  if (lg2.status !== 200 || !lg2.token) {
    console.log(`❌ ${tag}: 重启后登录失败 status=${lg2.status} body=${lg2.body}`)
    await killProc(p)
    out.error = 'login-failed-restart'
    return out
  }

  const after = await listMeetings(base, lg2.token)
  console.log(`[6] 重启后 GET /api/meetings → status=${after.status} total=${after.total} count=${after.count}`)

  // 逐条按 id 复查：总数对了还不够，内容也要在
  const byId = []
  for (const c of created) {
    if (!c.id) { byId.push({ id: null, ok: false, why: '创建时没拿到 id' }); continue }
    const r = await fetch(`${base}/api/meetings/${c.id}`, { headers: { Authorization: `Bearer ${lg2.token}` }, signal: AbortSignal.timeout(20000) })
    const t = await r.text()
    byId.push({ id: c.id, status: r.status, ok: r.status === 200, head: t.slice(0, 100).replace(/\s+/g, ' ') })
  }
  for (const b of byId) console.log(`[7] 按 id 复查 ${b.id} → ${b.status} ${b.ok ? '✅' : '❌'} ${b.head || ''}`)

  // PG 侧直读：证明"在库里"而不是"接口缓存还在"。
  // psql 用环境变量连，不把 DSN 当 dbname —— 本机这个 psql 构建会把
  // dbname 之后的 -t/-A/-c 全当成多余参数吞掉（实测 warning 三条）。
  let pgRows = null
  try {
    const psql = 'C:/workspace/openpocket/logs/pg/dist2/pgsql/bin/psql.exe'
    const q = `SELECT count(*) FROM information_schema.tables WHERE table_schema='${schema}' AND table_name='meetings';`
    const has = execFileSync(psql, ['-t', '-A', '-c', q], {
      encoding: 'utf8',
      env: { ...process.env, PGHOST: '127.0.0.1', PGPORT: '5432', PGUSER: 'postgres', PGDATABASE: 'postgres' },
    }).trim()
    if (has !== '1') {
      // 老版本压根没有这张表 —— 这本身就是"零持久化"的直接证据
      pgRows = `无 meetings 表（该版本会议只存内存）`
    } else {
      const n = execFileSync(psql, ['-t', '-A', '-c', `SELECT count(*) FROM ${schema}.meetings;`], {
        encoding: 'utf8',
        env: { ...process.env, PGHOST: '127.0.0.1', PGPORT: '5432', PGUSER: 'postgres', PGDATABASE: 'postgres' },
      }).trim()
      pgRows = n
    }
  } catch (e) {
    pgRows = `查询失败: ${String(e.message).slice(0, 80)}`
  }
  console.log(`[8] PG 直读 ${schema}.meetings 行数 = ${pgRows}`)

  await killProc(p)

  out.before = before
  out.after = after
  out.byId = byId
  out.pgRows = pgRows
  // 两侧期望不同：老侧按 id 复查 404 **是正确结果**，不能套新侧的期望。
  // （第一版这里两边用同一套判据，导致老侧明明复现了缺陷却被判"不自洽"。）
  const byIdOk = byId.every((b) => (b.status === 200) === expectByIdOk)
  out.pass = after.count === wantAfterRestart && byIdOk
  console.log(`\n[判定] ${tag}: 期望重启后 ${wantAfterRestart} 条，实际 ${after.count} 条；按 id 期望 ${expectByIdOk ? '200' : '404(缺陷表现)'}，实际 ${byId.map((b) => b.status).join(',')} → ${out.pass ? '✅ 与期望一致' : '❌ 与期望不一致'}`)
  return out
}

// ─────────────────────────────────────────────────────────────
const OLD_BIN = `${LOGS}/pocketd-old-ab.exe`
const NEW_BIN = `${LOGS}/pocketd-new-ab.exe`

// 可重复运行：先把两侧 schema 整个丢掉。
// 不清的话，上一轮残留的行会让"建 3 条"变成 6 条，期望值对不上，
// 判据就会因为脏数据而误判 —— 那是判据的假阴性，不是被测代码的问题。
function dropSchemas() {
  const psql = 'C:/workspace/openpocket/logs/pg/dist2/pgsql/bin/psql.exe'
  const env = { ...process.env, PGHOST: '127.0.0.1', PGPORT: '5432', PGUSER: 'postgres', PGDATABASE: 'postgres' }
  for (const s of ['mtg_ab_old', 'mtg_ab_new']) {
    try {
      execFileSync(psql, ['-t', '-A', '-c', `DROP SCHEMA IF EXISTS ${s} CASCADE;`], { encoding: 'utf8', env })
      console.log(`[clean] 已 DROP SCHEMA ${s}`)
    } catch (e) {
      console.error(`[clean] DROP ${s} 失败: ${String(e.message).slice(0, 100)}`)
      process.exit(2)
    }
  }
}

console.log('清理上一轮残留 …')
dropSchemas()
console.log('构建两侧二进制 …')
build(WT_OLD, OLD_BIN)
build(WT_NEW, NEW_BIN)

// 两侧期望刻意相反：老侧"丢了"才是对的，新侧"留住"才是对的。
// 同一套探针、同一套断言，只有期望值随被测版本变化 —— 这样"红"和"绿"
// 都由真实数据决定，不是由判据写死。
const oldRes = await runSide({ tag: '老版本 11b7da5（内存 store）', bin: OLD_BIN, port: 18201, schema: 'mtg_ab_old', wantAfterRestart: 0, expectByIdOk: false })
const newRes = await runSide({ tag: '新版本（PG store）', bin: NEW_BIN, port: 18202, schema: 'mtg_ab_new', wantAfterRestart: 3, expectByIdOk: true })

console.log(`\n${'═'.repeat(72)}\nA/B 汇总\n${'═'.repeat(72)}`)
console.log(`老版本：重启前 ${oldRes.before?.count} 条 → 重启后 ${oldRes.after?.count} 条  ${oldRes.pass ? '✅ 符合预期（数据确实会丢）' : '❌ 不符合预期'}`)
console.log(`新版本：重启前 ${newRes.before?.count} 条 → 重启后 ${newRes.after?.count} 条  ${newRes.pass ? '✅ 符合预期（数据保住了）' : '❌ 不符合预期'}`)
console.log(`PG 行数：old=${oldRes.pgRows}  new=${newRes.pgRows}`)

// 判据成立的条件：老侧必须红（丢），新侧必须绿（留住），两边同时成立才算证完
const ok = oldRes.pass && newRes.pass
console.log(`\n结论：${ok ? '✅ 判据自洽 —— 同一套判据在缺陷侧红、在修复侧绿，BUG-AW 根因与修复均被证伪/证实' : '❌ 判据不自洽，需要先修判据或修实现'}`)
process.exit(ok ? 0 : 1)
