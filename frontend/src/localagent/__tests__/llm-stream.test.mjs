/**
 * llm-stream.test.mjs — StreamFn 传输适配:收流聚合 / usage / 外部 abort
 * 必须同步掐断底层流 / 服务端错误映射。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'

import { createLlmStreamFn } from '../llm-stream.ts'

function fakeHandle() {
  const calls = []
  return {
    calls,
    handle: {
      id: 'fake',
      abort() {
        calls.push('abort')
        return true
      },
      status: () => 'running',
    },
  }
}

test('正常收流:聚合文本 + 末帧 usage', async () => {
  const { handle } = fakeHandle()
  const streamFn = createLlmStreamFn({
    sessionId: 't1',
    spawner: (id, input, handlers) => {
      queueMicrotask(() => {
        handlers.onDelta?.({ content: '你' })
        handlers.onDelta?.({ content: '好' })
        handlers.onDelta?.({ done: true, usage: { prompt_tokens: 11, completion_tokens: 3, total_tokens: 14 } })
        handlers.onDone?.({ prompt_tokens: 11, completion_tokens: 3, total_tokens: 14 })
      })
      return handle
    },
  })
  const deltas = []
  const res = await streamFn({
    messages: [{ role: 'user', content: 'hi' }],
    signal: new AbortController().signal,
    onDelta: (t) => deltas.push(t),
  })
  assert.equal(res.text, '你好')
  assert.deepEqual(res.usage, { promptTokens: 11, completionTokens: 3 })
  assert.deepEqual(deltas, ['你', '好'])
})

test('外部 abort:reject AbortError 且底层流被 abort(不泄漏僵尸 fetch)', async () => {
  const { handle, calls } = fakeHandle()
  const ctrl = new AbortController()
  const streamFn = createLlmStreamFn({
    sessionId: 't2',
    spawner: (_id, _input, handlers) => {
      // 流挂着不动,模拟上游沉默;外部 signal 负责取消。
      setTimeout(() => ctrl.abort(), 20)
      return handle
    },
  })
  await assert.rejects(
    streamFn({ messages: [{ role: 'user', content: 'hi' }], signal: ctrl.signal }),
    /Abort/,
  )
  assert.ok(calls.includes('abort'), '底层流未被 abort')
})

test('服务端错误:非 user 原因映射为普通 Error(message 透传)', async () => {
  const { handle } = fakeHandle()
  const streamFn = createLlmStreamFn({
    sessionId: 't3',
    spawner: (_id, _input, handlers) => {
      queueMicrotask(() => handlers.onError?.(new Error('LLM 网关 502'), 'server-error'))
      return handle
    },
  })
  await assert.rejects(
    streamFn({ messages: [{ role: 'user', content: 'hi' }], signal: new AbortController().signal }),
    /LLM 网关 502/,
  )
})

test('已 aborted 的 signal:同步 reject,不发起流', async () => {
  const { handle, calls } = fakeHandle()
  const ctrl = new AbortController()
  ctrl.abort()
  const streamFn = createLlmStreamFn({
    sessionId: 't4',
    spawner: () => handle,
  })
  await assert.rejects(
    streamFn({ messages: [{ role: 'user', content: 'hi' }], signal: ctrl.signal }),
    /Abort/,
  )
  assert.deepEqual(calls, [], '未起跑就不该碰底层流')
})
