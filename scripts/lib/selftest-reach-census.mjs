#!/usr/bin/env node
// ─────────────────────────────────────────────────────────────────────────────
// 自检可达性普查：「--selftest 分支是不是压根走不到」
//
// 为什么要有这个工具（docs/design §103.2 / §104）
//   probe-email-sync-honesty 的 requireDevPass() 在**模块顶层、且在 --selftest 块之前**，
//   缺口令即 process.exit(2) ⇒ 没人设 POCKET_AUTH_PASS / POCKET_DEV_PASS / POCKET_MASTER 时，
//   --selftest 分支**永远走不到**。实测删光 10 个夹具后跑 --selftest，拿到的
//   是口令门的说明文字 + EXIT=2，不是自检读数。
//
//   这个形状与 §102/§103 那一族「假绿」**不同**，要说清楚：
//     假绿 = 测了，然后说通过     —— 错在结论
//     不可达 = 压根没测          —— 错在覆盖面
//   后者更隐蔽：文件里明明有自检、注释也写着自检该做什么，看起来一切正常。
//
// 怎么判（**靠读数，不靠读代码猜**）
//   在受限环境（清空全部 POCKET_*）下跑每个 --selftest，看末行：
//     末行是自检的收尾句 ⇒ REACHABLE
//     输出里全是守卫的报错（要凭据/要配置/要目录）、末行不是收尾句 ⇒ UNREACHABLE
//
// 用法
//   node scripts/lib/selftest-reach-census.mjs            # 普查
//   node scripts/lib/selftest-reach-census.mjs --json     # 机器可读
//
// 退出码：0 = 全仓可达；1 = 有不可达的（那是要修的）；2 = 用法错。
// ─────────────────────────────────────────────────────────────────────────────
import { readdirSync, existsSync, readFileSync } from 'node:fs'
import { execFileSync } from 'node:child_process'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..', '..')
const SELF_DIRS = ['scripts', 'frontend/scripts']

// 输出**不是**自检读数的特征：守卫在拦人
const GUARD = /DEV_PASS_MISSING|MISSING|not found|ENOENT|No such file|拒绝|未设置|缺少|需要|POCKET_|请先|用法|Usage/i
// 自检收尾句的特征
const SELFTEST_END = /实跑|自检|判据自测|selftest|verdict|\/\d+\s*通过|例，/i

function listScripts() {
  const out = []
  for (const d of SELF_DIRS) {
    const dir = path.join(ROOT, d)
    if (!existsSync(dir)) continue
    for (const f of readdirSync(dir)) {
      if (!f.endsWith('.mjs')) continue
      out.push(path.relative(ROOT, path.join(dir, f)))
    }
  }
  return out
}

// 这个判据直接读文件，不 exec `node -e` 拼正则 ——
// 第一版用 execFileSync 把拼好的正则塞给子进程，模板字面量里的反引号把代码拼坏了，
// 异常又被 `catch { return false }` 静默吞掉 ⇒ **全仓 413 个脚本都被判成「没有自检」**，
// 普查器报「0 个可达」，读数完全正常。这类「量具坏了但读数看起来很干净」最费时间。
const SELFTEST_FLAG = /argv\s*\.\s*includes\s*\(\s*['"`]--selftest/

function hasSelftestFlag(rel) {
  try {
    return SELFTEST_FLAG.test(readFileSync(path.join(ROOT, rel), 'utf8'))
  } catch { return false }
}

function runClean(rel) {
  // 受限环境：清空全部 POCKET_*。**不打印任何环境变量的值**。
  const env = Object.fromEntries(Object.entries(process.env).filter(([k]) => !k.startsWith('POCKET_')))
  try {
    const stdout = execFileSync(process.execPath, [rel, '--selftest'],
      { cwd: ROOT, env, encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'], timeout: 120000 })
    return { rc: 0, out: stdout }
  } catch (e) {
    return { rc: typeof e.status === 'number' ? e.status : -1, out: `${e.stdout || ''}${e.stderr || ''}` }
  }
}

const only = process.argv.slice(2).filter((a) => !a.startsWith('--'))
const AS_JSON = process.argv.includes('--json')
if (process.argv.includes('--help')) {
  console.log('用法: node scripts/lib/selftest-reach-census.mjs [--json] [脚本路径…]')
  process.exit(2)
}

const all = listScripts().filter(hasSelftestFlag).sort()
const targets = only.length ? only : all

const rows = []
for (const rel of targets) {
  const { rc, out } = runClean(rel)
  const lines = out.split('\n').map((l) => l.trim()).filter(Boolean)
  const last = lines[lines.length - 1] || '(空输出)'
  const guarded = GUARD.test(out)
  // ★ 不死盯末行：有的门在自检收尾句**后面**还印一行操作说明
  //   （check-router-runtime-parity 就是 `自检: 3/3 通过` + `（真实 … 未被触碰…）`），
  //   死盯末行会把它判成 REVIEW —— 一个纯噪音的假警报。
  //   改成：末行往前找第一行像收尾句的。
  const looksSelf = lines.some((l) => SELFTEST_END.test(l))
  const verdict = (rc === 0 && looksSelf && !guarded) ? 'REACHABLE'
    : (guarded && !looksSelf) ? 'UNREACHABLE' : 'REVIEW'
  rows.push({ rel, rc, verdict, last: last.slice(0, 90) })
}

if (AS_JSON) {
  console.log(JSON.stringify({ total: rows.length, unreachable: rows.filter((r) => r.verdict === 'UNREACHABLE').length, rows }, null, 2))
} else {
  console.log(`== 自检可达性普查（受限环境：清空全部 POCKET_*）==\n`)
  for (const r of rows) {
    console.log(`  ${r.verdict.padEnd(12)} rc=${String(r.rc).padEnd(2)} ${r.rel}`)
    console.log(`  ${''.padEnd(12)}      末行: ${r.last}`)
  }
  const bad = rows.filter((r) => r.verdict === 'UNREACHABLE')
  const rev = rows.filter((r) => r.verdict === 'REVIEW')
  console.log(`\n可达 ${rows.length - bad.length - rev.length} / 不可达 ${bad.length} / 需人工看 ${rev.length}`)
  if (bad.length) {
    console.log('\n✗ 以下脚本的 --selftest 在受限环境下走不到：')
    for (const r of bad) console.log(`    ${r.rel}  末行: ${r.last}`)
    console.log('\n  修法：把「要凭据/要配置」的守卫**延后到 --selftest 分支之后**，')
    console.log('       判据自检通常是纯函数，不需要凭据。')
  }
  if (rev.length) {
    console.log('\n⚠ 需人工看（末行既不是守卫报错、也不像自检收尾句）：')
    for (const r of rev) console.log(`    ${r.rel}  末行: ${r.last}`)
  }
}
process.exit(rows.some((r) => r.verdict === 'UNREACHABLE') ? 1 : 0)