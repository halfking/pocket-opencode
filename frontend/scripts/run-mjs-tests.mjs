// run-mjs-tests.mjs — 跑全量 src/**/*.test.mjs，并在跑完之后**核对每个文件真的被执行了**。
//
// 为什么不是直接 `node --test "src/**/*.test.mjs"` 就完事：
//   2026-10-01 实测，前端 146 个测试文件里只有 32 个被某个 npm script 引用，
//   114 个是孤儿，其中 62 个 .mjs 一次都没跑过。最刺眼的是
//   `email-cid-images.test.mjs`——里面有个用例名字就叫「cid 内嵌图被解析成 data URI
//   （**用户报的「详情缺失图片」**）」：护栏本来就是为那条投诉写的，却从未执行，
//   于是修复可以随时静默回归，而 gates 全绿。
//   （见 4cd6e7e：改用 glob 把全量接进 gates，959 个用例。）
//
// 但改成 glob 之后仍有一个洞：**glob 匹配不到东西时 node 不报错，退出码是 0。**
//   glob 写错、路径前缀变了、新测试落在 src 之外——都会变成「安静地少跑一批」。
//   「959 个用例通过」并不等于「91 个文件都跑了」：前者是加总，后者才是覆盖。
//
// 所以这里做两件事：
//   1. 先**自己枚举**文件（fs 遍历，不依赖 glob 语义），一个都没找到就直接失败——
//      防止路径写错导致空转绿灯；
//   2. 跑完后用 test-file-census-reporter 记录实际执行到的文件，
//      **枚举结果 ⊆ 实际执行结果** 才算通过；两者不一致立刻失败并列出差集。
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

/** 覆盖范围在这里定义一次；静态卡口通过 --print-coverage 读同一份，避免两处漂移。 */
export const COVERAGE_GLOB = 'src/**/*.test.mjs'

if (process.argv.includes('--print-coverage')) {
  process.stdout.write(COVERAGE_GLOB + '\n')
  process.exit(0)
}

// --coverage-glob 只用于**负控**：故意把覆盖范围缩小，验证「文件没被真正执行」这条
// 判据真的会红（否则一个从不失败的卡口和一个没有卡口没有区别）。
// gates 不带这个参数；package.json 里的 test:all 也不带。
const override = process.argv.indexOf('--coverage-glob')
const glob = override >= 0 ? process.argv[override + 1] : COVERAGE_GLOB
if (!glob) {
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

// 零用例文件的豁免名单：**只登记存量，不许新增**。每条必须写清为什么。
// 「跑到了却一个用例都没产出」和「压根没被执行」是两种病，但都等于没有护栏。
const KNOWN_ZERO_CASE = {
  'src/native/__tests__/recordingRuntimeMimeFallback.test.mjs':
    'STT 会话遗留的空占位：全文只有 3 行注释，自称 placeholder，实际 mime 回退用例在 recorderMime.test.mjs。' +
    '它当年存在的原因是"空文件会让 test runner discovery 抱怨"，而现在 test:all 用的是 glob，空文件无害。' +
    '之所以不删：wt-stt 上还有一个 STT 会话在动这块，可能正要补上用例。补完或删除后请从这里移除。',
}

const discovered = walk(SRC, '.test.mjs').sort()
if (!discovered.length) {
  console.error(`❌ ${relative(ROOT, SRC)} 下没找到任何 *.test.mjs —— 路径写错了，别空转绿灯。`)
  process.exit(1)
}

const reporterPath = 'scripts/test-file-census-reporter.mjs'
if (!existsSync(join(ROOT, reporterPath))) {
  console.error(`❌ 普查 reporter 不存在：${reporterPath}`)
  process.exit(1)
}
const censusPath = join(tmpdir(), `pocket-test-census-${process.pid}.json`)

// reporter 必须传**相对路径**：绝对路径在 Windows 上带盘符，node 会当成 URL scheme
// （ERR_UNSUPPORTED_ESM_URL_SCHEME）直接拒绝加载。cwd 固定为 ROOT，所以相对路径可用。
const res = spawnSync(
  process.execPath,
  ['--test', '--test-reporter', './' + reporterPath, glob],
  { cwd: ROOT, stdio: 'inherit', env: { ...process.env, POCKET_TEST_CENSUS: censusPath } },
)

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
const empty = discovered.filter((f) => executed.get(f) === 0 && !KNOWN_ZERO_CASE[f])
const waived = Object.keys(KNOWN_ZERO_CASE).filter((f) => discovered.includes(f) && executed.get(f) === 0)

console.log(
  `\n【测试覆盖】枚举 ${discovered.length} 个 .test.mjs · 实际执行 ${executed.size} 个文件 · ` +
    `执行事件 ${[...executed.values()].reduce((a, b) => a + b, 0)}（含子测试/套件，不等于用例数）`,
)
if (waived.length) {
  console.log(`ℹ️  ${waived.length} 个零用例文件在豁免名单里（见 KNOWN_ZERO_CASE）：`)
  for (const f of waived) console.log(`   ${f}\n      理由：${KNOWN_ZERO_CASE[f]}`)
}

if (missing.length || empty.length) {
  console.error()
  for (const f of missing) console.error(`❌ 从未被执行：${f} —— ${glob} 没匹配到它。`)
  for (const f of empty) console.error(`❌ 跑到了但一个用例都没产出：${f} —— 等于没测。`)
  console.error('\n这类文件是「孤儿测试」：护栏写在纸上、gates 却从不执行，回归可以静默通过。')
  process.exit(1)
}

if (res.status !== 0) {
  process.exit(res.status ?? 1)
}
console.log(`✅ ${discovered.length}/${discovered.length} 个测试文件全部被实际执行`)
