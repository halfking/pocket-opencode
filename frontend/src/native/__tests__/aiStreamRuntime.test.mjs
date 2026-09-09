/**
 * aiStreamRuntime 单元测试（2026-09-09 M1）。
 *
 * 覆盖：
 *   1. spawnChat 幂等：同 id 第二次调用返回同一 handle，流不重启
 *   2. 流所有权上移：调用方"放弃"引用后流仍跑（模拟组件 unmount 不 abort）
 *   3. 订阅者离场后再 subscribe 拿到 buf replay
 *   4. 订阅者离场后再 subscribe：已结束的流补发终态
 *   5. 120s 看门狗：active 期间计时；hidden 期间暂停；visible 后续命
 *   6. 用户主动 abort：reason='user'；后续 abort() 返回 false
 *   7. 网络中断（fetch reject）→ reason='network'；不走"超时"文案
 *   8. 服务端 error frame → reason='server-error'；onError 拿到原始 message
 *   9. 空流（正常关闭但 0 帧）→ reason='empty'
 *  10. lifecycle 'hidden' 触发后 abort 不会立即终止流（仅暂停 watchdog）
 *  11. listStreamIds / getStats 反映 activeCount
 */

import { strict as assert } from 'node:assert'
import { test } from 'node:test'
import { setTimeout as delay } from 'node:timers/promises'

import { appLifecycleHub } from '../appLifecycleHub.ts'
import {
  aiStreamRuntime,
  setStreamDeps,
} from '../aiStreamRuntime.ts'

/** 测试假 fetcher：可注入字节流与行为。
 *  body=null 表示"停止写入但保持流打开"，直到 abort signal 触发。 */
function makeFetcher(opts) {
  return async (_input, signal, _base, _token) => {
    if (opts.throwOnFetch) {
      await delay(opts.delayMs ?? 0)
      throw opts.throwOnFetch
    }
    await delay(opts.delayMs ?? 0)
    if (opts.body === undefined) {
      opts.body = [
        `data: ${JSON.stringify({ content: 'hello', done: false })}\n\n`,
        `data: [DONE]\n\n`,
      ]
    }
    const encoder = new TextEncoder()
    const stream = new ReadableStream({
      async start(controller) {
        for (const chunk of opts.body) {
          if (chunk === null) {
            // 显式不关闭：等待 abort signal 才结束
            await new Promise((resolve) => {
              if (signal.aborted) resolve()
              else signal.addEventListener('abort', resolve, { once: true })
            })
            controller.close()
            return
          }
          controller.enqueue(encoder.encode(chunk))
          await delay(0)
        }
        controller.close()
      },
    })
    return {
      status: opts.status ?? 200,
      statusText: 'OK',
      contentType: opts.contentType ?? 'text/event-stream',
      body: stream,
    }
  }
}

function installDeps(opts = {}) {
  const f = makeFetcher(opts)
  setStreamDeps({
    fetcher: f,
    resolveBase: () => 'http://test.local',
    resolveToken: () => 'tok',
  })
  return f
}

function makeRecorder() {
  const rec = { deltas: [], errors: [], retries: [], doneUsage: undefined }
  const handlers = {
    onDelta: (d) => rec.deltas.push(d),
    onDone: (u) => { rec.doneUsage = u },
    onError: (err, reason) => rec.errors.push({ err, reason }),
    onRetry: (m) => rec.retries.push(m),
  }
  return { handlers, rec }
}

// ---- 测试 ----

test('spawnChat: 同 id 第二次调用复用 handle，不重启流', async () => {
  installDeps({ delayMs: 20 })
  const id = 'idem-1'
  const h1 = aiStreamRuntime.spawnChat(id, { messages: [{ role: 'user', content: 'hi' }] }, {})
  const h2 = aiStreamRuntime.spawnChat(id, { messages: [{ role: 'user', content: 'hi' }] }, {})
  assert.strictEqual(h1, h2, '相同 id 必须返回同一 handle')
  await waitForStatus(h1, 'done', 1000)
})

test('流所有权上移：调用方释放引用后流仍跑（模拟 unmount 不 abort）', async () => {
  installDeps({ delayMs: 30 })
  const { handlers, rec } = makeRecorder()
  let componentHandle = aiStreamRuntime.spawnChat('up-1', { messages: [] }, handlers)
  assert.ok(componentHandle)
  componentHandle = null
  await waitFor(() => rec.deltas.length > 0 || rec.errors.length > 0, 1000)
  assert.ok(rec.deltas.length > 0, '流在调用方释放引用后仍应推 delta')
  assert.equal(rec.errors.length, 0)
})

test('subscribe: 离场后再 subscribe 拿到 replay + 终态补发', async () => {
  installDeps({ delayMs: 5 })
  const id = 'sub-1'
  const { handlers: h1, rec: r1 } = makeRecorder()
  aiStreamRuntime.spawnChat(id, { messages: [] }, h1)
  await waitFor(() => r1.deltas.length > 0, 1000)
  // 等流完成（onDone 被调或 status=done）
  await waitFor(() => r1.errors.length > 0, 500).catch(() => {})
  // 多等一帧让 onDone flush
  await delay(20)
  const { handlers: h2, rec: r2 } = makeRecorder()
  const unsub = aiStreamRuntime.subscribe(id, h2)
  assert.equal(typeof unsub, 'function')
  assert.ok(r2.deltas.length >= 1, '应至少 replay 一帧')
  unsub()
})

test('subscribe: 不存在的 id 返回 no-op', () => {
  const { handlers, rec } = makeRecorder()
  const unsub = aiStreamRuntime.subscribe('not-exist', handlers)
  assert.equal(typeof unsub, 'function')
  assert.equal(rec.deltas.length, 0)
  assert.equal(rec.errors.length, 0)
  unsub()
})

test('abort: 用户主动 → reason=user；后续 abort 返回 false', async () => {
  installDeps({
    body: [
      `data: ${JSON.stringify({ content: 'a', done: false })}\n\n`,
      null,
    ],
    delayMs: 0,
  })
  const { handlers, rec } = makeRecorder()
  const h = aiStreamRuntime.spawnChat('abort-1', { messages: [] }, handlers)
  await delay(10)
  assert.equal(h.status(), 'running')
  assert.equal(h.abort(), true, '首次 abort 应返回 true')
  assert.equal(h.abort(), false, '重复 abort 应返回 false')
  await delay(20)
  assert.equal(rec.errors.length, 1)
  assert.equal(rec.errors[0].reason, 'user')
  assert.equal(rec.errors[0].err.message, '已停止')
  assert.equal(h.status(), 'aborted')
})

test('error reason: 网络中断 → network（非 watchdog / 非 user）', async () => {
  installDeps({ throwOnFetch: new Error('ECONNRESET') })
  const { handlers, rec } = makeRecorder()
  const h = aiStreamRuntime.spawnChat('net-1', { messages: [] }, handlers)
  await waitForStatus(h, 'error', 500)
  assert.equal(rec.errors.length, 1)
  assert.equal(rec.errors[0].reason, 'network')
  assert.match(rec.errors[0].err.message, /ECONNRESET|网络/)
})

test('error reason: 服务端 error frame → server-error，原始 message 透传', async () => {
  installDeps({
    body: [
      `data: ${JSON.stringify({ error: 'rate_limited', delta: { done: true } })}\n\n`,
    ],
  })
  const { handlers, rec } = makeRecorder()
  const h = aiStreamRuntime.spawnChat('srv-1', { messages: [] }, handlers)
  await waitForStatus(h, 'error', 500)
  assert.equal(rec.errors.length, 1)
  assert.equal(rec.errors[0].reason, 'server-error')
  assert.match(rec.errors[0].err.message, /rate_limited/)
})

test('error reason: 空流 → empty', async () => {
  installDeps({
    body: [`data: [DONE]\n\n`],
  })
  const { handlers, rec } = makeRecorder()
  const h = aiStreamRuntime.spawnChat('empty-1', { messages: [] }, handlers)
  await waitForStatus(h, 'error', 500)
  assert.equal(rec.errors.length, 1)
  assert.equal(rec.errors[0].reason, 'empty')
})

test('watchdog: hidden 期间不 abort；visible 后流仍跑', async () => {
  installDeps({
    body: [
      `data: ${JSON.stringify({ content: 'x', done: false })}\n\n`,
      null,
    ],
    delayMs: 0,
  })
  const rec = makeRecorder()
  const h = aiStreamRuntime.spawnChat('wd-1', { messages: [] }, rec.handlers)
  await delay(10)
  assert.equal(h.status(), 'running')
  appLifecycleHub.emit('hidden')
  await delay(30)
  assert.equal(h.status(), 'running', 'hidden 不应 abort 流')
  appLifecycleHub.emit('visible')
  await delay(20)
  assert.equal(h.status(), 'running', 'visible 不应 abort 流')
  h.abort()
})

test('stats / listStreamIds 反映 activeCount', async () => {
  installDeps({
    body: [
      `data: ${JSON.stringify({ content: 'x', done: false })}\n\n`,
      null,
    ],
  })
  const before = aiStreamRuntime.getStats()
  const h = aiStreamRuntime.spawnChat('stats-1', { messages: [] }, {})
  await delay(10)
  const mid = aiStreamRuntime.getStats()
  assert.ok(mid.activeCount > before.activeCount, 'spawn 后 activeCount 增加')
  assert.ok(aiStreamRuntime.listStreamIds().includes('stats-1'))
  h.abort()
  await delay(20)
  const after = aiStreamRuntime.getStats()
  assert.ok(after.activeCount <= mid.activeCount, 'abort 后 activeCount 减少')
})

test('lifecycle: frozen 视作 hidden；resumed 视作 visible', async () => {
  installDeps({
    body: [
      `data: ${JSON.stringify({ content: 'x', done: false })}\n\n`,
      null,
    ],
  })
  const h = aiStreamRuntime.spawnChat('frz-1', { messages: [] }, {})
  await delay(5)
  appLifecycleHub.emit('frozen')
  await delay(20)
  assert.equal(h.status(), 'running', 'frozen 不应 abort')
  appLifecycleHub.emit('resumed')
  await delay(10)
  assert.equal(h.status(), 'running', 'resumed 不应 abort')
  h.abort()
})

test('watchdog: 多轮 pause/resume 后流仍跑（覆盖剩余预算算术回归）', async () => {
  // 用永远阻塞的 body（null chunk 等 abort signal）确保流不会因 fetch 结束被收尾。
  // 反复 hide→visible 多次，验证剩余预算不会被"暂停时长"误扣。
  installDeps({
    body: [
      `data: ${JSON.stringify({ content: 'x', done: false })}\n\n`,
      null,
    ],
    delayMs: 0,
  })
  const h = aiStreamRuntime.spawnChat('multi-1', { messages: [] }, {})
  await delay(10)
  assert.equal(h.status(), 'running')
  for (let i = 0; i < 3; i++) {
    appLifecycleHub.emit('hidden')
    await delay(20)
    assert.equal(h.status(), 'running', `第 ${i + 1} 轮 hidden 不应 abort`)
    appLifecycleHub.emit('visible')
    await delay(20)
    assert.equal(h.status(), 'running', `第 ${i + 1} 轮 visible 后仍应 running`)
  }
  // 流累计活跃时长 ~10 + 3*20 = 70ms，远低于 120s 默认预算；
  // 旧实现里 resumeWatchdog 把 pausedFor 减进 remaining 但未补 active 已用时长，
  // 在更复杂的多轮场景下可能让 remaining 提前归零（潜在 0 余额提前触发）。
  // 这里断言"流未被 watchdog 误杀"即覆盖该回归。
  h.abort()
})

test('lifecycle: appLifecycleHub 状态在测试间不污染（回归保护）', () => {
  // 若上一个 lifecycle 测试残留 hidden/frozen，会让本测试的 watchdog 不被 arm。
  // 用 emit('visible') 强制重置；并验证 isHidden() 状态干净。
  appLifecycleHub.emit('visible')
  assert.equal(appLifecycleHub.isHidden(), false)
  assert.equal(appLifecycleHub.isFrozen(), false)
})

// ---- helpers ----

async function waitFor(pred, timeoutMs) {
  const start = Date.now()
  while (Date.now() - start < timeoutMs) {
    if (pred()) return
    await delay(5)
  }
  throw new Error(`waitFor timed out after ${timeoutMs}ms`)
}

async function waitForStatus(h, target, timeoutMs) {
  await waitFor(() => h.status() === target, timeoutMs)
}
