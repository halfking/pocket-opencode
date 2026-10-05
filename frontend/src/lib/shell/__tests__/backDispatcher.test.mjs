/**
 * backDispatcher 契约测试（UI规范 06 §5）。
 *
 * 守住四条：
 *   1. 覆盖层优先于页面返回；嵌套时只关最上层
 *   2. 有未保存数据时 beforeClose 拒绝 → **消费返回并保持现状**，绝不跳背景路由
 *   3. 返回单飞：飞行中重复提交不会关两层
 *   4. 返回钮的语义随动作变化（返回/关闭/退出专注）
 *
 * Run: node --test src/lib/shell/__tests__/backDispatcher.test.mjs
 */
import { strict as assert } from 'node:assert'
import { test } from 'node:test'
import { BackDispatcher } from '../backDispatcher.ts'

function ctx(entries = [], cursor = 0) {
  return { version: 2, entries, cursor, overlayIds: [], operations: [] }
}
function page(id, routeName) {
  return { id, fullPath: `/${id}`, presentation: 'page', openedBy: 'push', title: id, titleSource: 'registered', scope: {}, view: { scroll: {} }, createdAt: 0, routeName }
}

/** 可延迟的 close，用来制造「返回在飞行中」的窗口。 */
function deferred() {
  let resolve
  const promise = new Promise((r) => {
    resolve = r
  })
  return { promise, resolve }
}

test('无覆盖层时按已知页面前驱返回', async () => {
  const d = new BackDispatcher()
  let popped = 0
  d.setRouter({ pop: async () => { popped += 1; return true }, replace: async () => true })
  const out = await d.back(ctx([page('a'), page('b')], 1))
  assert.equal(out.kind, 'page-popped')
  assert.equal(popped, 1)
})

test('覆盖层优先：只关最上层，router 完全不被调用', async () => {
  const d = new BackDispatcher()
  let routerCalls = 0
  d.setRouter({ pop: async () => { routerCalls += 1; return true }, replace: async () => true })
  const closed = []
  d.registerOverlay({ id: 'm1', presentation: 'modal', close: () => closed.push('m1') })
  d.registerOverlay({ id: 'm2', presentation: 'modal', close: () => closed.push('m2') })
  const out = await d.back(ctx([page('a')], 0))
  assert.equal(out.kind, 'overlay-closed')
  assert.equal(out.id, 'm2', '只关最上层')
  assert.deepEqual(closed, ['m2'])
  assert.equal(routerCalls, 0, '有关闭层时不得跳路由')
})

test('beforeClose 拒绝：消费返回并保持现状，不跳背景路由', async () => {
  const d = new BackDispatcher()
  let routerCalls = 0
  let closed = 0
  d.setRouter({ pop: async () => { routerCalls += 1; return true }, replace: async () => true })
  d.registerOverlay({
    id: 'dirty',
    presentation: 'modal',
    beforeClose: () => false,
    close: () => { closed += 1 },
  })
  const out = await d.back(ctx([page('a'), page('b')], 1))
  assert.equal(out.kind, 'overlay-rejected')
  assert.equal(out.reason, 'unsaved-changes')
  assert.equal(closed, 0, '被拒绝时不得关闭')
  assert.equal(routerCalls, 0, '被拒绝时**绝不**跳到背景路由')
})

test('beforeClose 允许：关闭后才出栈', async () => {
  const d = new BackDispatcher()
  let closed = 0
  d.registerOverlay({ id: 'ok', presentation: 'modal', beforeClose: () => true, close: () => { closed += 1 } })
  const out = await d.back(ctx([page('a')], 0))
  assert.equal(out.kind, 'overlay-closed')
  assert.equal(closed, 1)
})

test('子菜单声明消费返回时只关它，不连带关闭其下的弹层', async () => {
  const d = new BackDispatcher()
  const closed = []
  d.registerOverlay({ id: 'sheet', presentation: 'sheet', close: () => closed.push('sheet') })
  d.registerOverlay({ id: 'picker', presentation: 'modal', consumesBack: true, close: () => closed.push('picker') })
  const out = await d.back(ctx([page('a')], 0))
  assert.equal(out.id, 'picker')
  assert.deepEqual(closed, ['picker'])
})

test('返回单飞：飞行中重复提交复用同一次，不关两层', async () => {
  const d = new BackDispatcher()
  const gate = deferred()
  const closed = []
  d.registerOverlay({ id: 'm1', presentation: 'modal', close: () => gate.promise.then(() => closed.push('m1')) })
  d.registerOverlay({ id: 'm2', presentation: 'modal', close: () => closed.push('m2') })

  const first = d.back(ctx([page('a')], 0))
  const second = d.back(ctx([page('a')], 0)) // 飞行中再来一次
  assert.equal(first, second, '单飞期间应返回同一个 Promise')
  gate.resolve()
  await first
  assert.deepEqual(closed, ['m2'], '只关了一层，重复提交不能叠加')
})

test('返回钮语义随动作变化：页面/弹窗/专注', () => {
  const d = new BackDispatcher()
  assert.equal(d.affordance(ctx([page('a')], 0)).actionKind, 'back')
  assert.equal(d.affordance(ctx([page('a')], 0)).label, '返回')

  d.registerOverlay({ id: 'm', presentation: 'modal', close: () => {} })
  assert.equal(d.affordance(ctx([page('a')], 0)).actionKind, 'close')
  assert.equal(d.affordance(ctx([page('a')], 0)).label, '关闭')

  const d2 = new BackDispatcher()
  d2.registerOverlay({ id: 'f', presentation: 'focus', close: () => {} })
  assert.equal(d2.affordance(ctx([page('a')], 0)).actionKind, 'exit-focus')
  assert.equal(d2.affordance(ctx([page('a')], 0)).label, '退出专注')
})

test('路由守卫阻止时返回被记为 blocked（不是「成功返回」）', async () => {
  const d = new BackDispatcher()
  d.setRouter({ pop: async () => false, replace: async () => true })
  const out = await d.back(ctx([page('a'), page('b')], 1))
  assert.equal(out.kind, 'blocked')
  assert.equal(out.reason, 'router-guard')
})

test('无已知前驱时走登记的 fallback，且用 replace 避免首页↔详情循环', async () => {
  const d = new BackDispatcher()
  const replaced = []
  d.setRouter({ pop: async () => true, replace: async (p) => { replaced.push(p); return true } })
  d.registerFallback('detail', '/list')
  const out = await d.back(ctx([page('d', 'detail')], 0))
  assert.equal(out.kind, 'page-popped')
  assert.deepEqual(replaced, ['/list'], 'fallback 必须走 replace')
})

test('首页无层：交还宿主，不假装已经返回', async () => {
  const d = new BackDispatcher()
  d.setRouter({ pop: async () => true, replace: async () => true })
  const out = await d.back(ctx([], -1))
  assert.equal(out.kind, 'handed-to-system')
})

test('前进：有覆盖层时禁用', async () => {
  const d = new BackDispatcher()
  d.registerOverlay({ id: 'm', presentation: 'modal', close: () => {} })
  const out = await d.forward(ctx([page('a'), page('b')], 0), async () => true)
  assert.equal(out.kind, 'blocked')
  assert.equal(out.reason, 'overlay-open')
})

test('reset 清空覆盖层注册（换账号不得残留上一个账号的 beforeClose）', async () => {
  const d = new BackDispatcher()
  d.setRouter({ pop: async () => true, replace: async () => true })
  d.registerOverlay({ id: 'm', presentation: 'modal', beforeClose: () => false, close: () => {} })
  d.reset()
  const out = await d.back(ctx([page('a'), page('b')], 1))
  assert.equal(out.kind, 'page-popped', 'reset 后应直接走页面返回')
})
