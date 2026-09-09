/**
 * aiStreamKeepalive 单元测试（2026-09-10 M5/T2）。
 *
 * 覆盖（决策表见 aiStreamKeepalive.ts 文件头）：
 *   1. 前台 + 活跃流 > 0   → 不起服务（下发 stop / no-op）
 *   2. 后台 + 活跃流 > 0   → start 被调；重复 sync 幂等不重发
 *   3. 后台 + 活跃流 = 0   → stop 被调（流自然结束靠心跳收尾）
 *   4. start 保持中活跃数变化 → update 刷新通知
 *   5. bridge = null（Web/iOS）→ 全 no-op 不抛错
 */

import { strict as assert } from 'node:assert'
import { test, before, beforeEach, after } from 'node:test'

// 必须在 import 前 mock DOM，避免 appLifecycleHub 模块级副作用
const noop = () => {}
globalThis.document = {
  visibilityState: 'visible',
  addEventListener: noop,
  removeEventListener: noop,
}
globalThis.window = {
  addEventListener: noop,
  removeEventListener: noop,
}

const { appLifecycleHub } = await import('../appLifecycleHub.ts')
const {
  startAiStreamKeepalive,
  stopAiStreamKeepalive,
  syncKeepalive,
  setKeepaliveBridge,
  setKeepaliveStatsProvider,
  resetKeepaliveForTest,
} = await import('../aiStreamKeepalive.ts')

before(() => {
  appLifecycleHub.setPlatform({
    document: globalThis.document,
    window: globalThis.window,
    async loadCapacitorApp() {
      throw new Error('mock: no capacitor in node test')
    },
  })
})

/** 假桥：记录调用序列，可模拟 permGranted。 */
function makeFakeBridge(opts = {}) {
  const calls = []
  return {
    calls,
    async start(o) {
      calls.push({ op: 'start', activeCount: o.activeCount })
      return { running: true, permGranted: opts.permGranted !== false }
    },
    async stop() {
      calls.push({ op: 'stop' })
      return { running: false, permGranted: true }
    },
    async update(o) {
      calls.push({ op: 'update', activeCount: o.activeCount })
      return { running: true, permGranted: true }
    },
  }
}

beforeEach(() => {
  resetKeepaliveForTest()
  setKeepaliveStatsProvider(() => ({ activeCount: 0 }))
  appLifecycleHub.emit('visible')
})

after(() => {
  stopAiStreamKeepalive()
})

test('前台 + 活跃流：不起前台服务', async () => {
  const bridge = makeFakeBridge()
  setKeepaliveBridge(bridge)
  setKeepaliveStatsProvider(() => ({ activeCount: 2 }))
  appLifecycleHub.emit('visible')
  await syncKeepalive('visible')
  assert.deepEqual(bridge.calls, [{ op: 'stop' }])
})

test('后台 + 活跃流：start；重复 sync 幂等不重发', async () => {
  const bridge = makeFakeBridge()
  setKeepaliveBridge(bridge)
  setKeepaliveStatsProvider(() => ({ activeCount: 1 }))
  appLifecycleHub.emit('hidden')
  await syncKeepalive('hidden')
  await syncKeepalive() // 心跳再跑一次
  assert.deepEqual(bridge.calls, [{ op: 'start', activeCount: 1 }])
})

test('后台 + 流自然结束：心跳下发 stop', async () => {
  const bridge = makeFakeBridge()
  setKeepaliveBridge(bridge)
  setKeepaliveStatsProvider(() => ({ activeCount: 1 }))
  appLifecycleHub.emit('hidden')
  await syncKeepalive('hidden')
  setKeepaliveStatsProvider(() => ({ activeCount: 0 }))
  await syncKeepalive()
  assert.deepEqual(bridge.calls, [
    { op: 'start', activeCount: 1 },
    { op: 'stop' },
  ])
})

test('start 保持中活跃数变化：update 刷新通知', async () => {
  let active = 2
  const bridge = makeFakeBridge()
  setKeepaliveBridge(bridge)
  setKeepaliveStatsProvider(() => ({ activeCount: active }))
  appLifecycleHub.emit('hidden')
  await syncKeepalive('hidden')
  active = 1
  await syncKeepalive() // 一条流结束
  assert.deepEqual(bridge.calls, [
    { op: 'start', activeCount: 2 },
    { op: 'update', activeCount: 1 },
  ])
})

test('回前台：start → stop 收尾', async () => {
  const bridge = makeFakeBridge()
  setKeepaliveBridge(bridge)
  setKeepaliveStatsProvider(() => ({ activeCount: 1 }))
  appLifecycleHub.emit('hidden')
  await syncKeepalive('hidden')
  appLifecycleHub.emit('visible')
  await syncKeepalive('visible')
  assert.deepEqual(bridge.calls, [
    { op: 'start', activeCount: 1 },
    { op: 'stop' },
  ])
})

test('bridge=null（Web/iOS）：全 no-op 不抛错', async () => {
  setKeepaliveBridge(null)
  setKeepaliveStatsProvider(() => ({ activeCount: 3 }))
  appLifecycleHub.emit('hidden')
  await syncKeepalive('hidden') // 不应抛错
  appLifecycleHub.emit('visible')
  await syncKeepalive('visible')
})

test('start/stop 生命周期：start 幂等、stop 可重复', () => {
  // 不依赖 bridge：只验证不抛错且状态可反复进出
  startAiStreamKeepalive()
  startAiStreamKeepalive() // 二次调用应 no-op
  stopAiStreamKeepalive()
  stopAiStreamKeepalive() // 二次调用应 no-op
})
