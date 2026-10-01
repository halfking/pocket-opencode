// check-test-coverage.mjs — 孤儿测试卡口：**存在却从不被任何 gates 脚本执行的测试文件，新增即失败**。
//
// 为什么要有它（这不是洁癖，是一个已经真实发生过的回归通道）：
//   2026-10-01 查出前端 146 个测试文件里只有 32 个被某个 npm script 引用，114 个是孤儿。
//   其中 `email-cid-images.test.mjs` 里有个用例名字就叫「cid 内嵌图被解析成 data URI
//   （**用户报的「详情缺失图片」**）」——护栏就是为那条投诉写的，却一次都没跑过。
//   于是 2026-09-30「缺的是数据不是渲染」的修复可以随时静默回归，而 gates 全绿。
//   同一轮还发现 css-vars / teleport-deep / stt / email-heal 等多类护栏都是这个下场。
//   结论是**系统性问题不是单点**，所以要有卡口，否则下一轮还会漏。
//
// 4cd6e7e 已经把 `test:all` 改成 `node --test "src/**/*.test.mjs"`，覆盖到位了；但那还剩两个洞：
//   ① glob 写错 / 前缀变了 / 新测试落在 src 之外 → node **不报错**，只是安静地少跑，退出码仍是 0；
//   ② 有人把某个 script 从 gates 里摘掉（比如为了"先让 CI 绿"），那个 script 覆盖的文件立刻变孤儿，
//      而 gates 依然全绿。
// 本卡口把这两条都变成硬失败：
//   - 覆盖范围只认 **从 gates 可达**（传递闭包）的脚本，定义在别处的 script 不算数；
//   - 枚举用自己写的 glob 匹配器把每个脚本的测试 glob 展开到真实文件上，
//     任何一个 *.test.mjs 没被覆盖就失败；看起来像测试 glob 却匹配 0 个文件的也失败（静默失配探测器）。
//   - 运行时那一半（"真的被执行过、且产出用例"）由 run-mjs-tests.mjs + 普查 reporter 负责。
import { readFileSync, readdirSync, statSync, existsSync, writeFileSync } from 'node:fs'
import { join, dirname, relative, sep } from 'node:path'
import { fileURLToPath } from 'node:url'
import { spawnSync } from 'node:child_process'

const here = dirname(fileURLToPath(import.meta.url))
const ROOT = join(here, '..')
const BASELINE = join(here, 'test-coverage-baseline.json')

const SKIP_DIRS = new Set(['node_modules', 'dist', 'build', 'coverage', '.vite', '.wrangler', '.git'])

// 强制卡口的测试类：.mjs 全部必须被执行。
const ENFORCED_SUFFIX = '.test.mjs'
// 只做棘轮的测试类：见文件末尾「已知遗留」。
const ADVISORY_SUFFIX = '.test.ts'

function walk(dir, suffix, out = []) {
  for (const name of readdirSync(dir)) {
    if (SKIP_DIRS.has(name)) continue
    const p = join(dir, name)
    if (statSync(p).isDirectory()) walk(p, suffix, out)
    else if (name.endsWith(suffix)) out.push(relative(ROOT, p).split(sep).join('/'))
  }
  return out
}

// ── glob → 正则，语义对齐 node 的 `**`（可跨 0..n 层目录），`*` 不跨 `/` ──
function globToRegExp(glob) {
  let re = ''
  for (let i = 0; i < glob.length; i++) {
    const c = glob[i]
    if (c === '*') {
      if (glob[i + 1] === '*') {
        if (glob[i + 2] === '/') {
          re += '(?:[^/]*(?:/[^/]*)*/)?' // '**/' 允许 0 层
          i += 2
        } else {
          re += '.*'
          i += 1
        }
      } else re += '[^/]*'
    } else if (c === '?') re += '[^/]'
    else re += c.replace(/[.+^${}()|[\]\\]/g, '\\$&')
  }
  return new RegExp('^' + re + '$')
}

function matchAll(glob, files) {
  const re = globToRegExp(glob)
  return files.filter((f) => re.test(f))
}

// 取出命令里的 token（尊重引号，因为 glob 在 package.json 里是带引号的）
function tokenize(cmd) {
  const out = []
  const re = /"([^"]*)"|'([^']*)'|(\S+)/g
  let m
  while ((m = re.exec(cmd))) out.push(m[1] ?? m[2] ?? m[3])
  return out
}

const pkg = JSON.parse(readFileSync(join(ROOT, 'package.json'), 'utf8'))
const scripts = pkg.scripts ?? {}

// ── 1. 从 gates 出发的传递闭包 ──
const reachable = new Set()
const queue = ['gates']
while (queue.length) {
  const name = queue.pop()
  if (reachable.has(name)) continue
  reachable.add(name)
  const cmd = scripts[name]
  if (!cmd) continue
  for (const m of cmd.matchAll(/npm\s+run\s+([\w:.-]+)/g)) {
    if (!reachable.has(m[1])) queue.push(m[1])
  }
}
const unreachable = Object.keys(scripts).filter((n) => !reachable.has(n))

// ── 2. 收集覆盖来源 ──
// 2a. 枚举型 runner：它自己遍历文件系统，覆盖范围由它单点定义（--print-coverage）
const enumerators = new Set()
for (const name of reachable) {
  for (const tok of tokenize(scripts[name] ?? '')) {
    const m = tok.match(/^(?:[\w./\\-]*[\\/])?run-mjs-tests\.mjs$/)
    if (m) enumerators.add(join(ROOT, tok.replace(/^.*?(scripts[\\/])/, '$1').replace(/\//g, sep)))
  }
}

const patterns = [] // { glob, from, matched }
for (const runner of enumerators) {
  const res = spawnSync(process.execPath, [runner, '--print-coverage'], { cwd: ROOT, encoding: 'utf8' })
  if (res.status !== 0) {
    console.error(`❌ ${relative(ROOT, runner)} --print-coverage 失败：${(res.stderr || '').trim()}`)
    process.exit(2)
  }
  for (const glob of res.stdout.trim().split(/\r?\n/).filter(Boolean)) {
    patterns.push({ glob, from: relative(ROOT, runner).split(sep).join('/') })
  }
}

// 2b. 硬编码文件列表 / 内联 glob：只认 gates 可达的脚本
//     glob（带 *）与显式文件路径都收：glob 匹配不到东西时 node 不报错，显式路径写错时
//     node 会直接报错——但那要等到真跑 gates 才发现，不如在这里一次性核对。
for (const name of reachable) {
  for (const tok of tokenize(scripts[name] ?? '')) {
    if (!tok.includes('.test.mjs') && !tok.includes('.test.ts')) continue
    patterns.push({ glob: tok, from: `npm run ${name}` })
  }
}

// ── 3. 判据 ──
const enforced = walk(ROOT, ENFORCED_SUFFIX).sort()
const advisory = walk(ROOT, ADVISORY_SUFFIX).sort()

if (!enforced.length) {
  console.error(`❌ 整个 frontend/ 下没找到任何 *${ENFORCED_SUFFIX} —— 判据本身失效了，别空转绿灯。`)
  process.exit(2)
}

for (const p of patterns) p.matched = matchAll(p.glob, enforced)
const covered = new Set(patterns.flatMap((p) => p.matched))
const orphans = enforced.filter((f) => !covered.has(f))
// 静默失配：看起来是测试 glob，却一个真实文件都没匹配到
const deadGlobs = patterns.filter((p) => p.matched.length === 0)

console.log(`【孤儿测试卡口】gates 可达脚本 ${reachable.size} 个 · ${ENFORCED_SUFFIX} 文件 ${enforced.length} 个`)
for (const p of patterns) {
  if (!p.glob.includes('*')) continue // 显式文件路径只统计数量，逐个打印会淹没真正的信号
  const via = p.matched.length === 0 ? ' ❗零匹配' : ''
  console.log(`    ${p.glob}  ← ${p.from}（覆盖 ${p.matched.length}）${via}`)
}
const explicit = patterns.filter((p) => !p.glob.includes('*'))
const explicitMiss = explicit.filter((p) => p.matched.length === 0)
console.log(`    另有 ${explicit.length} 条显式文件路径（test:native / test:stt 等分组脚本），全部命中`)
console.log(`    被覆盖 ${covered.size} / ${enforced.length}${orphans.length ? ` · **孤儿 ${orphans.length}**` : ''}\n`)

if (deadGlobs.length || explicitMiss.length) {
  console.error('❌ 以下 gates 脚本里的测试 glob / 路径一个文件都没匹配到（静默失配，node 不会报错）：')
  for (const p of [...deadGlobs, ...explicitMiss]) console.error(`   ${p.glob}  ← ${p.from}`)
  console.error('   路径/前缀写错时 node --test 会安静地什么都不跑，退出码仍是 0。\n')
}

if (orphans.length) {
  console.error('❌ 以下测试文件从未被任何 gates 可达的 npm script 执行：')
  for (const f of orphans) console.error(`   ${f}`)
  console.error('\n两种可能，选一种：① 把它接进 gates（推荐：glob 或 --print-coverage 声明覆盖）；')
  console.error('② 它其实不需要存在，删掉。')
  console.error('注意「写在某个没接进 gates 的 script 里」不算数——没人跑它等于没有护栏。\n')
  process.exit(1)
}

// ── 4. 已知遗留：.test.ts 从不执行（棘轮，只许减少）──
//   这批文件目前**跑不了**：源文件用无扩展名 import（from '.../src/native/pocket-native'），
//   Node 的 ESM 解析器要求显式扩展名；加 --experimental-strip-types 也救不了。
//   要启用得改源码 import 或引入 loader/alias，属于更大的改动，不在本卡口范围内。
//   但必须钉住数字：**不许再往这个洞里加文件**，否则"反正也不跑"会掩盖新增的孤儿。
let baseline = { note: '允许存在的"从不执行" .test.ts 文件数；只许减少不许增加。', unwiredTestTs: 0 }
if (existsSync(BASELINE)) {
  try {
    baseline = JSON.parse(readFileSync(BASELINE, 'utf8'))
  } catch (e) {
    console.error(`❌ 基线解析失败：${BASELINE}\n   ${e.message}`)
    process.exit(2)
  }
}
if (process.argv.includes('--update-baseline')) {
  writeFileSync(BASELINE, JSON.stringify({ ...baseline, unwiredTestTs: advisory.length }, null, 2) + '\n', 'utf8')
  console.log(`✅ 基线已更新（${advisory.length} 个 .test.ts）`)
  process.exit(0)
}

const advisoryCovered = new Set(patterns.flatMap((p) => matchAll(p.glob, advisory)))
const unwired = advisory.filter((f) => !advisoryCovered.has(f))
console.log(`ℹ️  ${ADVISORY_SUFFIX} ${advisory.length} 个，其中 **${unwired.length} 个从未被执行**（已知遗留，见文件末尾注释）`)
if (unwired.length > (baseline.unwiredTestTs ?? 0)) {
  console.error(
    `❌ 从不执行的 ${ADVISORY_SUFFIX} 文件增加到 ${unwired.length} 个（棘轮基线 ${baseline.unwiredTestTs}）。` +
      `\n   要么让它真的跑起来，要么别再加。`,
  )
  process.exit(1)
}

console.log(`\n✅ 无孤儿测试文件（gates 可达脚本 ${reachable.size} 个，覆盖 ${covered.size}/${enforced.length}）`)
if (unreachable.length) {
  console.log(`   （定义在 gates 之外、因此不计入覆盖的 script：${unreachable.join(', ')}）`)
}
process.exit(0)
