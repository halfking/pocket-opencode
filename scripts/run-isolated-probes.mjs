// 在**隔离**后端（18101 / opencode_pocket_verify）上实跑那批写路径探针。
//
// 为什么能这么跑：这 6 个脚本全是纯 API，读 POCKET_API_HOST/POCKET_API_PORT，
// 不碰 adb、不碰 CDP、不直接查 PG ⇒ 把 base 指到隔离后端，写入就落在隔离 schema。
// （对比：verify-finance-writepath.mjs 走真机 UI，后端由 adb reverse 决定，
//   它的 PG 断言还把 opencode_pocket 写死了 —— 那种脚本隔离环境救不了。）
//
// 判据纪律：spawnSync 的非 0 退出与「我自己抛了」必须分开，
// 否则 runner 的 bug 会被当成被测对象的结论。
import { spawnSync } from 'node:child_process'
import { writeFileSync, mkdirSync } from 'node:fs'
import path from 'node:path'

const PORT = process.env.POCKET_API_PORT || '18101'
const BASE = `http://127.0.0.1:${PORT}`
const SCRIPTS = [
  'probe-vault-api.mjs',
  'probe-vault-sync-empty-blob.mjs',
  'probe-gateway-nodes-api.mjs',
  'probe-email-account-api.mjs',
  'probe-email-sync-honesty.mjs',
  'verify-bug-z.mjs',
]
const LOGDIR = 'backend/data-verify/probe-logs'
const TIMEOUT = Number(process.env.PROBE_TIMEOUT_MS || 120000)

const health = await fetch(`${BASE}/healthz`, { signal: AbortSignal.timeout(5000) }).catch((e) => ({ status: 'ERR:' + e.message }))
if (health.status !== 200) {
  console.error(`隔离后端 ${BASE} 不通（${health.status}）—— 先起它，别拿 ECONNREFUSED 当结论`)
  process.exit(2)
}
console.log(`隔离后端 ${BASE} healthz=200\n`)

const env = { ...process.env, POCKET_API_HOST: '127.0.0.1', POCKET_API_PORT: PORT }
if (!env.POCKET_AUTH_PASS) { console.error('缺 POCKET_AUTH_PASS'); process.exit(2) }

// ⚠️ 目录不存在会让 writeFileSync 抛 ENOENT，而那一行原本在 try 之外 ——
//    runner 自己崩掉，第一个脚本的结果也一起丢了。建目录，且写日志失败不许中断整轮。
mkdirSync(LOGDIR, { recursive: true })
function saveLog(name, out) {
  try { writeFileSync(path.join(LOGDIR, name), out, 'utf8') } catch (e) { console.error(`  [warn] 日志写不进去：${e.message}`) }
}

const rows = []
for (const s of SCRIPTS) {
  const file = path.resolve('scripts', s)
  process.stdout.write(`--- ${s} ... `)
  let r
  try {
    r = spawnSync(process.execPath, [file], { env, encoding: 'utf8', timeout: TIMEOUT, maxBuffer: 16 * 1024 * 1024 })
  } catch (e) {
    // runner 自己炸了 ≠ 被测脚本失败
    console.log('RUNNER-THREW')
    rows.push({ s, code: 'RUNNER-THREW', pass: '?', fail: '?', out: String(e.message) })
    continue
  }
  const out = `${r.stdout || ''}${r.stderr || ''}`
  saveLog(s.replace(/\.mjs$/, '.log'), out)

  if (r.error) {
    // 分类：超时 / 启动失败
    const kind = r.error.code === 'ETIMEDOUT' ? 'TIMEOUT' : `SPAWN-${r.error.code}`
    console.log(kind)
    rows.push({ s, code: kind, pass: '?', fail: '?', out })
    continue
  }
  // 只统计**行首**的 PASS/FAIL。响应体或文案里出现 "pass"/"fail" 子串不算 ——
  // 之前就栽在 Select-String 大小写不敏感上。
  const lines = out.split(/\r?\n/)
  const pass = lines.filter((l) => /^\s*PASS\b/.test(l)).length
  const fail = lines.filter((l) => /^\s*FAIL\b/.test(l)).length
  const code = r.status
  console.log(`exit=${code}  PASS=${pass} FAIL=${fail}`)
  rows.push({ s, code, pass, fail, out })
}

console.log('\n===== 汇总 =====')
let bad = 0
for (const r of rows) {
  const verdict = r.code === 0 ? 'exit0' : `exit${r.code}`
  if (r.code !== 0) bad++
  console.log(`  ${String(r.code).padEnd(12)} ${r.s}  PASS=${r.pass} FAIL=${r.fail}  ${verdict}`)
}
console.log(`\n${rows.length} 个脚本，${bad} 个非零退出。日志在 ${LOGDIR}/`)
process.exitCode = bad ? 1 : 0
