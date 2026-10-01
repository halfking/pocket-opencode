// verify-live-meeting-pg.mjs — 确认**当前正在跑**的 18099 后端确实接了 PG 版会议存储。
//
// 为什么不能只看 healthz：healthz 只能证明进程活着，证明不了
//   (a) 跑的是含 BUG-AW 修复的新二进制；
//   (b) meeting.NewStore 真的被 meeting.PGStore 覆盖了；
//   (c) 写入真的落到了 PG 而不是内存。
// 内存版与 PG 版在**进程不重启时**表现完全一样，只有直读数据库才分得开。
import { execFileSync } from 'node:child_process'
import fs from 'node:fs'

const BASE = process.env.POCKET_API_BASE || 'http://127.0.0.1:18099'
const SCHEMA = process.env.POCKET_PG_SCHEMA || 'opencode_pocket'
const goSrc = fs.readFileSync('C:/workspace/openpocket/wt3/backend/internal/server/server_assistant.go', 'utf8')
const PASS = goSrc.match(/devPass\s*=\s*"([^"]+)"/)?.[1]
if (!PASS) { console.error('取不到 dev 口令'); process.exit(2) }

const psql = (sql) => execFileSync('C:/workspace/openpocket/logs/pg/dist2/pgsql/bin/psql.exe', ['-t', '-A', '-c', sql], {
  encoding: 'utf8',
  env: { ...process.env, PGHOST: '127.0.0.1', PGPORT: '5432', PGUSER: 'postgres', PGDATABASE: 'postgres' },
}).trim()

const stamp = `LIVE-PROBE-${Date.now()}`

async function j(pathname, token, method = 'GET', body) {
  const r = await fetch(`${BASE}${pathname}`, {
    method,
    headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' },
    body: body ? JSON.stringify(body) : undefined,
    signal: AbortSignal.timeout(20000),
  })
  const t = await r.text()
  let parsed = null
  try { parsed = JSON.parse(t) } catch { /* 非 JSON */ }
  return { status: r.status, json: parsed, raw: t.slice(0, 160) }
}

const lg = await j('/api/auth/login', 'x', 'POST', { username: 'admin', password: PASS })
if (lg.status !== 200 || !lg.json?.token) {
  console.error(`❌ 登录失败 status=${lg.status} ${lg.raw}`)
  process.exit(1)
}
const token = lg.json.token
console.log(`登录 ok (${lg.json.auth_method || 'n/a'})`)

// (0) 表在不在
const hasTable = psql(`SELECT count(*) FROM information_schema.tables WHERE table_schema='${SCHEMA}' AND table_name='meetings';`)
console.log(`\n[1] ${SCHEMA}.meetings 表存在 = ${hasTable === '1' ? '✅' : '❌（这说明跑的是旧二进制，内存版不会建表）'}`)
if (hasTable !== '1') process.exit(1)

// (1) 写
const before = Number(psql(`SELECT count(*) FROM ${SCHEMA}.meetings;`) || 0)
const c = await j('/api/meetings', token, 'POST', { title: stamp })
console.log(`[2] POST /api/meetings status=${c.status} id=${c.json?.id || '-'}`)
if (c.status !== 201) { console.error(c.raw); process.exit(1) }
const id = c.json.id

// (2) 进程不重启时接口读得到 —— 这一条内存版也会过，不作为判据
const list = await j('/api/meetings', token)
const inList = Array.isArray(list.json?.meetings) && list.json.meetings.some((m) => m.id === id)
console.log(`[3] GET /api/meetings 能读到 = ${inList ? '✅' : '❌'}（内存版也会过，仅作 sanity）`)

// (3) 真正的判据：直读 PG
const after = Number(psql(`SELECT count(*) FROM ${SCHEMA}.meetings WHERE id='${id}';`) || 0)
console.log(`[4] PG 直读该 id 行数 = ${after} ${after === 1 ? '✅ 确实落库了' : '❌ 接口说创建成功，库里没有'}`)

// (4) 清理，别把探针数据留在 dev 库
await j(`/api/meetings/${id}`, token, 'DELETE')
const left = Number(psql(`SELECT count(*) FROM ${SCHEMA}.meetings WHERE id='${id}';`) || 0)
console.log(`[5] 清理后残留 = ${left} ${left === 0 ? '✅' : '❌'}`)

const pass = hasTable === '1' && after === 1 && left === 0 && inList
console.log(`\n结论：${pass ? '✅ 当前 18099 后端跑的是含 BUG-AW 修复的版本，会议写入已落 PG' : '❌ 判定不通过'}`)
process.exit(pass ? 0 : 1)
