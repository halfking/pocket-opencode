/**
 * email-inbox-adapter.test.mjs — 迁移适配层的契约（UI规范 07 §2.5 / §2.6）。
 *
 * 这里守三件最容易在迁移中写错、且错了**只在真机上表现为「翻不出更多页」**的事：
 *   1. 页码 → offset 换算（去重丢行时不能用「列表长度」当游标）；
 *   2. hasMore 由「这页取满没有」推导（本地查询没有 total）；
 *   3. 合并后整表按 date 倒序（`[...fresh, ...kept]` 是不够的）。
 *
 * Run: node --test src/features/email/__tests__/email-inbox-adapter.test.mjs
 */
import { strict as assert } from 'node:assert'
import { test } from 'node:test'
import { inboxMergeRows, makeInboxFetchPage, pageToOffset } from '../email-inbox-adapter.ts'

const row = (id, date) => ({ id, date })

/** 可控的本地库替身：记录每次被索取的 offset。 */
function makeLocal(plan, opts = {}) {
  const calls = []
  return {
    calls,
    category: opts.category ?? '',
    folder: opts.folder ?? '',
    getCategory: () => opts.category ?? '',
    getFolder: () => opts.folder ?? '',
    countAll: async () => plan.flat().length,
    readPage: async (category, offset, folder) => {
      calls.push({ category, offset, folder })
      return plan[offset] ?? []
    },
  }
}

test('页码→offset：(page-1)*pageSize，第 1 页必须从 0 开始', async () => {
  const local = makeLocal([Array.from({ length: 3 }, (_, i) => row(`a${i}`, i))])
  const fetchPage = makeInboxFetchPage(local, 3)
  await fetchPage({ page: 1, pageSize: 3 })
  await fetchPage({ page: 2, pageSize: 3 })
  await fetchPage({ page: 3, pageSize: 3 })
  assert.deepEqual(local.calls.map((c) => c.offset), [0, 3, 6], '游标必须按页码推进，不得跳号')
})

test('pageToOffset 与 fetchPage 的换算互为逆运算（page 1 → 0）', () => {
  assert.equal(pageToOffset(1, 30), 0)
  assert.equal(pageToOffset(2, 30), 30)
  assert.equal(pageToOffset(0, 30), 0, '页码 <1 必须夹到 0，不能出负 offset')
})

test('hasMore 由「这页取满没有」推导：满页=true，空页=false', async () => {
  const local = makeLocal([
    Array.from({ length: 3 }, (_, i) => row(`a${i}`, i)),
    Array.from({ length: 1 }, (_, i) => row(`b${i}`, i)),
    [],
  ])
  const fetchPage = makeInboxFetchPage(local, 3)
  assert.equal((await fetchPage({ page: 1, pageSize: 3 })).hasMore, true)
  assert.equal((await fetchPage({ page: 2, pageSize: 3 })).hasMore, false, '不足一整页 = 到底')
  assert.equal((await fetchPage({ page: 3, pageSize: 3 })).hasMore, false, '空页 = 到底')
})

test('筛选条件是**现读**的，不能在建适配器时快照', async () => {
  const opts = { category: '' }
  const local = makeLocal([[row('a', 1)]], opts)
  const fetchPage = makeInboxFetchPage(local, 3)
  await fetchPage({ page: 1, pageSize: 3 })
  opts.category = '工作'
  await fetchPage({ page: 2, pageSize: 3 })
  assert.deepEqual(local.calls.map((c) => c.category), ['', '工作'], '切分类后必须带新条件去取')
})

test('目录条件同样透传（收件箱 = 空串，不是「全部」）', async () => {
  const opts = { folder: '' }
  const local = makeLocal([[row('a', 1)]], opts)
  const fetchPage = makeInboxFetchPage(local, 3)
  await fetchPage({ page: 1, pageSize: 3 })
  assert.equal(local.calls[0].folder, '', '空串表示收件箱视图')
})

test('合并序：整表按 date 倒序，而不只是 [fresh, ...kept]', () => {
  const fresh = [row('new', 300), row('mid', 200)]
  const kept = [row('old', 100), row('newer-than-fresh', 400)]
  const merged = inboxMergeRows(fresh, kept)
  assert.deepEqual(merged.map((r) => r.id), ['newer-than-fresh', 'new', 'mid', 'old'])
})

test('合并序：同 date 时保持传入次序（稳定排序）', () => {
  const merged = inboxMergeRows([row('a', 100), row('b', 100)], [row('c', 100)])
  assert.deepEqual(merged.map((r) => r.id), ['a', 'b', 'c'])
})

test('合并序：缺失 date 不产生 NaN 导致顺序错乱', () => {
  const merged = inboxMergeRows([row('a', undefined)], [row('b', 5), row('c', undefined)])
  assert.deepEqual(merged.map((r) => r.id), ['b', 'a', 'c'], 'undefined 必须按 0 处理')
})

test('【变异 1 · 必须转红】用「已合并列表长度」当 offset', async () => {
  // 模拟去重丢行：第 1 页有 3 行但 id 重复，实际新增只有 2 行。
  // 若用列表长度当游标，下一次会从 2 开始 ⇒ 重复索取第 3 行。
  const calls = []
  const local = {
    calls,
    getCategory: () => '',
    getFolder: () => '',
    readPage: async (_c, offset) => {
      calls.push(offset)
      const all = [row('x', 1), row('x', 1), row('y', 2), row('z', 3)]
      return all.slice(offset, offset + 3)
    },
  }
  const fetchPage = makeInboxFetchPage(local, 3)
  const p1 = await fetchPage({ page: 1, pageSize: 3 })
  const deduped = new Set(p1.rows.map((r) => r.id))
  assert.equal(p1.rows.length, 3, '本地返回 3 行')
  assert.equal(deduped.size, 2, '去重后只剩 2 行 —— 列表长度与已索取行数已不等')
  await fetchPage({ page: 2, pageSize: 3 })
  assert.equal(calls[1], 3, '游标必须仍按已索取行数 3 推进，不能按去重后的 2')
})
