/**
 * 归类分批循环的终止性（§7dn）。
 *
 * 核心命题：**当服务端逐封分类全部失败时（classified:0 / remaining 恒大于 0），
 * 这个循环必须有出口。** 服务端侧的复现在
 * `backend/internal/server/server_email_classify_progress_test.go`
 * （网关报错 / 输出无法解析两种场景，各连打三次证明不收敛）。
 *
 * 用例设计要点：**每条断言都必须能被证伪**。所以这里不只测「新循环会停」，
 * 还测「老逻辑不会停」——把旧判定原样写进测试里跑一遍，让它在有界步数内
 * 被强制中断，从而把「不收敛」这件事也变成可复现的证据，而不是一句断言。
 */

import test from 'node:test'
import assert from 'node:assert/strict'
import { runClassifyLoop, classifyStopHint, DEFAULT_MAX_BATCHES } from '../email-classify-loop.ts'

/** 造一个「每轮 classified=0、remaining 恒定」的 fetchBatch，返回计数。 */
function stuckFetch(remaining) {
  const calls = { n: 0 }
  const fetchBatch = async () => {
    calls.n += 1
    return { classified: 0, remaining, results: [] }
  }
  return { calls, fetchBatch }
}

// ---------------------------------------------------------------------------
// 正常路径
// ---------------------------------------------------------------------------
test('有进展时一直跑到 remaining 归零', async () => {
  const seq = [5, 3, 1, 0]
  let i = 0
  const res = await runClassifyLoop({
    fetchBatch: async () => ({ classified: 2, remaining: seq[i++] }),
    onBatch: () => {},
    isCancelled: () => false,
  })
  assert.equal(res.stopped, 'drained')
  assert.equal(res.batches, 4)
  assert.equal(res.remaining, 0)
  assert.equal(res.classified, 8)
})

test('onBatch 按顺序收到每一批，且在 fetchBatch 之后', async () => {
  const order = []
  const seq = [3, 2, 1, 0]
  let i = 0
  await runClassifyLoop({
    fetchBatch: async () => {
      order.push('fetch')
      return { classified: 1, remaining: seq[i++] }
    },
    onBatch: (_b, idx) => {
      order.push(`on:${idx}`)
    },
    isCancelled: () => false,
  })
  assert.deepEqual(order, ['fetch', 'on:0', 'fetch', 'on:1', 'fetch', 'on:2', 'fetch', 'on:3'])
})

// ---------------------------------------------------------------------------
// 核心：零进展必须停
// ---------------------------------------------------------------------------
test('逐封全部失败（classified:0、remaining 恒定）时只发一批就停', async () => {
  const { calls, fetchBatch } = stuckFetch(5)
  const res = await runClassifyLoop({ fetchBatch, onBatch: () => {}, isCancelled: () => false })
  assert.equal(res.stopped, 'no-progress')
  assert.equal(calls.n, 1, '零进展时不得再发第二批 —— 这正是修复点')
  assert.equal(res.remaining, 5, 'remaining 必须如实带回，供调用方显示「还剩几封」')
})

test('老逻辑（只判 remaining<=0）在同一输入下不会停 —— 用来证明这条用例不是永真', async () => {
  const { calls, fetchBatch } = stuckFetch(5)
  // 原 use-email-inbox.ts 的判定，原样搬过来。
  let cancel = false
  let batches = 0
  const SAFETY = 50 // 真实代码没有这个上限；这里加它只是为了让测试能停下来
  do {
    await fetchBatch()
    batches += 1
    const remaining = 5
    if (cancel || remaining <= 0) break
  } while (!cancel && batches < SAFETY)

  assert.equal(calls.n, SAFETY, '老逻辑会一直打到人为上限；新逻辑 1 批就停')
  assert.equal(batches, SAFETY)
})

test('classified>0 但 remaining 没下降时也停（更隐蔽的零进展）', async () => {
  let calls = 0
  const res = await runClassifyLoop({
    fetchBatch: async () => {
      calls += 1
      // 声称分类了 3 封，但 remaining 纹丝不动 —— 写库没生效或统计口径不一致。
      return { classified: 3, remaining: 7, results: [] }
    },
    onBatch: () => {},
    isCancelled: () => false,
  })
  // 两批才停，不是三批也不是一批：第一批没有「上一轮」可拿来比
  // （remaining 是分类**之后**的数，服务端没告诉我们分类之前是多少），
  // 所以这一形态最早只能在第二轮被识破。这是有下限的，不是判据失效。
  assert.equal(calls, 2, '第二批发现 remaining 没下降就必须停')
  assert.equal(res.stopped, 'no-progress')
  assert.equal(res.classified, 6, '仍然如实累加已分类数，不因为判定为零进展就吞掉它')
})

test('第一批 remaining 就很大也不算零进展（用 Infinity 起手，不能误判）', async () => {
  const seq = [10000, 9999, 9998, 0]
  let i = 0
  const res = await runClassifyLoop({
    fetchBatch: async () => ({ classified: 1, remaining: seq[i++] }),
    onBatch: () => {},
    isCancelled: () => false,
  })
  assert.equal(res.batches, 4, '第一批不能被判成零进展')
  assert.equal(res.stopped, 'drained')
})

// ---------------------------------------------------------------------------
// 取消
// ---------------------------------------------------------------------------
test('用户取消后立刻停，且不再发新请求', async () => {
  const { calls, fetchBatch } = stuckFetch(5)
  let cancelled = false
  const res = await runClassifyLoop({
    fetchBatch: async () => {
      if (cancelled) throw new Error('取消后不得再发请求')
      const r = await fetchBatch()
      cancelled = true // 第一批之后用户点了取消
      return r
    },
    onBatch: () => {},
    isCancelled: () => cancelled,
  })
  assert.equal(res.stopped, 'cancelled')
  assert.equal(calls.n, 1)
})

test('开始前就已取消时，一次请求都不发', async () => {
  const { calls, fetchBatch } = stuckFetch(5)
  const res = await runClassifyLoop({ fetchBatch, onBatch: () => {}, isCancelled: () => true })
  assert.equal(res.stopped, 'cancelled')
  assert.equal(calls.n, 0)
})

// ---------------------------------------------------------------------------
// 硬上限兜底
// ---------------------------------------------------------------------------
test('批次数硬上限生效，且 stopped 如实为 batch-cap（不静默收工）', async () => {
  let calls = 0
  const res = await runClassifyLoop({
    // 每批都有进展（remaining 一直降），所以只有上限能拦住它。
    fetchBatch: async () => {
      calls += 1
      return { classified: 1, remaining: 1000 - calls, results: [] }
    },
    onBatch: () => {},
    isCancelled: () => false,
    maxBatches: 4,
  })
  assert.equal(calls, 4)
  assert.equal(res.stopped, 'batch-cap')
  assert.equal(res.batches, 4)
})

test('默认上限是 200 批，且不会被 maxBatches:0 之类的脏值绕过', async () => {
  assert.equal(DEFAULT_MAX_BATCHES, 200)
  for (const bad of [0, -1, undefined, NaN]) {
    let calls = 0
    const res = await runClassifyLoop({
      fetchBatch: async () => {
        calls += 1
        return { classified: 1, remaining: 1000 - calls, results: [] }
      },
      onBatch: () => {},
      isCancelled: () => false,
      maxBatches: bad,
    })
    assert.equal(res.batches, DEFAULT_MAX_BATCHES, `maxBatches=${bad} 时应回落到默认值`)
  }
})

// ---------------------------------------------------------------------------
// 畸形输入不许把它变成另一种死循环
// ---------------------------------------------------------------------------
test('remaining 缺失/非法时按 0 处理并正常收尾（不许 undefined >= prev 恒真而空转）', async () => {
  for (const bad of [undefined, null, NaN, 'abc', -1]) {
    let calls = 0
    const res = await runClassifyLoop({
      fetchBatch: async () => {
        calls += 1
        return { classified: 0, remaining: bad, results: [] }
      },
      onBatch: () => {},
      isCancelled: () => false,
    })
    assert.equal(calls, 1, `remaining=${String(bad)} 时应一轮收尾`)
    assert.equal(res.stopped, 'drained', `remaining=${String(bad)} 视为已排空`)
  }
})

// ---------------------------------------------------------------------------
// 终止原因 -> 提示文案
// ---------------------------------------------------------------------------
// 关键点：四种终止原因**不能**塌成同一句话。「分类器坏了所以我停了」和
// 「确实跑完了」在用户眼里必须一眼可分，否则就是又一个「数字分不清两种情况」。
test('四种终止原因给出四种可区分的文案', () => {
  const hints = ['drained', 'cancelled', 'no-progress', 'batch-cap'].map((stopped) =>
    classifyStopHint({ stopped, classified: 0 }, 7),
  )
  assert.equal(new Set(hints).size, 4, `四种终止原因产生了重复文案: ${JSON.stringify(hints)}`)
})

test('no-progress 的文案要说明是「已停止」并给出最可能的原因', () => {
  const hint = classifyStopHint({ stopped: 'no-progress', classified: 0 }, 7)
  assert.match(hint, /已停止/)
  assert.match(hint, /7 封未归类/)
  assert.match(hint, /AI 分类/, '必须指向最可能的原因，否则用户不知道下一步做什么')
  assert.doesNotMatch(hint, /归类完成/, '分类器没干活时不得说「完成」')
})

test('drained 且没有剩余时才是「归类完成」', () => {
  assert.equal(classifyStopHint({ stopped: 'drained', classified: 20 }, 0), '归类完成')
  assert.match(classifyStopHint({ stopped: 'drained', classified: 20 }, 3), /3 封未归类/)
})

test('batch-cap 的文案要说「可再点一次继续」，否则用户以为只能归这么多', () => {
  assert.match(classifyStopHint({ stopped: 'batch-cap', classified: 0 }, 900), /再点一次继续/)
})

test('no-progress 但本地已无剩余时不说「未归类」（避免自相矛盾）', () => {
  assert.equal(classifyStopHint({ stopped: 'no-progress', classified: 0 }, 0), '归类完成')
})
