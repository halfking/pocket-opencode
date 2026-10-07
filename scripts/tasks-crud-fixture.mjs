// tasks-crud-fixture —— 删掉上一轮 tasks-crud.yaml 建的任务，让它每次都从
// 「没有 Maestro* 任务」的状态起步。
//
// 为什么必需（与 flashcards-test-fixture.mjs 同理）：
//   flow 第 5 步断言 `visible: "Maestro任务.*"`。而 Maestro 判断 visible 只看
//   节点在不在无障碍树里，**不看它是不是上一轮留下的**。于是上一轮崩在中间时
//   残留的同名卡片会让第 5 步假通过 —— 恰好在最需要它报警的时候不报。
//   这不是理论风险：本轮开工时开发库里就躺着一条 2026-10-02 早先留下的
//   `Maestro任务`。
//
// ⚠️ 全部 ASCII。带中文的 WHERE 条件经 PowerShell 传给 psql 会报
//    `invalid byte sequence for encoding "UTF8": 0xc8 0xce`（GBK 字节），
//    2026-10-02 踩过。标题 `Maestro任务` 的 ASCII 前缀是 `Maestro`，
//    用 `title LIKE 'Maestro%'` 即可，不需要在命令行传任何非 ASCII 字节。
//
// 用法：node scripts/tasks-crud-fixture.mjs [--dry]
import { execFileSync } from 'node:child_process'
import { existsSync } from 'node:fs'
import { join } from 'node:path'

// ⚠️ 2026-10-07 修两处，与 flashcards-test-fixture.mjs 对齐：
//
// ① 原来写死 'C:/workspace/openpocket/logs/pg/dist2/pgsql/bin/psql.exe'。
//    本机 PG 开了 scram，在 macOS 上这个路径**必然 ENOENT**。
//    ⇒ 手工跑本夹具在 macOS/Linux 上从来就没成功过，而 tasks-crud.yaml
//      第 5 步 `assertVisible: "Maestro任务.*"` 的全部防假通过价值
//      恰恰依赖这个夹具先删干净——护栏一直在，**从没人能把它跑起来**。
//
// ② 连库参数原来固定 `-U postgres` 且**不带口令** ⇒ `fe_sendauth: no password supplied`。
//    这是 stt-error-fixture.mjs 在 e791a971 修过的同一条。
//
// DSN 的坑（一并照抄同目录现成做法）：
//   - POCKET_POSTGRES_DSN 必须放**位置参数位**，不能写成 `DSN=x psql …` 或跟在 -d 后面；
//   - DSN 里的 host 是 `host.docker.internal`，那是**容器内**主机名，
//     从宿主 macOS 跑解析不了 ⇒ 换成 127.0.0.1，凭据/库名原样保留。
const PSQL = (process.env.POCKET_PSQL
  || [
      '/opt/homebrew/opt/libpq/bin/psql',
      '/usr/local/opt/libpq/bin/psql',
      '/usr/bin/psql',
      '/opt/homebrew/bin/psql',
      join(process.env.LOCALAPPDATA || '', 'Programs/PostgreSQL/*/bin/psql.exe'),
    ].find((p) => p && !p.includes('*') && existsSync(p))
  || 'psql')

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
const DRY = process.argv.includes('--dry')

// PG schema：跟随后端配置（backend/internal/config/config.go 的 POCKET_PG_SCHEMA，默认值相同）。
// 写死 opencode_pocket 会让本脚本只能对着共享库跑 —— 失败时 SEED 就留在别人的库里。
//
// ⚠️ 这两行**必须在模块顶层**，不能放进下面的 q()。
// 2026-10-03：24abc616 那次批量去写死改造把插入锚点选在了 `const q = (sql) => {`
// 的函数体首行，于是声明落进函数体、而 `${SCHEMA}` 的引用全在顶层 ——
// `const` 是块级作用域，函数外看不见它，脚本一启动就
// `ReferenceError: SCHEMA is not defined`（36 行）。
// 门禁：node scripts/check-pg-schema-scope.mjs
const SCHEMA = process.env.POCKET_PG_SCHEMA || 'opencode_pocket'
if (SCHEMA !== 'opencode_pocket') console.log(`PG schema = ${SCHEMA}（非共享库）`)

const q = (sql) => {
  const out = execFileSync(PSQL, [...connArgs, '-t', '-A', '-c', sql], {
    encoding: 'utf8', timeout: 60000, maxBuffer: 33554432,
  })
  return String(out).trim()
}

// 前缀匹配而不是全等：flow 里将来可能给标题加后缀做区分。
const MATCH = `title LIKE 'Maestro%'`

const before = q(`SELECT count(*) FROM ${SCHEMA}.tasks WHERE ${MATCH}`)
console.log(`before [tasks matching ${MATCH}] = ${before}`)

if (DRY) {
  console.log('--dry: no delete.')
  process.exit(0)
}

const deleted = q(`WITH d AS (DELETE FROM ${SCHEMA}.tasks WHERE ${MATCH} RETURNING 1) SELECT count(*) FROM d`)
const after = q(`SELECT count(*) FROM ${SCHEMA}.tasks WHERE ${MATCH}`)
console.log(`deleted = ${deleted}`)
console.log(`after  [tasks matching ${MATCH}] = ${after}`)

// 自证：删完必须真的是 0。「以为删了」和「删了」不是一回事，
// 而这个残留恰好是第 5 步假通过的来源。
if (after !== '0') {
  console.error(`FAIL: 删完还剩 ${after} 条，flow 的第 5 步会假通过。不要跑 flow。`)
  process.exit(1)
}
console.log('OK: 已清零，可以跑 tasks-crud.yaml')
