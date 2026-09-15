/**
 * agent-loop.test.mjs — 核心循环:直接回答 / 工具执行 / 审批放行与拒绝 /
 * 未知工具纠偏 / 工具抛错 / 空流 / maxSteps / abort / usage 透传。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'

import { runAgentLoop } from '../agent-loop.ts'

const echoTool = {
  name: 'echo',
  label: '回声',
  description: '原样返回输入',
  parameters: { type: 'object', properties: { text: { type: 'string' } }, required: ['text'] },
  risk: 'low',
  async execute(args) {
    return { ok: true, result: `echo:${String(args.text)}` }
  },
}

/** 顺序返回 responses 的 fake streamFn。 */
function sequentialStream(responses) {
  let call = 0
  return async () => {
    const idx = Math.min(call++, responses.length - 1)
    return { text: responses[idx] }
  }
}

test('直接回答:无工具调用即完成', async () => {
  const events = []
  await runAgentLoop({
    systemPrompt: 'sys',
    history: [],
    prompt: '做点事',
    tools: [echoTool],
    streamFn: sequentialStream(['你好,我是答案']),
    onEvent: (evt) => events.push(evt),
    signal: new AbortController().signal,
  })
  const final = events.find((e) => e.type === 'assistant')
  assert.equal(final.text, '你好,我是答案')
  const doneEvt = events.find((e) => e.type === 'run_done')
  assert.equal(doneEvt.reason, 'completed')
  assert.equal(doneEvt.steps, 1)
})

test('工具调用:执行 → 结果回灌 → 下一轮直接回答', async () => {
  const events = []
  await runAgentLoop({
    systemPrompt: 'sys',
    history: [],
    prompt: 'p',
    tools: [echoTool],
    streamFn: sequentialStream([
      '我来调用工具\n```json\n{"tool": "echo", "args": {"text": "hi"}}\n```',
      '工具说 echo:hi,完成。',
    ]),
    onEvent: (evt) => events.push(evt),
    signal: new AbortController().signal,
  })
  const running = events.find((e) => e.type === 'tool_call' && e.state === 'running')
  assert.equal(running.name, 'echo')
  const completed = events.find((e) => e.type === 'tool_call' && e.state === 'completed')
  assert.equal(completed.result, 'echo:hi')
  assert.ok(completed.durationMs != null)
  // 思考段作为 interim assistant 展示。
  const lead = events.find((e) => e.type === 'assistant' && e.interim)
  assert.equal(lead.text, '我来调用工具')
  assert.equal(events.find((e) => e.type === 'run_done').steps, 2)
})

test('未知工具:错误卡片 + 回灌,循环继续', async () => {
  const events = []
  await runAgentLoop({
    systemPrompt: 'sys',
    history: [],
    prompt: 'p',
    tools: [echoTool],
    streamFn: sequentialStream([
      '```json\n{"tool": "nope", "args": {}}\n```',
      '好的,我直接回答。',
    ]),
    onEvent: (evt) => events.push(evt),
    signal: new AbortController().signal,
  })
  const errCard = events.find((e) => e.type === 'tool_call' && e.state === 'error')
  assert.match(errCard.error, /未知工具/)
  assert.equal(events.find((e) => e.type === 'run_done').reason, 'completed')
})

test('medium 风险工具:拒绝后模型改道继续', async () => {
  const riskyTool = { ...echoTool, name: 'risky', risk: 'medium' }
  const events = []
  const gateCalls = []
  await runAgentLoop({
    systemPrompt: 'sys',
    history: [],
    prompt: 'p',
    tools: [riskyTool],
    streamFn: sequentialStream([
      '```json\n{"tool": "risky", "args": {"text": "x"}}\n```',
      '好的,不用工具了,直接回答。',
    ]),
    approvalGate: async (req) => {
      gateCalls.push(req)
      return false
    },
    onEvent: (evt) => events.push(evt),
    signal: new AbortController().signal,
  })
  // 审批闸门收到正确请求;拒绝后卡片为 denied,循环继续到自然完成。
  assert.equal(gateCalls.length, 1)
  assert.equal(gateCalls[0].tool, 'risky')
  assert.equal(gateCalls[0].risk, 'medium')
  assert.ok(events.find((e) => e.type === 'tool_call' && e.state === 'denied'))
  assert.equal(events.find((e) => e.type === 'run_done').reason, 'completed')
})

test('high 风险工具:放行后执行', async () => {
  const riskyTool = { ...echoTool, name: 'risky', risk: 'high' }
  const events = []
  await runAgentLoop({
    systemPrompt: 'sys',
    history: [],
    prompt: 'p',
    tools: [riskyTool],
    streamFn: sequentialStream([
      '```json\n{"tool": "risky", "args": {"text": "go"}}\n```',
      '执行完成。',
    ]),
    approvalGate: async () => true,
    onEvent: (evt) => events.push(evt),
    signal: new AbortController().signal,
  })
  const completed = events.find((e) => e.type === 'tool_call' && e.state === 'completed')
  assert.equal(completed.result, 'echo:go')
})

test('工具执行抛错:转为 error 卡片并回灌', async () => {
  const badTool = {
    ...echoTool,
    name: 'bad',
    async execute() {
      throw new Error('炸了')
    },
  }
  const events = []
  await runAgentLoop({
    systemPrompt: 'sys',
    history: [],
    prompt: 'p',
    tools: [badTool],
    streamFn: sequentialStream([
      '```json\n{"tool": "bad", "args": {}}\n```',
      '工具坏了,我直接说明。',
    ]),
    onEvent: (evt) => events.push(evt),
    signal: new AbortController().signal,
  })
  assert.equal(events.find((e) => e.type === 'tool_call' && e.state === 'error').error, '炸了')
})

test('空流:run_error 终态', async () => {
  const events = []
  await runAgentLoop({
    systemPrompt: 'sys',
    history: [],
    prompt: 'p',
    tools: [echoTool],
    streamFn: sequentialStream(['   ']),
    onEvent: (evt) => events.push(evt),
    signal: new AbortController().signal,
  })
  assert.equal(events.find((e) => e.type === 'run_error').error, '模型未返回内容(空流)')
})

test('maxSteps:工具循环超限被截断', async () => {
  const events = []
  await runAgentLoop({
    systemPrompt: 'sys',
    history: [],
    prompt: 'p',
    tools: [echoTool],
    streamFn: sequentialStream(['```json\n{"tool": "echo", "args": {"text": "again"}}\n```']),
    onEvent: (evt) => events.push(evt),
    signal: new AbortController().signal,
    maxSteps: 3,
  })
  const doneEvt = events.find((e) => e.type === 'run_done')
  assert.equal(doneEvt.reason, 'max_steps')
  assert.equal(doneEvt.steps, 3)
  assert.ok(events.some((e) => e.type === 'assistant' && /最大执行步数/.test(e.text ?? '')))
})

test('abort:signal 取消后循环以 aborted 终态退出', async () => {
  const ctrl = new AbortController()
  const events = []
  await runAgentLoop({
    systemPrompt: 'sys',
    history: [],
    prompt: 'p',
    tools: [echoTool],
    streamFn: async ({ signal }) => {
      ctrl.abort()
      if (signal.aborted) throw new DOMException('Aborted', 'AbortError')
      return { text: '不该到这里' }
    },
    onEvent: (evt) => events.push(evt),
    signal: ctrl.signal,
  })
  assert.equal(events.find((e) => e.type === 'run_done').reason, 'aborted')
})

test('usage 事件透传', async () => {
  const events = []
  await runAgentLoop({
    systemPrompt: 'sys',
    history: [],
    prompt: 'p',
    tools: [echoTool],
    streamFn: async () => ({ text: '答案', usage: { promptTokens: 10, completionTokens: 5 } }),
    onEvent: (evt) => events.push(evt),
    signal: new AbortController().signal,
  })
  const u = events.find((e) => e.type === 'usage')
  assert.equal(u.promptTokens, 10)
  assert.equal(u.completionTokens, 5)
})

test('task_plan 工具的 emit 事件经循环透传(plan 事件可达订阅方)', async () => {
  const planTool = {
    name: 'planner',
    label: '计划',
    description: '建立计划',
    parameters: { type: 'object', properties: {} },
    risk: 'low',
    async execute(_args, ctx) {
      ctx.emit({ type: 'plan', items: [{ title: '一步', status: 'todo' }] })
      return { ok: true, result: '已建立' }
    },
  }
  const events = []
  await runAgentLoop({
    systemPrompt: 'sys',
    history: [],
    prompt: 'p',
    tools: [planTool],
    streamFn: sequentialStream([
      '```json\n{"tool": "planner", "args": {}}\n```',
      '计划建好了。',
    ]),
    onEvent: (evt) => events.push(evt),
    signal: new AbortController().signal,
  })
  const planEvt = events.find((e) => e.type === 'plan')
  assert.ok(planEvt, 'plan 事件未到达订阅方')
  assert.equal(planEvt.items[0].title, '一步')
})
