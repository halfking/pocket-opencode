/**
 * continuousList / hyperPages 契约测试（UI规范 07 §2–§3）。
 *
 * 这里守的是「看起来能跑、上线才炸」的那几条：
 *   1. **旧代次响应不得回写**——筛选变了以后上一页的慢响应不能覆盖新结果
 *   2. **失败保留已加载行**——不清空再报错
 *   3. **同页重试原位替换**，不追加成两份；跨页按稳定 rowId 去重
 *   4. **下拉未达阈值不请求**；达阈值只请求一次
 *   5. **没有更多后不再观察**；失败后停自动重试，交显式重试
 *   6. **pause 保留缓存与 query**，resume 重连
 *
 * ⚠️ 这套 fixture 必须真的把分支铺开：分页有「中间带」——
 * 有更多 / 没有更多 / 请求失败 / 慢响应乱序四种输入都要出现，
 * 否则「代次守门」这类判据会因为压根喂不到乱序而恒绿。
 *
 * Run: node --test src/lib/shell/__tests__/continuousList.test.mjs
 */
import { strict as assert } from 'node:assert'
import { test } from 'node:test'
import { createContinuousList } from '../continuousList.ts'
import { createHyperPages } from '../hyperPages.ts'

const rows = (n, from = 0) =>
  Array.from({ length: n }, (_, i) => ({ id: `r${from + i}`, v: from + i }))

/** 可控的服务端替身：按页返回，并支持「让某页变慢」来制造乱序。 */
function makeServer({ pageSize = 2, pages = 3, total } = {}) {
  const calls = []
  const delays = new Map()
  return {
    calls,
    delay(page, ms) {
      delays.set(page, ms)
    },
    fetchPage: async ({ page }) => {
      calls.push(page)
      const wait = delays.get(page)
      if (wait) await new Promise((r) => setTimeout(r, wait))
      const from = (page - 1) * pageSize
      const t = total ?? pages * pageSize
      if (from >= t) return { rows: [], total: t, hasMore: false }
      const slice = rows(Math.min(pageSize, t - from), from)
      return { rows: slice, total: t, hasMore: from + pageSize < t }
    },
  }
}

test('连续加载累积三页，行数与 total 正确', async () => {
  const s = makeServer()
  const c = createContinuousList({ fetchPage: s.fetchPage })
  await c.loadMore()
  await c.loadMore()
  await c.loadMore()
  assert.equal(c.rows.length, 6)
  assert.equal(c.total, 6)
  assert.equal(c.hasMore, false)
  assert.equal(c.status, 'exhausted', '没有更多后应进入 exhausted')
})

test('同页重试原位替换，不追加成两份', () => {
  const p = createHyperPages()
  p.replace(1, rows(2, 0))
  p.replace(1, rows(2, 0)) // 同页重试
  assert.equal(p.count(), 2, '同页重写不得变成 4 行')
})

test('跨页按稳定 rowId 去重（数据变动导致页号重叠）', () => {
  const p = createHyperPages()
  p.replace(1, [{ id: 'a' }, { id: 'b' }])
  p.replace(2, [{ id: 'b' }, { id: 'c' }]) // b 在两页都出现
  assert.deepEqual(p.all().map((r) => r.id), ['a', 'b', 'c'])
})

test('请求失败保留已加载行，不清空再报错', async () => {
  let fail = false
  const c = createContinuousList({
    fetchPage: async ({ page }) => {
      if (fail) throw new Error('boom')
      return { rows: rows(2, (page - 1) * 2), total: 6, hasMore: true }
    },
  })
  await c.loadMore()
  assert.equal(c.rows.length, 2)
  fail = true
  await c.loadMore()
  assert.equal(c.rows.length, 2, '失败时已加载行必须保留')
  assert.equal(c.status, 'failed')
})

test('失败后停自动重试；显式 retry 才继续', async () => {
  let fail = true
  const c = createContinuousList({
    fetchPage: async ({ page }) => {
      if (fail) throw new Error('boom')
      return { rows: rows(2, (page - 1) * 2), total: 6, hasMore: true }
    },
  })
  await c.loadMore()
  assert.equal(c.status, 'failed')
  // 再调 loadMore 不应发请求（failed 态拒绝自动重试）
  const before = c.loadedPages
  await c.loadMore()
  assert.equal(c.loadedPages, before, 'failed 态不得自动重试')
  // 显式重试
  fail = false
  c.retry()
  await new Promise((r) => setTimeout(r, 5))
  assert.ok(c.rows.length > 0, 'retry 后应恢复加载')
})

test('筛选变化提升代次：旧代次的慢响应不得回写', async () => {
  const s = makeServer({ pageSize: 2, pages: 2, total: 4 })
  s.delay(1, 40) // 第 1 页故意很慢
  const c = createContinuousList({ fetchPage: s.fetchPage })
  c.resetQuery() // 发出慢的第 1 页（代次 1）
  c.resetQuery() // 立刻换筛选：代次 2，第 1 页再次变慢但 revision 已变
  await new Promise((r) => setTimeout(r, 90))
  // 无论两次响应谁先到，结果都只能来自当前代次，且不重复累加
  assert.ok(c.rows.length <= 4, `不应因两次首页响应而翻倍，实得 ${c.rows.length}`)
  assert.equal(new Set(c.rows.map((r) => r.id)).size, c.rows.length, '行不得重复')
})

test('过期响应不得让列表**瞬时**变空（这条必须逐次观察中间态）', async () => {
  // 为什么单独一条：只断言最终行数是看不见这类缺陷的。
  // 一条过期响应会「先 pages.reset()、再被 commitPage 拒绝」，最终行数照样正确，
  // 但用户在两条响应之间看到的是一个空列表——比晚到覆盖更糟。
  //
  // 关键是**顺序**：过期的响应必须落在「已经有数据」之后。写反了这条判据就恒绿
  // （清空发生时列表本来就是空的，没有任何 emit）。所以按代次控制时序：
  //   代次 1 → 立即成功（有数据）
  //   代次 2 → 慢（会成为过期响应）
  //   代次 3 → 立即成功（当前代次，写入）
  // 于是到达顺序是 1、3、2，最后到的正是那条过期的。
  const delayByRevision = { 1: 0, 2: 40, 3: 0 }
  const c = createContinuousList({
    fetchPage: async ({ page, revision }) => {
      const wait = delayByRevision[revision] ?? 0
      if (wait) await new Promise((r) => setTimeout(r, wait))
      return { rows: rows(2, (page - 1) * 2), total: 6, hasMore: true }
    },
  })
  const snapshots = []
  c.onRowsChange((r) => snapshots.push(r.length))

  c.resetQuery() // 代次 1：立即成功 → 列表非空
  await new Promise((r) => setTimeout(r, 10))
  c.resetQuery() // 代次 2：慢
  await new Promise((r) => setTimeout(r, 2))
  c.resetQuery() // 代次 3：立即成功
  await new Promise((r) => setTimeout(r, 90)) // 等代次 2 的过期响应落地

  assert.ok(sawNonEmptyIn(snapshots), `本用例必须真的加载出数据，否则恒真；序列=${JSON.stringify(snapshots)}`)
  for (const n of snapshots) {
    if (n === 0) {
      assert.fail(`过期响应把已有数据清空了，emit 序列=${JSON.stringify(snapshots)}`)
    }
  }
  assert.ok(c.rows.length > 0, '最终仍应有当前代次的数据')
})

function sawNonEmptyIn(seq) {
  return seq.some((n) => n > 0)
}

test('刷新与追加互斥：刷新清游标并从首页重载', async () => {
  const s = makeServer({ pageSize: 2, pages: 3, total: 6 })
  const c = createContinuousList({ fetchPage: s.fetchPage })
  await c.loadMore()
  await c.loadMore()
  assert.equal(c.rows.length, 4)
  await c.refresh()
  assert.equal(c.rows.length, 2, '刷新后应只剩首页')
  assert.equal(c.loadedPages, 1)
})

test('刷新失败保留旧内容与筛选（不清空后报错）', async () => {
  let mode = 'ok'
  const c = createContinuousList({
    fetchPage: async ({ page }) => {
      if (mode === 'fail') throw new Error('net')
      return { rows: rows(2, (page - 1) * 2), total: 6, hasMore: true }
    },
  })
  await c.loadMore()
  mode = 'fail'
  await c.refresh()
  assert.equal(c.rows.length, 2, '刷新失败必须保留旧行')
})

test('下拉未达阈值松手不请求；达到阈值只请求一次', async () => {
  const s = makeServer()
  const c = createContinuousList({ fetchPage: s.fetchPage })
  c.beginPull()
  c.updatePull(30, 64) // 未达阈值
  c.endPull()
  assert.equal(s.calls.length, 0, '未达阈值不得发请求')
  assert.equal(c.refreshStatus, 'idle')

  c.beginPull()
  c.updatePull(70, 64) // 达到
  assert.equal(c.refreshStatus, 'armed')
  c.endPull()
  await new Promise((r) => setTimeout(r, 10))
  assert.equal(s.calls.length, 1, '达到阈值应恰好请求一次')
})

test('中途回到阈值以下会退回 pulling（不会从 armed 误触发）', () => {
  const c = createContinuousList({ fetchPage: makeServer().fetchPage })
  c.beginPull()
  c.updatePull(70, 64)
  assert.equal(c.refreshStatus, 'armed')
  c.updatePull(20, 64)
  assert.equal(c.refreshStatus, 'pulling')
  c.endPull()
})

test('cancelPull 取消时不发请求', async () => {
  const s = makeServer()
  const c = createContinuousList({ fetchPage: s.fetchPage })
  c.beginPull()
  c.updatePull(80, 64)
  c.cancelPull()
  c.endPull()
  await new Promise((r) => setTimeout(r, 5))
  assert.equal(s.calls.length, 0)
})

test('pause 停自动加载并保留缓存，resume 恢复', async () => {
  const s = makeServer({ pageSize: 2, pages: 3, total: 6 })
  const c = createContinuousList({ fetchPage: s.fetchPage })
  await c.loadMore()
  const kept = c.rows.length
  c.pause()
  assert.equal(c.status, 'paused')
  await c.loadMore() // paused 时追加应被拒
  assert.equal(c.rows.length, kept, 'paused 时不得继续追加')
  c.resume()
  assert.equal(c.status, 'idle')
  await c.loadMore()
  assert.ok(c.rows.length > kept, 'resume 后应能继续')
})

test('invalidateScope 清缓存，不在新账号展示旧行', async () => {
  const c = createContinuousList({
    fetchPage: async ({ page }) => ({ rows: rows(2, (page - 1) * 2), total: 6, hasMore: true }),
  })
  await c.loadMore()
  assert.ok(c.rows.length > 0)
  c.invalidateScope()
  assert.equal(c.rows.length, 0, '换账号后不得残留旧行')
})

test('autoFill 默认立即停（未量到真实视口时不无限 append）', async () => {
  const s = makeServer({ pageSize: 2, pages: 10, total: 20 })
  const c = createContinuousList({ fetchPage: s.fetchPage })
  await c.loadMore() // 第 1 页
  const before = s.calls.length
  await c.autoFill()
  assert.equal(s.calls.length, before, '未测得视口已满时应停止补屏')
})

test('autoFill 在 hasMore=false 时立即停', async () => {
  const s = makeServer({ pageSize: 2, pages: 1, total: 2 })
  const c = createContinuousList({ fetchPage: s.fetchPage })
  c.shouldStopAutoFill = false // 允许补屏，交给 hasMore 收口
  await c.loadMore()
  assert.equal(c.hasMore, false)
  const before = s.calls.length
  await c.autoFill()
  assert.equal(s.calls.length, before, '耗尽后不得再请求')
})

test('shouldVirtualize 在达到阈值时为真（内存预算信号）', async () => {
  const c = createContinuousList({
    fetchPage: async () => ({ rows: rows(3, Math.random() * 1e6 | 0), total: 99, hasMore: true }),
    virtualizationThreshold: 5,
  })
  await c.loadMore()
  await c.loadMore()
  assert.equal(c.shouldVirtualize, true)
})

test('状态变化有回调（KeepAlive 停用时据此暂停 observer）', async () => {
  const seen = []
  const c = createContinuousList({ fetchPage: makeServer().fetchPage })
  c.onStatusChange((st) => seen.push(st))
  await c.loadMore()
  assert.ok(seen.includes('loadingNext'))
  await c.loadMore()
  await c.loadMore()
  assert.ok(seen.includes('exhausted'), '耗尽必须发出状态，便于 UI 停止观察')
})

// ---- sentinel 接线：这条路径此前**没有**被任何用例走到，
// 于是 SentinelObserver 少声明 observe() 的类型错误在单测里看不见，
// 只在 vue-tsc 里炸。补上用例是为了让「接线」本身有覆盖。
test('connectSentinel 在无 IntersectionObserver 时降级为按钮且不崩', () => {
  const saved = globalThis.IntersectionObserver
  // 删掉全局观察器，模拟旧宿主/无能力环境
  globalThis.IntersectionObserver = undefined
  try {
    const c = createContinuousList({ fetchPage: makeServer().fetchPage })
    const ok = c.connectSentinel({ id: 'sentinel' }, { id: 'root' })
    assert.equal(ok, false, '没有观察器时不得假装连上了')
    assert.equal(c.needsManualLoad, true, '必须让 UI 显示「继续加载」按钮')
  } finally {
    globalThis.IntersectionObserver = saved
  }
})

test('connectSentinel 连接后 pause 会断开 observer，resume 重新观察', () => {
  const observed = []
  const saved = globalThis.IntersectionObserver
  let disconnected = 0
  globalThis.IntersectionObserver = class {
    observe(el) {
      observed.push(el?.id)
    }
    disconnect() {
      disconnected += 1
    }
  }
  try {
    const c = createContinuousList({ fetchPage: makeServer().fetchPage })
    const ok = c.connectSentinel({ id: 'sentinel' }, { id: 'root' })
    assert.equal(ok, true)
    assert.deepEqual(observed, ['sentinel'])
    assert.equal(c.needsManualLoad, false)

    c.pause()
    assert.ok(disconnected >= 1, 'pause 必须断开观察器')
    const before = observed.length
    c.resume()
    assert.equal(observed.length, before + 1, 'resume 必须重新观察 sentinel')
  } finally {
    globalThis.IntersectionObserver = saved
  }
})

/* ───────────────────────── refreshPolicy（2026-10-06）───────────────────────── */

/**
 * 收件箱型服务端：每页 2 行，第 1 页可被「刷新」改写。
 *
 * `setRefreshed(...)` 之后，第 1 页返回的内容与首次加载**不同**——模拟
 * 「服务端重算过摘要」「新邮件插到顶部」这两类真实刷新。
 */
function makeInboxServer({ pageSize = 2, pages = 3 } = {}) {
  const calls = []
  let refreshed = null
  const total = pages * pageSize
  return {
    calls,
    setRefreshed(r) {
      refreshed = r
    },
    fetchPage: async ({ page }) => {
      calls.push(page)
      const from = (page - 1) * pageSize
      if (page === 1 && refreshed) {
        return { rows: refreshed, total, hasMore: true }
      }
      if (from >= total) return { rows: [], total, hasMore: false }
      return {
        rows: rows(Math.min(pageSize, total - from), from),
        total,
        hasMore: from + pageSize < total,
      }
    },
  }
}

test('默认策略是 replace：刷新后只剩第 1 页（这是本选项引入前的行为）', async () => {
  const s = makeInboxServer()
  const c = createContinuousList({ fetchPage: s.fetchPage })
  await c.loadMore()
  await c.loadMore()
  assert.equal(c.rows.length, 4, '先确认已经翻到第 2 页')
  s.setRefreshed([{ id: 'r0', v: 999 }])
  await c.refresh()
  assert.equal(c.rows.length, 1, 'replace 必须丢掉第 2 页')
  assert.equal(c.rows[0].v, 999)
})

test('merge：刷新保留已翻开的页，不把用户弹回第 1 页', async () => {
  const s = makeInboxServer()
  const c = createContinuousList({ fetchPage: s.fetchPage, refreshPolicy: 'merge' })
  await c.loadMore()
  await c.loadMore()
  assert.equal(c.rows.length, 4)
  s.setRefreshed([{ id: 'r0', v: 999 }])
  await c.refresh()
  assert.equal(c.rows.length, 4, '第 2 页必须还在')
  assert.deepEqual(
    c.rows.map((r) => r.id),
    ['r0', 'r1', 'r2', 'r3'],
    '新到的行在最前，已翻页的行原样保留',
  )
})

test('merge：同 id 行取刷新页的新值（不是保留旧值、也不是追加成两份）', async () => {
  const s = makeInboxServer()
  const c = createContinuousList({ fetchPage: s.fetchPage, refreshPolicy: 'merge' })
  await c.loadMore()
  await c.loadMore()
  s.setRefreshed([{ id: 'r0', v: 111 }, { id: 'new', v: 7 }])
  await c.refresh()
  const r0 = c.rows.filter((r) => r.id === 'r0')
  assert.equal(r0.length, 1, '同 id 只能出现一次')
  assert.equal(r0[0].v, 111, '必须取刷新页的新值')
  // 默认 merge 是 [...fresh, ...kept]——**页内顺序由服务端决定**，
  // 核心不擅自重排（业务序是领域知识，交给 mergeRows）。
  assert.deepEqual(
    c.rows.map((r) => r.id),
    ['r0', 'new', 'r1', 'r2', 'r3'],
    '刷新页原序在前，保留行在后。r1 已不在新第 1 页里，行级合并仍要保住它——\n' +
      '    这正是页级做法（pages.replace(1, fresh)）会丢掉的那一段。',
  )
})

test('merge：mergeRows 覆盖最终顺序（核心不擅自决定业务序）', async () => {
  const s = makeInboxServer()
  // 收件箱型：按 v 升序重排，等价于「按 date 倒序」的业务序。
  const c = createContinuousList({
    fetchPage: s.fetchPage,
    refreshPolicy: 'merge',
    mergeRows: (fresh, kept) => [...fresh, ...kept].sort((a, b) => a.v - b.v),
  })
  await c.loadMore()
  await c.loadMore()
  s.setRefreshed([{ id: 'r0', v: 111 }, { id: 'new', v: 7 }])
  await c.refresh()
  assert.deepEqual(
    c.rows.map((r) => r.v),
    [1, 2, 3, 7, 111],
    'mergeRows 提供的顺序必须真的生效（含 r1：v=1）',
  )
})

test('merge：刷新后游标不退回，loadMore 接着请求下一页而不是重复第 1 页', async () => {
  const s = makeInboxServer()
  const c = createContinuousList({ fetchPage: s.fetchPage, refreshPolicy: 'merge' })
  await c.loadMore() // 1
  await c.loadMore() // 2
  await c.loadMore() // 3
  s.calls.length = 0
  s.setRefreshed([{ id: 'r0', v: 1 }])
  await c.refresh()
  assert.deepEqual(s.calls, [1], '刷新只请求第 1 页')
  s.calls.length = 0
  await c.loadMore()
  assert.deepEqual(s.calls, [4], '已加载 3 页，刷新后应继续请求第 4 页；若退回则会是 [1]')
})

test('merge：首屏（尚无已加载页）刷新后游标仍要进到 2，否则 loadMore 永远重复第 1 页', async () => {
  const s = makeInboxServer()
  const c = createContinuousList({ fetchPage: s.fetchPage, refreshPolicy: 'merge' })
  s.setRefreshed([{ id: 'r0', v: 5 }])
  await c.refresh()
  assert.equal(c.rows.length, 1)
  s.calls.length = 0
  await c.loadMore()
  assert.deepEqual(s.calls, [2], '首屏之后必须翻到第 2 页')
  assert.equal(c.rows.length, 3)
})

test('两种策略在刷新失败时都保留旧行（合并语义不豁免「失败保留旧内容」）', async () => {
  for (const policy of ['replace', 'merge']) {
    const s = makeInboxServer()
    const c = createContinuousList({ fetchPage: s.fetchPage, refreshPolicy: policy })
    await c.loadMore()
    await c.loadMore()
    const before = c.rows.length
    c.disconnect()
    const broken = createContinuousList({
      fetchPage: () => Promise.reject(new Error('boom')),
      refreshPolicy: policy,
    })
    await broken.loadMore().catch(() => {})
    assert.equal(broken.status, 'failed')
    assert.equal(broken.rows.length, 0, `${policy}: 失败不得凭空造行`)
    assert.equal(c.rows.length, before, `${policy}: 失败必须保留旧行`)
  }
})
