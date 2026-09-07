/**
 * /ai 首页快速提问：默认隐藏，点相关入口才打开。
 *
 * Run with: `node --test frontend/src/features/tasks/__tests__/quick-prompt.test.mjs`
 */
import { strict as assert } from 'node:assert'
import { test } from 'node:test'

import {
  QUICK_PROMPT_DEFAULT,
  closeQuickPrompt,
  quickPromptInset,
  toggleQuickPrompt,
} from '../quick-prompt.ts'

test('quick prompt starts hidden', () => {
  assert.equal(QUICK_PROMPT_DEFAULT, 'hidden')
})

test('toggleQuickPrompt opens then hides', () => {
  assert.equal(toggleQuickPrompt('hidden'), 'open')
  assert.equal(toggleQuickPrompt('open'), 'hidden')
})

test('closeQuickPrompt always hides', () => {
  assert.equal(closeQuickPrompt('open'), 'hidden')
  assert.equal(closeQuickPrompt('hidden'), 'hidden')
})

test('hidden prompt reports zero chrome inset', () => {
  assert.equal(quickPromptInset('hidden', 160), 0)
  assert.equal(quickPromptInset('open', 160), 160)
})
