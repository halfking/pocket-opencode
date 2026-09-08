/**
 * Run: node --test src/composables/list-scene-store.test.ts
 */
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import {
  clearListScenes,
  consumeListDirty,
  markListDirty,
  peekListDirty,
  rememberListScroll,
  restoreListScroll,
} from './list-scene-store.ts'

describe('list-scene-store dirty flags', () => {
  it('consumeListDirty returns true once then false', () => {
    clearListScenes()
    assert.equal(peekListDirty('email'), false)
    markListDirty('email')
    assert.equal(peekListDirty('email'), true)
    assert.equal(consumeListDirty('email'), true)
    assert.equal(peekListDirty('email'), false)
    assert.equal(consumeListDirty('email'), false)
  })

  it('scopes are independent', () => {
    clearListScenes()
    markListDirty('notes')
    assert.equal(peekListDirty('email'), false)
    assert.equal(peekListDirty('notes'), true)
    clearListScenes()
    assert.equal(peekListDirty('notes'), false)
  })
})

describe('list-scene-store scroll memory', () => {
  it('returns -1 before first save and last saved value afterwards', () => {
    clearListScenes()
    assert.equal(restoreListScroll('email'), -1)
    rememberListScroll('email', 320)
    rememberListScroll('email', 640)
    assert.equal(restoreListScroll('email'), 640)
    assert.equal(restoreListScroll('notes'), -1)
    clearListScenes()
    assert.equal(restoreListScroll('email'), -1)
  })
})
