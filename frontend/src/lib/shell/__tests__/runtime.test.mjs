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
import { readFileSync } from 'node:fs'
import { join, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'
import { createShellRuntime, dispatchBack } from '../runtime.ts'

const RUNTIME_PATH = join(dirname(fileURLToPath(import.meta.url)), '..', 'runtime.ts')

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


// ── 异步导航落定（2026-10-06 回归护栏）───────────────────────────────
//
// 缺陷：toRouterAdapter.pop() 用 `await setTimeout(r, 0)` 判定导航是否成功。
// 本仓 72 个路由全是 import() 懒加载，导航必然跨多个宏任务 ⇒ 路径还没变
// ⇒ pop() 恒 false ⇒ 每次返回都记 blocked ⇒ **按返回键完全没反应**。
//
// 下面的 makeAsyncRouter 忠实复刻 vue-router 的两个关键行为：
//   1. 导航是异步的，要 await 懒加载 chunk（用 asyncTicks 控制耗时）；
//   2. 落定时触发 afterEach；被守卫阻止时 failure 非空且**路径不变**。
// ⚠️ 判据挂在这个钩子上，所以假 router 必须触发它——不触发的假 router
// 会让「对照组也红」，那是无效对照，不是产品缺陷。
function makeAsyncRouter({ asyncTicks = 4, guardBlock = false } = {}) {
  const hooks = { after: [], before: [] }
  const route = { fullPath: '/home', name: 'home', meta: {} }
  const stack = ['/home']
  const settleNav = (target, failure) => {
    if (!failure) route.fullPath = target
    for (const h of hooks.after) h(route, null, failure)
  }
  return {
    route,
    stack,
    currentRoute: { value: route },
    async push(to) {
      const p = String(to)
      stack.push(p)
      route.fullPath = p
    },
    async replace(to) {
      const p = String(to)
      stack[stack.length - 1] = p
      route.fullPath = p
    },
    back() {
      if (guardBlock) {
        setTimeout(() => settleNav(route.fullPath, new Error('blocked by guard')), asyncTicks)
        return
      }
      stack.pop()
      const target = stack[stack.length - 1]
      let n = 0
      const tick = () => {
        n += 1
        if (n < asyncTicks) {
          setTimeout(tick, 0)
          return
        }
        settleNav(target, null)
      }
      setTimeout(tick, 0)
    },
    afterEach(h) {
      hooks.after.push(h)
      return () => {}
    },
    beforeEach(h) {
      hooks.before.push(h)
      return () => {}
    },
    navigate(fullPath, name) {
      if (fullPath !== route.fullPath) stack.push(fullPath)
      route.fullPath = fullPath
      route.name = name
      route.meta = { title: fullPath }
      for (const h of hooks.after) h(route, null, null)
    },
  }
}

test('返回在懒加载导航下判 page-popped（不再误报 blocked）', async () => {
  const r = makeAsyncRouter({ asyncTicks: 4 })
  const rt = createShellRuntime(r, { scope: { serverId: 's1', accountId: 'a1' } })
  r.navigate('/home', 'home')
  r.navigate('/detail', 'detail')
  r.navigate('/note', 'note')

  const outcome = await dispatchBack(rt)
  assert.equal(outcome.kind, 'page-popped', '真实导航成功就不该记 blocked')
  assert.equal(r.currentRoute.value.fullPath, '/detail', '路径必须真的回退')
})

test('返回时长导航同样成立（慢网冷 chunk 不能被当成守卫阻止）', async () => {
  const r = makeAsyncRouter({ asyncTicks: 24 })
  const rt = createShellRuntime(r, { scope: { serverId: 's1', accountId: 'a1' } })
  r.navigate('/home', 'home')
  r.navigate('/detail', 'detail')
  r.navigate('/note', 'note')

  const outcome = await dispatchBack(rt)
  assert.equal(outcome.kind, 'page-popped')
  assert.equal(r.currentRoute.value.fullPath, '/detail')
})

test('守卫真的拦下返回时仍记 blocked，且上下文不动（no-op 不冒充 success）', async () => {
  const r = makeAsyncRouter({ asyncTicks: 4, guardBlock: true })
  const rt = createShellRuntime(r, { scope: { serverId: 's1', accountId: 'a1' } })
  r.navigate('/home', 'home')
  r.navigate('/detail', 'detail')
  r.navigate('/note', 'note')
  const before = rt.store.snapshot()

  const outcome = await dispatchBack(rt)
  assert.equal(outcome.kind, 'blocked', '被守卫拦下必须报 blocked')
  assert.equal(r.currentRoute.value.fullPath, '/note', '路径必须没变')
  assert.equal(rt.store.snapshot().entries.length, before.entries.length, '被拦下时不得提交条目')
})

test('返回在导航上下文里记成 pop：entries 变短而不是单调增长', async () => {
  const r = makeAsyncRouter({ asyncTicks: 4 })
  const rt = createShellRuntime(r, { scope: { serverId: 's1', accountId: 'a1' } })
  r.navigate('/home', 'home')
  r.navigate('/detail', 'detail')
  r.navigate('/note', 'note')
  assert.equal(rt.store.snapshot().entries.length, 3)

  await dispatchBack(rt)
  const s = rt.store.snapshot()
  assert.equal(s.entries.length, 2, '后退后栈必须变短')
  assert.equal(s.cursor, 1)
  assert.equal(rt.store.canForward(), false, '回到过去后未来不可达')
  assert.equal(s.operations.at(-1).type, 'pop', '最后一次操作必须记 pop 而不是 push')
})

test('连续两次返回逐级回退，最终 cursor 归零（不再恒 > 0）', async () => {
  const r = makeAsyncRouter({ asyncTicks: 4 })
  const rt = createShellRuntime(r, { scope: { serverId: 's1', accountId: 'a1' } })
  r.navigate('/home', 'home')
  r.navigate('/detail', 'detail')
  r.navigate('/note', 'note')

  assert.equal((await dispatchBack(rt)).kind, 'page-popped')
  assert.equal((await dispatchBack(rt)).kind, 'page-popped')
  const s = rt.store.snapshot()
  assert.equal(s.cursor, 0, '回到根后 cursor 必须归零')
  assert.equal(s.entries.length, 1, '栈应收缩回起点')
})

// ===========================================================================
// 单例契约（2026-10-06 新增）
//
// ## 为什么单独立一组
//
// 2026-10-06 设备实跑 UI-13 时查到：`registerOverlay()` 全仓零调用，
// 修法拆成「建」（`getShellRuntime`，只有 AppLayout 调）与
// 「取」（`peekShellRuntime`，只读、永不创建）。
// ⇒ 这两个函数是**本轮修复的承重墙**，而它们此前**一条单测都没有** ——
//   本文件 11 条用例全都直接调工厂 `createShellRuntime`，
//   **从不经过单例入口**。也就是说「单例语义」整个是未验证的：
//   单例没成立、peek 偷偷建了实例、dispose 没摘守卫，都不会被任何用例发现。
//
// 而 `__resetShellRuntimeForTest` 这个导出当时**连测试都没调用**（全仓零引用），
// 说明它就是为这组用例准备、却一直没写。⇒ 写完它就不再是死导出。
// ===========================================================================

import { getShellRuntime, peekShellRuntime, __resetShellRuntimeForTest } from '../runtime.ts'

/** 每条用例自带 setup/finally，保证不受执行顺序影响（node --test 顺序不保证）。 */
function withCleanSingleton(fn) {
  __resetShellRuntimeForTest()
  try {
    return fn()
  } finally {
    __resetShellRuntimeForTest()
  }
}

test('单例：getShellRuntime 连续两次返回**同一个**实例', () => {
  withCleanSingleton(() => {
    const a = makeRouter()
    const b = makeRouter()
    const first = getShellRuntime(a)
    const second = getShellRuntime(b)
    assert.equal(first, second,
      '第二次调用必须复用第一次的实例。' +
      '若它新建了，则守卫会装在**两个** router 上，返回仲裁形同虚设')
    // 反向证据：新 router 上**不该**再被装一遍守卫
    assert.equal(b.hooks.after.length, 0,
      '第二次传入的 router 仍被装了 afterEach ⇒ 说明实例被重建了')
  })
})

test('建：getShellRuntime 真的把守卫装到了传入的 router 上', () => {
  withCleanSingleton(() => {
    const r = makeRouter()
    getShellRuntime(r)
    assert.ok(r.hooks.after.length >= 1, 'afterEach 没装上 ⇒ 导航上下文永远不会被路由喂到')
    assert.ok(r.hooks.before.length >= 1, 'beforeEach 没装上 ⇒ 取消的导航不会被记账')
  })
})

test('取：peekShellRuntime 在没有单例时返回 null，且**永不创建**', () => {
  withCleanSingleton(() => {
    assert.equal(peekShellRuntime(), null, '还没建过就不该拿到实例')
    // ⚠️ 关键：**再 peek 一次**。若实现里偷偷建了实例，这里会拿到非 null，
    //   而更糟的是那个实例带着一个假 router —— 后面 getShellRuntime 就会
    //   把它当成已存在的单例返回，守卫全装在假 router 上。
    assert.equal(peekShellRuntime(), null, 'peek 有副作用地创建了实例')
    const r = makeRouter()
    const rt = getShellRuntime(r)
    assert.ok(r.hooks.after.length >= 1,
      'getShellRuntime 返回的不是「用这个 router 建的」实例 ⇒ peek 之前偷偷建过一个')
    assert.equal(peekShellRuntime(), rt, 'peek 必须能取回刚建好的那个实例')
  })
})

test('取：peekShellRuntime 忽略一切参数（它是零参的只读入口）', () => {
  withCleanSingleton(() => {
    const r = makeRouter()
    getShellRuntime(r)
    const rt = peekShellRuntime()
    // 即便有人误传参数，也必须**不建**新实例
    const again = peekShellRuntime(routerIgnored)
    assert.equal(again, rt, 'peek 传参后返回了另一个实例 ⇒ 它不再是只读入口')
  })
  function routerIgnored() { /* 只是被误传的对象 */ }
})

test('重置：__resetShellRuntimeForTest 之后 peek 回到 null，且旧实例的守卫被摘掉', () => {
  const r = makeRouter()
  const rt = getShellRuntime(r)
  assert.ok(r.hooks.after.length >= 1, '前提不成立：守卫没装上')
  __resetShellRuntimeForTest()
  assert.equal(peekShellRuntime(), null, '重置后 peek 仍拿到实例 ⇒ 单例没清干净，会串到下一条用例')
  assert.equal(r.hooks.after.length, 0,
    'dispose 没有摘掉 afterEach ⇒ 旧实例的守卫会继续把导航喂给一个已废弃的运行时')
  assert.equal(r.hooks.before.length, 0, 'dispose 没有摘掉 beforeEach')
  void rt
})

test('【量具自证】本组用的 makeRouter 能真的记录守卫注册（否则上面几条会空过）', () => {
  const r = makeRouter()
  assert.deepEqual(r.hooks, { after: [], before: [] })
  const off = r.afterEach(() => {})
  assert.equal(r.hooks.after.length, 1, 'afterEach 没记录 ⇒ 「守卫装上了」这个读数是假的')
  off()
  assert.equal(r.hooks.after.length, 0, 'afterEach 返回的退订函数没生效 ⇒ dispose 相关的读数不可信')
})

test('【变异 · 必须转红】peekShellRuntime 的函数体里不得出现「创建」调用', () => {
  // ⚠️ 第一版这条写成了「把源码替换成会偷偷创建的版本，再检查替换后的文本」——
  //   那只证明**我的替换串**里含有那个词，**没有跑过变异后的代码**，
  //   属于「探针是从我自己的读法抄出来的」。已删。
  // ⇒ 改成钉**源码属性**：peek 的函数体里一旦出现创建调用（无论写成什么样），
  //   「只读、永不创建」这条契约就已经破了。这条判据对具体写法免疫。
  //
  // 之所以必须钉这一条：上面那些行为用例只在**当前**实现下为绿。
  // 一旦有人把 `return singleton ?? null` 改成「没有就建一个」，
  // 单测会从「行为断言失败」退化成「测试文件整体报错」——
  // 而 run-mjs-tests 的覆盖普查只看文件有没有产出，**照样是绿的**。
  const src = readFileSync(RUNTIME_PATH, 'utf8')
  const m = /export function peekShellRuntime\([^)]*\)[^{]*\{([\s\S]*?)\n\}/.exec(src)
  assert.ok(m, 'runtime.ts 里找不到 peekShellRuntime 的函数体 —— 判据需要随实现改动而更新')
  const body = m[1]
  for (const forbidden of ['createShellRuntime', 'getShellRuntime', '= singleton']) {
    assert.ok(!body.includes(forbidden),
      `peekShellRuntime 的函数体里出现了 ${forbidden}。` +
      '它必须是**纯只读**：拿不到就返回 null，绝不能顺手建一个。' +
      '（建只能发生在 AppLayout 的 getShellRuntime 里）')
  }
  assert.match(body, /singleton/, '前提没了：函数体里连 singleton 都没提 —— 判据可能已脱离实现')
})
