import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { canRequestPermissionAgain, nextPermissionAction } from './permission-action.ts'

describe('nextPermissionAction', () => {
  it('already granted does nothing', () => {
    assert.equal(nextPermissionAction('granted'), 'none')
  })

  it('first deny still allows another system prompt', () => {
    assert.equal(canRequestPermissionAgain('prompt'), true)
    assert.equal(nextPermissionAction('prompt'), 'request')
    assert.equal(canRequestPermissionAgain('prompt-with-rationale'), true)
    assert.equal(nextPermissionAction('prompt-with-rationale'), 'request')
  })

  it('permanent deny goes to system settings', () => {
    assert.equal(canRequestPermissionAgain('denied'), false)
    assert.equal(nextPermissionAction('denied'), 'open-settings')
  })

  it('unavailable has no request path', () => {
    assert.equal(nextPermissionAction('unavailable'), 'none')
  })
})
