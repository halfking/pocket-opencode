import { strict as assert } from 'node:assert'
import { test } from 'node:test'
import { readFile } from 'node:fs/promises'

const source = await readFile(new URL('../opencode.ts', import.meta.url), 'utf8')

test('OpenCode realtime updates use the configured API base for WebSocket URLs', () => {
  assert.match(source, /buildWebSocketUrl\(apiBase, token\)/)
  assert.doesNotMatch(source, /window\.location\.(protocol|host)/)
})

test('OpenCode realtime updates fail closed without an endpoint or token', () => {
  assert.match(source, /if \(!wsUrl\)/)
  assert.match(source, /if \(!token\)/)
})
