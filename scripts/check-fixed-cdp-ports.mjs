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
//   node scripts/check-fixed-cdp-ports.mjs            # 有硬编码则 exit 1，并列出
//   node scripts/check-fixed-cdp-ports.mjs --list     # 只列出，恒 exit 0
//   node scripts/check-fixed-cdp-ports.mjs --selftest # 证明判据能转红/转绿
//
// ⚠️ 判据本身必须先证明有区分力，否则又是一道恒真的门。
//    --selftest 就是干这个的：拿**合成样本**跑同一套检测逻辑，
//    断言「该报的报、该放过的放过」。不是去改真文件（那会污染工作区）。
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

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

// ---- --selftest：证明判据能转红也能转绿 ----
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

  if (fails.length) {
    console.error('❌ 判据自测失败：')
    for (const f of fails) console.error('   ' + f)
    process.exit(1)
  }
  console.log('✅ 判据自测通过：3 类硬编码都能报出，5 类合法写法都能放过，变瞎对照会漏报')
}

const argv = process.argv.slice(2)
if (argv.includes('--selftest')) { selftest(); process.exit(0) }

const files = walk(path.join(ROOT, 'scripts'))
const all = []
for (const f of files) {
  const rel = path.relative(ROOT, f).replace(/\\/g, '/')
  all.push(...detectFixedCdpPort(fs.readFileSync(f, 'utf8'), rel))
}
all.sort((a, b) => a.file.localeCompare(b.file) || a.line - b.line)

const byKind = {}
for (const h of all) byKind[h.kind] = (byKind[h.kind] || 0) + 1

console.log(`扫描 ${files.length} 个 .mjs，硬编码固定 CDP 端口 ${all.length} 处`)
for (const [k, v] of Object.entries(byKind)) console.log(`  ${k}: ${v}`)
if (all.length) {
  console.log('')
  for (const h of all) console.log(`  ${h.file}:${h.line}  [${h.kind}] ${h.detail}`)
}
console.log('\n修法：改成 lib/adb-cdp.mjs 的 openCdp()，或至少 `forward tcp:0` + finally 里 --remove。')

process.exit(argv.includes('--list') || all.length === 0 ? 0 : 1)
