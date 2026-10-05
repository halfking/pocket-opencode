/**
 * navigationContext 契约测试。
 *
 * 覆盖 UI规范 06 §3–§4 里那些「写错了很难在真机上看出来」的规则：
 *   - 无标题弹层沿用打开前的标题（不是「弹窗」也不是空）
 *   - push 截断前进分支；replace 保留条目 id
 *   - 守卫阻止/取消时 cursor **不动**
 *   - 持久化只留白名单 query（搜索原文、token 不落盘）
 *   - schema 不过就整体丢弃，不「尽力修复」
 *   - scope 隔离：换账号整体作废
 *
 * Run: node --test src/lib/shell/__tests__/navigationContext.test.mjs
 */
import { strict as assert } from 'node:assert'
import { test } from 'node:test'
import { NavigationContextStore, sanitizePath, validateContext } from '../navigationContext.ts'

/** 可控时钟：让 id 与时间在测试里完全确定。 */
function fixedClock() {
  let t = 1_000
  let n = 0
  return {
    now: () => (t += 10),
    nextId: () => `e${++n}`,
  }
}

/** 最小可用的 sessionStorage 替身。 */
function memStorage() {
  const m = new Map()
  return {
    getItem: (k) => (m.has(k) ? m.get(k) : null),
    setItem: (k, v) => m.set(k, String(v)),
    removeItem: (k) => m.delete(k),
  }
}

const scope = { serverId: 's1', accountId: 'a1' }

function newStore(extra = {}) {
  return new NavigationContextStore({ clock: fixedClock(), storage: null, ...extra })
}

test('push 产生页面条目并推进 cursor；同 URL 多次打开有独立 id', () => {
  const s = newStore()
  const a = s.open({ fullPath: '/list', presentation: 'page', openedBy: 'push', scope, title: '列表' })
  const b = s.open({ fullPath: '/list', presentation: 'page', openedBy: 'push', scope, title: '列表' })
  assert.notEqual(a.id, b.id, '同一个 URL 两次打开必须是两个条目')
  assert.equal(s.snapshot().cursor, 1)
  assert.equal(s.snapshot().entries.length, 2)
})

test('replace 保留条目 id（同一页换筛选/月份不该产生可回退的新条目）', () => {
  const s = newStore()
  const a = s.open({ fullPath: '/list?month=1', presentation: 'page', openedBy: 'push', scope, title: '列表' })
  const b = s.open({ fullPath: '/list?month=2', presentation: 'page', openedBy: 'replace', scope, title: '列表' })
  assert.equal(a.id, b.id, 'replace 必须沿用同一个条目 id')
  assert.equal(s.snapshot().entries.length, 1, 'replace 不增栈')
  assert.equal(s.current()?.fullPath, '/list?month=2')
})

test('返回后打开新详情会截断前进分支，旧的 forward 不可达', () => {
  const s = newStore()
  s.open({ fullPath: '/a', presentation: 'page', openedBy: 'push', scope })
  s.open({ fullPath: '/b', presentation: 'page', openedBy: 'push', scope })
  s.open({ fullPath: '/c', presentation: 'page', openedBy: 'push', scope })
  s.pop()
  s.open({ fullPath: '/d', presentation: 'page', openedBy: 'push', scope })
  assert.equal(s.canForward(), false, '截断后不得还有前进目的地')
  assert.deepEqual(s.snapshot().entries.map((e) => e.fullPath), ['/a', '/b', '/d'])
})

test('无标题弹层沿用它打开前的有效标题，titleSource 记为 inherited', () => {
  const s = newStore()
  s.open({ fullPath: '/list', presentation: 'page', openedBy: 'push', scope, title: '员工' })
  const pageId = s.current().id
  const ov = s.openOverlay({ fullPath: '/list', presentation: 'modal', scope }) // 无 title
  assert.equal(ov.title, '员工', '无标题弹层必须继承父标题，不能是「弹窗」或空')
  assert.equal(ov.titleSource, 'inherited')
  assert.equal(ov.inheritedFrom, pageId)
})

test('嵌套无标题弹层逐级继承到页面标题', () => {
  const s = newStore()
  s.open({ fullPath: '/list', presentation: 'page', openedBy: 'push', scope, title: '张某' })
  s.openOverlay({ fullPath: '/edit', presentation: 'modal', scope, title: '编辑员工' })
  const confirm = s.openOverlay({ fullPath: '/confirm', presentation: 'modal', scope }) // 无标题
  assert.equal(confirm.title, '编辑员工', '继承应沿父链找到最近的非空标题')
  assert.equal(confirm.titleSource, 'inherited')
})

test('覆盖层不推进 cursor，关闭后回到原页面', () => {
  const s = newStore()
  s.open({ fullPath: '/list', presentation: 'page', openedBy: 'push', scope })
  const before = s.current().id
  const ov = s.openOverlay({ fullPath: '/list', presentation: 'sheet', scope })
  assert.equal(s.current()?.id, before, '弹层不推进 cursor')
  assert.equal(s.hasOverlay(), true)
  assert.equal(s.topOverlay()?.id, ov.id)
  s.closeOverlay(ov.id)
  assert.equal(s.hasOverlay(), false)
  assert.equal(s.current()?.id, before)
})

test('cancel 记录为 cancelled 且 cursor 不动', () => {
  const s = newStore()
  s.open({ fullPath: '/a', presentation: 'page', openedBy: 'push', scope })
  const cursor = s.snapshot().cursor
  s.recordCancelled(s.current().id, 'guard', 'blocked')
  assert.equal(s.snapshot().cursor, cursor, '被守卫阻止时 cursor 绝不能移动')
  const ops = s.snapshot().operations
  assert.equal(ops[ops.length - 1].outcome, 'cancelled')
})

test('前进在有覆盖层时不可用（即使有页面目的地）', () => {
  const s = newStore()
  s.open({ fullPath: '/a', presentation: 'page', openedBy: 'push', scope })
  s.open({ fullPath: '/b', presentation: 'page', openedBy: 'push', scope })
  s.pop()
  s.openOverlay({ fullPath: '/b', presentation: 'modal', scope })
  assert.equal(s.canForward(), true, '上下文层面有前进目的地')
  assert.equal(s.forwardTarget()?.fullPath, '/b')
  s.closeOverlay(s.snapshot().overlayIds[0])
  const f = s.forward()
  assert.equal(f?.fullPath, '/b', '覆盖层关掉后前进应可用')
})

test('持久化裁掉非白名单 query：搜索原文与 token 不落盘', () => {
  assert.equal(sanitizePath('/list?month=3&tab=a'), '/list?month=3&tab=a')
  assert.equal(sanitizePath('/list?q=张三李四&month=3'), '/list?month=3')
  assert.equal(sanitizePath('/list?token=secret123'), '/list')
  assert.equal(sanitizePath('/list'), '/list')
})

test('persist → restore 往返后过滤条件不丢，但冷启动不恢复未提交覆盖层', () => {
  const storage = memStorage()
  const s = newStore({ storage })
  s.open({ fullPath: '/list?month=3&tab=a', presentation: 'page', openedBy: 'push', scope, title: '列表' })
  s.openOverlay({ fullPath: '/list', presentation: 'modal', scope })
  s.persist()

  const s2 = newStore({ storage })
  assert.equal(s2.restore(), true)
  assert.equal(s2.snapshot().entries[0].fullPath, '/list?month=3&tab=a')
  assert.equal(s2.snapshot().entries[0].title, '列表')
  assert.equal(s2.hasOverlay(), false, '冷启动不恢复未提交弹窗')
})

test('schema 不符一律整体丢弃，不做部分恢复', () => {
  const storage = memStorage()
  storage.setItem('hyper.navigation.v2', JSON.stringify({ version: 2, entries: [{ id: 'x' }] }))
  const s = newStore({ storage })
  assert.equal(s.restore(), false, '缺字段的条目必须整体作废，不能部分恢复')

  storage.setItem('hyper.navigation.v2', '{ 这不是 JSON')
  assert.equal(newStore({ storage }).restore(), false)
})

test('validateContext 拒绝缺 scope 的条目（防止跨账号标题泄漏）', () => {
  const bad = {
    version: 2,
    cursor: 0,
    operations: [],
    entries: [{ id: 'e1', fullPath: '/x', presentation: 'page', title: '张三', createdAt: 1 }],
  }
  assert.equal(validateContext(bad), null)
})

test('reset 让换账号/登出整体作废，不留上一个账号的标题', () => {
  const storage = memStorage()
  const s = newStore({ storage })
  s.open({ fullPath: '/x', presentation: 'page', openedBy: 'push', scope, title: '张三' })
  s.persist()
  s.reset()
  assert.equal(s.snapshot().entries.length, 0)
  assert.equal(newStore({ storage }).restore(), false, 'reset 后不得留下可恢复的上下文')
})

test('页面条目封顶 80 条，且淘汰不丢当前条目', () => {
  const s = newStore()
  for (let i = 0; i < 200; i += 1) {
    s.open({ fullPath: `/p${i}`, presentation: 'page', openedBy: 'push', scope })
  }
  const snap = s.snapshot()
  assert.ok(snap.entries.length <= 80, `entries 应封顶 80，实得 ${snap.entries.length}`)
  assert.ok(snap.cursor >= 0 && snap.cursor < snap.entries.length, 'cursor 必须仍落在条目范围内')
  assert.equal(s.current()?.fullPath, '/p199', '当前条目不能被淘汰')
})

test('操作环封顶 100 条', () => {
  const s = newStore()
  for (let i = 0; i < 300; i += 1) {
    s.recordFailed('e', 'op', 'reason')
  }
  assert.ok(s.snapshot().operations.length <= 100)
})

test('标题去掉 HTML 注入并限长 200', () => {
  const s = newStore()
  const e = s.open({
    fullPath: '/x',
    presentation: 'page',
    openedBy: 'push',
    scope,
    title: '<img src=x onerror=alert(1)>标题',
  })
  assert.ok(!e.title.includes('<'), `标题不得保留标签，实得 ${e.title}`)
  assert.equal(e.title, '标题', '整段标签应被剥掉，而不是留下标签内的属性文本')

  const long = s.open({ fullPath: '/y', presentation: 'page', openedBy: 'push', scope, title: 'x'.repeat(500) })
  assert.equal(long.title.length, 200)
})

test('version 随变更自增，且能被 Vue computed 追踪（否则接线层标题永不更新）', async () => {
  // 这条判据直接对着一次真实事故写：store 是普通模块变量，顶栏
  // `computed(() => store.snapshot())` 没有响应式依赖 → 一次求值后永久缓存 →
  // 路由变了标题不动。必须有可追踪的 version 把它拉起来。
  const { computed, shallowRef, watchEffect } = await import('vue')
  const s = newStore()
  const before = s.version

  const seen = []
  // 用 watchEffect 模拟接线层：读 version 即建立依赖
  watchEffect(() => {
    seen.push({ v: s.version, cur: s.current()?.fullPath })
  })

  s.open({ fullPath: '/list', presentation: 'page', openedBy: 'push', scope, title: '列表' })
  // flush: 'sync' 时 watchEffect 会立即重跑；这里显式等一个微任务即可
  await Promise.resolve()

  assert.ok(s.version > before, 'open 之后 version 必须自增')
  assert.equal(seen.length, 2, `watchEffect 应被重新触发，实际只跑了 ${seen.length} 次`)
  assert.equal(seen.at(-1).cur, '/list', '重跑后应看到新的当前条目')

  // 再确认 computed 真的会因 version 变化而重新求值
  let reads = 0
  const c = computed(() => {
    void s.version
    reads += 1
    return s.current()?.title
  })
  const r1 = c.value
  s.open({ fullPath: '/other', presentation: 'page', openedBy: 'push', scope, title: '其他' })
  const r2 = c.value
  assert.equal(r1, '列表')
  assert.equal(r2, '其他', 'computed 必须因 version 变化而重算')
  assert.ok(reads >= 2, `computed 应至少求值两次，实得 ${reads}`)
})
