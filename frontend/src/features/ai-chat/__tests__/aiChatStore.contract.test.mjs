import { strict as assert } from 'node:assert'
import { test } from 'node:test'
import { readFile } from 'node:fs/promises'

const source = await readFile(new URL('../aiChatStore.ts', import.meta.url), 'utf8')

test('send() and regenerate() resolve the bound agent system prompt', () => {
  // 角色列表必须传入 buildRequestMessages，否则 resolveSystemPrompt 查不到
  // conv.agentId 对应的角色，选中角色的提示词不会注入为 system 消息。
  const occurrences = source.match(/buildRequestMessages\(conv, agentStore\.agents\)/g) ?? []
  assert.equal(occurrences.length, 2, 'expected send() and regenerate() to pass agents')
})

test('agent store is wired at store setup', () => {
  assert.match(source, /const agentStore = useChatAgentStore\(\)/)
})

test('resolveSystemPrompt keeps custom > agent > global priority', () => {
  assert.match(source, /conv\.customSystemPrompt\?\.trim\(\)/)
  assert.match(source, /conv\.agentId/)
  assert.match(source, /settings\.value\.systemPrompt\.trim\(\)/)
})
