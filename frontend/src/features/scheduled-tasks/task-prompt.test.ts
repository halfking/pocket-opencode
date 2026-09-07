/**
 * 任务提示词 ↔ 各 kind 的 payload。
 * Run: node --test src/features/scheduled-tasks/task-prompt.test.ts
 */
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { applyPrompt, extractPrompt, promptFieldForKind } from './task-prompt.ts'

describe('promptFieldForKind', () => {
  it('maps chat/summary to messages and knowledge to query', () => {
    assert.equal(promptFieldForKind('redclaw_chat'), 'messages')
    assert.equal(promptFieldForKind('llmbff_summary'), 'messages')
    assert.equal(promptFieldForKind('redclaw_knowledge'), 'query')
    assert.equal(promptFieldForKind('agent_bridge'), 'prompt')
    assert.equal(promptFieldForKind('local_agent'), 'prompt')
    assert.equal(promptFieldForKind('cloud_dispatch'), 'prompt')
    assert.equal(promptFieldForKind('webhook'), 'none')
    assert.equal(promptFieldForKind('acc_mcp'), 'none')
    assert.equal(promptFieldForKind('kxmemory_summary'), 'none')
  })
})

describe('extractPrompt / applyPrompt', () => {
  it('writes and reads redclaw chat user message', () => {
    const payload = applyPrompt('redclaw_chat', {}, '明早整理待办')
    assert.deepEqual(payload, { messages: [{ role: 'user', content: '明早整理待办' }] })
    assert.equal(extractPrompt('redclaw_chat', payload), '明早整理待办')
  })

  it('keeps extra fields when updating an existing chat payload', () => {
    const payload = applyPrompt(
      'redclaw_chat',
      { model: 'gpt-4', messages: [{ role: 'user', content: '旧' }] },
      '新提示词',
    )
    assert.deepEqual(payload, { model: 'gpt-4', messages: [{ role: 'user', content: '新提示词' }] })
  })

  it('writes knowledge query and agent prompt', () => {
    assert.deepEqual(applyPrompt('redclaw_knowledge', {}, '本周纪要'), { query: '本周纪要' })
    assert.equal(extractPrompt('redclaw_knowledge', { query: '本周纪要' }), '本周纪要')
    assert.deepEqual(applyPrompt('agent_bridge', { agentId: 'a1' }, '写周报'), {
      agentId: 'a1',
      prompt: '写周报',
    })
  })

  it('does not invent JSON for kinds without a prompt field', () => {
    const raw = { url: 'https://example.test' }
    assert.deepEqual(applyPrompt('webhook', raw, 'ignored'), raw)
    assert.equal(extractPrompt('webhook', raw), '')
  })

  it('treats a non-object payload as empty object', () => {
    assert.deepEqual(applyPrompt('redclaw_chat', 'not-json', 'hi'), {
      messages: [{ role: 'user', content: 'hi' }],
    })
    assert.equal(extractPrompt('redclaw_chat', null), '')
  })
})
