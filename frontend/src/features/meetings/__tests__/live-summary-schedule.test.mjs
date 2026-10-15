// live-summary-schedule.test.mjs — §424：滚动摘要的**调度**。
//
// ── 这道门补的是哪个洞（与 §418 的门不是同一个洞）──
//
// §418 立的门 `live-summary-refresh-gate.test.mjs` 守的是
// 「**这一轮要不要跑**」（那个 or 闸）+ 接线层。
// 本门守的是它上游的另一半：「**这一轮什么时候被调用**」——
// debounce 上限与在飞守卫。而后者此前的读数是：
//
//     if (throttleTimer) clearTimeout(throttleTimer)
//     throttleTimer = setTimeout(() => refresh(), 2000)
//     watch(segments, () => scheduleRefresh(), { deep: true })
//
// ⭐ 两个缺陷方向**相反**，根因同一个（这段调度没有纪律）：
//
//   分段间隔 < 2000ms  ⇒ 纯 debounce 被无限推后 ⇒ 整场会议刷新 **1** 次
//   分段间隔 > 2000ms 且模型慢 ⇒ 每次变化都起一轮、无在飞守卫
//                                ⇒ 同一时刻在飞 ~9 个 summary 请求，
//                                   且**后完成的旧轮会盖掉先完成的新轮**
//
// 用户需求「在录音时需要进行即时总结」在前一种节奏下**整场停摆**，零报错；
// 而 `useLiveSummary.ts` 的文件头写的是「节流更新」——声明与实现不一致。
//
// ── 判据为什么必须剥注释 ──
//
// 本轮的修法把旧代码**逐字抄进了注释**（`setTimeout(() => refresh(), 2000)`），
// 于是任何 `源码.includes(...)` 形式的判据都会被**它自己要判的那条注释**满足。
// §421.3 的同款纪律：否定式源码断言先剥注释再查。

import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

import { nextRefreshDelay, LIVE_SUMMARY_DEBOUNCE_MS } from '../live-summary-schedule.ts'

const HERE = dirname(fileURLToPath(import.meta.url))
const SCHEDULE_SRC = stripJsComments(
  readFileSync(join(HERE, '..', 'live-summary-schedule.ts'), 'utf8'),
)
const COMPOSABLE_SRC = stripJsComments(
  readFileSync(join(HERE, '..', '..', '..', 'composables', 'useLiveSummary.ts'), 'utf8'),
)

const CEILING = 30_000 // 生产传进来的 SUMMARY_INTERVAL_MS
const TEN_MIN = 600_000

test('§424 · 调度延迟', async (t) => {
  await t.test('前提自证：刚落一段、距上次成功更新不久、上一轮没在飞 ⇒ 等满静默期', () => {
    const r = nextRefreshDelay({
      sinceSegmentMs: 0, sinceRefreshMs: 1000, inFlight: false,
      debounceMs: 2000, ceilingMs: CEILING,
    })
    assert.equal(r.delayMs, 2000)
    assert.equal(r.reason, 'debounce')
  })

  await t.test('★ ceiling：距上次成功更新已超上限 ⇒ 立即刷新（防饿死的那一条）', () => {
    const r = nextRefreshDelay({
      sinceSegmentMs: 0, sinceRefreshMs: 35_000, inFlight: false,
      debounceMs: 2000, ceilingMs: CEILING,
    })
    assert.equal(r.delayMs, 0)
    assert.equal(r.reason, 'ceiling')
  })

  await t.test('★⭐ ceiling 判据是「距上次成功更新」不是「距上次分段变化」', () => {
    // 分段钟永远是 0（scheduleRefresh 就是在分段变化时被调的），
    // 若 ceiling 拿它当判据，这一分支永远为假 ⇒ 加了 ceiling 等于没加。
    // 本模块第一版就是这么错的，此处把它钉死。
    const 分段刚到 = nextRefreshDelay({
      sinceSegmentMs: 0, sinceRefreshMs: 60_000, inFlight: false,
      debounceMs: 2000, ceilingMs: CEILING,
    })
    assert.equal(分段刚到.reason, 'ceiling', '分段钟=0 而成功钟已超时 ⇒ 必须走 ceiling')

    const 都在上限内 = nextRefreshDelay({
      sinceSegmentMs: 0, sinceRefreshMs: CEILING - 1, inFlight: false,
      debounceMs: 2000, ceilingMs: CEILING,
    })
    assert.equal(都在上限内.reason, 'debounce')
  })

  await t.test('ceiling 边界：差一个毫秒都不算超时', () => {
    const under = nextRefreshDelay({
      sinceSegmentMs: 0, sinceRefreshMs: CEILING - 1, inFlight: false,
      debounceMs: 2000, ceilingMs: CEILING,
    })
    const at = nextRefreshDelay({
      sinceSegmentMs: 0, sinceRefreshMs: CEILING, inFlight: false,
      debounceMs: 2000, ceilingMs: CEILING,
    })
    assert.notEqual(under.reason, 'ceiling')
    assert.equal(at.reason, 'ceiling')
  })

  await t.test('★ 在飞优先于 ceiling：上一轮还在跑就等，绝不返回 0', () => {
    // 两个钟都已经远超上限也不许立刻再发一次，否则就是并发叠加；
    // 而返回 0 会变成忙等空转（一轮 25 秒能转上万次）。
    const r = nextRefreshDelay({
      sinceSegmentMs: 99_000, sinceRefreshMs: 99_000, inFlight: true,
      debounceMs: 2000, ceilingMs: CEILING,
    })
    assert.equal(r.reason, 'in-flight')
    assert.ok(r.delayMs > 0, `在飞时 delayMs 必须 >0，实测 ${r.delayMs}`)
  })

  await t.test('静默期内不早于静默期结束（不得因为分段变化就立刻刷）', () => {
    const r = nextRefreshDelay({
      sinceSegmentMs: 500, sinceRefreshMs: 500, inFlight: false,
      debounceMs: 2000, ceilingMs: CEILING,
    })
    assert.equal(r.delayMs, 1500)
    assert.equal(r.reason, 'debounce')
  })

  await t.test('扫描面下限：三条 reason 都必须真的会被产出', () => {
    const seen = new Set()
    for (let s = 0; s <= 40_000; s += 250) {
      for (let r = 0; r <= 40_000; r += 7000) {
        for (const inFlight of [false, true]) {
          seen.add(nextRefreshDelay({
            sinceSegmentMs: s, sinceRefreshMs: r, inFlight,
            debounceMs: 2000, ceilingMs: CEILING,
          }).reason)
        }
      }
    }
    // 三条都 < 4 就说明扫描面退化了（不是「代码变简单了」）
    assert.deepEqual([...seen].sort(), ['ceiling', 'debounce', 'in-flight'])
  })
})

/**
 * 虚拟时钟，驱动**真身** `nextRefreshDelay`。
 * `fixed=false` 复刻旧实现（硬编码 2000ms debounce、无在飞守卫）。
 *
 * ⚠ 这里建模的是生产里的**两个钟**：`lastSegmentAt` 每次分段变化重置，
 *   `lastRefreshAt` 只在一轮**成功完成后**才重置。
 *   第一版把 ceiling 挂在 `lastSegmentAt` 上（恒为 0）⇒ harness 会绿而生产仍饿死，
 *   这条教训已写进 live-summary-schedule.ts 的文件头。
 */
function simulate({ gapMs, refreshMs, fixed, durationMs = TEN_MIN }) {
  const queue = []
  let now = 0
  let seq = 0
  let timer = null
  let inFlight = 0
  let maxInFlight = 0
  let lastSegmentAt = 0
  let lastRefreshAt = 0
  const started = []

  // ⚠ 必须返回 **id**：`queue.push(...)` 返回的是新长度，不是 id。
  //   第一版直接 `return queue.push(...)` ⇒ clearT 按「长度」去找任务，
  //   经常清错/清不到 ⇒ 旧实现的读数变成 400 次而不是 1 次（量具骗了自己）。
  const setT = (fn, ms) => {
    const id = ++seq
    queue.push({ id, at: now + ms, fn })
    return id
  }
  const clearT = (id) => {
    const i = queue.findIndex((t) => t.id === id)
    if (i >= 0) queue.splice(i, 1)
  }
  // ⚠ 步数上限：某些「改回去」的写法会让调度变成**忙等**（在飞时返回 0ms ⇒
  //   重排立刻再到期 ⇒ 零延迟自旋）。那正是这道门要报的缺陷，但一个无上限的
  //   虚拟时钟会**挂死**而不是报红 ⇒ 台子被卡住、读数永远出不来。
  //   这里把「空转」显式变成一个会抛出的读数，而不是让整场测试卡住。
  const STEP_LIMIT = 200_000
  let steps = 0
  const runUntil = (limit) => {
    while (now < limit) {
      if (++steps > STEP_LIMIT) {
        throw new Error(
          `忙等空转：虚拟时钟在 ${limit}ms 之前已经跑了 ${steps} 个定时器任务（时钟没有前进）。` +
          `这是「在飞时返回 0ms」那类改法的直接后果 —— 生产里就是一个 0ms 定时器自旋。`,
        )
      }
      queue.sort((a, b) => a.at - b.at)
      const t0 = queue[0]
      if (!t0 || t0.at > limit) { now = limit; break }
      now = t0.at
      queue.shift()
      t0.fn()
    }
  }
  const schedule = () => {
    if (timer) clearT(timer)
    const delayMs = fixed
      ? nextRefreshDelay({
          sinceSegmentMs: now - lastSegmentAt,
          sinceRefreshMs: now - lastRefreshAt,
          inFlight: inFlight > 0,
          debounceMs: 2000,
          ceilingMs: CEILING,
        }).delayMs
      : 2000
    timer = setT(() => {
      // 旧实现没有入口守卫 ⇒ 直接开跑；新实现重排一次。
      if (fixed && inFlight > 0) { schedule(); return }
      inFlight++
      maxInFlight = Math.max(maxInFlight, inFlight)
      started.push(now)
      setT(() => {
        inFlight--
        lastRefreshAt = now
      }, refreshMs)
    }, delayMs)
  }

  for (let at = 0; at <= durationMs; at += gapMs) {
    runUntil(at)
    now = at
    lastSegmentAt = at
    schedule()
  }
  runUntil(durationMs + refreshMs + 10_000)
  return { started: started.length, maxInFlight }
}

test('§424 · ★ 场景一：分段比静默期更密 ⇒ 不得被饿死', () => {
  // refreshMs=0 是对旧实现**最有利**的假设（模型瞬间返回）。
  const old = simulate({ gapMs: 1500, refreshMs: 0, fixed: false })
  const now_ = simulate({ gapMs: 1500, refreshMs: 0, fixed: true })
  console.log(`  读数 · 10 分钟会议 / 分段每 1500ms：旧实现刷新 ${old.started} 次（峰值并发 ${old.maxInFlight}）` +
    ` ⇒ 修复后 ${now_.started} 次（峰值并发 ${now_.maxInFlight}）`)

  assert.equal(old.started, 1, `前提：旧实现在 1500ms 分隔下全场只刷新 ${old.started} 次（应恰为 1）`)
  assert.ok(
    now_.started >= 8,
    `加了 ceiling 后 10 分钟会议只刷新 ${now_.started} 次；` +
    `每轮至少占 ceiling+静默期，${CEILING}ms 上限下界为 8 次。旧实现是 ${old.started} 次。`,
  )
})

test('§424 · ★ 场景二：分段比静默期稀 + 慢模型 ⇒ 同时在飞的请求必须 ≤1', () => {
  // refreshMs=25000 取自本仓自记录的常态（§35.7：网关 auto → deepseek-v4-flash
  // 首字节 24~142s，agent 预算 30s）。这不是设备计时假设，是仓内事实。
  const old = simulate({ gapMs: 3000, refreshMs: 25_000, fixed: false })
  const now_ = simulate({ gapMs: 3000, refreshMs: 25_000, fixed: true })
  console.log(`  读数 · 10 分钟会议 / 分段每 3000ms + 慢模型 25s：旧实现刷新 ${old.started} 次（峰值并发 ${old.maxInFlight}）` +
    ` ⇒ 修复后 ${now_.started} 次（峰值并发 ${now_.maxInFlight}）`)

  assert.ok(
    old.maxInFlight > 1,
    `前提：旧实现在 3000ms 分隔 + 25s 慢模型下峰值并发只有 ${old.maxInFlight}，说明场景没打中`,
  )
  assert.equal(
    now_.maxInFlight, 1,
    `加了在飞守卫后峰值并发仍是 ${now_.maxInFlight} —— 慢模型下会叠加，且旧轮会盖掉新轮`,
  )
})

test('§424 · 接线层：纯函数不得变成第二个没人调的实现', () => {
  const callRe = /nextRefreshDelay\s*\(/
  assert.match(
    COMPOSABLE_SRC, callRe,
    'useLiveSummary.ts 没有调用 nextRefreshDelay —— 纯模块会变成第二个没人用的实现',
  )
  assert.match(
    COMPOSABLE_SRC,
    /nextRefreshDelay\s*\(\s*\{[^}]*sinceRefreshMs:[^}]*ceilingMs:\s*SUMMARY_INTERVAL_MS/s,
    '必须同时传 sinceRefreshMs 与 ceilingMs: SUMMARY_INTERVAL_MS（否则 ceiling 不可达，见模块文件头 ⭐）',
  )
  assert.match(
    COMPOSABLE_SRC,
    /async function refresh\(force = false\)\s*\{[\s\S]{0,400}?if \(isUpdating\.value\)/,
    'refresh() 入口没有在飞守卫 ⇒ 慢模型下并发叠加，且旧轮会盖掉新轮',
  )

  // ⚠ 剥过注释才查：旧代码逐字出现在注释里，不剥会被自己的注释满足。
  const hardcoded = /setTimeout\(\s*\(\)\s*=>\s*refresh\(\)\s*,\s*2000\s*\)/
  assert.doesNotMatch(
    COMPOSABLE_SRC, hardcoded,
    '调度仍是硬编码的 2000ms debounce（无 ceiling）⇒ 分段间隔 <2000ms 时即时总结整场停摆',
  )

  // 同形负控：上面三条正则必须能分辨「有接线」与「没接线」，不能恒真。
  // 把接线整段抠掉再问一次 —— 必须变成不匹配。
  const stripped = COMPOSABLE_SRC
    .replace(/if \(isUpdating\.value\)[\s\S]{0,120}?return\n\s*\}\n/, '')
    .replace(/nextRefreshDelay\s*\([\s\S]*?\n\s*\}/, 'DELETED')
  assert.ok(!callRe.test(stripped), '同形负控：抠掉接线后 callRe 仍匹配 ⇒ 正向断言恒真')
  assert.ok(
    !/async function refresh\(force = false\)\s*\{[\s\S]{0,400}?if \(isUpdating\.value\)/.test(stripped),
    '同形负控：抠掉在飞守卫后接线判据仍匹配',
  )
})

test('§424 · 模块自身：默认值不得藏在实现里', () => {
  assert.match(SCHEDULE_SRC, /export const LIVE_SUMMARY_DEBOUNCE_MS\s*=\s*\d+/)
  assert.match(
    SCHEDULE_SRC,
    /const ceiling = ceilingMs \?\? Number\.POSITIVE_INFINITY/,
    'ceiling 缺省必须是 Infinity（由产品常量给），不得在模块里编一个默认值',
  )
  assert.doesNotMatch(
    SCHEDULE_SRC, /sinceRefreshMs\s*>=\s*ceiling\s*===\s*false/,
    'ceiling 判据写成了反向条件',
  )
})

/** 与 meeting-action-items-source.test.mjs 同源：去掉 // 与块注释，
 *  字符串字面量内的内容按整体处理。 */
function stripJsComments(src) {
  let out = ''
  let i = 0
  let state = 'code'
  while (i < src.length) {
    const c = src[i]
    const d = src[i + 1]
    if (state === 'code') {
      if (c === '/' && d === '/') { state = 'line'; i += 2; continue }
      if (c === '/' && d === '*') { state = 'block'; i += 2; continue }
      if (c === "'") state = 'sq'
      else if (c === '"') state = 'dq'
      else if (c === '`') state = 'tpl'
      out += c
      i++
      continue
    }
    if (state === 'line') {
      if (c === '\n') { state = 'code'; out += '\n' }
      i++
      continue
    }
    if (state === 'block') {
      if (c === '*' && d === '/') { state = 'code'; i += 2; continue }
      if (c === '\n') out += '\n'
      i++
      continue
    }
    if (c === '\\') { out += c + (d ?? ''); i += 2; continue }
    if ((state === 'sq' && c === "'") || (state === 'dq' && c === '"') || (state === 'tpl' && c === '`')) {
      state = 'code'
    }
    out += c
    i++
  }
  return out
}
