/**
 * 详情页加载时延回归测试（2026-10-01 真机：点进邮件详情失败/极慢）。
 *
 * 真机症状：点列表 → 整屏「加载中…」最久几十秒。
 * 根因：`loading` 闸门罩住整个页面，而 load() 把「正文网络 → LLM 翻译 →
 * 远程图预加载」串成一条 await 链。慢的是其中某一段，代价却是整页不可见。
 *
 * 本文件锁死两条不变量：
 *  1. 首屏只由**本地记录**决定，不被正文/翻译/图片拖住；
 *  2. 预取在途去重——同一封邮件不会被并发拉两次。
 */
import test from 'node:test'
import assert from 'node:assert/strict'
import {
  __resetInflightForTest,
  inflightBodyCount,
  isBodyPrefetching,
  prefetchEmailBody,
  prefetchEmailBodySeries,
} from '../email-body-prefetch.ts'
import {
  initialDetailState,
  isBodyReady,
  shouldAutoTranslate,
  shouldBlockScreen,
  shouldFetchRemoteBody,
} from '../email-detail-load.ts'

// ── 闸门：首屏不能被慢链路拖住 ──────────────────────────────────────────────

test('拿到本地记录就应解除整屏 loading（不等正文/翻译/图片）', () => {
  // 这是本次修复的核心：原实现要等整条串行链跑完才置 loading=false。
  assert.equal(shouldBlockScreen({ hasLocalRecord: true, failed: false }), false)
})

test('没有本地记录才遮挡；失败时不遮挡（要给出错误而不是转圈）', () => {
  assert.equal(shouldBlockScreen({ hasLocalRecord: false, failed: false }), true)
  assert.equal(shouldBlockScreen({ hasLocalRecord: false, failed: true }), false)
})

test('初始状态处于整屏加载', () => {
  const s = initialDetailState()
  assert.equal(s.blocking, true)
  assert.equal(s.bodyReady, false)
})

// ── 正文就绪判定 ────────────────────────────────────────────────────────────

test('有正文即就绪', () => {
  assert.equal(
    isBodyReady({ bodyText: '<p>hi</p>', hasSnippet: 's', bodyPurged: false, bodyFailed: false }),
    true,
  )
})

test('正文已清除：就绪（明确显示「无正文」，不该一直转圈）', () => {
  assert.equal(
    isBodyReady({ bodyText: '', hasSnippet: '', bodyPurged: true, bodyFailed: false }),
    true,
  )
})

test('正文拉取失败但有 snippet：仍然就绪（用 snippet 顶上，别留空白）', () => {
  assert.equal(
    isBodyReady({ bodyText: '', hasSnippet: '订单已发货', bodyPurged: false, bodyFailed: true }),
    true,
  )
})

test('既无正文又无 snippet 且未失败：不就绪（继续等）', () => {
  assert.equal(
    isBodyReady({ bodyText: '', hasSnippet: '', bodyPurged: false, bodyFailed: false }),
    false,
  )
})

// ── 远程正文请求策略 ────────────────────────────────────────────────────────

test('有缓存也允许后台刷新，但不阻塞', () => {
  // 允许刷新保证内容新鲜；关键是它的结果不参与闸门（见上面两条不变量）。
  assert.equal(shouldFetchRemoteBody({ hasCache: true, bodyPurged: false, alreadyFetching: false }), true)
})

test('正文已清除 / 已在途：不重复发起', () => {
  assert.equal(shouldFetchRemoteBody({ hasCache: false, bodyPurged: true, alreadyFetching: false }), false)
  assert.equal(shouldFetchRemoteBody({ hasCache: false, bodyPurged: false, alreadyFetching: true }), false)
})

// ── 翻译不应阻塞首屏 ────────────────────────────────────────────────────────

test('中文正文不自动翻译（省 token，也避免把自己的中文再译一遍）', () => {
  assert.equal(
    shouldAutoTranslate({ hasBody: true, mostlyChinese: true, alreadyCached: false, bodyPurged: false }),
    false,
  )
})

test('非中文正文且无缓存时自动翻译', () => {
  assert.equal(
    shouldAutoTranslate({ hasBody: true, mostlyChinese: false, alreadyCached: false, bodyPurged: false }),
    true,
  )
})

test('已有译文缓存 / 无正文 / 正文已清除：不触发翻译', () => {
  assert.equal(
    shouldAutoTranslate({ hasBody: true, mostlyChinese: false, alreadyCached: true, bodyPurged: false }),
    false,
  )
  assert.equal(
    shouldAutoTranslate({ hasBody: false, mostlyChinese: false, alreadyCached: false, bodyPurged: false }),
    false,
  )
  assert.equal(
    shouldAutoTranslate({ hasBody: true, mostlyChinese: false, alreadyCached: false, bodyPurged: true }),
    false,
  )
})

// ── 预取：在途去重是重点 ────────────────────────────────────────────────────

function makeDeps(overrides = {}) {
  const calls = []
  return {
    calls,
    deps: {
      fetchBody: async (id) => {
        calls.push(id)
        return { body: `<p>body of ${id}</p>` }
      },
      readCache: async () => '',
      writeCache: async () => {},
      extract: (raw) => raw,
      ...overrides,
    },
  }
}

test('点击预取：拉取远端正文并写入缓存', async () => {
  __resetInflightForTest()
  const { calls, deps } = makeDeps()
  const out = await prefetchEmailBody('em-1', deps)
  assert.equal(out, '<p>body of em-1</p>')
  assert.deepEqual(calls, ['em-1'])
  assert.equal(inflightBodyCount(), 0, '完成后应清理在途记录')
})

test('命中本地缓存时完全不联网（真机弱网下的关键快路径）', async () => {
  __resetInflightForTest()
  const { calls, deps } = makeDeps({ readCache: async () => 'cached body' })
  const out = await prefetchEmailBody('em-2', deps)
  assert.equal(out, 'cached body')
  assert.deepEqual(calls, [], '有缓存就不该发起网络请求')
})

test('并发调用同一封只发一次请求（真机上两次并发正是拖垮首屏的元凶）', async () => {
  __resetInflightForTest()
  let inflightNow = 0
  let peak = 0
  const calls = []
  const deps = {
    fetchBody: async (id) => {
      calls.push(id)
      inflightNow++
      peak = Math.max(peak, inflightNow)
      await new Promise((r) => setTimeout(r, 10))
      inflightNow--
      return { body: 'B' }
    },
    readCache: async () => '',
    writeCache: async () => {},
    extract: (r) => r,
  }
  // 列表点击与详情页挂载几乎同时发起——必须合并成一次。
  const [a, b, c] = await Promise.all([
    prefetchEmailBody('em-3', deps),
    prefetchEmailBody('em-3', deps),
    prefetchEmailBody('em-3', deps),
  ])
  assert.equal(calls.length, 1, `同封只应请求一次，实际 ${calls.length}`)
  assert.equal(peak, 1)
  assert.equal(a, 'B')
  assert.equal(b, 'B')
  assert.equal(c, 'B')
})

test('预取失败不抛错（加速手段失败不该影响点击跳转）', async () => {
  __resetInflightForTest()
  const { deps } = makeDeps({
    fetchBody: async () => {
      throw new Error('network down')
    },
  })
  const out = await prefetchEmailBody('em-4', deps)
  assert.equal(out, '', '失败应回落到空串而不是抛出')
  assert.equal(isBodyPrefetching('em-4'), false, '失败后必须清理在途，否则该邮件再也预取不了')
})

test('预取序列：只处理前 N 封，串行不挤占连接', async () => {
  __resetInflightForTest()
  const order = []
  const deps = {
    fetchBody: async (id) => {
      order.push(id)
      return { body: id }
    },
    readCache: async () => '',
    writeCache: async () => {},
    extract: (r) => r,
  }
  await prefetchEmailBodySeries(['a', 'b', 'c', 'd', 'e'], deps, 2)
  assert.deepEqual(order, ['a', 'b'], '只应预取前 2 封')
})

test('预取序列遇到坏 id 跳过，不影响其余', async () => {
  __resetInflightForTest()
  const order = []
  const deps = {
    fetchBody: async (id) => {
      order.push(id)
      return { body: id }
    },
    readCache: async () => '',
    writeCache: async () => {},
    extract: (r) => r,
  }
  await prefetchEmailBodySeries(['', 'x', ''], deps, 3)
  assert.deepEqual(order, ['x'])
})

test('远端标记 purged 时不缓存正文', async () => {
  __resetInflightForTest()
  let wrote = false
  const { deps } = makeDeps({
    fetchBody: async () => ({ body: 'xx', purged: true }),
    writeCache: async () => {
      wrote = true
    },
  })
  const out = await prefetchEmailBody('em-5', deps)
  assert.equal(out, '')
  assert.equal(wrote, false, '已清除正文的邮件不应再写缓存')
})
