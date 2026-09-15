/**
 * tool-protocol.test.mjs — 围栏 JSON 工具协议解析与 system prompt 片段生成。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'

import { parseToolCall, buildToolProtocolPrompt, formatToolResult } from '../tool-protocol.ts'

test('标准 json 围栏块:解析 tool/args 与 leadText', () => {
  const text = '我先查一下时间。\n```json\n{"tool": "current_time", "args": {}}\n```'
  const call = parseToolCall(text)
  assert.ok(call)
  assert.equal(call.tool, 'current_time')
  assert.deepEqual(call.args, {})
  assert.equal(call.leadText, '我先查一下时间。')
})

test('无语言标记的围栏块也可命中', () => {
  const call = parseToolCall('```\n{"tool":"echo","args":{"text":"a"}}\n```')
  assert.ok(call)
  assert.equal(call.tool, 'echo')
  assert.equal(call.args.text, 'a')
})

test('取最后一个工具调用块;忽略普通 json 块', () => {
  const text = [
    '```json\n{"note": "这不是工具调用"}\n```',
    '中间说明',
    '```json\n{"tool":"echo","args":{"text":"1"}}\n```',
    '```json\n{"tool":"echo","args":{"text":"2"}}\n```',
  ].join('\n')
  const call = parseToolCall(text)
  assert.equal(call.args.text, '2')
  assert.ok(call.leadText.includes('中间说明'))
})

test('普通代码块不误判为工具调用', () => {
  const text = '示例:\n```json\n{"name": "x"}\n```\n完毕'
  assert.equal(parseToolCall(text), null)
})

test('非法 JSON 不算工具调用(按纯文本回答)', () => {
  assert.equal(parseToolCall('```json\n{"tool": "echo", "args": oops}\n```'), null)
})

test('尾逗号容错', () => {
  const call = parseToolCall('```json\n{"tool": "echo", "args": {"text": "a",},}\n```')
  assert.ok(call)
  assert.equal(call.tool, 'echo')
})

test('args 缺省归一为空对象;非对象 args 包装为 value', () => {
  assert.deepEqual(parseToolCall('```json\n{"tool":"a"}\n```').args, {})
  assert.deepEqual(parseToolCall('```json\n{"tool":"a","args":"hello"}\n```').args, { value: 'hello' })
})

test('tool 非字符串不算', () => {
  assert.equal(parseToolCall('```json\n{"tool": 42}\n```'), null)
})

test('buildToolProtocolPrompt:包含协议说明与每个工具的参数', () => {
  const prompt = buildToolProtocolPrompt([
    {
      name: 'calculate',
      description: '计算',
      promptSnippet: '乘号写 *',
      parameters: {
        type: 'object',
        properties: { expression: { type: 'string', description: '表达式' } },
        required: ['expression'],
      },
    },
  ])
  assert.match(prompt, /工具调用协议/)
  assert.match(prompt, /### calculate — 计算/)
  assert.match(prompt, /expression\(string,必填\):表达式/)
  assert.match(prompt, /乘号写 \*/)
})

test('formatToolResult:成功/失败/截断', () => {
  const ok = formatToolResult('echo', { ok: true, result: 'hi' }, 100)
  assert.match(ok, /<tool_result tool="echo" ok="true">/)
  assert.match(ok, /hi/)
  const err = formatToolResult('echo', { ok: false, error: '坏了' }, 100)
  assert.match(err, /执行失败.*坏了/)
  const long = formatToolResult('echo', { ok: true, result: 'x'.repeat(300) }, 100)
  assert.match(long, /已截断/)
})
