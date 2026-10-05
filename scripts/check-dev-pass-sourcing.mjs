// check-dev-pass-sourcing.mjs —— 守门：拦住「设备侧/接口探针把 dev 口令弄错来源」这一整类。
//
// ## 为什么要有这道门禁（2026-10-02 实测出来的）
//
// 仓库里有 7 个探针/验证脚本用**同一种写法**取 dev 口令：
//
//   const devPass = (readFileSync('backend/internal/server/server_assistant.go','utf8')
//     .match(/devPass\s*=\s*"([^"]+)"/) || [])[1] || ''
//
// 那个 `devPass = "…"` 常量**已经被 `b6187bc1`（dev 旁路移除硬编码 admin 口令）删掉了**。
// 于是刮取必然得到**空串** —— 不是「有时失败」，是**必然失败**：
//
//   login(password='') → 401 → **拿不到 token**
//   → 后面每一条探测都是**未鉴权**的
//   → `/api/marketplace/*` 一律返回 401（requireAuth 先于路由匹配跑）
//   → 而这份输出被当成证据记进了 handoff，
//     变成一条「`/api/marketplace` 带有效 token 仍 401，守卫与 `/api/tasks` 不同」的**幽灵结论**，
//     连续三轮（§4.83 / §4.84 / §4.85）被当成待查项搬来搬去。
//
// 实测（2026-10-02，带**有效** token，291 字符）：
//   /api/tasks                    → 200
//   /api/marketplace/packages     → 200  {"packages":[]}
//   /api/marketplace/releases     → 200  {"releases":[]}
//   /api/marketplace/agents       → 404  {"error":"not found"}   ← 过了守卫，路由确实不存在
//   401 的条数 = 0
//
// **401 和 404 只有靠「同一个 token 下的对照路由」才分得开。**
// 而「401 处处都是」最常见的成因不是路由问题，是**这个脚本自己没拿到 token**。
//
// 这与 BUG-V10（verify-https-prod.mjs 的 adminPass 调用点被漏掉）是**同一类**：
// 安全整改删掉了一个字面量，某个消费者仍在按名字找它；
// 而消费者的报错指向的是一个**已经不存在的东西**。
//
// ## 两条规则
//
//   scrape-dev-pass      脚本从 .go 源码里正则刮取口令类常量（devPass / masterPass / MASTER…）
//   hardcoded-fallback   `process.env.X || '<字面量>'` 且字面量长得像真口令
//
// ⚠️ R2 只在字面量**同时含字母与数字**且长度 >= 8 时才报。
//    放宽到「任何 || 'xxx'」会立刻炸出几百条 `|| 'utf8'` / `|| '2024'` 之类的噪声 ——
//    secrets_test.go:138-140 记的正是这个坑（「放宽会一次性炸出几十条噪声」）。
//    宁可漏，不要一条红的守门脚本被当成噪声忽略掉。
//
// 用法：
//   node scripts/check-dev-pass-sourcing.mjs            # 只对**新增**违规 exit 1
//   node scripts/check-dev-pass-sourcing.mjs --list     # 只列不判
//   node scripts/check-dev-pass-sourcing.mjs --selftest # 判据自检
//   node scripts/check-dev-pass-sourcing.mjs --write-baseline # 重录基线（有意为之才用）
//
// ⚠️ 2026-10-05：本门已从「有任何命中就 exit 1」改成**基线棘轮**。
//    原形态下存量 24 处 ⇒ 这道门永远红 ⇒ 既拦不住东西也没人敢接进 gates。
//    机制与理由见 scripts/lib/baseline-ratchet.mjs。
//    key 形如 `file|rule|text`，**不含行号**（行号一漂就全判陈旧）。
import { readFileSync, readdirSync, writeFileSync, unlinkSync } from 'node:fs'
import { dirname, join, relative, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import {
  countKeysBy,
  diffRatchet,
  loadBaseline,
  ratchetSelfTestCases,
  writeBaseline,
} from './lib/baseline-ratchet.mjs'

// ⚠️ ROOT 必须由**脚本自身位置**推导，不能用 process.cwd()。
//    这道门在 2026-10-05 之前从未被任何 runner 调用过，所以「必须从仓库根
//    目录跑」这个隐含前提从没被验证过：接进 gates 后 npm run 的 cwd 是
//    frontend/，于是它会去扫 frontend/scripts/、去找 frontend/scripts/baselines/…，
//    找不到就 exit 2 —— 门禁一接线就红，且报错信息与真实原因毫无关系。
//    其余根级门禁（check-fixed-cdp-ports 等）都用 import.meta.url，此处对齐。
const HERE = dirname(fileURLToPath(import.meta.url))
const ROOT = resolve(HERE, '..')
const SELFTEST = process.argv.includes('--selftest')
const LIST = process.argv.includes('--list')
const WRITE_BASELINE = process.argv.includes('--write-baseline')
const BASELINE_PATH = join(ROOT, 'scripts', 'baselines', 'dev-pass-sourcing.json')
const BASELINE_COMMENT =
  'check-dev-pass-sourcing.mjs 的基线棘轮存量清单。key 形如 file|rule|text，' +
  '**故意不含行号**（行号一漂就全判陈旧）。只对不在此清单里的新增违规判红。' +
  '用 --write-baseline 重录。'
const keyOf = (h) => `${h.file}|${h.rule}|${h.text}`

/** 递归收集 scripts/ 下的 .mjs */
function collect(dir, out = []) {
  for (const e of readdirSync(dir, { withFileTypes: true })) {
    if (['node_modules', '.git', 'archive'].includes(e.name)) continue
    const p = join(dir, e.name)
    if (e.isDirectory()) collect(p, out)
    else if (e.name.endsWith('.mjs')) out.push(p)
  }
  return out
}

// R1：从 .go 源码里刮口令。关键是「读 .go」+「正则里出现口令类变量名」两个条件同时成立。
const R1 = /readFileSync\(\s*[^)]*\.go['"`][^)]*\)\s*(?:\.match|\.split)[\s\S]{0,80}?\b(devPass|masterPass|MasterPass|DEV_PASS|masterPassword)\b/
const R1_ANYVAR = /readFileSync\(\s*[^)]*\.go['"`][^)]*\)[\s\S]{0,120}?\.match\(\s*\/([^/\n]*(?:pass|pwd|Pass|PASS|secret|Secret)[^/\n]*)\/i/

// R2：env 兜底里塞**凭据**字面量。
// ⚠️ 第一版写成「任何 `process.env.X || '字面量'` 都报」，扫出 142 处，其中大半是
//    `POCKET_SERIAL || '4c308e2e'`、`POCKET_API || 'http://127.0.0.1:8096'`、
//    `JAVA_HOME || 'C:\Program Files\...'` 这类**无害默认值**。
//    一道 142 条里大半是假的守门脚本，下一周就会被 `--list | head` 忽略掉 —— 比没有更糟。
//    ⇒ 收紧成：**变量名本身像凭据**（pass/pwd/secret/token/master/credential/apikey）
//      且字面量含字母与数字、长度 >= 8。
//      这样 SERIAL / BASE / PSQL / JAVA_HOME 全部放过，而
//      `MASTER = process.env.POCKET_MASTER || 'PocketTest2026'` 照报。
//      ⚠️ 收紧时我第一版多写了个否定预查 `(?![A-Za-z]*$)`（「排除纯字母的名字」），
//      结果 **MASTER 恰好是纯字母**，被它排除 ⇒ 真实案例一起收没了。
//      是 --selftest 的「该报的报」当场抓到的。这类多余限定符在写规则时很自然，
//      但它们**不报红、只是让判据变瞎**——和 §4.86 记的四种静默漏扫同一个病。
const CRED_NAME = /^(?=.*(?:pass|pwd|secret|token|master|credential|apikey|api_key))[A-Za-z_][A-Za-z0-9_]*$/i
const R2 = /([A-Za-z_][A-Za-z0-9_]*)\s*=\s*process\.env\.[A-Za-z_][A-Za-z0-9_]*\s*\|\|\s*'([^']{8,})'/g
const looksLikeSecret = (s) => /[A-Za-z]/.test(s) && /[0-9]/.test(s)
const isCredName = (s) => CRED_NAME.test(s)

// 自身豁免：本文件的规则表与判据自检样本**必然**含有被搜形态。
// 这不是开后门，是自指——与 backend/internal/repohygiene/secrets_test.go:202-205
// 的 exemptionMarker 同一个道理：把模式写在源码里就一定会被自己搜到。
// 范围严格限定为本文件。
// ⚠️ 两边都必须归一化成正斜杠：SELF 来自 URL pathname（正斜杠），
//    而 f 来自 path.join（Windows 上是**反斜杠**）。不归一化的话
//    `f.toLowerCase() === SELF` 永远为 false，豁免静默失效——
//    又是一个「不报红、只是让判据少干活」的例子。
const SELF = new URL(import.meta.url).pathname.replace(/^\/([A-Za-z]:)/, '$1').replace(/\\/g, '/').toLowerCase()
const SELF_BEGIN = '// __SELF_REGION_BEGIN__'
const SELF_END = '// __SELF_REGION_END__'

// __SELF_REGION_BEGIN__
const RULES = [
  {
    name: 'scrape-dev-pass',
    test: (line) => R1.test(line) || R1_ANYVAR.test(line),
  },
  {
    name: 'hardcoded-fallback',
    test: (line) => { R2.lastIndex = 0; for (const m of line.matchAll(R2)) if (isCredName(m[1]) && looksLikeSecret(m[2])) return true; return false },
  },
]
// __SELF_REGION_END__

function scan(files) {
  const hits = []
  for (const f of files) {
    const isSelf = f.replace(/\\/g, '/').toLowerCase() === SELF
    let inSelfRegion = false
    const s = readFileSync(f, 'utf8')
    s.split(/\r?\n/).forEach((line, i) => {
      // 自指区域：规则表与判据自检样本**必然**含有被搜形态。
      // 用显式标记划出来，而不是靠「跳过整个文件」——那样会把真正的漏洞一起放过。
      if (line.trim() === SELF_BEGIN) { inSelfRegion = true; return }
      if (line.trim() === SELF_END) { inSelfRegion = false; return }
      if (isSelf && inSelfRegion) return
      // 注释行不算：文档里引用旧写法是合理的
      const t = line.trim()
      if (t.startsWith('//') || t.startsWith('*') || t.startsWith('#')) return
      for (const r of RULES) if (r.test(line)) {
        hits.push({ file: relative(ROOT, f).replace(/\\/g, '/'), line: i + 1, rule: r.name, text: t.slice(0, 90) })
        break
      }
    })
  }
  return hits
}

// ── 判据自检：三项都做，缺一项这道门禁就只是装饰 ─────────────────────
// 自检样本写进 scripts/ 下的临时文件（因为 scan() 收的是文件路径），
// 跑完立刻删；样本全用 ASCII，避免任何编码往返。
const temps = []
function writeTemp(name, body) {
  const p = join(ROOT, 'scripts', name)
  writeFileSync(p, body, 'utf8')
  temps.push(p)
  return p
}
function cleanupTemps() {
  for (const t of temps) { try { unlinkSync(t) } catch { /* 已不在 */ } }
}

function selftest() {
  // __SELF_REGION_BEGIN__
  const SAMPLES = [
    ['_ok_env_only.mjs', "const PASSWD = process.env.POCKET_PROBE_PASS\nif (!PASSWD) { console.error('no pass'); process.exit(2) }", false],
    ['_ok_scraping_go_for_port.mjs', "const src = readFileSync('backend/internal/server/server.go', 'utf8')\nconst m = src.match(/http\\.ListenAndServe\\(\":(\\d+)\"/)", false],
    ['_ok_comment.mjs', "// const devPass = readFileSync('a.go','utf8').match(/devPass\\s*=\\s*\"([^\"]+)\"/)\n", false],
    // 下面两条分别隔离 R2 的**两个**条件：只有两个都满足才该报。
    ['_ok_fallback_nodigit.mjs', "const SECRET = process.env.X || 'production'", false],
    ['_ok_fallback_noncred_name.mjs', "const SERIAL = process.env.POCKET_SERIAL || '4c308e2e'", false],
    ['_ok_fallback_url.mjs', "const BASE = process.env.POCKET_API || 'http://127.0.0.1:8096'", false],
    ['_bad_scrape.mjs', "const devPass = (readFileSync('backend/internal/server/server_assistant.go','utf8').match(/devPass\\s*=\\s*\"([^\"]+)\"/)||[])[1]||''", true],
    ['_bad_fallback.mjs', "const MASTER = process.env.POCKET_MASTER || 'PocketTest2026'", true],
  ]
  // __SELF_REGION_END__
  let bad = 0
  for (const [name, body, shouldHit] of SAMPLES) {
    const hit = scan([writeTemp(name, body)])
    const got = hit.length > 0
    const ok = got === shouldHit
    if (!ok) bad++
    console.log(`  ${(got ? '报出' : '放过').padEnd(5)}${ok ? '✅' : '❌ 期望' + (shouldHit ? '报出' : '放过')}  ${name}`)
  }
  // 变盲对照：逐条关掉规则，每条应恰好少报 1 个「真样本」。
  // 只做「故意改坏看它红不红」是不够的——那验的是敏感度；这里验的是覆盖面。
  const real = SAMPLES.filter(([, , e]) => e).map(([n, b]) => writeTemp(n, b))
  const full = scan(real).length
  for (const r of RULES) {
    const saved = r.test
    r.test = () => false
    const blind = scan(real).length
    r.test = saved
    const d = full - blind
    const ok = d === 1
    if (!ok) bad++
    console.log(`  变盲对照：关掉「${r.name}」→ ${full} → ${blind}（差 ${d}，期望 1）${ok ? ' ✅' : ' ❌'}`)
  }
  // ---- 棘轮自测（2026-10-05 新增，机制见 lib/baseline-ratchet.mjs） ----
  // 本门的命中字段是 {file, line, rule, text}，所以 key 用 file|rule|text。
  const inBaselineHit = {
    file: 'synthetic.mjs',
    line: 13,
    rule: 'hardcoded-fallback',
    text: "const MASTER = process.env.POCKET_MASTER || 'PocketTest2026'",
  }
  const newHit = {
    file: 'synthetic2.mjs',
    line: 3,
    rule: 'hardcoded-fallback',
    text: "const TOKEN = process.env.POCKET_TOKEN || 'Abcdef123456'",
  }
  for (const f of ratchetSelfTestCases({
    diff: (h, b) => diffRatchet(h, b, keyOf),
    keyFn: keyOf,
    inBaselineHit,
    newHit,
    sameHit2: { ...inBaselineHit, line: 14 },
    driftKey: 'synthetic.mjs|hardcoded-fallback|__OLD_LINE_13__',
  })) {
    bad++
    console.log(`  ❌ ${f}`)
  }
  console.log(bad ? `\n❌ 判据自检失败 ${bad} 项` : '\n✅ 判据自检通过：能报出该报的、能放过该放过的、变盲会漏报、棘轮只在新增时转红')
  return bad === 0
}

if (SELFTEST) {
  const ok = selftest()
  cleanupTemps()
  process.exit(ok ? 0 : 1)
}

const files = collect(join(ROOT, 'scripts'))
const hits = scan(files)

// --write-baseline：重录基线。这等于宣布「这些存量暂时不判红」，
// 必须显眼，不能和普通判定混在一条命令里，否则「顺手跑一下」就会把门禁洗绿。
if (WRITE_BASELINE) {
  const counts = writeBaseline(BASELINE_PATH, countKeysBy(hits, keyOf), BASELINE_COMMENT)
  console.log(
    `已写入基线：${BASELINE_PATH}\n  key ${Object.keys(counts).length} 个 / 命中 ${hits.length} 处`,
  )
  console.log('⚠️ 这一步等于宣布「这些存量暂时不判红」。请在提交信息里写清为什么。')
  process.exit(0)
}

if (hits.length === 0) {
  console.log(`扫描 ${files.length} 个 .mjs：没有从源码刮口令 / 没有硬编码口令兜底`)
  process.exit(0)
}
if (LIST) {
  console.log(`扫描 ${files.length} 个 .mjs，发现 ${hits.length} 处\n`)
  for (const h of hits) console.log(`  ${h.file}:${h.line}  [${h.rule}]  ${h.text}`)
  process.exit(0)
}

let baseline
try {
  baseline = loadBaseline(BASELINE_PATH)
} catch (e) {
  // 门禁自身报错（exit 2）≠ 判红（exit 1）：基线坏了是「判据跑不起来」。
  console.error(`[check-dev-pass-sourcing] 基线读取失败：${e.message}`)
  process.exit(2)
}
if (!baseline) {
  console.error(
    '[check-dev-pass-sourcing] 基线文件不存在：scripts/baselines/dev-pass-sourcing.json\n' +
      '  录基线：node scripts/check-dev-pass-sourcing.mjs --write-baseline',
  )
  process.exit(2)
}

const { newHits, removed } = diffRatchet(hits, baseline, keyOf)
const baselineCount = Object.values(baseline).reduce((a, b) => a + b, 0)
console.log(`扫描 ${files.length} 个 .mjs，命中 ${hits.length} 处`)
console.log(
  `基线棘轮：存量 ${baselineCount} 处（基线 key ${Object.keys(baseline).length} 个）→ ` +
    `本次实测 ${hits.length} 处，新增 ${newHits.length} 处，已消失 ${removed.length} 处`,
)

if (newHits.length) {
  console.error('\n❌ 以下是**新增**违规（不在基线里），必须改掉：')
  for (const h of newHits) console.error(`  ${h.file}:${h.line}  [${h.rule}]  ${h.text}`)
  console.error('\n修法：口令只从环境取（process.env.POCKET_PROBE_PASS / POCKET_MASTER），')
  console.error('      缺就**在碰设备之前**大声退出（exit 2），不要拿着空口令继续跑——')
  console.error('      那会把「未鉴权的 401」印成「路由有问题」。')
  console.error('若这确实是「已知且暂时接受」的存量，用 --write-baseline 重录，并在提交信息里写明为什么。')
  process.exit(1)
}
if (removed.length) {
  console.log(`\n✅ 无新增违规。另外有 ${removed.length} 处存量已消失（棘轮可以收紧了）：`)
  for (const k of removed.slice(0, 10)) console.log(`  - ${k}`)
  console.log('如需把基线同步收紧：node scripts/check-dev-pass-sourcing.mjs --write-baseline')
}
console.log(`\n✅ 无新增违规（存量 ${hits.length} 处不判红，这是棘轮的约定）。`)
process.exit(0)
