// check-dead-features.mjs —— 「写好了没人接线」的**卡口**，范围限定在 features/meetings/。
//
// 为什么需要它：2026-10-07 查声纹库时发现，voiceprints-store.ts 的
// listVoiceprints / deleteVoiceprint / enrollFromAudio **都实现了、零调用方**，
// 界面上没有任何入口。后果不是编译错误，是「存错的声纹会一直污染后续所有会议
// 而且无法删除」—— 用户看得见的东西坏了，但所有门禁全绿。
//
// 做法是**棘轮**而不是要求清零：features/meetings/ 下已有一批历史死导出
// （filterMeetings / gatewayWorkType 等，其中 5 个「只有测试引用」），
// 一次删干净属于另一个 PR；本卡口保证**不再新增**，并把存量钉成可核对的基线。
//
// ⚠ 与 check:dead-api 的口径**故意不同**，别把它们当同一件事：
//   check-dead-api 分了 wired / testOnly / moduleInternal / dead 四类，棘轮只管 dead；
//   本门把「只有测试引用」也算进存量。理由是本门要回答的问题是
//   「这个能力**接进 App** 了吗」—— 只被测试引用的能力，运行时没有任何路径能到达，
//   和彻底死代码对用户是同一件事。加一条测试不该让一个符号从门禁视野里消失。
//   代价：谁给某个死函数补了测试，报告会从「死」变成「仅测试」，但仍在存量里，不翻红。
//
// ⚠ 判据的输入是 audit-dead-features.mjs 的 **--json** 输出，不是给人看的报表。
//   报表的分组、缩进、中文标题都在变；解析它等于把排版和门禁结论焊死。
//   audit 跑不起来或 JSON 解析不了 → 退出码 3（拒绝给结论），绝不当成「通过」。

import { execFileSync } from 'node:child_process'
import { existsSync, readFileSync, writeFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

const here = dirname(fileURLToPath(import.meta.url))
const AUDIT = join(here, 'audit-dead-features.mjs')
const BASELINE = join(here, 'dead-features-baseline.json')
const SCOPE = 'meetings'

/** 跑审计拿 JSON。任何拿不到结论的情况都必须抛出去，不能退化成「空 = 全通过」。 */
function collectDead() {
  let raw
  try {
    raw = execFileSync(process.execPath, [AUDIT, '--dir', SCOPE, '--json'], {
      encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'],
    })
  } catch (e) {
    const err = e
    // 审计自己退出 0 时 stdout 才有 JSON；它若以非 0 退出，那是「拒绝给结论」。
    console.error('✗ 审计跑不起来，拒绝给出「通过」结论：')
    console.error((err.stderr || '').trim() || (err.message || '').trim())
    process.exit(3)
  }
  let parsed
  try {
    parsed = JSON.parse(raw)
  } catch (e) {
    console.error(`✗ 审计输出不是合法 JSON，拒绝给出「通过」结论：${e && e.message}`)
    process.exit(3)
  }
  if (!Array.isArray(parsed?.dead)) {
    console.error('✗ 审计输出里没有 dead 数组（审计脚本可能改了协议），拒绝给出「通过」结论。')
    process.exit(3)
  }
  return parsed.dead
}

/**
 * 审计自带的 `--selftest` 必须先过，才准信任它的 dead 列表。
 *
 * ★ 为什么这道闸是必需的（2026-10-07 实测，不是推断）：
 *   自检跑的是**真实的 deadExports**，覆盖「只有声明 / 只有注释提到 / 只有测试提到 /
 *   跨文件调用 / 同文件自调用」四个易错样本。
 *   而探测器是会回归的，且**回归形态恰好是让本门变绿**——
 *   例如有人「简化」建 body 的代码时删掉 `.filter(不保留 export 行)`，
 *   于是每个声明行都含自己的名字 ⇒ hits ≥ 1 ⇒ **一个死导出都检不出**。
 *   实测那种情况下：自检 EXIT=1（明说「实得 []」），**而本门 EXIT=0**，
 *   还打出 11 行「已清掉的死能力」+ 一行绿色 ✅ —— 绿灯配误导文案。
 *   ⇒ 本门之前**从不调用自检**，等于把唯一能识别「探测器坏了」的东西闲置着。
 */
function assertDetectorTrustworthy() {
  try {
    execFileSync(process.execPath, [AUDIT, '--selftest'], {
      encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'],
    })
  } catch (e) {
    console.error('✗ 审计自检未通过 ⇒ 探测器不可信，拒绝给出「通过」结论。')
    console.error('  （下面那句「未被接线的导出未新增」在这种状态下是**假的**：')
    console.error('   探测器报不出东西时，本门会把整份基线当成「已清理」。）')
    const out = `${e.stdout || ''}${e.stderr || ''}`.trim() || (e.message || '').trim()
    console.error(out)
    console.error('\n先修审计（scripts/audit-dead-features.mjs），别去改基线。')
    process.exit(3)
  }
}

assertDetectorTrustworthy()

const cur = collectDead()
const key = (f, n) => `${f}:${n}`
const curKeys = cur.map((d) => key(d.file, d.name)).sort()

let baseline = { note: '', dead: [], waived: {} }
if (existsSync(BASELINE)) {
  try {
    baseline = JSON.parse(readFileSync(BASELINE, 'utf8'))
  } catch (e) {
    console.error(`❌ 基线解析失败：${BASELINE}\n   ${e && e.message}`)
    process.exit(2)
  }
}

if (process.argv.includes('--update-baseline')) {
  // ⚠ waived **必须原样保留**：--update-baseline 的语义是「刷新 dead 列表」，
  //   不是「重写整份基线」。若在这里丢掉了 waived，一次例行基线刷新就会把
  //   「有意不接」的登记悄悄洗掉，而那正是本门要防的事。
  const keptWaived = {}
  for (const [k, why] of Object.entries(baseline.waived || {})) {
    if (curKeys.includes(k)) keptWaived[k] = why
  }
  const dropped = Object.keys(baseline.waived || {}).filter((k) => !curKeys.includes(k))
  writeFileSync(BASELINE, JSON.stringify({
    note: `src/features/${SCOPE}/ 下「未被 App 引用」的导出符号；只许减少不许增加。含「仅测试引用」。`
      + `\nwaived = **有意不接**（不是缺口；接上去反而有害）。值是理由，不许留空。`,
    dead: curKeys,
    waived: keptWaived,
  }, null, 2) + '\n', 'utf8')
  console.log(`✅ 基线已更新（dead ${curKeys.length} 条 / waived ${Object.keys(keptWaived).length} 条）`)
  if (dropped.length) {
    console.log(`   ⤵️ 顺带清掉 ${dropped.length} 条已失效的 waived（该符号已不再「死」）：`)
    for (const d of dropped) console.log(`      ${d}`)
  }
  process.exit(0)
}

const baseSet = new Set(baseline.dead || [])

/**
 * 基线非空、检出为空 ⇒ 拒绝给结论。
 *
 * 自检只覆盖**探测逻辑**。它盖不住另一种坏法：`--json` 输出那条路本身出问题
 * （实测把 `dead: []` 写死进去，自检照样 4/4 通过）。
 * 而「基线 11 条 / 检出 0 条」这个组合只有两种可能：
 *   ① 探测器坏了（更常见，而且上面那句「已清掉」是假的）
 *   ② 确实全清干净了 —— 那就跑 --update-baseline，落盘后基线为空，本闸自然不再触发
 * 不区分这两者之前，本门不报「通过」。
 */
if (baseSet.size > 0 && curKeys.length === 0) {
  console.error(`✗ 基线里有 ${baseSet.size} 条死能力，审计却一条都没检出。`)
  console.error('  两种可能，本门不替你选：')
  console.error('    ① 探测器坏了 —— 上面那种「已清掉」全是假的；')
  console.error('    ② 你确实把它们全清了 —— 跑 --update-baseline 落盘，棘轮会认新基线。')
  process.exit(3)
}

// waived = **有意不接**。它与「还没接线」是**两件事**，混在一起会让本门
// 说错话：前者是设计决定，接上去反而有害；后者是缺口。
// 例：`voiceprints-store.enrollFromAudio` —— 声纹的**录入**走
// `recordingRuntime.labelSpeaker → upsertVoiceprintLabel`（开会时给说话人起名），
// 而 `enrollFromAudio` 内部调的是 `saveVoiceprint`，`recordingRuntime.ts:608`
// 明写「⚠ 用 upsertVoiceprintLabel 而不是 saveVoiceprint」——
// 接上它会重新引入「改一次名就多记一个样本」那个被刻意拆开的 bug。
const waived = baseline.waived || {}
const waivedKeys = Object.keys(waived)
const waivedSet = new Set(waivedKeys)

const added = curKeys.filter((x) => !baseSet.has(x) && !waivedSet.has(x))
const removed = (baseline.dead || []).filter((x) => !curKeys.includes(x))
// ★ stale 检查：登记为「有意不接」的符号，若已不再「死」（被接上或被删），
//   说明豁免过期了 —— 要么它变好了（该销掉登记），要么探测器漏报了（更糟）。
const staleWaived = waivedKeys.filter((k) => !curKeys.includes(k))
const emptyWhy = waivedKeys.filter((k) => !String(waived[k] || '').trim())

const gapCount = curKeys.length - waivedKeys.filter((k) => curKeys.includes(k)).length
console.log(`【死能力卡口】src/features/${SCOPE}/：未被 App 引用 ${curKeys.length} 个`
  + `（缺口 ${gapCount} + 有意不接 ${waivedKeys.length}；基线 ${baseSet.size} 条）`)

if (waivedKeys.length) {
  console.log()
  console.log('有意不接（不是缺口 —— 接上去反而有害，理由见基线）：')
  for (const k of waivedKeys.sort()) {
    const live = curKeys.includes(k)
    console.log(`  ${live ? '✓' : '⚠'} ${k}${live ? '' : '  ← 已不再「死」，登记过期'}`)
    console.log(`      ${String(waived[k] || '').trim() || '⚠ 理由为空 —— 这不是登记，是占位'}`)
  }
}

if (added.length) {
  console.error()
  for (const a of added) {
    const d = cur.find((x) => key(x.file, x.name) === a)
    const why = d?.testOnly ? '（仅测试引用：测试能调到它，App 调不到）' : ''
    console.error(`❌ 新增未被接线的导出：${a}${why}`)
  }
  console.error()
  console.error('两种可能，选一种：① 真接上去（写调用方 + 界面入口）；② 确实是死代码，删掉导出。')
  console.error('若是有意保留的历史债，跑 --update-baseline 落盘，并在基线注释里写明为什么不接。')
  process.exit(1)
}
if (removed.length) {
  console.log()
  // ⚠ 措辞刻意**不用**「已清掉」：那三个字读起来像进展，而实际情况有两种
  //   （真清理了 / 探测器漏报了），本门区分不了就别替它下结论。
  for (const r of removed) console.log(`⤵️ 基线里有、本次未检出：${r} —— 若确已清理，跑 --update-baseline 落盘。`)
}
if (staleWaived.length || emptyWhy.length) {
  console.error()
  for (const k of staleWaived) {
    console.error(`❌ waived 登记已过期：${k} —— 它不再出现在「未被 App 引用」里。`)
    console.error('   两种可能：① 它被接上了或被删了 ⇒ 销掉这条登记；② 探测器漏报了 ⇒ 门无牙。')
  }
  for (const k of emptyWhy) {
    console.error(`❌ waived 缺理由：${k} —— 「有意不接」必须写清为什么，否则它就是缺口。`)
  }
  console.error('   修法：编辑基线 dead-features-baseline.json 的 waived 字段。')
  process.exit(1)
}

console.log(`\n✅ 未被接线的导出未新增（缺口 ${gapCount}，棘轮通过；有意不接 ${waivedKeys.length} 条已登记）`)
process.exit(0)