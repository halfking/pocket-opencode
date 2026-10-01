// check-i18n-translated.mjs — i18n「未翻译」棘轮卡口。
//
// 为什么要有它：现有 check-i18n-keys.mjs 只校验「代码在用的 key 在每份语言文件里都存在」，
// **完全不校验值是否翻译**。实测（2026-10-01）以 en-US 为基准：
//   de-DE 148 / es-ES 144 / fr-FR 153 / ko-KR 138 / pt-BR 146 / ja-JP 100 / zh-TW 100 / zh-CN 6
// 条 key 的值与 en-US **完全相同**，而 check:i18n 全绿。
// 换句话说这些欠账是**静默通过**的，卡口从没为它们负过责。
//
// 为什么不直接 exit 1 要求清零：这批债是成片的（settings/nav/routes 整块还是英文），
// 一次改完属于大规模内容变更，不该由一个卡口顺手决定。本卡口做**棘轮**：
//   - 基线存在 i18n-untranslated-baseline.json，钉住当前每种语言的未翻译条数；
//   - 任何语言的欠账**增加** → exit 1（不许变差）；
//   - 欠账**减少** → 提示「基线可以下调」，需显式跑 --update-baseline 才落盘。
// 这样债既可见、又不阻塞日常提交，改一批就能降一次基线。
//
// 用法：
//   node scripts/check-i18n-translated.mjs                 # 卡口（gates 用这个）
//   node scripts/check-i18n-translated.mjs --update-baseline  # 确认是主动进展后下调基线
//   node scripts/check-i18n-translated.mjs --list          # 打印欠账明细（按命名空间聚合）
import { readFileSync, writeFileSync, readdirSync, existsSync } from 'node:fs'
import { join, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'

const here = dirname(fileURLToPath(import.meta.url))
const locDir = join(here, '..', 'src', 'locales')
const BASELINE = join(here, 'i18n-untranslated-baseline.json')
const REF = 'en-US'

function flatten(obj, prefix = '', out = {}) {
  for (const [k, v] of Object.entries(obj)) {
    const key = prefix ? `${prefix}.${k}` : k
    if (v && typeof v === 'object' && !Array.isArray(v)) flatten(v, key, out)
    else out[key] = v
  }
  return out
}

const files = readdirSync(locDir).filter((f) => f.endsWith('.json'))
const data = Object.fromEntries(
  files.map((f) => [f.replace('.json', ''), flatten(JSON.parse(readFileSync(join(locDir, f), 'utf8')))]),
)
if (!data[REF]) {
  console.error(`❌ 找不到基准语言 ${REF}.json`)
  process.exit(2)
}
const refKeys = Object.keys(data[REF])

/** 未翻译 = 值与 en-US 逐字相同（缺 key 也算，它同样会显示成 en-US 或 key 本身） */
function untranslated(lang) {
  const d = data[lang]
  return refKeys.filter((k) => d[k] === undefined || d[k] === data[REF][k])
}
/** 一级命名空间，用来把欠账聚合成可执行的块 */
function nsOf(key) {
  return key.split('.')[0]
}

const langs = Object.keys(data).filter((l) => l !== REF).sort()
const debt = Object.fromEntries(langs.map((l) => [l, untranslated(l)]))

console.log(`【i18n 未翻译棘轮】基准 ${REF}，共 ${refKeys.length} 个 key，语言 ${langs.length + 1} 份`)

if (process.argv.includes('--list')) {
  for (const l of langs) {
    const byNs = {}
    for (const k of debt[l]) (byNs[nsOf(k)] ||= []).push(k)
    console.log(`\n--- ${l}：${debt[l].length} 条未翻译`)
    for (const [ns, ks] of Object.entries(byNs).sort((a, b) => b[1].length - a[1].length)) {
      console.log(`    ${ns.padEnd(16)} ${String(ks.length).padStart(3)}`)
    }
    if (debt[l].length) {
      console.log(`    示例: ${debt[l].slice(0, 6).map((k) => `${k}="${data[REF][k]}"`).join('  ')}`)
    }
  }
  process.exit(0)
}

const update = process.argv.includes('--update-baseline')
let baseline = { ref: REF, note: '每种语言允许的未翻译 key 条数上限；只许下调，不许上调。', langs: {} }
if (existsSync(BASELINE)) {
  try {
    baseline = JSON.parse(readFileSync(BASELINE, 'utf8'))
  } catch (e) {
    console.error(`❌ 基线文件解析失败：${BASELINE}\n   ${e.message}`)
    process.exit(2)
  }
  if (baseline.ref !== REF) {
    console.error(`❌ 基线的基准语言是 ${baseline.ref}，与当前 ${REF} 不符，拒绝混用`)
    process.exit(2)
  }
}

const grew = []
const shrunk = []
for (const l of langs) {
  const n = debt[l].length
  const cap = baseline.langs?.[l]
  if (cap === undefined) {
    if (!update) {
      console.error(`❌ 基线里没有 ${l}。新语言请显式跑一次 --update-baseline 确认基线。`)
      process.exit(1)
    }
    baseline.langs[l] = n
    continue
  }
  if (n > cap) grew.push([l, cap, n])
  else if (n < cap) shrunk.push([l, cap, n])
  if (!update) {
    const mark = n > cap ? '❌' : n < cap ? '⤵️' : '✅'
    console.log(`  ${mark} ${l.padEnd(6)} ${String(n).padStart(3)} / 上限 ${cap}`)
  }
}

if (update) {
  for (const l of langs) baseline.langs[l] = debt[l].length
  writeFileSync(BASELINE, JSON.stringify(baseline, null, 2) + '\n', 'utf8')
  console.log(`\n✅ 基线已更新：${langs.map((l) => `${l}=${baseline.langs[l]}`).join('  ')}`)
  process.exit(0)
}

if (grew.length) {
  console.error()
  for (const [l, cap, n] of grew) {
    console.error(`❌ ${l} 未翻译从 ${cap} 涨到 ${n}（+${n - cap}）。i18n 卡口不负责翻译，但不许新增欠账。`)
    const added = debt[l].slice(0, 8)
    console.error(`   当前欠账示例：${added.join(', ')}`)
  }
  console.error()
  console.error('处理：把 en-US.json 里新增的 key 同步翻译进该语言文件。')
  console.error('如果这是主动补齐历史欠账导致的重算，跑 --update-baseline 落盘新基线。')
  process.exit(1)
}

if (shrunk.length) {
  console.log()
  for (const [l, cap, n] of shrunk) {
    console.log(`⤵️ ${l} 未翻译已从 ${cap} 降到 ${n} —— 确认是主动进展后跑 --update-baseline 落盘。`)
  }
}
console.log('\n✅ 未翻译欠账未增长（棘轮通过）')
process.exit(0)
