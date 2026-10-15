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

// 基线在扫描**之前**读：下面三道闸都要拿它做对比，读路径上的失败要早报。
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
function untranslatedOf(d, ref, refKeys) {
  return refKeys.filter((k) => d[k] === undefined || d[k] === ref[k])
}
const untranslated = (lang) => untranslatedOf(data[lang], data[REF], refKeys)
/** 一级命名空间，用来把欠账聚合成可执行的块 */
function nsOf(key) {
  return key.split('.')[0]
}

const langs = Object.keys(data).filter((l) => l !== REF).sort()
const debt = Object.fromEntries(langs.map((l) => [l, untranslated(l)]))

console.log(`【i18n 未翻译棘轮】基准 ${REF}，共 ${refKeys.length} 个 key，语言 ${langs.length + 1} 份`)

// ---- 检测链自检 + 三道拒绝给结论的闸 ----
//
// ★ 为什么要加（2026-10-08 实测，不是推断）：本门实测能拿到两种绿灯，
//   而且**都自称是进展**：
//   ① `refKeys` 被打空（en-US 被扫没了）⇒ 八种语言**逐个**打印
//      「⤵️ 未翻译已从 148/160/138/147/100/100/145/6 降到 0 —— 确认是主动进展」
//      随后「✅ 未翻译欠账未增长（棘轮通过）」EXIT=0。
//      这是本仓见过最可信的一次假绿灯：它把每种语言、每个数字都摆了出来，
//      人读了只会得出「翻译终于做完了」的结论。
//   ② `langs` 被打空（一种语言都没检）⇒ 「共 451 个 key，语言 **1** 份」
//      +「✅ 未翻译欠账未增长」EXIT=0。基线里那 8 种语言的欠账被整个跳过。
//
//   共同形状与 §68/§70/§72/§78 同族：**门的输入被打坏时不报「我不知道」，
//   而给一个看起来像进展的正面结论。**

/**
 * 判定逻辑自检：用合成对象走**同一条**判定函数，不依赖真实语料的状态。
 *
 * 三条期望值都是**逐键手算**出来的，不是跑一遍抄输出——否则就成了
 * 「断言现状」，代码怎么错都对。
 * 基准 SYN_REF = { 'a.b': 'Hello', 'a.c': 'World', 'n.s': 'Same' }
 *   ① d={a.b:'Hallo'}：
 *        a.b 'Hallo'≠'Hello' → 不算；a.c 缺 → **算**；n.s 缺 → **算** ⇒ ['a.c','n.s']
 *        （注意：缺 key 算未翻译，这是本卡的定义，见 untranslatedOf 的注释）
 *   ② d={a.b:'Hallo',a.c:'World',n.s:'Hallo'}：
 *        a.b 不同 → 不算；a.c 'World'==='World' → **算**；n.s 'Hallo'≠'Same' → 不算 ⇒ ['a.c']
 *   ③ 三条都真译 ⇒ []
 * 第一版把 ① 的期望写成 ['n.s']、② 写成 ['n.s']，被自检当场打脸——错的是夹具不是代码。
 */
const SYN_REF = { 'a.b': 'Hello', 'a.c': 'World', 'n.s': 'Same' }
const SYN_CASES = [
  { name: '缺 key 算未翻译（a.c/n.s 缺，a.b 已真译）', d: { 'a.b': 'Hallo' }, want: ['a.c', 'n.s'] },
  {
    name: '值与基准逐字相同算未翻译（只有 a.c 没翻）',
    d: { 'a.b': 'Hallo', 'a.c': 'World', 'n.s': 'Hallo' },
    want: ['a.c'],
  },
  { name: '全部真译 ⇒ 0 条', d: { 'a.b': 'Hallo', 'a.c': 'Welt', 'n.s': 'Gleich' }, want: [] },
]
for (const c of SYN_CASES) {
  const got = untranslatedOf(c.d, SYN_REF, Object.keys(SYN_REF))
  const ok = JSON.stringify(got) === JSON.stringify(c.want)
  console.log(`  判定自检：${c.name} ${ok ? '✅' : `❌ 期望 ${JSON.stringify(c.want)} 实得 ${JSON.stringify(got)}`}`)
  if (!ok) {
    console.error('\n❌ 判定逻辑自检未通过——本门此刻对「未翻译欠账」的任何结论都不可信。')
    console.error('   先修 untranslatedOf，别去调基线。')
    process.exit(3)
  }
}

// 闸 1：一种语言都没检。基线里 8 种语言的欠账会被整个跳过，而门报绿。
if (langs.length === 0) {
  console.error(`\n❌ ${locDir} 下**一种待检语言都没找到**（只认出了基准 ${REF}）。`)
  console.error('  这不是「翻译做完了」，是扫描没扫到东西。修 locales 目录或语言文件名。')
  process.exit(3)
}

// 闸 2：基线里记着、这次没检到的语言。删掉/改名一个语言文件，
// 会让它的欠账**静默退出棘轮**——基线里那条上限从此再没人对账。
const baseLangs = Object.keys(baseline.langs || {})
const langsLost = baseLangs.filter((l) => !langs.includes(l))
if (langsLost.length) {
  console.error(`\n❌ 基线里有 ${langsLost.length} 种语言，本次**没检到**：${langsLost.join(', ')}`)
  console.error('  这些语言的欠账上限从此不再被对账。')
  console.error('  若确实要移除某种语言，请手工编辑 ' + BASELINE + ' 的 langs 删掉它，再重跑本门。')
  console.error('  本门**不会**用 --update-baseline 替你做这件事：那等于让一次误伤永久生效。')
  process.exit(3)
}

// 闸 3：基准语言的 key 数下限。refKeys 被打空 ⇒ 每种语言的欠账都算成 0，
// 于是「148 降到 0」被当成主动进展报出来（见文件上方 ①）。
// 下限可以自由上调（新增 key 是好事），但**不能靠 --update-baseline 悄悄下调**。
//
// ⚠️ 引导路径必须放行（§78.6 同款死锁，实测刚踩了一次）：
//   「字段缺失」这一档要允许 --update-baseline 通过——报错文案让人跑它，
//   它自己却跑不了，那被告知的解法就是死胡同。
//   但「**数值变小**」那一档**不放行**：那种情况要让人手工改 JSON。
const bootstrapping = process.argv.includes('--update-baseline')
if (typeof baseline.refKeysFloor !== 'number') {
  if (!bootstrapping) {
    console.error(`\n❌ 基线里没有 refKeysFloor（基准语言 key 数下限）：${BASELINE}`)
    console.error('  没有它就无法判断「基准语言是不是被扫没了」。跑一次 --update-baseline 记进去。')
    process.exit(3)
  }
} else if (refKeys.length < baseline.refKeysFloor) {
  console.error(`\n❌ 基准语言 ${REF} 的 key 数从 ${baseline.refKeysFloor} 掉到 ${refKeys.length}。`)
  console.error('  这不可能是「翻译进展」，只会是：en-US.json 结构变了 / 解析器被改坏 / 文件被截断。')
  console.error('  此时每种语言的欠账都会被算成 0，门会把 0 报成「主动进展」。')
  console.error('  先修 en-US.json 或 flatten。真要下调这个下限，请手工编辑 ' + BASELINE + ' 的 refKeysFloor。')
  process.exit(3)
}

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
  // refKeysFloor 可以**自由上调**（给 en-US 新增 key 是好事），
  // 但**不能靠本命令悄悄下调**——§78.6 在另一道门上踩过同一个坑：
  // 一个「记录基线」的动作把基线写坏，而输出还打着 ✅。
  // 真要下调请手工编辑 JSON，那一眼是看得到的。
  baseline.refKeysFloor = Math.max(refKeys.length, baseline.refKeysFloor || 0)
  writeFileSync(BASELINE, JSON.stringify(baseline, null, 2) + '\n', 'utf8')
  console.log(`\n✅ 基线已更新：${langs.map((l) => `${l}=${baseline.langs[l]}`).join('  ')}`)
  console.log(`   基准 key 数下限：${baseline.refKeysFloor}（本次实测 ${refKeys.length}）`)
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
