/**
 * approvalsRuntime 单元测试（M2，2026-09-09）。
 *
 * 覆盖（依赖注入风格，与 mobileSyncRuntime.test.mjs 一致）：
 *   1. start 幂等
 *   2. subscribe 触发 onChange（fetchPending 假实现返回 list）
 *   3. 退订后 onChange 不再被调
 *   4. 空 iid / sid 直接 no-op
 *   5. 同 (iid, sid, onChange) 幂等订阅
 *   6. 多次订阅不同 onChange 各自被通知
 *   7. size() 反映当前订阅数
 *   8. fetchPending 抛错 → onChange 收到 err
 *   9. offline 跳过 fetch
 *  10. stop 清理订阅集合
 */

import { strict as assert } from 'node:assert'
import { test, afterEach } from 'node:test'

import { ApprovalsRuntime } from '../approvalsRuntime.ts'

const activeRuntimes = []

/** 假 deps：fetchPending 同步返回、isOnline 默认 true、isWsConnected 默认 false（强制轮询路径）。 */
function makeDeps(opts = {}) {
  return {
    isOnline: opts.isOnline ?? (() => true),
    isWsConnected: () => false,
    fetchPending: opts.fetchPending ?? (async () => ({ permissions: [] })),
    subscribeApprovalEvents: () => () => {},
  }
}

function newRuntime(deps) {
  const r = new ApprovalsRuntime(deps, { unrefTimer: true })
  activeRuntimes.push(r)
  return r
}

afterEach(() => {
  while (activeRuntimes.length > 0) activeRuntimes.pop().stop()
})

test('start 幂等', () => {
  const r = newRuntime(makeDeps())
  r.start()
  r.start() // 不抛
})

test('subscribe 触发 onChange（fetchPending 返回 list）', async () => {
  const r = newRuntime(
    makeDeps({ fetchPending: async () => ({ permissions: [{ id: 'p1', sessionID: 'ses-1' }] }) }),
  )
  let calls = 0
  let lastList = null
  let lastErr = null
  const off = r.subscribe('inst-1', 'ses-1', (list, err) => {
    calls++
    lastList = list
    lastErr = err
  })
  await r.refresh()
  assert.ok(calls >= 1)
  assert.deepEqual(lastList, [{ id: 'p1', sessionID: 'ses-1' }])
  assert.equal(lastErr, '')
  off()
})

test('退订后 onChange 不再被调', async () => {
  let callIdx = 0
  let firstResolve
  const fetchPending = () => {
    callIdx++
    if (callIdx === 1) {
      return new Promise((resolve) => {
        firstResolve = () => resolve({ permissions: [] })
      })
    }
    return Promise.resolve({ permissions: [{ id: 'p2', sessionID: 'ses-1' }] })
  }
  const r = newRuntime(makeDeps({ fetchPending }))
  let calls = 0
  const off = r.subscribe('inst-1', 'ses-1', () => calls++)
  // 第一次 refresh：用 pending Promise 控制节奏
  const p1 = r.refresh()
  firstResolve()
  await p1
  assert.ok(calls >= 1)
  const before = calls
  off()
  await r.refresh()
  assert.equal(calls, before, '退订后 onChange 不再被调')
})

test('空 iid / sid 直接 no-op', async () => {
  const r = newRuntime(makeDeps())
  let calls = 0
  const off1 = r.subscribe('', 'ses-1', () => calls++)
  const off2 = r.subscribe('inst-1', '', () => calls++)
  assert.equal(typeof off1, 'function')
  assert.equal(typeof off2, 'function')
  await r.refresh()
  assert.equal(calls, 0)
  off1()
  off2()
})

test('同 (iid, sid, onChange) 幂等订阅', () => {
  const r = newRuntime(makeDeps())
  const fn = () => {}
  const off1 = r.subscribe('inst-2', 'ses-2', fn)
  const off2 = r.subscribe('inst-2', 'ses-2', fn)
  assert.equal(r.size(), 1)
  off1()
  const off3 = r.subscribe('inst-2', 'ses-2', fn)
  assert.equal(r.size(), 1)
  off2()
  off3()
})

test('多次订阅不同 onChange 各自被通知', async () => {
  const r = newRuntime(
    makeDeps({ fetchPending: async () => ({ permissions: [{ id: 'p1', sessionID: 'ses-3' }] }) }),
  )
  let a = 0
  let b = 0
  const off1 = r.subscribe('inst-3', 'ses-3', () => a++)
  const off2 = r.subscribe('inst-3', 'ses-3', () => b++)
  await r.refresh()
  assert.equal(a, 1)
  assert.equal(b, 1)
  off1()
  off2()
})

test('size() 反映订阅数', () => {
  const r = newRuntime(makeDeps())
  const off1 = r.subscribe('inst-4', 'ses-4', () => {})
  const off2 = r.subscribe('inst-4', 'ses-5', () => {})
  assert.equal(r.size(), 2)
  off1()
  off2()
  assert.equal(r.size(), 0)
})

test('fetchPending 抛错 → onChange 收到 err', async () => {
  const r = newRuntime(
    makeDeps({
      fetchPending: async () => {
        throw new Error('ECONNRESET')
      },
    }),
  )
  let calls = 0
  let lastErr = ''
  r.subscribe('inst-5', 'ses-5', (_list, err) => {
    calls++
    lastErr = err
  })
  await r.refresh()
  assert.equal(calls, 1)
  assert.match(lastErr, /ECONNRESET|网络/)
})

test('offline 跳过 fetch', async () => {
  let fetchCalled = false
  const r = newRuntime(
    makeDeps({
      isOnline: () => false,
      fetchPending: async () => {
        fetchCalled = true
        return { permissions: [] }
      },
    }),
  )
  let calls = 0
  r.subscribe('inst-6', 'ses-6', () => calls++)
  await r.refresh()
  assert.equal(calls, 0, 'offline 应跳过 fetch')
  assert.equal(fetchCalled, false, 'offline 不应调用 fetchPending')
})

test('stop 清理订阅集合', () => {
  const r = newRuntime(makeDeps())
  r.subscribe('inst-7', 'ses-7', () => {})
  assert.equal(r.size(), 1)
  r.stop()
  assert.equal(r.size(), 0)
})
