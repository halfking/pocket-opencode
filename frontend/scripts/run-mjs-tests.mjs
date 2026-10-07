// run-mjs-tests.mjs — 跑全量 src 下的 .test.mjs + .test.ts，跑完核对**每个文件真的被执行了**。
//
// 为什么要有这一层：把测试接进 gates 很容易，难的是证明"真的接住了"。
//   2026-10-01 实测：前端 146 个测试文件里只有 32 个被某个 npm script 引用，114 个是孤儿。
//   其中 `email-cid-images.test.mjs` 里有个用例名字就叫「cid 内嵌图被解析成 data URI
//   （**用户报的「详情缺失图片」**）」——护栏本来就是为那条投诉写的，却从未执行，
//   于是修复可以随时静默回归，而 gates 全绿。（见 4cd6e7e：改用 glob 接进 gates。）
//
// 但 glob 有两个不会报错的洞：
//   ① glob 写错/前缀变了/新文件落在外面 → node 安静地少跑，退出码仍是 0；
//   ② 文件"跑到了"却一个用例都没产出（空文件、全被 skip）→ 同样是一片绿。
// 「1291 个用例通过」是加总，「145 个文件都跑了且都有产出」才是覆盖。
//
// 所以这里做三件事：
//   1. 先自己枚举文件（fs 遍历，不依赖 glob 语义），一个都没找到就直接失败；
//   2. 跑完后用 test-file-census-reporter 记录实际执行到的文件，
//      **枚举结果 ⊆ 实际执行结果** 才算通过；两者不一致立刻失败并列出差集；
//   3. 零产出文件单独报——它和"压根没执行"是两种病，但都等于没有护栏。
//
// 用法：
//   node scripts/run-mjs-tests.mjs              跑测试 + 覆盖核对
//   node scripts/run-mjs-tests.mjs --print-coverage
//         只输出本 runner 覆盖的 glob（check-test-coverage.mjs 用它做静态判据，
//         避免"覆盖范围"在两处各写一份、日后漂移）
import { spawnSync } from 'node:child_process'
import { readFileSync, readdirSync, statSync, existsSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join, dirname, relative, sep } from 'node:path'
import { fileURLToPath } from 'node:url'

const here = dirname(fileURLToPath(import.meta.url))
const ROOT = join(here, '..')
const SRC = join(ROOT, 'src')

/**
 * 覆盖范围在这里定义；静态卡口通过 --print-coverage / --print-files 读**同一份**枚举结果。
 *
 * ★ 2026-10-07 修：原来注释写「定义一次」，而文件里其实有**两份**：
 *   `COVERAGE_GLOBS`（对外声明的 glob）与 `SUFFIXES`（真正用来遍历文件系统的）。
 *   两者没有交叉校验。实测只改 `SUFFIXES` 加一类 `.test.js`：
 *   本 runner 会**真的枚举并执行**它们，而孤儿卡口的 `ENFORCED_SUFFIXES` 不知道这一类
 *   ⇒ **这一类的孤儿永远不会被报出来** —— 正是这道门存在的目的被绕过。
 *   而 `COVERAGE_GLOBS` 加一项时，孤儿卡口会因为「新 glob 匹配 0 个文件」而 exit 1，
 *   报出来的却是**错误理由**（让人去查路径拼写，而不是查后缀清单不一致）。
 *   ⇒ 现在 `SUFFIXES` 由 `COVERAGE_GLOBS` **推导**，不再各写一份；
 *     并且新增 `--print-files`：把**真正枚举到的文件**吐出来给孤儿卡口对账。
 */
export const COVERAGE_GLOBS = ['src/**/*.test.mjs', 'src/**/*.test.ts']
// 从 glob 推后缀，避免两份清单漂移。
// ⚠ 注释里**不要**写出形如 `src/**/*.test.x` 的样例：其中的 `*` + `/` 会提前闭合块注释。
export const SUFFIXES = COVERAGE_GLOBS.map((g) => g.slice(g.lastIndexOf('/') + 1).replace(/^\*+/, ''))

/**
 * 豁免清单与 check-test-coverage.mjs 共用同一份 JSON。
 * unrunnable：node --test 根本跑不起来（源码无扩展名 import），不进本次运行；
 * zeroCase  ：跑得起来但一个用例都不产出，运行时判据会点名，按理由豁免。
 */
const WAIVERS = JSON.parse(readFileSync(join(here, 'test-coverage-waivers.json'), 'utf8'))
const UNRUNNABLE = WAIVERS.unrunnable ?? {}
const ZERO_CASE = WAIVERS.zeroCase ?? {}

if (process.argv.includes('--print-coverage')) {
  process.stdout.write(COVERAGE_GLOBS.join('\n') + '\n')
  process.exit(0)
}

// --coverage-glob 只用于**负控**：故意缩小覆盖范围，验证「文件没被真正执行」这条
// 判据真的会红（否则一个从不失败的卡口和一个没有卡口没有区别）。
// gates 不带这个参数；package.json 里的 test:all 也不带。
const override = process.argv.indexOf('--coverage-glob')
const globs = override >= 0 ? [process.argv[override + 1]] : COVERAGE_GLOBS
if (override >= 0 && !globs[0]) {
  console.error('❌ --coverage-glob 需要一个 glob 参数')
  process.exit(2)
}

function walk(dir, suffix, out = []) {
  for (const name of readdirSync(dir)) {
    const p = join(dir, name)
    if (statSync(p).isDirectory()) walk(p, suffix, out)
    else if (name.endsWith(suffix)) out.push(relative(ROOT, p).split(sep).join('/'))
  }
  return out
}

const allFiles = SUFFIXES.flatMap((s) => walk(SRC, s)).sort()
if (!allFiles.length) {
  console.error(`❌ ${relative(ROOT, SRC)} 下没找到任何测试文件 —— 路径写错了，别空转绿灯。`)
  process.exit(1)
}

// --print-files：把**真正枚举到的文件**吐给孤儿卡口对账（2026-10-07 新增）。
// 孤儿卡口的覆盖判定是「glob 展开 ∩ 自己的清单」；如果 runner 的枚举清单与
// 它的清单不一致（多一类/少一类），那一类的孤儿就永远没人管。这个出口让对账成为可能。
if (process.argv.includes('--print-files')) {
  process.stdout.write(allFiles.join('\n') + '\n')
  process.exit(0)
}
const discovered = allFiles.filter((f) => !UNRUNNABLE[f])
const skipped = allFiles.filter((f) => UNRUNNABLE[f])

const reporterPath = 'scripts/test-file-census-reporter.mjs'
if (!existsSync(join(ROOT, reporterPath))) {
  console.error(`❌ 普查 reporter 不存在：${reporterPath}`)
  process.exit(1)
}
const censusPath = join(tmpdir(), `pocket-test-census-${process.pid}.json`)

// 文件是**显式**传给 node 的，不是 glob。三个原因：
//   ① 已知跑不起来的文件（unrunnable）必须真正不被加载——它们只要被 glob 带进去就会
//      ERR_MODULE_NOT_FOUND，把**整轮**测试判红。node 没有"按文件排除"的开关，
//      --test-skip-pattern 也救不了：node 必须先 import 才能枚举用例，import 就已经炸了。
//   ② glob 静默失配时 node 不报错、退出码仍是 0；显式列表则由我们自己枚举并核对。
//   ③ 传列表让"枚举 ⊆ 实际执行"这条断言变得没有解释空间。
// reporter 同样必须传相对路径：绝对路径在 Windows 上带盘符，node 会当成 URL scheme
// （ERR_UNSUPPORTED_ESM_URL_SCHEME）直接拒绝加载。cwd 固定为 ROOT。
const argv = ['--test', '--test-reporter', './' + reporterPath, ...discovered]
const argvLen = argv.reduce((a, s) => a + s.length + 1, 0)
if (argvLen > 20000) {
  // Windows CreateProcess 命令行上限 32767；留足余量。超了就该修那个无扩展名 import，
  // 而不是把兜底逻辑继续堆在这儿。
  console.error(
    `❌ 测试文件列表长度 ${argvLen} 超过 20000 字符上限（Windows 命令行 32767 的安全余量）。\n` +
      `   文件数已经到 ${discovered.length}。这时最该做的是修掉 unrunnable 清单里的源码 import，\n` +
      `   让全部测试都能用 glob 跑；请勿在此处加兜底分支。`,
  )
  process.exit(2)
}
const res = spawnSync(process.execPath, argv, {
  cwd: ROOT,
  stdio: 'inherit',
  env: { ...process.env, POCKET_TEST_CENSUS: censusPath },
})

let census = {}
if (existsSync(censusPath)) {
  try {
    census = JSON.parse(readFileSync(censusPath, 'utf8'))
  } catch (e) {
    console.error(`❌ 测试普查结果无法解析：${e.message}`)
    process.exit(1)
  }
  rmSync(censusPath, { force: true })
} else {
  console.error('\n❌ 没拿到测试普查结果 —— reporter 没落盘，不能声称覆盖完整。')
  process.exit(1)
}

// 事件里的 file 是绝对路径；统一成相对 ROOT 的 posix 路径再比对
const executed = new Map()
for (const [abs, n] of Object.entries(census)) {
  executed.set(relative(ROOT, abs).split(sep).join('/'), n)
}

const missing = discovered.filter((f) => !executed.has(f))
const empty = discovered.filter((f) => executed.get(f) === 0 && !ZERO_CASE[f])
const waived = [...skipped, ...Object.keys(ZERO_CASE).filter((f) => discovered.includes(f) && executed.get(f) === 0)]

console.log(
  `\n【测试覆盖】枚举 ${discovered.length} 个测试文件（.mjs ${discovered.filter((f) => f.endsWith('.test.mjs')).length}` +
    ` / .ts ${discovered.filter((f) => f.endsWith('.test.ts')).length}）· 实际执行 ${executed.size} 个文件 · ` +
    `执行事件 ${[...executed.values()].reduce((a, b) => a + b, 0)}（含子测试/套件，不等于用例数）`,
)
if (waived.length) {
  console.log(`ℹ️  ${waived.length} 个文件在豁免名单里（见 test-coverage-waivers.json）：`)
  for (const f of waived) {
    const why = UNRUNNABLE[f] ?? ZERO_CASE[f]
    console.log(`   ${f}\n      理由：${why}`)
  }
}

if (missing.length || empty.length) {
  console.error()
  for (const f of missing) console.error(`❌ 从未被执行：${f} —— ${globs.join(' / ')} 没匹配到它。`)
  for (const f of empty) console.error(`❌ 跑到了但一个用例都没产出：${f} —— 等于没测。`)
  console.error('\n这类文件是「孤儿测试」：护栏写在纸上、gates 却从不执行，回归可以静默通过。')
  process.exit(1)
}

if (res.status !== 0) {
  process.exit(res.status ?? 1)
}
console.log(`✅ ${discovered.length}/${discovered.length} 个测试文件全部被实际执行且都有产出`)
