/**
 * 列表 LWW：远程新下行；本地新或 dirty 上行；本地-only 默认可上行。
 * Run: node --test src/native/list-sync/planner.test.ts
 */
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { isLocalOnlyId, newLocalId, planListSync, type ListStamp } from './planner.ts'

const row = (id: string, updatedAt: number, extra: Partial<ListStamp> = {}): ListStamp => ({
  id, updatedAt, ...extra,
})

describe('planListSync', () => {
  it('remote newer is pulled', () => {
    const got = planListSync([row('a', 10)], [row('a', 20)])
    assert.deepEqual(got, { pullIds: ['a'], pushIds: [] })
  })

  it('local newer is pushed', () => {
    const got = planListSync([row('a', 30)], [row('a', 20)])
    assert.deepEqual(got, { pullIds: [], pushIds: ['a'] })
  })

  it('dirty local is pushed even when timestamps are equal', () => {
    const got = planListSync([row('a', 7, { dirty: true })], [row('a', 7)])
    assert.deepEqual(got, { pullIds: [], pushIds: ['a'] })
  })

  it('missing local row is pulled', () => {
    const got = planListSync([], [row('b', 5)])
    assert.deepEqual(got, { pullIds: ['b'], pushIds: [] })
  })

  it('local-only row is pushed by default', () => {
    const got = planListSync([row('local-inv-1', 99)], [])
    assert.deepEqual(got, { pullIds: [], pushIds: ['local-inv-1'] })
  })

  it('local-only row stays local when pushLocalOnly is false', () => {
    const got = planListSync([row('local-inv-1', 99)], [], { pushLocalOnly: false })
    assert.deepEqual(got, { pullIds: [], pushIds: [] })
  })

  it('equal timestamps do nothing when not dirty', () => {
    const got = planListSync([row('a', 7)], [row('a', 7)])
    assert.deepEqual(got, { pullIds: [], pushIds: [] })
  })
})

describe('isLocalOnlyId / newLocalId', () => {
  it('treats local- prefix as local-only', () => {
    assert.equal(isLocalOnlyId('local-inv-1'), true)
    assert.equal(isLocalOnlyId('inv_abc'), false)
    assert.equal(isLocalOnlyId('note-1'), false)
  })

  it('builds a local-only id with kind', () => {
    const id = newLocalId('inv')
    assert.equal(isLocalOnlyId(id), true)
    assert.match(id, /^local-inv-/)
  })
})
