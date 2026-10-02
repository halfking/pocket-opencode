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

const PSQL = 'C:/workspace/openpocket/logs/pg/dist2/pgsql/bin/psql.exe'
const DRY = process.argv.includes('--dry')

const q = (sql) => {
  const out = execFileSync(PSQL, ['-h', '127.0.0.1', '-p', '5432', '-U', 'postgres', '-d', 'postgres', '-t', '-A', '-c', sql], {
    encoding: 'utf8', timeout: 60000, maxBuffer: 33554432,
  })
  return String(out).trim()
}

// 前缀匹配而不是全等：flow 里将来可能给标题加后缀做区分。
const MATCH = `title LIKE 'Maestro%'`

const before = q(`SELECT count(*) FROM opencode_pocket.tasks WHERE ${MATCH}`)
console.log(`before [tasks matching ${MATCH}] = ${before}`)

if (DRY) {
  console.log('--dry: no delete.')
  process.exit(0)
}

const deleted = q(`WITH d AS (DELETE FROM opencode_pocket.tasks WHERE ${MATCH} RETURNING 1) SELECT count(*) FROM d`)
const after = q(`SELECT count(*) FROM opencode_pocket.tasks WHERE ${MATCH}`)
console.log(`deleted = ${deleted}`)
console.log(`after  [tasks matching ${MATCH}] = ${after}`)

// 自证：删完必须真的是 0。「以为删了」和「删了」不是一回事，
// 而这个残留恰好是第 5 步假通过的来源。
if (after !== '0') {
  console.error(`FAIL: 删完还剩 ${after} 条，flow 的第 5 步会假通过。不要跑 flow。`)
  process.exit(1)
}
console.log('OK: 已清零，可以跑 tasks-crud.yaml')
