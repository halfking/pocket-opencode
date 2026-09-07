/**
 * Run: node --test src/native/list-sync/id-align.test.ts
 */
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { applyIdRemap, applyIdRemaps, remapRecordMap } from './id-align.ts'

describe('applyIdRemap', () => {
  it('replaces the local id and keeps other rows', () => {
    const rows = [{ id: 'local-1', name: 'a' }, { id: 'srv-2', name: 'b' }]
    const got = applyIdRemap(rows, { localId: 'local-1', serverId: 'srv-9' })
    assert.deepEqual(got.map((r) => r.id), ['srv-9', 'srv-2'])
    assert.equal(got[0].name, 'a')
  })

  it('drops the local row when the server id already exists', () => {
    const rows = [{ id: 'local-1', name: 'old' }, { id: 'srv-9', name: 'new' }]
    const got = applyIdRemap(rows, { localId: 'local-1', serverId: 'srv-9' })
    assert.deepEqual(got.map((r) => r.id), ['srv-9'])
    assert.equal(got[0].name, 'new')
  })

  it('applies several remaps in order', () => {
    const rows = [{ id: 'local-1' }, { id: 'local-2' }]
    const got = applyIdRemaps(rows, [
      { localId: 'local-1', serverId: 's1' },
      { localId: 'local-2', serverId: 's2' },
    ])
    assert.deepEqual(got.map((r) => r.id), ['s1', 's2'])
  })
})

describe('remapRecordMap', () => {
  it('moves a map entry from local id to server id', () => {
    const thumbs = { 'local-1': 'blob:a', 'srv-2': 'blob:b' }
    const got = remapRecordMap(thumbs, { localId: 'local-1', serverId: 'srv-9' })
    assert.deepEqual(got, { 'srv-9': 'blob:a', 'srv-2': 'blob:b' })
  })

  it('keeps the server entry when both keys exist', () => {
    const thumbs = { 'local-1': 'blob:old', 'srv-9': 'blob:new' }
    const got = remapRecordMap(thumbs, { localId: 'local-1', serverId: 'srv-9' })
    assert.deepEqual(got, { 'srv-9': 'blob:new' })
  })
})
