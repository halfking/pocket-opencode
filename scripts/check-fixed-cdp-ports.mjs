// check-fixed-cdp-ports.mjs —— 找出仍在硬编码固定 CDP 端口的脚本。
//
// 为什么要有这道门（2026-10-02）：
//   仓库里曾有 **157 处** `forward tcp:${PORT}` 硬绑写死的端口
//   （40 来个 diag/verify/sweep 脚本各占一个，分布在 9402-9476）。
//   端口是**同机所有会话共享**的，撞上就抛 10048，而那句报错指向装置，
//   看不出「真问题是上次没清干净」。BUG-V9 就是这么撞上的，而且它落在
//   assertFetchIntact 上时**只降级成一句「未能判定，不阻断」然后照样 exit=0**
//   —— 守卫没跑成，绿灯照出。
//
//   这道门的作用不是「一次改完 157 处」，而是让剩下的债务**可审计**：
//   谁还硬编码、写在第几行，一眼可见，不靠记忆。
//
// 用法：
//   node scripts/check-fixed-cdp-ports.mjs            # 只对**新增**违规 exit 1
//   node scripts/check-fixed-cdp-ports.mjs --list     # 只列出，恒 exit 0
//   node scripts/check-fixed-cdp-ports.mjs --selftest # 证明判据能转红/转绿
//   node scripts/check-fixed-cdp-ports.mjs --write-baseline  # 重录基线（有意为之才用）
//
// ⚠️ 判据本身必须先证明有区分力，否则又是一道恒真的门。
//    --selftest 就是干这个的：拿**合成样本**跑同一套检测逻辑，
//    断言「该报的报、该放过的放过」。不是去改真文件（那会污染工作区）。
//
// ---------------------------------------------------------------------------
// 为什么改成基线棘轮（2026-10-05，本轮实现；决策见 docs/handoff/_part-4.86 §4.86.2）
// ---------------------------------------------------------------------------
// 原形态是「有任何命中就 exit 1」。实测命中 148 处 ⇒ 这道门**永远红**。
// 而 2026-10-01 落档的结论已经写明：正因为它永远红，才**刻意没接进 gates**
// （docs/handoff/_part-4.86：「要接必须是基线棘轮形态（只对新增违规失败），
// 那是独立一件事，本轮没做」）。那个文件至今只有 3a848d43 一次提交，
// ⇒ 这道门从写下那天起就**既不能真正拦截任何东西、也没人敢接它**，
// 「有护栏」和「有护栏」在这里被读成了同一件事。
//
// 棘轮形态：已录基线里的存量违规**不判红**（否则永远红），只对
// 「基线里没有的新违规」判红。债务因此可审计、可下降、且**能进门禁**。
//
// ⚠️ 基线**绝对不能按行号记录**（本仓已经因此踩过一次，见下）。
//    scripts/z-index-ladder.test.mjs / bottom-chrome-gate.test.mjs 的
//    ALLOWLIST 用的就是 `rel:line` 作 key，Windows 上反斜杠 + 行号漂移
//    让 11 条全部被判「陈旧」（round43 §2）。行号一漂、文件一改就误报，
//    久而久之没人看它 —— 那不叫棘轮，叫噪声发生器。
//    所以 key 只用 `file|kind|detail`，**不含行号**；同一 key 允许重复出现，
//    用计数容忍「同一个文件里多加一处」。
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import {
  countKeysBy,
  defaultKeyFn,
  diffRatchet,
  loadBaseline as loadBaselineFile,
  ratchetSelfTestCases,
  writeBaseline as writeBaselineFile,
} from './lib/baseline-ratchet.mjs'

const HERE = path.dirname(fileURLToPath(import.meta.url))
const ROOT = path.resolve(HERE, '..')

/**
 * 对单个文件文本做检测。返回命中项数组（每项含行号与原因）。
 * 拆出来是为了能拿合成样本做自测。
 *
 * @param {string} text
 * @param {string} file
 * @param {{disable?: string[]}} opts `disable` 用于**故意让判据变瞎**，
 *        用来量化「去掉某条规则后少算了多少」——只验「能报错」是敏感度，
 *        验「看得见该看的」是特异度，两件事要分开做。
 */
export function detectFixedCdpPort(text, file, opts = {}) {
  const off = new Set(opts.disable || [])
  const hits = []
  const lines = text.split('\n')

  // 已经走共享 helper 的直接放行：它的端口是 adb 分配的。
  if (!off.has('helper-exemption') && /from\s+['"].*lib\/adb-cdp\.mjs['"]/.test(text)) return hits

  lines.forEach((raw, i) => {
    const line = raw.replace(/\r$/, '')
    const ln = i + 1

    // 1) 字面量端口：'tcp:9223' / `tcp:9223`
    if (!off.has('literal-port')) {
      const lit = line.match(/forward[^\n]*?['"`]tcp:(\d{4,5})['"`]/)
      if (lit && lit[1] !== '0') {
        hits.push({ file, line: ln, kind: 'literal-port', detail: `tcp:${lit[1]}` })
      }
    }

    // 2) 硬编码默认端口：const PORT = process.env.POCKET_CDP_PORT || '9418'
    if (!off.has('hardcoded-default')) {
      const def = line.match(/POCKET_CDP_PORT\s*\|\|\s*['"](\d{4,5})['"]/)
      if (def && def[1] !== '0') {
        hits.push({ file, line: ln, kind: 'hardcoded-default', detail: `POCKET_CDP_PORT || '${def[1]}'` })
      }
    }

    // 3) 直接写死的 const PORT = 9418 / CDP_PORT = 9472
    if (!off.has('plain-port-const')) {
      const plain = line.match(/^\s*const\s+(?:CDP_)?PORT\s*=\s*['"]?(\d{4,5})['"]?\s*$/)
      if (plain) {
        hits.push({ file, line: ln, kind: 'plain-port-const', detail: plain[1] })
      }
    }
  })
  return hits
}

function walk(dir, out = []) {
  for (const n of fs.readdirSync(dir)) {
    if (n === 'node_modules' || n === '.git') continue
    const p = path.join(dir, n)
    const st = fs.statSync(p)
    if (st.isDirectory()) walk(p, out)
    else if (/\.mjs$/.test(n)) out.push(p)
  }
  return out
}

// ---- 基线棘轮 ----------------------------------------------------------------
//
// 机制在 scripts/lib/baseline-ratchet.mjs（两道债务门禁共用，理由与
// 「key 为什么不能含行号」的教训都写在那里）。此处只做本门的接线。
const BASELINE_PATH = path.join(HERE, 'baselines', 'fixed-cdp-ports.json')
const BASELINE_COMMENT =
  'check-fixed-cdp-ports.mjs 的基线棘轮存量清单。key 形如 file|kind|detail，' +
  '**故意不含行号**（行号一漂就全判陈旧，本仓已在 z-index-ladder ALLOWLIST 上踩过）。' +
  '只对不在此清单里的新增违规判红。用 --write-baseline 重录。'

const keyOf = defaultKeyFn
const countKeys = (hits) => countKeysBy(hits)
const diffAgainstBaseline = (hits, baseline) => diffRatchet(hits, baseline)
const loadBaseline = () => loadBaselineFile(BASELINE_PATH)
const writeBaseline = (counts) => writeBaselineFile(BASELINE_PATH, counts, BASELINE_COMMENT)


function selftest() {
  const shouldHit = [
    ["const PORT = process.env.POCKET_CDP_PORT || '9418'\n", 'hardcoded-default'],
    ["execFileSync(ADB, ['-s', S, 'forward', 'tcp:9223', `localabstract:${sock}`])\n", 'literal-port'],
    ["const CDP_PORT = 9472\n", 'plain-port-const'],
  ]
  const shouldMiss = [
    "adb(['forward', 'tcp:0', `localabstract:${sock}`])\n",              // 动态
    "const PORT = process.env.POCKET_CDP_PORT || '0'\n",                  // 默认 0
    "import { openCdp } from './lib/adb-cdp.mjs'\n",                     // 走 helper
    "const PORT = Number(adb(['forward', 'tcp:0', sock]).trim())\n",     // 已改造
    "console.log('9402 is the old default')\n",                          // 注释里提到不算
  ]

  const fails = []
  for (const [text, kind] of shouldHit) {
    const hits = detectFixedCdpPort(text, 'synthetic')
    if (!hits.some((h) => h.kind === kind)) {
      fails.push(`该报没报：kind=${kind}  text=${JSON.stringify(text.slice(0, 60))}`)
    }
  }
  for (const text of shouldMiss) {
    const hits = detectFixedCdpPort(text, 'synthetic')
    if (hits.length) {
      fails.push(`不该报却报了：${JSON.stringify(text.slice(0, 60))} -> ${JSON.stringify(hits)}`)
    }
  }
  // ---- 反向验证覆盖面（验特异度，不是敏感度）----
  // 只做「故意改坏看它红不红」只证明了它**敏感**；那不足以说明它**看得见该看的**。
  // 这里逐条把规则关掉，看它**少算了多少**，并把差集写进断言。
  const RULES = ['literal-port', 'hardcoded-default', 'plain-port-const']
  const allSample = [
    "const PORT = process.env.POCKET_CDP_PORT || '9418'\n",
    "execFileSync(ADB, ['-s', S, 'forward', 'tcp:9223', `localabstract:${sock}`])\n",
    "const CDP_PORT = 9472\n",
  ]
  for (const rule of RULES) {
    const full = detectFixedCdpPort(allSample.join(''), 'synthetic').length
    const blind = detectFixedCdpPort(allSample.join(''), 'synthetic', { disable: [rule] }).length
    if (full - blind !== 1) {
      fails.push(`变瞎对照异常：关掉「${rule}」后少报 ${full - blind} 条，应恰好少 1 条（说明这条规则可能没在生效）`)
    } else {
      console.log(`  变盲对照：关掉「${rule}」→ ${full} → ${blind}（差 1，规则确实在起作用）`)
    }
  }

  // ---- 棘轮本身的判据自测（2026-10-05 新增） ----
  // 只测「检测器能报」不够：那只能证明**敏感度**。棘轮真正的风险是
  // 「接进来以后一直绿」——而一直绿既可能是债务清零，也可能是棘轮压根没生效。
  // 用例集在 lib/baseline-ratchet.mjs（两道门共用），这里只喂本门的样本。
  const inBaselineHit = {
    file: 'synthetic.mjs',
    line: 49,
    kind: 'hardcoded-default',
    detail: "POCKET_CDP_PORT || '9418'",
  }
  const newHit = { file: 'other.mjs', line: 3, kind: 'literal-port', detail: 'tcp:9223' }
  for (const f of ratchetSelfTestCases({
    diff: (h, b) => diffAgainstBaseline(h, b),
    keyFn: keyOf,
    inBaselineHit,
    newHit,
    sameHit2: { ...inBaselineHit, line: 50 },
    // 模拟「基线是按行号录的」那种形态：key 里带了一个旧行号。
    driftKey: `synthetic.mjs|hardcoded-default|__OLD_LINE_49__`,
  })) fails.push(f)

  // 真仓库上棘轮必须已经录好基线且当下无新增。
  // 这一条是「接进 gates 之前」的最后一关：录基线这一步不能靠人肉。
  {
    const b = loadBaseline()
    if (!b) {
      fails.push('基线文件不存在，棘轮无从比对；先跑 --write-baseline 录基线')
    } else {
      const real = scanRepo()
      const got = diffAgainstBaseline(real.all, b)
      if (got.newHits.length)
        fails.push(
          `刚录的基线与自己都对不上（新增 ${got.newHits.length} 条）：` +
            got.newHits.slice(0, 3).map((h) => `${h.file}:${h.line}`).join(', '),
        )
      else
        console.log(
          `  真仓库棘轮：${real.all.length} 处存量 / 基线 ${Object.keys(b).length} 个 key，新增 0 ✅`,
        )
    }
  }

  if (fails.length) {
    console.error('❌ 判据自测失败：')
    for (const f of fails) console.error('   ' + f)
    process.exit(1)
  }
  console.log('✅ 判据自测通过：3 类硬编码都能报出，5 类合法写法都能放过，变瞎对照会漏报，棘轮只在新增时转红')
}

/** 扫真仓库。selftest 与主流程共用，避免「自测扫的是合成样本、主流程扫的是别的东西」。 */
function scanRepo() {
  const files = walk(path.join(ROOT, 'scripts'))
  const all = []
  for (const f of files) {
    const rel = path.relative(ROOT, f).replace(/\\/g, '/')
    all.push(...detectFixedCdpPort(fs.readFileSync(f, 'utf8'), rel))
  }
  all.sort((a, b) => a.file.localeCompare(b.file) || a.line - b.line)
  return { files, all }
}

const argv = process.argv.slice(2)

// --write-baseline：重录基线。这是**有意扩大存量**的动作，必须显眼，
// 不能和普通判定混在一条命令里，否则「顺手跑一下」就会把门禁洗绿。
if (argv.includes('--write-baseline')) {
  const { all } = scanRepo()
  const counts = writeBaseline(countKeys(all))
  console.log(
    `已写入基线：${BASELINE_PATH}\n  key ${Object.keys(counts).length} 个 / 命中 ${all.length} 处`,
  )
  console.log('⚠️ 这一步等于宣布「这些存量暂时不判红」。请在提交信息里写清为什么。')
  process.exit(0)
}

if (argv.includes('--selftest')) { selftest(); process.exit(0) }

const LIST = argv.includes('--list')
const repo = scanRepo()
const all = repo.all

const byKind = {}
for (const h of all) byKind[h.kind] = (byKind[h.kind] || 0) + 1

console.log(`扫描 ${repo.files.length} 个 .mjs，硬编码固定 CDP 端口 ${all.length} 处`)
for (const [k, v] of Object.entries(byKind)) console.log(`  ${k}: ${v}`)

// --list 保持原语义：只列全部存量，恒 exit 0。
if (LIST) {
  if (all.length) {
    console.log('')
    for (const h of all) console.log(`  ${h.file}:${h.line}  [${h.kind}] ${h.detail}`)
  }
  console.log('\n修法：改成 lib/adb-cdp.mjs 的 openCdp()，或至少 `forward tcp:0` + finally 里 --remove。')
  process.exit(0)
}

let baseline
try {
  baseline = loadBaseline()
} catch (e) {
  // 门禁自身报错（exit 2）≠ 判红（exit 1）。基线文件坏了是「判据跑不起来」，
  // 报成「有新增违规」会把「我的观测手段坏了」说成「代码有问题」。
  console.error(`[check-fixed-cdp-ports] 基线读取失败：${e.message}`)
  process.exit(2)
}
if (!baseline) {
  console.error(
    '[check-fixed-cdp-ports] 基线文件不存在：scripts/baselines/fixed-cdp-ports.json\n' +
      '  录基线：node scripts/check-fixed-cdp-ports.mjs --write-baseline',
  )
  process.exit(2)
}

const { newHits, removed } = diffAgainstBaseline(all, baseline)
const baselineCount = Object.values(baseline).reduce((a, b) => a + b, 0)
console.log(
  `基线棘轮：存量 ${baselineCount} 处（基线 key ${Object.keys(baseline).length} 个）→ ` +
    `本次实测 ${all.length} 处，新增 ${newHits.length} 处，已消失 ${removed.length} 处`,
)

if (newHits.length) {
  console.error('\n❌ 以下是**新增**违规（不在基线里），必须改掉：')
  for (const h of newHits) console.error(`  ${h.file}:${h.line}  [${h.kind}] ${h.detail}`)
  console.error('\n修法：改成 lib/adb-cdp.mjs 的 openCdp()，或至少 `forward tcp:0` + finally 里 --remove。')
  console.error('若这确实是「已知且暂时接受」的存量，用 --write-baseline 重录，并在提交信息里写明为什么。')
  process.exit(1)
}

if (removed.length) {
  console.log(`\n✅ 无新增违规。另外有 ${removed.length} 处存量已消失（棘轮可以收紧了）：`)
  for (const k of removed.slice(0, 10)) console.log(`  - ${k}`)
  if (removed.length > 10) console.log(`  …（另有 ${removed.length - 10} 处）`)
  console.log('如需把基线同步收紧：node scripts/check-fixed-cdp-ports.mjs --write-baseline')
}
console.log(`\n✅ 无新增违规（存量 ${all.length} 处不判红，这是棘轮的约定）。`)
process.exit(0)
