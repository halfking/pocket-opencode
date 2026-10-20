// live-summary-refresh-gate.test.mjs — §418：滚动摘要**「这一轮要不要跑」**的闸。
//
// ── 为什么这道门存在（且以前一道都没有）──
//
// `useLiveSummary.refresh()` 里那条闸此前是**内联的一行**：
//
//     if (!force && !topicChanged && newCount < 3 && elapsed < 30_000) return
//
// 三个条件、两个常量都在，但**没有任何行为断言**验证过
// 「哪个条件单独就能触发」。这与 §52 在 `planRefine` 上修掉的是**同一个形状**：
// 决策内联在 composable 里 ⇒ 它一行行为门都没有 ⇒ 改坏了没有任何测试会响。
// 本轮把决策提成纯函数 `shouldRefreshSummary` 并用这道门钉住它的语义。
//
// ⭐ 钉的是**四选一的或**，不是「每 30 秒一次」：
//
//     force || topicChanged || newCount >= 3 || elapsed >= 30_000
//
// 这条事实有三处用户可见的后果，所以值得钉：
//   ① 密集讨论时实际节奏快于 30 秒（仓库里有两处曾写成单一的「每 30 秒」）；
//   ② **每轮都跟着跑一次 `/recommend` 的 LLM 调用** ⇒ 调用次数由分段到达速度决定；
//   ③ 「主题切换时立刻刷新」是产品特性 —— 把它误改成纯节流会悄悄砍掉它。
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, it } from 'node:test'

import { shouldRefreshSummary, SUMMARY_INTERVAL_MS, SUMMARY_SEGMENT_THRESHOLD } from '../live-summary-gate.ts'

const HERE = path.dirname(fileURLToPath(import.meta.url))
const SRC = path.resolve(HERE, '..', '..', '..')
const LIVE_SRC = readFileSync(path.join(SRC, 'composables', 'useLiveSummary.ts'), 'utf8')

/** 一组「四个条件都不成立」的基线入参，逐条只放开一个。 */
const QUIET = { force: false, topicChanged: false, newCount: 1, elapsedMs: 1_000 }

describe('§418 · 滚动摘要的触发闸', () => {
  it('前提自证：基线确实一个条件都不成立', () => {
    assert.equal(shouldRefreshSummary(QUIET), false,
      '基线就触发了 ⇒ 下面「各自单独触发」那几条证明不了任何东西')
  })

  it('★ 四个触发条件必须**各自单独**成立', () => {
    const cases = [
      { what: 'force（换场/强制）', patch: { force: true } },
      { what: '主题切换', patch: { topicChanged: true } },
      // elapsed=0：只有 newCount 这一条可能放行它 ⇒ 证明不是靠时间兜的底
      { what: '新增 ≥3 段', patch: { newCount: 3, elapsedMs: 0 } },
      { what: '距上次 ≥30s', patch: { newCount: 0, elapsedMs: 30_000 } },
    ]
    for (const { what, patch } of cases) {
      assert.equal(shouldRefreshSummary({ ...QUIET, ...patch }), true,
        `只满足「${what}」时不刷新 ⇒ 这一条触发条件是死的`)
    }
  })

  it('★ 边界：差一个都不行，差一个都必须行', () => {
    // ⚠ 边界值跟着**导出的常量**走，不写死数字：
    //   否则有人把阈值从 3 改成 5 时，这条判据会红，而那未必是缺陷 ——
    //   判据该在「语义被改坏」时红，不在「数字被改了」时红。
    const T = SUMMARY_SEGMENT_THRESHOLD
    const I = SUMMARY_INTERVAL_MS
    assert.equal(T, 3, `默认阈值变成了 ${T} —— 本判据的边界值按 3 推的，请一并复核`)
    assert.equal(I, 30_000, `默认间隔变成了 ${I} —— 同上`)
    assert.equal(shouldRefreshSummary({ ...QUIET, newCount: T - 1, elapsedMs: I - 1 }), false,
      `差一个（${T - 1} 段 / ${I - 1}ms）就刷新 ⇒ 阈值形同虚设，成本会翻倍`)
    assert.equal(shouldRefreshSummary({ ...QUIET, newCount: T, elapsedMs: 0 }), true)
    assert.equal(shouldRefreshSummary({ ...QUIET, newCount: 0, elapsedMs: I }), true)
  })

  it('首轮那一轮必然刷新（elapsed = now - 0 ⇒ 极大值）', () => {
    // 首轮 lastUpdateAt = 0 ⇒ elapsed 是 Date.now() ⇒ 远大于阈值。
    // 这里用一个小值模拟「刚刚」以证明闸门对 elapsed 是**单调**的。
    assert.equal(shouldRefreshSummary({ newCount: 1, elapsedMs: 30_000 }), true)
  })

  it('阈值可注入（测试与将来的 A/B 复核不必改常量）', () => {
    assert.equal(
      shouldRefreshSummary({ force: false, topicChanged: false, newCount: 10, elapsedMs: 0 }),
      true, '默认 3 段应触发')
    assert.equal(
      shouldRefreshSummary({ force: false, topicChanged: false, newCount: 10, elapsedMs: 0, segmentThreshold: 20 }),
      false, '把阈值抬到 20 段之后，10 段不该触发 ⇒ 形参没接上')
    assert.equal(
      shouldRefreshSummary({ force: false, topicChanged: false, newCount: 0, elapsedMs: 60_000, intervalMs: 120_000 }),
      false, '把间隔抬到 2 分钟之后，60s 不该触发 ⇒ 形参没接上')
  })

  it('★★ 接线层：refresh() 必须真的走这个闸（否则纯函数是第二个 listMeetingTodos）', () => {
    // §402 那条判据的同款理由：`mergeSummaryReferences` 完全可能变成
    // 「有测试、有导出、生产侧没人调」的死函数。
    assert.match(LIVE_SRC, /shouldRefreshSummary\(/,
      'useLiveSummary 里找不到 shouldRefreshSummary 的调用')
    const inline = LIVE_SRC.match(/if\s*\(!force\s*&&\s*!topicChanged/)
    assert.equal(inline, null,
      '内联旧闸还在：两处闸并存 ⇒ 改一处就会与另一处不一致，且看不出哪条生效')
  })

  it('★★ 负控：把闸误改成「纯 30s 节流」（与而不是或）必须判红', () => {
    // 同形负控：样本与真实语义**形状一致**（同样四个字段、同样的比较），
    // 只是把「或」写成「与」—— 这正是最可能被"顺手优化"出来的形态。
    const andNotOr = (i) => Boolean(i.force && i.topicChanged && i.newCount >= 3 && i.elapsedMs >= 30_000)
    for (const patch of [{ force: true }, { topicChanged: true }, { newCount: 3, elapsedMs: 0 }]) {
      assert.equal(andNotOr({ ...QUIET, ...patch }), false,
        '负控前提失效：样本竟然会触发')
      assert.equal(shouldRefreshSummary({ ...QUIET, ...patch }), true,
        '真实现与负控同形 ⇒ 量具坏了，不是判据坏了')
    }
  })
})

// ══════════════════════════════════════════════════════════════════════
// §924 · 滚动摘要「这一轮送什么」
// ══════════════════════════════════════════════════════════════════════
//
// ── 这道门守的是什么 ──
//
// 服务端 `buildSummaryPrompt` 的契约是「已有摘要 + **新增转写**」
// ⇒ 请求体里的 `segments` 本该是「这一轮新增的部分」。
// 而前端此前每 30 秒把**整场会议**当「新增转写」发过去
// ⇒ 请求体与提示词都 O(会议时长)、累计 O(时长²)；
// ⇒ 且服务端那条一次性 chat 路径不截断转写 ⇒ 45 秒预算被撑爆
// ⇒ 回落浏览器内 32000 **字节**闸 ⇒ §916 的「裁尾」让长会议上的摘要
//    **静默退化成「最后那 37 分钟」**，而用户看不到任何提示。
//
// ⚠ 为什么判据分两半（行为 + 接线）──
//   `useLiveSummary.ts` 的 import **无扩展名** ⇒ `node --test` import 不了它
//   ⇒ 行为只能钉在提出来的纯函数上（§418 同一个理由）；
//   而「纯函数对了但生产没接上」是本仓最常见的一种假绿
//   ⇒ 必须另加一条**接线扫描** + **负控**证明扫描器有牙。
import { selectSummarySegments, selectRecommendSegments, RECOMMEND_MIN_TEXT_RUNES } from '../live-summary-payload.ts'

/** 服务端 `segmentsToText` 的渲染形态（backend/internal/server/server_meeting.go）。 */
function segmentsToText(segs) {
  return segs.map((s) => `[${s.speaker || '说话人'}] ${s.text}\n`).join('')
}

/** 服务端 `maxRunes = 2000` 的截断（按**码元**取尾部）。 */
function serverWindow(transcript) {
  const runes = Array.from(transcript)
  return runes.length > 2000 ? runes.slice(runes.length - 2000).join('') : transcript
}

/** 造一段会议：每段 n 个中文字。 */
function makeSegments(count, runesEach) {
  return Array.from({ length: count }, (_, i) => ({
    id: `s${i}`,
    speaker: '说话人',
    text: '议'.repeat(runesEach),
  }))
}

describe('§924 · 滚动摘要这一轮送哪几段', () => {
  it('★ 首轮必须送全量（此时还没有「已并入」的任何段）', () => {
    const segs = makeSegments(5, 10)
    const sel = selectSummarySegments(segs, [])
    assert.equal(sel.segments.length, 5, '首轮只送了一部分 ⇒ 第一份摘要从一开始就是残缺的')
    assert.deepEqual(sel.sentIds, ['s0', 's1', 's2', 's3', 's4'])
  })

  it('★★ 第二轮只送新增的那几段（这条是本节修的那个行为本体）', () => {
    const segs = makeSegments(5, 10)
    const first = selectSummarySegments(segs, [])
    const grown = [...segs, { id: 's5', speaker: '说话人', text: '新内容' }]
    const second = selectSummarySegments(grown, first.sentIds)
    assert.deepEqual(second.segments.map((s) => s.id), ['s5'],
      '第二轮把已送过的段又送了一遍 ⇒ 请求体又是 O(会议时长)')
    assert.equal(second.segments.length, 1)
  })

  it('★★ 连续三轮：每一轮都只带自己那一段（累计 O(时长²) 消失）', () => {
    let sent = []
    const seen = []
    for (let i = 0; i < 3; i++) {
      const segs = makeSegments(i + 1, 10)
      const sel = selectSummarySegments(segs, sent)
      seen.push(sel.segments.map((s) => s.id))
      sent = sel.sentIds
    }
    assert.deepEqual(seen, [['s0'], ['s1'], ['s2']],
      '某一轮带上了历史段 ⇒ 这一轮不是增量')
  })

  it('★ 整份替换（seedSegments 把库里那场灌回来）后，新 id 必须整段重发', () => {
    // ⚠ 这是「按下标切」会踩的坑：数组被整份替换后，
    //   按下标切会切到新数组的中间 ⇒ 新会议的前半段被静默跳过。
    // ⚠ 替换后的数组必须**比原来长**：等长时按长度切的形态恰好蒙对 ⇒ 抓不到它。
    const old = makeSegments(6, 10)
    const first = selectSummarySegments(old, [])
    const replaced = makeSegments(10, 10).map((s, i) => ({ ...s, id: `new${i}` }))
    const sel = selectSummarySegments(replaced, first.sentIds)
    assert.deepEqual(sel.segments.map((s) => s.id), replaced.map((s) => s.id),
      '替换后的新段没被整段认成新增 ⇒ 新会议的内容一段都进不了摘要')
  })

  it('★ 没有新增段时仍然送全量（强制刷新必须能出摘要）', () => {
    const segs = makeSegments(3, 10)
    const first = selectSummarySegments(segs, [])
    const again = selectSummarySegments(segs, first.sentIds)
    assert.equal(again.segments.length, 3, '没有新内容时送了空 ⇒ 强制刷新会拿到一份空摘要')
    assert.equal(again.mode, 'all')
  })

  it('★ 水位线只认「当前数组里有的 id」（被换掉的 id 不再算数）', () => {
    const segs = makeSegments(3, 10)
    const first = selectSummarySegments(segs, [])
    assert.deepEqual(first.sentIds, ['s0', 's1', 's2'])
    assert.equal(first.sentIds.length, new Set(first.sentIds).size, '水位线里有重复 id')
  })

  it('★★ 推荐链：短会议送全量（与服务端今天收到的完全一样）', () => {
    const segs = makeSegments(10, 10) // 100 码元，远小于预算
    const sel = selectRecommendSegments(segs)
    assert.equal(sel.length, segs.length, '短会议被截了 ⇒ 服务端提示词变了')
  })

  it('★★★ 推荐链：长会议只送尾部，且服务端截出来的提示词**逐字不变**', () => {
    // 这是本节最要紧的一条性质：省体积**不能**改变模型看到的内容。
    const segs = makeSegments(400, 100) // 40000 码元
    const sel = selectRecommendSegments(segs)
    assert.ok(sel.length < segs.length, '长会议没有被截 ⇒ 请求体还是 O(会议时长)')
    assert.ok(sel.length > 0, '截成了空')
    // 服务端只看最后 2000 个渲染码元 ⇒ 两边必须一模一样
    assert.equal(
      serverWindow(segmentsToText(sel)),
      serverWindow(segmentsToText(segs)),
      '截尾部之后服务端看到的提示词变了 ⇒ 这是行为变更，不是优化',
    )
    // 尾部必须**足够大**，否则上面那条相等只是因为两边都短
    const sentRunes = Array.from(sel.map((s) => s.text).join('')).length
    assert.ok(sentRunes >= RECOMMEND_MIN_TEXT_RUNES,
      `尾部只覆盖了 ${sentRunes} 码元 < 预算 ${RECOMMEND_MIN_TEXT_RUNES} ⇒ 送少了`)
  })

  it('★ 推荐链：尾部必须是**后缀**（最新内容），不能是任意一段', () => {
    const segs = makeSegments(400, 100)
    const sel = selectRecommendSegments(segs)
    assert.equal(sel[sel.length - 1].id, segs[segs.length - 1].id, '尾部不是以最新一段收尾')
    assert.ok(segs.indexOf(sel[0]) > 0, '尾部从数组中间开始了')
  })

  it('★ 推荐链：正文不足预算时不得截（截了就等于改了提示词）', () => {
    const segs = makeSegments(3, 10)
    assert.equal(selectRecommendSegments(segs).length, 3)
    assert.equal(selectRecommendSegments([], 10).length, 0)
  })

  it('★★ 接线层：摘要请求必须送「选出来的增量」，不是整场会议', () => {
    const args = callArgs(LIVE_SRC, 'meetingsApi.summarize')
    assert.ok(args, 'useLiveSummary 里找不到 meetingsApi.summarize 的调用')
    assert.equal(args[1], 'payloadSegments',
      `摘要请求的第 2 个实参是 ${args[1]} ⇒ 送的不是增量`)
    assert.match(LIVE_SRC, /selectSummarySegments\(/,
      'useLiveSummary 里找不到 selectSummarySegments 的调用')
  })

  it('★★ 接线层：推荐请求必须送「有界的尾部」', () => {
    const args = callArgs(LIVE_SRC, 'meetingsApi.recommend')
    assert.ok(args, 'useLiveSummary 里找不到 meetingsApi.recommend 的调用')
    assert.match(args[1], /selectRecommendSegments\(/,
      `推荐请求的第 2 个实参是 ${args[1]} ⇒ 送的不是有界尾部`)
  })

  it('★★ 负控：把摘要请求改回「送整场会议」必须被抓出来', () => {
    // 与真实源码**同形**的样本：只把第 2 个实参换回去。
    const mutated = LIVE_SRC.replace(
      /meetingsApi\.summarize\(\s*\n\s*id,\s*\n\s*payloadSegments,/,
      'meetingsApi.summarize(\n        id,\n        cleanSegments,',
    )
    assert.notEqual(mutated, LIVE_SRC, '负控样本没改成功 ⇒ 变异没落盘')
    const args = callArgs(mutated, 'meetingsApi.summarize')
    assert.equal(args[1], 'cleanSegments',
      '扫描器没看出「送整场会议」⇒ 接线判据没有牙')
  })

  it('★★ 负控：注释里写一个调用形态不得被当成调用', () => {
    const code = maskNonCode(`
      // meetingsApi.summarize(id, cleanSegments, prev)
      /* meetingsApi.recommend(id, cleanSegments, summary) */
    `)
    assert.equal(callArgs(code, 'meetingsApi.summarize'), null,
      '注释里的调用形态被算成了真调用')
    assert.equal(callArgs(code, 'meetingsApi.recommend'), null,
      '注释里的调用形态被算成了真调用')
  })
})

/**
 * 在**剥过注释与字符串**的源码里取某个调用的实参列表。
 *
 * ⚠ 为什么要剥字符串内容：判据里出现过「`meetingsApi.summarize(...)`」这种
 *   **说明文字**时，它不是调用 —— 不剥就会被算成真调用（假阳性）。
 * ⚠ 为什么要按括号配平切分：实参里有数组/对象/嵌套调用时，
 *   按逗号裸切会把一个实参切成几个。
 */
function callArgs(src, callee) {
  const code = maskNonCode(src)
  const at = code.indexOf(callee + '(')
  if (at < 0) return null
  let i = at + callee.length + 1
  let depth = 1
  const args = []
  let cur = ''
  while (i < code.length) {
    const c = code[i]
    if (c === '(' || c === '[' || c === '{') {
      depth++
      cur += c
      i++
      continue
    }
    if (c === ')' || c === ']' || c === '}') {
      depth--
      if (depth === 0) break
      cur += c
      i++
      continue
    }
    if (c === ',' && depth === 1) {
      args.push(cur.trim())
      cur = ''
      i++
      continue
    }
    cur += c
    i++
  }
  if (cur.trim()) args.push(cur.trim())
  return args
}

/**
 * 掩掉注释**正文**与字符串**内容**（引号本身与换行保留）。
 *
 * ⚠ 与 features/notes/__tests__/note-live-summary-gap.test.mjs 里那份**同款**
 *   （那里踩过一次坑：只处理定界符、不抹正文 ⇒ 注释里的 `useLiveSummary(` 会被当真调用）。
 */
function maskNonCode(src) {
  const out = src.split('')
  const n = src.length
  let state = 'code'
  let i = 0
  while (i < n) {
    const c = src[i]
    const c2 = src[i + 1]
    if (state === 'code') {
      if (c === '/' && c2 === '/') { out[i] = ' '; out[i + 1] = ' '; state = 'line'; i += 2; continue }
      if (c === '/' && c2 === '*') { out[i] = ' '; out[i + 1] = ' '; state = 'block'; i += 2; continue }
      if (c === "'") { state = 'sq'; i++; continue }
      if (c === '"') { state = 'dq'; i++; continue }
      if (c === '`') { state = 'tq'; i++; continue }
      i++
      continue
    }
    if (state === 'line') {
      if (c === '\n') { state = 'code'; i++; continue }
      out[i] = ' '
      i++
      continue
    }
    if (state === 'block') {
      if (c === '*' && c2 === '/') { out[i] = ' '; out[i + 1] = ' '; state = 'code'; i += 2; continue }
      if (c !== '\n') out[i] = ' '
      i++
      continue
    }
    if (c === '\\') {
      out[i] = ' '
      if (i + 1 < n && src[i + 1] !== '\n') out[i + 1] = ' '
      i += 2
      continue
    }
    if ((state === 'sq' && c === "'") || (state === 'dq' && c === '"') || (state === 'tq' && c === '`')) {
      state = 'code'
      i++
      continue
    }
    if (c !== '\n') out[i] = ' '
    i++
  }
  return out.join('')
}