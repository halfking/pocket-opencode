/**
 * Run: node --test --experimental-strip-types src/features/notes/note-edit-helpers.test.ts
 */
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { parseTagsInput, tagsFromArray } from './note-edit-helpers.ts'

describe('parseTagsInput', () => {
  it('splits chinese and english commas', () => {
    assert.deepEqual(parseTagsInput('周报, OKR，待办'), ['周报', 'OKR', '待办'])
  })
})

describe('tagsFromArray', () => {
  it('joins tags or returns empty', () => {
    assert.equal(tagsFromArray(['a', 'b']), 'a, b')
    assert.equal(tagsFromArray(null), '')
  })
})
