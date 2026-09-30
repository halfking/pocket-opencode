/**
 * 收件箱分页状态机回归测试（2026-10-01 需求：分页 / 下拉最新 / 上滑续页）。
 *
 * 重点是**锁住游标语义**。原实现用 `emails.value.length` 当 offset，而列表是
 * 「去重后追加」的结果——一旦两者不等，下一批就会重复索取同一区间，表现为
 * 「上滑一直转圈、后面的页永远出不来」。下面第 2 个用例就是这条的负控。
 */
import test from 'node:test'
import assert from 'node:assert/strict'
import {
  INBOX_PAGE_SIZE,
  advanceInboxPage,
  applyRefreshPage,
  createInboxPageState,
  mergeInboxPages,
  resetInboxPage,
  shouldAutoLoadMore,
} from '../email-inbox-pagination.ts'

const PAGE = 3 // 测试用小页长，边界更密

test('初始状态：可加载、游标在 0', () => {
  const s = createInboxPageState()
  assert.equal(s.nextOffset, 0)
  assert.equal(s.hasMore, true)
  assert.equal(s.loadingMore, false)
})

test('游标只按「数据库已返回的原始行数」前进，不受去重影响', () => {
  // 这一页 3 行里只有 1 行是新的（另 2 行此前已在列表里）。
  // 正确行为：游标 +3（已索取的原始行数），而不是 +1（合并后的长度）。
  let s = createInboxPageState()
  s = advanceInboxPage(s, 3, 1, PAGE)
  assert.equal(s.nextOffset, 3, '游标必须按已索取行数推进，否则会重复索取同一区间')
  assert.equal(s.hasMore, true)
  assert.equal(s.loadingMore, false, '取完必须解除 loadingMore，否则永远不再触发')
})

test('末页：不满页长即判定没有更多', () => {
  let s = createInboxPageState()
  s = advanceInboxPage(s, 3, 3, PAGE)
  s = advanceInboxPage(s, 2, 2, PAGE) // 最后 2 行 < 页长
  assert.equal(s.hasMore, false)
})

test('数据库返回 0 行：立即判定到底（避免空转）', () => {
  let s = createInboxPageState()
  s = advanceInboxPage(s, 3, 3, PAGE)
  s = advanceInboxPage(s, 0, 0, PAGE)
  assert.equal(s.hasMore, false)
  assert.equal(s.nextOffset, 3, '游标不应在 0 行时乱动')
})

test('连续多页无新增时收敛停止（offset 语义平移的死循环保护）', () => {
  // 新邮件插到顶部会让 offset 整体平移，可能连续取回「全都已在列表里」的页。
  let s = createInboxPageState()
  s = advanceInboxPage(s, 3, 0, PAGE) // 第 1 页无新增
  assert.equal(s.hasMore, true, '单页无新增不应立即放弃')
  assert.equal(s.nextOffset, 3, '无新增时仍要推进游标，否则死循环')
  s = advanceInboxPage(s, 3, 0, PAGE) // 第 2 页
  s = advanceInboxPage(s, 3, 0, PAGE) // 第 3 页 → 收敛
  assert.equal(s.hasMore, false, '连续多页无新增应停止，避免无限空转')
})

test('一旦有新增就清零无进展计数', () => {
  let s = createInboxPageState()
  s = advanceInboxPage(s, 3, 0, PAGE)
  s = advanceInboxPage(s, 3, 0, PAGE)
  s = advanceInboxPage(s, 3, 2, PAGE) // 终于有新行
  assert.equal(s.hasMore, true)
  s = advanceInboxPage(s, 3, 0, PAGE) // 计数应从 0 重新开始
  assert.equal(s.hasMore, true, '重新有新增后应重置无进展计数')
})

test('resetInboxPage 把游标归零但不动其它语义', () => {
  let s = createInboxPageState()
  s = advanceInboxPage(s, 6, 6, PAGE)
  s = resetInboxPage(s)
  assert.equal(s.nextOffset, 0)
  assert.equal(s.hasMore, true)
  assert.equal(s.loadingMore, false)
})

// ── 合并去重 ────────────────────────────────────────────────────────────────

const mk = (id, date) => ({ id, date })

test('mergeInboxPages 按 id 去重并按时间倒序', () => {
  const a = [mk('a', 300), mk('b', 200)]
  const b = [mk('b', 200), mk('c', 100), mk('a', 300)]
  const merged = mergeInboxPages(a, b)
  assert.deepEqual(merged.map((e) => e.id), ['a', 'b', 'c'])
})

test('mergeInboxPages 全部重复时返回原数组（不产生新引用噪声）', () => {
  const a = [mk('a', 200), mk('b', 100)]
  const merged = mergeInboxPages(a, [mk('a', 200)])
  assert.equal(merged, a, '无新增时不应重建数组（会让 v-for 全量重渲染）')
})

test('mergeInboxPages 忽略空 id（脏数据不污染列表）', () => {
  const merged = mergeInboxPages([], [mk('', 100), mk('x', 50)])
  assert.deepEqual(merged.map((e) => e.id), ['x'])
})

// ── 下拉刷新：保留已加载分页 ─────────────────────────────────────────────────

test('下拉刷新把新邮件并到顶部，且保留已翻开的分页', () => {
  // 用户已加载 6 封（两页），此时下拉刷新带回 2 封更新 + 1 封新邮件。
  const existing = [mk('n1', 600), mk('n2', 500), mk('n3', 400), mk('n4', 300)]
  const fresh = [mk('n1', 600), mk('new', 700)]
  const { list, addedCount } = applyRefreshPage(existing, fresh)
  assert.equal(addedCount, 1)
  assert.deepEqual(list.map((e) => e.id), ['new', 'n1', 'n2', 'n3', 'n4'],
    '新邮件置顶，且第 2 页的 n3/n4 必须还在（不能被弹回第 1 页）')
})

test('下拉刷新无新邮件时不重建列表', () => {
  const existing = [mk('a', 200), mk('b', 100)]
  const { list, addedCount } = applyRefreshPage(existing, [mk('a', 200)])
  assert.equal(addedCount, 0)
  assert.equal(list, existing)
})

test('刷新只做合并，不删除任何行', () => {
  // 回归护栏：下拉刷新绝不能把本地已有但服务端这页没有的邮件弄丢。
  const existing = [mk('localOnly', 100), mk('b', 200)]
  const { list } = applyRefreshPage(existing, [mk('b', 200), mk('c', 300)])
  assert.ok(list.some((e) => e.id === 'localOnly'), '仅存在于本地的邮件不得被刷新丢掉')
})

// ── 哨兵自动续页 ────────────────────────────────────────────────────────────

test('哨兵仍在视口内时应继续加载（修「只加载一页就停」）', () => {
  assert.equal(
    shouldAutoLoadMore({ hasMore: true, loadingMore: false, sentinelTop: 400, viewportHeight: 800 }),
    true,
  )
})

test('哨兵远离视口底部时不加载', () => {
  assert.equal(
    shouldAutoLoadMore({ hasMore: true, loadingMore: false, sentinelTop: 2000, viewportHeight: 800 }),
    false,
  )
})

test('加载中或没有更多时不再触发（防并发翻页）', () => {
  assert.equal(
    shouldAutoLoadMore({ hasMore: true, loadingMore: true, sentinelTop: 100, viewportHeight: 800 }),
    false,
  )
  assert.equal(
    shouldAutoLoadMore({ hasMore: false, loadingMore: false, sentinelTop: 100, viewportHeight: 800 }),
    false,
  )
})

test('页长常量与列表默认值一致', () => {
  assert.equal(INBOX_PAGE_SIZE, 30)
})
