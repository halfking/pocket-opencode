// baseline-ratchet.mjs —— 「存量不判红、只对新增判红」的基线棘轮，供多道债务门禁共用。
//
// 为什么需要它（2026-10-05）
// --------------------------
// 本仓有两道门是**债务计数器**而不是开关：
//   scripts/check-fixed-cdp-ports.mjs   硬编码 CDP 端口
//   scripts/check-dev-pass-sourcing.mjs  设备探针从源码刮口令 / 硬编码口令兜底
// 它们原本的形态都是「有任何命中就 exit 1」。而存量分别是 148 处与 24 处，
// 于是两道门**从写下那天起就永远红**。
//
// 永远红的门不是门，是装饰：它既不能真正拦住任何东西，也没人敢把它接进
// gates/CI（接进去的第一天就会把所有会话打红，于是被集体绕过）。
// docs/handoff/_part-4.86 §4.86.2 已经把这条写清楚了——
// 「要接必须是基线棘轮形态（只对新增违规失败），那是独立一件事，本轮没做」——
// 那个文件至今只有一次提交，所以本轮补上。
//
// 关键设计：key 里**绝对不能有行号**
// --------------------------------
// scripts/z-index-ladder.test.mjs 与 bottom-chrome-gate.test.mjs 的 ALLOWLIST
// 用的就是 `rel:line` 作 key，Windows 上路径反斜杠 + 行号漂移让 11 条
// ALLOWLIST 全被判「陈旧」（round43 §2.1）。行号一漂就误报，久而久之没人看它。
// 所以本模块的 key 由调用方给定，**规范上就是「文件 + 违规种类 + 违规内容」，
// 不含行号**；同一个 key 出现多次用计数容忍（「同款再多加一处」仍算新增）。
//
// 三态而不是两态
// --------------
// diffRatchet 返回 new / removed / unchanged 三类。把「观测不到」与
// 「观测到不通过」折叠成一个布尔值，重试与放行逻辑就会朝错误方向使劲
// （本仓已在 push 校验循环上真踩过：观测失败被当成推送失败，报了 4 次）。
//
// 用法：
//   const counts = countKeysBy(hits, (h) => `${h.file}|${h.kind}|${h.detail}`)
//   const { newHits, removed } = diffRatchet(hits, loadBaseline(path), keyFn)
//   writeBaseline(path, counts)          // 有意扩大存量时才调
import fs from 'node:fs'
import path from 'node:path'

export const defaultKeyFn = (h) => `${h.file}|${h.kind}|${h.detail}`

export function countKeysBy(hits, keyFn = defaultKeyFn) {
  const m = new Map()
  for (const h of hits) {
    const k = keyFn(h)
    m.set(k, (m.get(k) || 0) + 1)
  }
  return m
}

export function diffRatchet(hits, baseline, keyFn = defaultKeyFn) {
  const now = countKeysBy(hits, keyFn)
  const newHits = []
  const removed = []
  const unchanged = []
  for (const [k, n] of now) {
    const b = baseline[k] || 0
    if (n > b) {
      // 超出基线计数的部分逐条算新增，保留原始 hit 以便打印行号。
      for (const h of hits.filter((x) => keyFn(x) === k).slice(0, n - b)) newHits.push(h)
    }
    for (let i = 0; i < Math.min(n, b); i += 1) unchanged.push(k)
  }
  for (const [k, b] of Object.entries(baseline)) {
    const n = now.get(k) || 0
    for (let i = n; i < b; i += 1) removed.push(k)
  }
  return { newHits, removed, unchanged }
}

/**
 * 扫描器**零命中**时的拒绝闸。
 *
 * ★ 为什么必须有（2026-10-07 实测，不是推断）：
 *   本模块的 `writeBaseline` 已经拦住了「把空基线写出去」（那是本轮真踩过的坑），
 *   但**读路径当时没有任何保护**。实测把 `check-fixed-cdp-ports` 的扫描目录
 *   从 `scripts` 改成 `docs`（一个非常现实的路径笔误）⇒
 *     基线棘轮：存量 148 处 → 本次实测 **0** 处，新增 0 处，**已消失 148 处**
 *     ✅ 无新增违规。另外有 148 处存量已消失（棘轮可以收紧了）：
 *     ✅ 无新增违规（**存量 0 处不判红，这是棘轮的约定**）。  EXIT=0
 *   ⇒ 扫描器被打死，门禁报绿、把整份基线说成「债还了」，
 *     而且**最后一句还主动替这个 0 找了个理由**（「这是棘轮的约定」）。
 *     读起来就是「正确，债务已清」——这是本仓见过最恶劣的一次绿灯。
 *
 *   棘轮语义上「存量可以少」是对的，但**一次扫描从 148 直接掉到 0** 不可能是
 *   「还债」，只能是**扫描器瞎了**。这两件事必须分开判。
 *
 * 不满足条件时**什么都不做**（让调用方照常按 new/removed 判定）。
 * 抛 Error 而不是 exit，是为了让调用方自己决定怎么报；
 * 两个调用方目前都用 exit 3（沿用仓库既有的「拒绝给结论」口径）。
 *
 * ⚠️ 本函数的适用前提：**基线里那条「存量从不为 0」**。
 *   2026-10-08 曾把它接到 check-raw-error 上（那道门基线非空+零命中会误报
 *   「扫描器失灵」——因为那条违规是真的被还掉了），已撤回。
 *   那道门改用**活体正控**：往临时目录真放一个已知违规文件、走同一条扫描函数、
 *   要求必须被抓到。正控给出的是「扫描器确实活着」的**正面证据**，
 *   而本函数给出的是从基线推出的**推断**，两者不能互换。
 *   代价：本函数只适合「存量很大、一次性掉到 0 不可能是还债」的门。
 */
export function assertScannerNotBlind(hits, baseline, label) {
  const nBaseline = Object.keys(baseline || {}).length
  if (nBaseline > 0 && (!Array.isArray(hits) || hits.length === 0)) {
    throw new Error(
      `${label}：基线里有 ${nBaseline} 条存量，本次扫描却**一条都没命中**。\n` +
        '  这不可能是「还债」，只能是扫描器失灵（路径写错 / 规则表被清空 / 过滤条件过严）。\n' +
        '  先修扫描器。\n' +
        '  若你确认这笔债**确实全部还清**了（不是扫描器的问题），那就用\n' +
        '    --write-baseline --record-full-repayment "<理由>"\n' +
        '  显式记录——**只跑 --write-baseline 会被拒绝**，那是故意的：\n' +
        '  它正是这个「拒绝写空基线」闸与本守卫互相把对方指给对方的地方。',
    )
  }
}

export function loadBaseline(baselinePath) {
  if (!fs.existsSync(baselinePath)) return null
  const doc = JSON.parse(fs.readFileSync(baselinePath, 'utf8'))
  if (!doc || typeof doc.counts !== 'object' || doc.counts === null)
    throw new Error(`基线文件格式不对（缺 counts 对象）：${baselinePath}`)
  return doc.counts
}

/**
 * 写基线。默认**拒绝写空基线** —— 空基线 = 全部存量都算新增 = 门禁一接就红。
 *
 * ★ 为什么需要 `--record-full-repayment` 这条显式逃生口（2026-10-08 实测）：
 *   本函数的「拒绝空基线」与 `assertScannerNotBlind` 的「基线非空+零命中 ⇒ 失灵」
 *   **合起来是一个死锁**，而且它们的报错文案互相指向对方：
 *     · 跑门  →「真还了债就跑 --write-baseline」
 *     · 跑它  →「扫描到 0 处命中，拒绝写入空基线」
 *   ⇒ 「债真的全部还清」这件事，在这两道门里**无法被记录**。
 *   这是同一个缺陷在 §78（check-raw-error）与 §77（另外两道门）两处独立出现，
 *   说明它是**这一族守卫的结构性问题**，不是某一道门的笔误。
 *
 *   逃生口的设计取舍：不能用「扫描器自证活着」来放行（那对合成样本成立，
 *   对「扫描根写错」同样成立，§78 已实测）；也不能静默放行（空基线后患极大）。
 *   所以要求**人显式声明**「这笔债确实全部还清了」，并把这个声明**写进基线文件**，
 *   让下一个读基线的人看得见。声明必须是一个不好敲的独立开关，不是顺手能带上的参数。
 *
 * @param {object} [opts]
 * @param {string} [opts.recordFullRepayment] 非空则允许写空基线；该串会被写进文件作为声明
 */
export function writeBaseline(baselinePath, countMap, comment, opts = {}) {
  // ⚠️ 入参通常是 Map。直接 Object.keys(map) 会静默得到 []，于是写出「0 个 key」
  // 的空基线而不报任何错 —— 空基线 = 全部存量都算新增 = 门禁一接就红。
  // 本轮真踩过一次，所以这里显式转换并对空基线直接抛错。
  const src = countMap instanceof Map ? Object.fromEntries(countMap) : countMap || {}
  const declared = typeof opts.recordFullRepayment === 'string' ? opts.recordFullRepayment.trim() : ''
  if (Object.keys(src).length === 0 && !declared)
    throw new Error(
      '扫描到 0 处命中，拒绝写入空基线（空基线会把全部存量判成新增）。\n' +
        '  若这笔债**确实全部还清**了，用 --record-full-repayment "<理由>" 显式声明：\n' +
        '  那会把这份声明连同时间戳一起写进基线文件，下一个读它的人看得见。\n' +
        '  不带那个声明就写空基线，通常意味着扫描器瞎了。',
    )
  const sorted = {}
  for (const k of Object.keys(src).sort()) sorted[k] = src[k]
  const paidOff =
    Object.keys(src).length === 0
      ? `  "_fullRepayment": {\n    "declared": ${JSON.stringify(declared)},\n    "at": ${JSON.stringify(new Date().toISOString())}\n  },\n`
      : ''
  const body =
    '{\n' +
    `  "_comment": ${JSON.stringify(comment)},\n` +
    paidOff +
    '  "counts": ' +
    JSON.stringify(sorted, null, 2).split('\n').join('\n  ') +
    '\n}\n'
  fs.mkdirSync(path.dirname(baselinePath), { recursive: true })
  fs.writeFileSync(baselinePath, body, 'utf8')
  return sorted
}

/**
 * 棘轮自测用例。**每道用这套棘轮的门都必须跑它** ——
 * 只测「检测器能报」只证明敏感度；棘轮真正的风险是「接进来以后一直绿」，
 * 而一直绿既可能是债务清零，也可能是棘轮压根没生效，这两者必须分开。
 *
 * @param {object} o
 * @param {(hits:any[],baseline:object)=>{newHits:any[],removed:string[]}} o.diff  被测的 diff 函数
 * @param {(h:any)=>string} o.keyFn **必须**与 o.diff 用的是同一个 key 函数
 * @param {any[]} o.hitsBaselineFixture 基线内的一条命中
 * @param {any[]} o.hitsNewFixture 基线外的一条命中
 * @param {string} o.driftNote 漂移后基线里那个「旧行号」key 的说明
 * @returns {string[]} 失败信息数组（空 = 全过）
 *
 * ⚠️ keyFn 不是可选参数。曾经在这里硬编码 `${h.file}|${h.kind}|${h.detail}`，
 *    而第二道门（check-dev-pass-sourcing）的字段叫 `rule` / `text`，
 *    于是合成样本的 key 全落成 `synthetic.mjs|undefined|undefined`，
 *    5 条棘轮用例一起转红。**判据自测抓到了它，真仓库判定却是绿的**
 *    （真基线是用正确 keyOf 录的）—— 这正是「自测与主流程口径不一致」的样子：
 *    只跑主流程会把这个 bug 当成「棘轮工作正常」。
 */
export function ratchetSelfTestCases({ diff, keyFn = defaultKeyFn, inBaselineHit, newHit, driftKey, sameHit2 }) {
  const base = { [keyFn(inBaselineHit)]: 1 }
  const cases = [
    { name: '存量违规不判红', hits: [inBaselineHit], wantNew: 0, wantRemoved: 0 },
    {
      name: '行号漂移不判红（key 不含行号）',
      hits: [{ ...inBaselineHit, line: (inBaselineHit.line || 1) + 950 }],
      baseline: { ...base, [driftKey]: 1 },
      wantNew: 0,
      wantRemoved: 1,
    },
    { name: '同 key 计数超出基线 ⇒ 判红', hits: [inBaselineHit, sameHit2], wantNew: 1, wantRemoved: 0 },
    { name: '基线外的 key ⇒ 判红', hits: [inBaselineHit, newHit], wantNew: 1, wantRemoved: 0 },
    { name: '债务下降不判红（只报进度）', hits: [], wantNew: 0, wantRemoved: 1 },
  ]
  const fails = []
  for (const c of cases) {
    const got = diff(c.hits, c.baseline || base)
    const ok = got.newHits.length === c.wantNew && got.removed.length === c.wantRemoved
    if (!ok)
      fails.push(
        `棘轮自测「${c.name}」不符：期望 new=${c.wantNew}/removed=${c.wantRemoved}，` +
          `实际 new=${got.newHits.length}/removed=${got.removed.length}`,
      )
    else console.log(`  棘轮自测：${c.name} ✅（new=${got.newHits.length} removed=${got.removed.length}）`)
  }
  // 变盲对照：把「基线外的 key 必须判红」这一步拆掉，上面第 4 条就不再报红。
  // 差 1 才说明棘轮这一步承重；差 0 说明它压根没参与判定。
  {
    const all = [inBaselineHit, newHit]
    const full = diff(all, base).newHits.length
    const blind = diff(all, { [keyFn(inBaselineHit)]: 1, [keyFn(newHit)]: 1 }).newHits.length
    if (full - blind !== 1)
      fails.push(`棘轮变盲对照异常：棘轮失效后应少报 1 条，实际少 ${full - blind} 条`)
    else console.log(`  棘轮变盲对照：${full} → ${blind}（差 1，棘轮这一步确实承重）`)
  }
  return fails
}
