import { strict as assert } from 'node:assert'
import { test } from 'node:test'
import {
  composeSheetTitle,
  defaultForwardSubject,
  defaultReplySubject,
  todoFromDraft,
  toggleCompose,
} from '../compose-mode.ts'

test('toggleCompose opens a kind and hides when tapped again', () => {
  assert.equal(toggleCompose('hidden', 'reply'), 'reply')
  assert.equal(toggleCompose('reply', 'reply'), 'hidden')
  assert.equal(toggleCompose('reply', 'forward'), 'forward')
})

test('reply and forward subjects keep a single prefix', () => {
  assert.equal(defaultReplySubject('Hello'), 'Re: Hello')
  assert.equal(defaultReplySubject('Re: Hello'), 'Re: Hello')
  assert.equal(defaultForwardSubject('Hello'), 'Fwd: Hello')
  assert.equal(defaultForwardSubject('Fwd: Hello'), 'Fwd: Hello')
})

test('compose sheet titles name the action', () => {
  assert.equal(composeSheetTitle('hidden', 'Ada'), '')
  assert.equal(composeSheetTitle('reply', 'Ada'), '回复 Ada')
  assert.equal(composeSheetTitle('forward', 'Ada'), '转发')
  assert.equal(composeSheetTitle('todo', 'Ada'), '转 Todo')
})

test('todoFromDraft uses first line as title', () => {
  assert.deepEqual(todoFromDraft('发票待付\n金额 120', 'fallback'), {
    title: '发票待付',
    description: '金额 120',
  })
  assert.deepEqual(todoFromDraft('   ', '无主题'), {
    title: '无主题',
    description: '',
  })
})
