/**
 * appLifecycleHub 单元测试（2026-09-09 M1）。
 *
 * 覆盖：
 *   1. emit('hidden') → isHidden()=true；emit('visible') → false
 *   2. 多个订阅者都能收到事件；单个抛错不影响其他
 *   3. start/stop 幂等
 *   4. frozen/resumed 也计入 isHidden（frozen 视作更深的 hidden）
 */

import { strict as assert } from 'node:assert'
import { test, before } from 'node:test'

// 必须在 import hub 前 mock DOM，避免 start() 副作用
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

before(() => {
  // 用 mock platform 替换 detectPlatform 内的 Capacitor 动态导入，
  // 避免 Node 测试环境下找不到 @capacitor/app 触发 unhandledRejection。
  appLifecycleHub.setPlatform({
    document: globalThis.document,
    window: globalThis.window,
    async loadCapacitorApp() {
      throw new Error('mock: no capacitor in node test')
    },
  })
})

test('emit hidden/visible 切换 isHidden', () => {
  appLifecycleHub.emit('visible')
  assert.equal(appLifecycleHub.isHidden(), false)
  appLifecycleHub.emit('hidden')
  assert.equal(appLifecycleHub.isHidden(), true)
  appLifecycleHub.emit('visible')
  assert.equal(appLifecycleHub.isHidden(), false)
})

test('frozen/resumed 也计入 hidden 状态', () => {
  appLifecycleHub.emit('frozen')
  assert.equal(appLifecycleHub.isHidden(), true, 'frozen 应视作 hidden')
  assert.equal(appLifecycleHub.isFrozen(), true)
  appLifecycleHub.emit('resumed')
  assert.equal(appLifecycleHub.isFrozen(), false)
})

test('多个订阅者全部收到；单个抛错不阻断其他', () => {
  const seen1 = []
  const seen2 = []
  const off1 = appLifecycleHub.on((e) => seen1.push(e))
  const off2 = appLifecycleHub.on((e) => {
    if (e === 'hidden') throw new Error('simulated')
    seen2.push(e)
  })
  try {
    appLifecycleHub.emit('hidden')
    appLifecycleHub.emit('visible')
    assert.deepEqual(seen1, ['hidden', 'visible'])
    // seen2：hidden 抛错所以未 push，但 visible 应收到
    assert.deepEqual(seen2, ['visible'])
  } finally {
    off1()
    off2()
  }
})

test('on 返回反订阅函数', () => {
  const seen = []
  const off = appLifecycleHub.on((e) => seen.push(e))
  appLifecycleHub.emit('hidden')
  off()
  appLifecycleHub.emit('visible')
  assert.deepEqual(seen, ['hidden'], 'off 后不应再收到')
})

test('start/stop 幂等；HMR 兼容（globalThis 单例）', () => {
  // 单例已存在；多次 start 不应抛
  appLifecycleHub.start()
  appLifecycleHub.start()
  appLifecycleHub.stop()
  appLifecycleHub.stop()
})
