import { strict as assert } from 'node:assert'
import { test } from 'node:test'

import {
  migrateConversations,
} from '../conversationMigration.ts'

test('migrateConversations 修复刷新时的流式消息：标记为 interrupted，不再当历史发送', () => {
  const parsed = [
    {
      id: 'c1',
      title: 't',
      model: 'auto',
      mode: 'single',
      messages: [
        { id: 'u1', role: 'user', content: 'hi', createdAt: 1 },
        { id: 'a1', role: 'assistant', content: 'part', streaming: true, createdAt: 2 },
      ],
      createdAt: 1,
      updatedAt: 2,
    },
  ]
  const out = migrateConversations(parsed)
  const assistant = out[0].messages[1]
  assert.equal(assistant.streaming, false)
  assert.equal(assistant.interrupted, true)
})

test('migrateConversations 容忍 messages 缺失或非数组', () => {
  const parsed = [{ id: 'c2', title: 't', model: 'auto', mode: 'single', createdAt: 1, updatedAt: 1 }]
  const out = migrateConversations(parsed)
  assert.deepEqual(out[0].messages, [])
})

test('migrateConversations 对非数组输入返回空', () => {
  assert.deepEqual(migrateConversations(null), [])
  assert.deepEqual(migrateConversations('x'), [])
})
