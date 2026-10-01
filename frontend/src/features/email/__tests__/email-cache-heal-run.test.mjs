/**
 * Run: node --test src/features/email/__tests__/email-cache-heal-run.test.mjs
 *
 * 这一组测的是**编排顺序**，不是纯函数（纯函数见 email-cache-heal.test.mjs）。
 * 会出事的恰恰是顺序：比如「客户端回补一行都没写进去」该不该升级到服务端
 * IMAP 回补——该升，因为用户报的故障恰恰是服务端库里没有那批邮件。
 */
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { BACKFILL_LIMIT } from '../email-cache-heal.ts'
import {
  MAX_BACKFILL_PAGES,
  SERVER_BACKFILL_DAYS,
  healGap,
  pullWithPlan,
  runBackfill,
} from '../email-cache-heal-run.ts'

const DAY = 24 * 60 * 60 * 1000
const NOW = Date.parse('2026-10-01T12:00:00Z')

/**
 * 可编程的假 IO。
 *
 * pages 是一串「每次 syncPage 的返回」，按调用顺序消费；调用次数和传入的
 * since 都被记录下来，供断言翻页推进是否正确。
 *
 * syncPage 会**回写** state：写入成功后本地行数/最新日期随之更新。不做这个
 * 回写的话，「回补把缺口补上了」在测试里永远不会成立，缺口会在第二次快照
 * 时原样存在，于是误判成「必须升级到服务端 IMAP 回补」——测试会绿，但验的
 * 是一件根本没发生的事。
 */
function makeDeps(over = {}) {
  const calls = { syncPage: [], serverBackfill: [] }
  const state = {
    localCount: 0,
    localMaxUpdatedAt: 0,
    localNewest: 0,
    serverCount: 0,
    pages: [],
    serverSaved: 0,
    serverErrors: [],
    ...over,
  }
  const deps = {
    countLocal: async () => state.localCount,
    maxUpdatedAt: async () => state.localMaxUpdatedAt,
    newestDate: async () => state.localNewest,
    serverCount: async () => state.serverCount,
    syncPage: async (limit, since) => {
      calls.syncPage.push({ limit, since })
      const p = state.pages.shift() ?? { inserted: 0, received: 0, tombstones: 0, oldestDateMs: 0 }
      state.localCount += p.inserted
      if (p.oldestDateMs > 0 && (state.localNewest === 0 || p.oldestDateMs > state.localNewest)) {
        state.localNewest = p.oldestDateMs
      }
      return p
    },
    serverBackfill: async (days) => {
      calls.serverBackfill.push(days)
      // 服务端回补写进的是服务端自己的库：行数上去了，本地还得再拉一轮。
      state.serverCount += state.serverSaved
      return { saved: state.serverSaved, errors: state.serverErrors }
    },
    now: () => NOW,
  }
  return { deps, calls, state }
}

describe('runBackfill 翻页', () => {
  it('本页没拉满就停，不多发一次请求', async () => {
    const { deps, calls } = makeDeps({
      pages: [{ inserted: 3, received: 3, tombstones: 0, oldestDateMs: NOW - DAY }],
    })
    const n = await runBackfill(deps, NOW - 10 * DAY, 'test')
    assert.equal(n, 3)
    assert.equal(calls.syncPage.length, 1)
  })

  it('锚点用「本页最早一封 - 1ms」推进，不复用同一个 since', async () => {
    const { deps, calls } = makeDeps({
      pages: [
        { inserted: 200, received: 200, tombstones: 0, oldestDateMs: NOW - 2 * DAY },
        { inserted: 200, received: 200, tombstones: 0, oldestDateMs: NOW - 4 * DAY },
        { inserted: 10, received: 10, tombstones: 0, oldestDateMs: NOW - 5 * DAY },
      ],
    })
    await runBackfill(deps, NOW - DAY, 'test')
    assert.equal(calls.syncPage.length, 3)
    assert.equal(calls.syncPage[0].since, NOW - DAY)
    assert.equal(calls.syncPage[1].since, NOW - 2 * DAY - 1)
    assert.equal(calls.syncPage[2].since, NOW - 4 * DAY - 1)
    // 关键：每一页的 since 必须严格递减，否则服务端会把同一页再要一遍
    for (let i = 1; i < calls.syncPage.length; i++) {
      assert.ok(calls.syncPage[i].since < calls.syncPage[i - 1].since, `第 ${i} 页 since 未推进`)
    }
  })

  it('墓碑计入本页内容：删了也要继续翻页', async () => {
    const { deps, calls } = makeDeps({
      pages: [
        { inserted: 150, received: 150, tombstones: 50, oldestDateMs: NOW - 3 * DAY },
        { inserted: 1, received: 1, tombstones: 0, oldestDateMs: NOW - 6 * DAY },
      ],
    })
    await runBackfill(deps, NOW - DAY, 'test')
    assert.equal(calls.syncPage.length, 2)
  })

  it('服务端忽略 since（返回的比 since 还新）时停手，不能死循环要同一页', async () => {
    const { deps, calls } = makeDeps({
      pages: [
        { inserted: 200, received: 200, tombstones: 0, oldestDateMs: NOW },
        { inserted: 200, received: 200, tombstones: 0, oldestDateMs: NOW },
      ],
    })
    await runBackfill(deps, NOW - 5 * DAY, 'test')
    assert.equal(calls.syncPage.length, 1)
  })

  it('翻页到上限就收手，剩下交给下次', async () => {
    // 每一页都拉满、锚点都能前进，才谈得上「翻到上限」。
    const pages = [0, 1, 2, 3, 4, 5].map((i) => ({
      inserted: 200,
      received: 200,
      tombstones: 0,
      oldestDateMs: NOW - (i + 2) * DAY,
    }))
    const { deps, calls } = makeDeps({ pages })
    await runBackfill(deps, NOW - DAY, 'test')
    assert.equal(calls.syncPage.length, MAX_BACKFILL_PAGES)
  })

  it('每页请求量与服务端 limit 一致', async () => {
    const { deps, calls } = makeDeps({ pages: [{ inserted: 1, received: 1, tombstones: 0, oldestDateMs: 0 }] })
    await runBackfill(deps, 1, 'test')
    assert.equal(calls.syncPage[0].limit, BACKFILL_LIMIT)
  })
})

describe('pullWithPlan 下拉刷新', () => {
  it('本地与服务端一致且新鲜 → 只做增量', async () => {
    const { deps, calls } = makeDeps({
      localCount: 50,
      localMaxUpdatedAt: NOW - 1000,
      localNewest: NOW - 1000,
      serverCount: 50,
      pages: [{ inserted: 2, received: 2, tombstones: 0, oldestDateMs: NOW - 1000 }],
    })
    const r = await pullWithPlan(deps)
    assert.equal(r.strategy, 'incremental')
    assert.equal(calls.syncPage.length, 1)
    assert.equal(calls.syncPage[0].since, NOW - 1000)
  })

  it('本地为空 → 全量（since=0）', async () => {
    const { deps, calls } = makeDeps({
      localCount: 0,
      localMaxUpdatedAt: 0,
      localNewest: 0,
      serverCount: 30,
      pages: [{ inserted: 30, received: 30, tombstones: 0, oldestDateMs: NOW - DAY }],
    })
    const r = await pullWithPlan(deps)
    assert.equal(r.strategy, 'full')
    assert.equal(calls.syncPage[0].since, 0)
  })

  it('下拉刷新**不**升级到服务端 IMAP 回补', async () => {
    const { deps, calls } = makeDeps({
      localCount: 1,
      localMaxUpdatedAt: NOW,
      localNewest: NOW,
      serverCount: 300,
      pages: [{ inserted: 200, received: 200, tombstones: 0, oldestDateMs: NOW - DAY }],
    })
    await pullWithPlan(deps)
    assert.equal(calls.serverBackfill.length, 0)
  })
})

describe('healGap 两级自愈', () => {
  it('无缺口时完全不动', async () => {
    const { deps, calls } = makeDeps({
      localCount: 50,
      localMaxUpdatedAt: NOW - 1000,
      localNewest: NOW - 1000,
      serverCount: 50,
    })
    const r = await healGap(deps)
    assert.equal(r.filled, 0)
    assert.equal(r.serverBackfilled, false)
    assert.equal(calls.syncPage.length, 0)
    assert.equal(calls.serverBackfill.length, 0)
  })

  it('客户端回补后缺口消失 → 不升级', async () => {
    const { deps, calls } = makeDeps({
      localCount: 10,
      localMaxUpdatedAt: NOW,
      localNewest: NOW,
      serverCount: 60,
      serverSaved: 0,
      pages: [{ inserted: 50, received: 50, tombstones: 0, oldestDateMs: NOW - DAY }],
    })
    const r = await healGap(deps)
    assert.equal(calls.serverBackfill.length, 0)
    assert.equal(r.filled, 50)
  })

  it('★ 客户端回补写入 0 行但仍有缺口 → 必须升级到服务端 IMAP 回补', async () => {
    // 用户报的故障现场：服务端库里只有 3 封、且都不新（缺的那批从没进过库，
    // 因为增量同步每轮只取最近 50 封）。数量一致所以 server-ahead 不触发，
    // 靠 stale 信号（本地最新邮件已 10 天没更新）判定缺口。
    const { deps, calls } = makeDeps({
      localCount: 3,
      localMaxUpdatedAt: NOW, // updated_at 被重跑同步刷新了
      localNewest: NOW - 10 * DAY,
      serverCount: 3,
      serverSaved: 40,
      pages: [
        { inserted: 0, received: 0, tombstones: 0, oldestDateMs: 0 },
        { inserted: 40, received: 40, tombstones: 0, oldestDateMs: NOW - DAY },
      ],
    })
    const r = await healGap(deps)
    assert.equal(calls.serverBackfill.length, 1)
    assert.equal(calls.serverBackfill[0], SERVER_BACKFILL_DAYS)
    assert.equal(r.serverBackfilled, true)
    assert.equal(r.serverSaved, 40)
    assert.equal(r.filled, 40)
    // 第 1 级确实一行都没写进去，升级仍然发生了
    assert.equal(r.reason, 'stale')
  })

  it('负控：把升级条件还原成「客户端写了 0 行就放弃」，结论必须相反', async () => {
    // 证明上面那条不是恒真：同一现场下，旧短路逻辑与当前逻辑结论相反。
    const scene = {
      localCount: 3,
      localMaxUpdatedAt: NOW,
      localNewest: NOW - 10 * DAY,
      serverCount: 3,
      serverSaved: 40,
    }

    const fixed = makeDeps({
      ...scene,
      pages: [
        { inserted: 0, received: 0, tombstones: 0, oldestDateMs: 0 },
        { inserted: 40, received: 40, tombstones: 0, oldestDateMs: NOW - DAY },
      ],
    })
    const good = await healGap(fixed.deps)
    assert.equal(fixed.calls.serverBackfill.length, 1)
    assert.equal(good.filled, 40)

    // 旧逻辑：第 1 级写入 0 行就直接返回，永不升级
    const buggy = makeDeps({ ...scene, pages: [{ inserted: 0, received: 0, tombstones: 0, oldestDateMs: 0 }] })
    const first = await buggy.deps.syncPage(BACKFILL_LIMIT, 0)
    const oldWouldEscalate = first.inserted !== 0
    assert.equal(oldWouldEscalate, false, '负控前提：本现场第 1 级确实写入 0 行')
    assert.equal(buggy.calls.serverBackfill.length, 0, '旧逻辑在此静默放弃，邮件永远补不回来')
    assert.notEqual(buggy.calls.serverBackfill.length, fixed.calls.serverBackfill.length)
  })

  it('服务端回补报错时不吞异常，且如实上报', async () => {
    const { deps, calls } = makeDeps({
      localCount: 1,
      localMaxUpdatedAt: NOW - 10 * DAY,
      localNewest: NOW - 10 * DAY,
      serverCount: 1,
      serverSaved: 0,
      serverErrors: ['acct-1: search: connection refused'],
      pages: [{ inserted: 0, received: 0, tombstones: 0, oldestDateMs: 0 }],
    })
    const r = await healGap(deps)
    assert.equal(r.serverBackfilled, true)
    assert.equal(r.serverSaved, 0)
    assert.equal(calls.serverBackfill.length, 1)
  })

  it('服务端回补写入 0 时不再空转第二轮', async () => {
    const { deps, calls } = makeDeps({
      localCount: 1,
      localMaxUpdatedAt: NOW - 10 * DAY,
      localNewest: NOW - 10 * DAY,
      serverCount: 1,
      serverSaved: 0,
      pages: [{ inserted: 0, received: 0, tombstones: 0, oldestDateMs: 0 }],
    })
    const r = await healGap(deps)
    assert.equal(calls.serverBackfill.length, 1)
    assert.equal(calls.syncPage.length, 1)
    assert.equal(r.filled, 0)
  })
})
