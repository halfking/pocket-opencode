// find-note-in-pg.mjs — 在整个 opencode_pocket schema 里搜一个字符串，回答「它到底落到哪张表」。
//
// 为什么需要：本轮 notes-crud 断言「列表里看得见刚建的笔记」全绿，
// 但 `opencode_pocket.notes` 里最新一条还是前天的 —— 说明它没落在那张表。
// 只查一张表就会得出「笔记没落库」的错误结论；也可能它落在另一张表里而我们没看见。
// 判据要能回答「在哪儿」，而不是只回答「在不在 notes 表」。
//
// 用法: node scripts/find-note-in-pg.mjs <要搜的字符串>
import { execFileSync } from 'node:child_process'

const needle = process.argv[2] || 'MaestroPKM'
const PSQL = 'C:/workspace/openpocket/logs/pg/dist2/pgsql/bin/psql.exe'
const DB = ['-h', '127.0.0.1', '-p', '5432', '-U', 'postgres', '-d', 'postgres', '-t', '-A', '-F', '|']

// PG schema：跟随后端配置（backend/internal/config/config.go 的 POCKET_PG_SCHEMA，默认值相同）。
// 写死 opencode_pocket 会让本脚本只能对着共享库跑 —— 失败时 SEED 就留在别人的库里。
//
// ⚠️ 必须在模块顶层：24abc616 曾把这行插进下面的 q() 函数体，导致 68 行的
// `${SCHEMA}` 引用在运行时 ReferenceError。门禁：node scripts/check-pg-schema-scope.mjs
const SCHEMA = process.env.POCKET_PG_SCHEMA || 'opencode_pocket'
if (SCHEMA !== 'opencode_pocket') console.log(`PG schema = ${SCHEMA}（非共享库）`)

const q = (sql) => {
  try {
    return execFileSync(PSQL, [...DB, '-c', sql], { encoding: 'utf8', timeout: 60000 })
  } catch (e) {
    return ''
  }
}

const esc = needle.replace(/'/g, "''")
const cols = q(
  `select table_name || '.' || column_name from information_schema.columns
   where table_schema='opencode_pocket' and data_type in ('text','character varying','jsonb','json')
   order by 1;`,
)
  .split(/\r?\n/)
  .map((s) => s.trim())
  .filter(Boolean)

// 哪些表有 workspace_id（用来决定要不要把它带进输出，不是假设所有表都有）
const wsTables = new Set(
  q(
    `select distinct table_name from information_schema.columns
     where table_schema='opencode_pocket' and column_name='workspace_id';`,
  )
    .split(/\r?\n/)
    .map((s) => s.trim())
    .filter(Boolean),
)

console.log(`在 opencode_pocket 的 ${cols.length} 个文本列里搜 "${needle}" …\n`)

// 判据自身的第一版有致命缺陷：它无条件 select workspace_id::text，
// 而 schema 里多数表**没有**这一列，于是查询报错、被 catch 吞成空串，
// 最后打出「全 schema 都没搜到」——那是**假的否定结论**，比报错危险得多。
// 现在改成：只取命中的那一列；若该表恰好有 workspace_id 才附带输出。
let hits = 0
let queried = 0
let errored = 0
for (const c of cols) {
  const dot = c.indexOf('.')
  const t = c.slice(0, dot)
  const col = c.slice(dot + 1)
  const hasWs = wsTables.has(t)
  const sel = hasWs ? `workspace_id::text || ' :: ' || left("${col}"::text, 90)` : `left("${col}"::text, 90)`
  let out
  try {
    out = execFileSync(
      PSQL,
      [...DB, '-v', 'ON_ERROR_STOP=1', '-c',
        `select ${sel} from ${SCHEMA}."${t}" where "${col}"::text like '%${esc}%' limit 3;`],
      { encoding: 'utf8', timeout: 60000 },
    ).trim()
    queried++
  } catch (e) {
    errored++
    console.error(`  !! ${t}.${col} 查询失败：${String(e.stderr || e.message).split('\n')[0]}`)
    continue
  }
  if (out) {
    hits++
    console.log(`✅ ${t}.${col}`)
    for (const line of out.split(/\r?\n/)) console.log(`     ${line}`)
  }
}
console.log(
  `\n实查 ${queried} 列，查询失败 ${errored} 列` +
    (errored ? '（**结论不可信，先修判据**）' : '') +
    `，命中 ${hits} 张表`,
)
process.exit(hits && !errored ? 0 : 1)
