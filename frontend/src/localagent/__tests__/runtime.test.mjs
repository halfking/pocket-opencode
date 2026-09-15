/**
 * runtime.test.mjs — localAgentRuntime singleton 语义:send 事件流 / 审批 /
 * abort / 持久化与恢复 / 会话管理。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'

import { createLocalAgentRuntimeForTest } from '../runtime.ts'

/** 内存 StoreLike(模拟 localStorage)。 */
function memStore() {
  const m = new Map()
  return {
    getItem: (k) => (m.has(k) ? m.get(k) : null),
    setItem: (k, v) => m.set(k, v),
    removeItem: (k) => m.delete(k),
  }
}

/** 直接驱动 streamFn 的 fake(按回合返回文本)。 */
function scriptedStreamFn(responses) {
  let call = 0
  return async () => {
    const idx = Math.min(call++, responses.length - 1)
    return { text: responses[idx] }
  }
}

test('send:完整事件流落到时间线(用户条目/工具卡/最终回答)', async () => {
  const rt = createLocalAgentRuntimeForTest(null)
  const s = rt.createSession('general')
  const handle = rt.send(s.id, '帮我算 1+1', {
    streamFnOverride: scriptedStreamFn([
      '我来算一下\n```json\n{"tool": "calculate", "args": {"expression": "1+1"}}\n```',
      '答案是 2。',
    ]),
  })
  assert.ok(handle)
  // 等循环结束:轮询 session 状态。
  for (let i = 0; i < 100 && rt.getSession(s.id).status === 'thinking'; i++) {
    await new Promise((r) => setTimeout(r, 10))
  }
  const session = rt.getSession(s.id)
  assert.equal(session.status, 'idle')
  // 顺序:用户 → 思考段(lead interim)→ 工具卡(running/completed 合并为一张)
  // → 最终回答。
  const kinds = session.timeline.map((it) => it.kind)
  assert.deepEqual(kinds, ['user', 'assistant', 'tool', 'assistant'])
  const leadItem = session.timeline[1]
  assert.equal(leadItem.interim, true)
  assert.equal(leadItem.text, '我来算一下')
  const toolItem = session.timeline.find((it) => it.kind === 'tool' && it.state === 'completed')
  assert.equal(toolItem.result, '1+1 = 2')
  const answer = session.timeline.find((it) => it.kind === 'assistant' && !it.interim)
  assert.equal(answer.text, '答案是 2。')
  assert.equal(session.title, '帮我算 1+1')
  // 跨 send 历史含 prompt 与最终回答。
  assert.equal(session.history.length, 2)
  assert.equal(session.history[0].role, 'user')
  assert.equal(session.history[1].role, 'assistant')
})

test('medium 风险工具触发审批,respond 后继续', async () => {
  const rt = createLocalAgentRuntimeForTest(null)
  const s = rt.createSession('general')
  const responses = [
    '```json\n{"tool": "write_file", "args": {"path": "a.md", "content": "hi"}}\n```',
    '已保存。',
  ]
  rt.send(s.id, '存个文件', { streamFnOverride: scriptedStreamFn(responses) })
  // 等审批出现。
  let approval = null
  for (let i = 0; i < 100 && !approval; i++) {
    approval = rt.getPendingApproval(s.id)
    if (!approval) await new Promise((r) => setTimeout(r, 10))
  }
  assert.ok(approval)
  assert.equal(approval.tool, 'write_file')
  assert.equal(rt.getSession(s.id).status, 'waiting_approval')
  assert.ok(rt.respondApproval(s.id, approval.toolCallId, true))
  for (let i = 0; i < 100 && rt.isRunning(s.id); i++) {
    await new Promise((r) => setTimeout(r, 10))
  }
  const session = rt.getSession(s.id)
  const completed = session.timeline.find((it) => it.kind === 'tool' && it.state === 'completed')
  assert.ok(completed)
  assert.match(session.timeline.at(-1).text, /已保存/)
})

test('拒绝审批:denied 卡片,模型改道收尾', async () => {
  const rt = createLocalAgentRuntimeForTest(null)
  const s = rt.createSession('general')
  const responses = [
    '```json\n{"tool": "http_fetch", "args": {"url": "https://example.com"}}\n```',
    '好的,不查了。',
  ]
  rt.send(s.id, '查个网页', { streamFnOverride: scriptedStreamFn(responses) })
  let approval = null
  for (let i = 0; i < 100 && !approval; i++) {
    approval = rt.getPendingApproval(s.id)
    if (!approval) await new Promise((r) => setTimeout(r, 10))
  }
  assert.ok(approval)
  rt.respondApproval(s.id, approval.toolCallId, false)
  for (let i = 0; i < 100 && rt.isRunning(s.id); i++) {
    await new Promise((r) => setTimeout(r, 10))
  }
  const session = rt.getSession(s.id)
  assert.ok(session.timeline.find((it) => it.kind === 'tool' && it.state === 'denied'))
  assert.match(session.timeline.at(-1).text, /不查了/)
})

test('abort:停止后状态 aborted', async () => {
  const rt = createLocalAgentRuntimeForTest(null)
  const s = rt.createSession('general')
  const ctrlLiked = { aborted: false }
  rt.send(s.id, '长任务', {
    streamFnOverride: () =>
      new Promise((resolve) => {
        // 挂住流,直到被 abort。
        const timer = setInterval(() => {
          if (ctrlLiked.aborted) {
            clearInterval(timer)
            resolve({ text: '中断后的文本' })
          }
        }, 5)
      }),
  })
  // 等 run 真正起来。
  await new Promise((r) => setTimeout(r, 30))
  assert.ok(rt.isRunning(s.id))
  assert.ok(rt.abort(s.id))
  ctrlLiked.aborted = true
  for (let i = 0; i < 100 && rt.isRunning(s.id); i++) {
    await new Promise((r) => setTimeout(r, 10))
  }
  assert.equal(rt.getSession(s.id).status, 'aborted')
})

test('持久化:写盘 + 恢复时运行中状态归 error', async () => {
  const store = memStore()
  const rt = createLocalAgentRuntimeForTest(store)
  const s = rt.createSession('general', '持久化测试')
  // 手动把状态置成运行中再 persist(用 send 立即 abort 的方式模拟)。
  const responses = ['答案']
  rt.send(s.id, 'hi', { streamFnOverride: scriptedStreamFn(responses) })
  for (let i = 0; i < 100 && rt.isRunning(s.id); i++) {
    await new Promise((r) => setTimeout(r, 10))
  }
  // 直接改写底层持久化数据模拟「崩溃时正在运行」。
  const raw = JSON.parse(store.getItem('pocket:localagent:sessions'))
  raw[0].status = 'thinking'
  store.setItem('pocket:localagent:sessions', JSON.stringify(raw))

  const rt2 = createLocalAgentRuntimeForTest(store)
  const loaded = rt2.listSessions()
  assert.equal(loaded.length, 1)
  assert.equal(loaded[0].status, 'error')
  assert.equal(loaded[0].title, '持久化测试')
})

test('会话管理:同会话串行 / 删除 / 专家切换', async () => {
  const rt = createLocalAgentRuntimeForTest(null)
  const s = rt.createSession('general')
  // 挂住的流让会话进入 running。
  rt.send(s.id, '第一条', {
    streamFnOverride: () => new Promise(() => {}),
  })
  await new Promise((r) => setTimeout(r, 20))
  assert.equal(rt.send(s.id, '第二条', { streamFnOverride: scriptedStreamFn(['x']) }), null)
  rt.abort(s.id)

  const s2 = rt.createSession('trip-planner')
  assert.equal(s2.expert, 'trip-planner')
  rt.deleteSession(s2.id)
  assert.equal(rt.getSession(s2.id), undefined)
})

test('事件订阅:subscribe 能收到 tool_call 与 run_done', async () => {
  const rt = createLocalAgentRuntimeForTest(null)
  const s = rt.createSession('general')
  const seen = []
  const unsub = rt.subscribe((_sid, evt) => seen.push(evt))
  rt.send(s.id, 'ping', { streamFnOverride: scriptedStreamFn(['pong']) })
  for (let i = 0; i < 100 && rt.isRunning(s.id); i++) {
    await new Promise((r) => setTimeout(r, 10))
  }
  assert.ok(seen.some((e) => e.type === 'run_done'))
  assert.ok(seen.some((e) => e.type === 'status' && e.status === 'thinking'))
  unsub()
})

test('技能附加:send 传入 skills 时正文注入 prompt', async () => {
  const rt = createLocalAgentRuntimeForTest(null)
  const s = rt.createSession('general')
  let capturedPrompt = ''
  const scripted = async () => ({ text: '收到' })
  // 用事件流捕获不了 prompt,改走 streamFnOverride 包装:从 history 无法拿,
  // 这里直接验证 runtime 行为的副作用即可——技能正文进的是 loop 的 prompt,
  // 通过 runAgentLoop 的可观测出口(history)验证:最终 assistant 前的 user
  // prompt 含 <skill> 标记。取 session.history[0].content。
  rt.send(s.id, '做周报', { skills: ['weekly-report'], streamFnOverride: scripted })
  for (let i = 0; i < 100 && rt.isRunning(s.id); i++) {
    await new Promise((r) => setTimeout(r, 10))
  }
  capturedPrompt = rt.getSession(s.id).history[0].content
  assert.match(capturedPrompt, /<skill name="weekly-report">/)
  assert.match(capturedPrompt, /周报生成/)
})
