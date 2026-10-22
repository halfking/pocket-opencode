// check-doc-section-number-uniqueness.mjs
// §960：主文档的**节号 / 小节号不许重号** —— 读者按 `§NNN` 找过去，
// 命中的是**两段互不相干**的内容时，「引用」就变成了「猜」。
//
// 为什么这道门是新的（既有门为什么看不见）
// -----------------------------------------
// 既有门 `check:doc-ref-integrity` 只判**死引用**（`§NNN` 在主文档里
// **没有实体**）⇒⇒⇒ **重号**对它完全不可见：
// 引用能解析 ⇒ 门绿 ⇒ 但读者会落到**另一段**内容上。
//
// ★ 本门抓到过什么（实测，2026-10-22）
//   上一轮合并（0fdda422）把两条互不相干的线并进同一份主文档：
//     · 「字节级行为门」侧   →  §895 / §896
//     · 「语义检索/向量化」侧 →  §895 D65 / §896 D66
//   ⇒ `### §895.1`–`§895.4` 与 `### §896.1`–`§896.5` **各有两份**
//   ⇒ 9 个**严格小节指针**同时指向两个不同的小节。
//   ⇒ 而 `check:doc-ref-integrity` 全程绿（引用都能解析）。
//
// 口径（两条规则 + 一条例外）
// --------------------------
//   A. `## §NNN`（**非** `D` 形态）—— 同一个 NNN 只许出现一次。
//   B. `### §NNN.M` —— 同一个 NNN.M 只许出现一次。
//      ★ **例外**：`§NNN.Mbis` / `§NNN.Mb` 这类**带字母后缀**的续写
//        是本仓既有约定（实测 8 对：818.6/818.6bis、819.4/819.4bis…）
//        ⇒ 「同一个后缀出现两次」才算重号；
//        ⇒ 「一个空后缀 + 若干带后缀」= 续写，**放行**。
//
// 豁免与棘轮
// ----------
// 存量（3 条）登记在 `scripts/doc-section-number-waivers.json`：
//   · 实测有、清单没有 ⇒ 🔴 新增重号（必须回填或补一条带理由的豁免）
//   · 清单有、实测没有 ⇒ ⛔ 豁免过期（必须删）
//   · 每条必须有 why ⇒ 豁免只能变少，不会变成永久藏污纳垢。
//
// ⚠ **量具下限哨兵**：主文档标题数低于 300 ⇒ **退出码 2（量具坏了）**
//   而不是 1 —— 口径一坏时报出来的是「0 个问题」，和真的干净完全同形。
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

const ROOT = join(dirname(fileURLToPath(import.meta.url)), '..')
const DOC = join(ROOT, 'docs', 'design', '2026-10-06-recording-quality-fixes.md')
const WAIVERS = join(ROOT, 'scripts', 'doc-section-number-waivers.json')

const MIN_HEADINGS = 300

// ── 扫描：把标题解析成「节号 / 小节号」两类 ────────────────────────────
//
// ⚠⚠ 数字必须**贪婪吃满**：`## §885 D55` 的节号是 **885**，不是 88。
//   首版正则写成 `^## §(\d+)(?![ \t]*D\d)` ⇒ 正则**回溯**到 "88"
//   ⇒⇒⇒ 把 8 个 `§88x` / `§89x` / `§90x` / `§91x` 全报成重号
//   ⇒⇒⇒ 而那 8 组其实是 `## §885 D55` 这种 **D 形态**标题。
//   ⇒⇒⇒ ⇒ 现在改成「先吃满数字，再看后缀」，**不回溯**。
export function scanHeadings(text) {
  const sections = new Map() // NNN -> [{ line, suffix, dForm }]
  const subs = new Map() // NNN.M -> [{ line, suffix }]
  const lines = text.split(/\r?\n/)
  lines.forEach((raw, i) => {
    const lineNo = i + 1
    let m = /^###\s+§(\d+)\.(\d+)([A-Za-z]*)(?![.\d])/.exec(raw)
    if (m) {
      const key = `${m[1]}.${m[2]}`
      if (!subs.has(key)) subs.set(key, [])
      subs.get(key).push({ line: lineNo, suffix: m[3] })
      return
    }
    m = /^##\s+§(\d+)([ \t]+D\d+)?\b/.exec(raw)
    if (m) {
      const key = m[1]
      if (!sections.has(key)) sections.set(key, [])
      sections.get(key).push({ line: lineNo, dForm: Boolean(m[2]), suffix: m[2] ? m[2].trim() : '' })
    }
  })
  return { sections, subs, headingCount: lines.filter((l) => /^#{1,6}\s/.test(l)).length }
}

// ── 判定 ────────────────────────────────────────────────────────────────
export function findViolations(scan) {
  const out = []
  // A. 普通形态的 `## §NNN` 重号（D 形态不算 —— 那是 D 系列的条目，身份由 D 号决定）
  for (const [key, hits] of scan.sections) {
    const plain = hits.filter((h) => !h.dForm)
    if (plain.length > 1) {
      out.push({ key: `section:${key}`, hits: plain.map((h) => h.line) })
    }
  }
  // B. `### §NNN.M` 重号：同一个**完整后缀**出现两次才算
  //    （空后缀 ×1 + 带后缀 ×N = 续写约定，放行）
  for (const [key, hits] of scan.subs) {
    const bySuffix = new Map()
    for (const h of hits) {
      if (!bySuffix.has(h.suffix)) bySuffix.set(h.suffix, [])
      bySuffix.get(h.suffix).push(h.line)
    }
    for (const [suffix, lines2] of bySuffix) {
      if (lines2.length > 1) {
        out.push({ key: `sub:${key}${suffix ? '.' + suffix : ''}`, hits: lines2 })
      }
    }
  }
  return out
}

function loadWaivers() {
  let doc
  try {
    doc = JSON.parse(readFileSync(WAIVERS, 'utf8').replace(/^\uFEFF+/, ''))
  } catch (e) {
    console.error(`[gates] ❌ 读不到豁免清单 ${WAIVERS}：${e.message}`)
    process.exit(2)
  }
  const list = Array.isArray(doc.waivers) ? doc.waivers : []
  const map = new Map()
  const bad = []
  for (const w of list) {
    if (!w || typeof w.key !== 'string' || !w.key) {
      bad.push('条目缺 key')
      continue
    }
    if (!w.why || !String(w.why).trim()) {
      bad.push(`${w.key} 缺 why`)
      continue
    }
    map.set(w.key, w)
  }
  return { map, bad, raw: list }
}

// ── 自证（量具自检）──────────────────────────────────────────────────────
function selftest() {
  const cases = [
    ['不同节号不报', '## §1 A\n## §2 B\n', 0],
    ['D 形态不算重号', '## §885 D55：x\n## §885 D56：y\n', 0],
    ['续写约定放行', '### §818.6 x\n### §818.6bis y\n', 0],
    ['同节号两处要报', '## §426 x\n## §426 y\n', 1],
    ['同小节号两处要报', '### §497.2 x\n### §497.2 y\n', 1],
    ['同后缀两处要报', '### §818.6bis x\n### §818.6bis y\n', 1],
    ['§885 D55 不算 §88 重号', '## §885 D55：x\n## §885 D56：y\n', 0],
    ['§885 D55 与普通 §885 并存不报', '## §885 D55：x\n## §885 y\n', 0],
  ]
  let pass = 0
  for (const [name, src, want] of cases) {
    const got = findViolations(scanHeadings(src)).length
    if (got === want) {
      pass++
      console.log(`  ✅ ${name}`)
    } else {
      console.log(`  ❌ ${name}（期望 ${want}，实际 ${got}）`)
    }
  }
  console.log(`\n量具自证 ${pass}/${cases.length} ${pass === cases.length ? '全通过' : '未通过'}`)
  if (pass !== cases.length) process.exit(1)
  console.log(`\n声明用例数：${cases.length}（MIN_SELFTEST_CASES 下限闸按此棘轮）`)
}

// ── 主流程 ──────────────────────────────────────────────────────────────
const argv = process.argv.slice(2)
if (argv.includes('--selftest')) {
  selftest()
  process.exit(0)
}

let text
try {
  text = readFileSync(DOC, 'utf8')
} catch (e) {
  console.error(`[gates] ❌ 读不到主文档 ${DOC}：${e.message}`)
  process.exit(2)
}
const scan = scanHeadings(text)
if (scan.headingCount < MIN_HEADINGS) {
  console.error(
    `[gates] ❌ 量具坏了：主文档只读到 ${scan.headingCount} 个标题（下限 ${MIN_HEADINGS}）—— ` +
      `口径一坏时报出来的是「0 个问题」，和真的干净完全同形。`,
  )
  process.exit(2)
}

const violations = findViolations(scan)
const { map, bad } = loadWaivers()
if (bad.length) {
  console.error(`[gates] ❌ 豁免清单有 ${bad.length} 条无效：\n` + bad.map((b) => `  - ${b}`).join('\n'))
  process.exit(2)
}

const seen = new Set()
const newOnes = []
for (const v of violations) {
  seen.add(v.key)
  if (!map.has(v.key)) newOnes.push(v)
}
const stale = [...map.keys()].filter((k) => !seen.has(k))

if (argv.includes('--meta')) {
  console.log(`[gates] 主文档：节 ${scan.sections.size} 个 / 小节 ${scan.subs.size} 个 / 标题 ${scan.headingCount} 个`)
  console.log(`[gates] 重号：实测 ${violations.length} 条 / 豁免 ${map.size} 条 / 新增 ${newOnes.length} 条 / 过期 ${stale.length} 条`)
  process.exit(0)
}

if (!newOnes.length && !stale.length) {
  console.log(`✅ 节号唯一性卡口通过：${violations.length} 条重号全部登记在案（scripts/doc-section-number-waivers.json），无新增、无过期。`)
  process.exit(0)
}

if (newOnes.length) {
  console.error(
    `[gates] ❌ 主文档有 ${newOnes.length} 处**节号/小节号重号**（读者按引用找过去会落到另一段内容上）：\n` +
      newOnes.map((v) => `  - ${v.key}  @行 ${v.hits.join(', ')}`).join('\n') +
      '\n处置：把其中一处改成**没被占用的号**（并同步它的入向引用），' +
      '或确认是「续写」约定后写进 scripts/doc-section-number-waivers.json 并附理由。',
  )
}
if (stale.length) {
  console.error(
    `[gates] ❌ 豁免清单里有 ${stale.length} 条**已过期**（实测已不重号，必须删）：\n` +
      stale.map((k) => `  - ${k}`).join('\n'),
  )
}
process.exit(1)
