/**
 * runtime 契约测试 —— 校验「单一返回来源」与「安装失败不阻断启动」两条。
 *
 * 这层的价值在于：BackDispatcher 的优先级逻辑单测已经绿了，但
 * **它有没有真的被四个返回来源共用**、以及**装不上时会不会白屏**，
 * 都不在那些单测的覆盖范围内。
 *
 * Run: node --test src/lib/shell/__tests__/runtime.test.mjs
 */
import { strict as assert } from 'node:assert'
import { test } from 'node:test'
import { createShellRuntime, dispatchBack } from '../runtime.ts'

/** 最小假 router：可控 afterEach / beforeEach / back。 */
function makeRouter() {
  const hooks = { after: [], before: [] }
  const route = { fullPath: '/', name: 'home', meta: {} }
  const pushed = []
  const replaced = []
  return {
    route,
    pushed,
    replaced,
    hooks,
    currentRoute: { value: route },
    async push(to) {
      pushed.push(to)
      route.fullPath = typeof to === 'string' ? to : to?.fullPath ?? route.fullPath
    },
    async replace(to) {
      replaced.push(to)
      route.fullPath = typeof to === 'string' ? to : to?.fullPath ?? route.fullPath
    },
    back() {
      route.fullPath = '/'
    },
    afterEach(h) {
      hooks.after.push(h)
      return () => hooks.after.splice(hooks.after.indexOf(h), 1)
    },
    beforeEach(h) {
      hooks.before.push(h)
      return () => hooks.before.splice(hooks.before.indexOf(h), 1)
    },
    /** 模拟一次成功导航。 */
    navigate(fullPath, name, meta = {}) {
      route.fullPath = fullPath
      route.name = name
      route.meta = meta
      for (const h of hooks.after) h(route, null, null)
    },
    /** 模拟一次被守卫/异常拦下的导航。 */
    fail(message = 'blocked') {
      for (const h of hooks.after) h(route, null, new Error(message))
    },
  }
}

test('导航成功后登记条目；同路由 query 变化按 replace 不增栈', () => {
  const r = makeRouter()
  const rt = createShellRuntime(r, { scope: { serverId: 's1', accountId: 'a1' } })
  r.navigate('/inbox', 'inbox', { title: '邮箱' })
  assert.equal(rt.store.snapshot().entries.length, 1)
  assert.equal(rt.store.current()?.fullPath, '/inbox', '当前条目应指向新导航的路径')

  r.navigate('/inbox?filter=unread', 'inbox', { title: '邮箱' })
  const snap = rt.store.snapshot()
  assert.equal(snap.entries.length, 1, '同路由 query 变化不得增栈')
  assert.equal(snap.entries[0].fullPath, '/inbox?filter=unread')
})

test('路由失败不提交条目，cursor 不动（守卫阻止/用户取消）', () => {
  const r = makeRouter()
  const rt = createShellRuntime(r, { scope: { serverId: 's1', accountId: 'a1' } })
  r.navigate('/a', 'a', { title: 'A' })
  const before = rt.store.snapshot()
  r.fail('aborted')
  const after = rt.store.snapshot()
  assert.equal(after.entries.length, before.entries.length, '失败不得新增条目')
  assert.equal(after.cursor, before.cursor, '失败不得移动 cursor')
  const last = after.operations[after.operations.length - 1]
  assert.equal(last.outcome, 'failed')
})

test('路由守卫被调用并记 cancelled（守卫阻止时留在原页面）', () => {
  const r = makeRouter()
  const rt = createShellRuntime(r, { scope: { serverId: 's1', accountId: 'a1' } })
  r.navigate('/a', 'a', { title: 'A' })
  for (const h of r.hooks.before) h({ fullPath: '/danger' }, { fullPath: '/a' })
  const last = rt.store.snapshot().operations.at(-1)
  assert.equal(last.outcome, 'cancelled')
})

test('四个返回来源共用一个 BackDispatcher：覆盖层打开时系统返回不跳路由', async () => {
  const r = makeRouter()
  const rt = createShellRuntime(r, { scope: { serverId: 's1', accountId: 'a1' } })
  r.navigate('/a', 'a', { title: 'A' })
  r.navigate('/b', 'b', { title: 'B' })

  let closed = 0
  rt.back.registerOverlay({ id: 'm1', presentation: 'modal', close: () => { closed += 1 } })

  // 模拟 Android 系统返回 / Esc / 左上角按钮——它们都只发意图
  const outcome = await dispatchBack(rt)
  assert.equal(outcome.kind, 'overlay-closed')
  assert.equal(closed, 1)
  assert.equal(r.pushed.length, 0, '有关闭层时不得发生页面跳转')
  assert.equal(r.replaced.length, 0)
})

test('dispatchBack 永不返回 undefined：异常时给出 blocked', async () => {
  const r = makeRouter()
  const rt = createShellRuntime(r, { scope: { serverId: 's1', accountId: 'a1' } })
  // 强行让 store.snapshot 抛错
  rt.store.snapshot = () => {
    throw new Error('boom')
  }
  const out = await dispatchBack(rt)
  assert.ok(out, '不得返回 undefined（调用方会据此决定是否退出应用）')
  assert.equal(out.kind, 'blocked')
  assert.equal(out.reason, 'exception')
  assert.ok(rt.diagnostics.some((d) => d.includes('返回裁决异常')))
})

test('安装失败不抛异常，只记诊断（增强层不得阻断启动）', () => {
  const broken = {
    currentRoute: { value: { fullPath: '/', meta: {} } },
    push: async () => {},
    replace: async () => {},
    back: () => {},
    afterEach() {
      throw new Error('afterEach boom')
    },
    beforeEach() {
      throw new Error('beforeEach boom')
    },
  }
  let rt
  assert.doesNotThrow(() => {
    rt = createShellRuntime(broken, { scope: { serverId: 's', accountId: 'a' } })
  })
  assert.equal(rt.diagnostics.length, 2, '两个失败都要留下诊断')
  assert.ok(rt.diagnostics.some((d) => d.includes('afterEach')))
  assert.ok(rt.diagnostics.some((d) => d.includes('beforeEach')))
})

test('换账号：上下文、标题登记、覆盖层注册一起作废', () => {
  const r = makeRouter()
  const rt = createShellRuntime(r, { scope: { serverId: 's1', accountId: 'a1' } })
  r.navigate('/x', 'x', { title: '张三' })
  assert.equal(rt.store.snapshot().entries.length, 1)
  rt.setScope({ serverId: 's1', accountId: 'a2' })
  assert.equal(rt.store.snapshot().entries.length, 0, '换账号不得保留上一个账号的导航条目')
  assert.equal(rt.scope().accountId, 'a2')
})

test('登出（scope 变回空）必须清空导航与标题——这是隐私边界', () => {
  // 这条曾经判不了：setScope 早期只在「非空」时清理，于是登出不触发 reset，
  // 上一位用户的标题（含姓名）留在内存里等下一个人登录。
  const r = makeRouter()
  const rt = createShellRuntime(r, { scope: { serverId: 's1', accountId: 'a1' } })
  r.navigate('/x', 'x', { title: '张三的邮箱' })
  rt.titles.register(rt.store.current().id, '张三的邮箱')
  assert.equal(rt.store.snapshot().entries.length, 1)

  rt.setScope({ serverId: '', accountId: '' }) // 登出

  assert.equal(rt.store.snapshot().entries.length, 0, '登出必须清空导航条目')
  assert.equal(
    rt.titles.resolve({ version: 2, entries: [], cursor: -1, overlayIds: [], operations: [] }, rt.store).title,
    'OpenCode Pocket',
    '登出后不得还能解析出上一位用户的标题',
  )
})

test('scope 未变时不清空（避免每次渲染都重置导航）', () => {
  const r = makeRouter()
  const rt = createShellRuntime(r, { scope: { serverId: 's1', accountId: 'a1' } })
  r.navigate('/x', 'x', { title: 'X' })
  rt.setScope({ serverId: 's1', accountId: 'a1' }) // 完全相同
  assert.equal(rt.store.snapshot().entries.length, 1, '相同 scope 不应清空')
})

test('dispose 注销路由监听（热重载/登出时不残留）', () => {
  const r = makeRouter()
  const rt = createShellRuntime(r, { scope: { serverId: 's1', accountId: 'a1' } })
  assert.equal(r.hooks.after.length, 1)
  rt.dispose()
  assert.equal(r.hooks.after.length, 0)
})

test('路由 meta 标题被登记为 title 并推进 render epoch', () => {
  const r = makeRouter()
  const rt = createShellRuntime(r, { scope: { serverId: 's1', accountId: 'a1' } })
  r.navigate('/inbox', 'inbox', { title: '邮箱' })
  const entry = rt.store.current()
  assert.equal(entry.title, '邮箱')
  assert.equal(entry.titleSource, 'route')
  // epoch 已推进：此后带旧 epoch 的异步结果会被拒
  assert.equal(rt.titles.acceptAsync(entry.id, 0, '过期'), false)
})
