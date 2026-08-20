import { strict as assert } from 'node:assert'
import { test } from 'node:test'

import {
  archiveStorageKey,
  parseArchivedIds,
  readArchivedIds,
  setSessionArchived,
} from '../sessionArchive.ts'

function memoryStorage() {
  const data = new Map()
  return {
    getItem(key) { return data.get(key) ?? null },
    setItem(key, value) { data.set(key, value) },
  }
}

test('archiveStorageKey 按 workspace/instance 分区', () => {
  assert.notEqual(
    archiveStorageKey({ workspaceId: 'ws-a', instanceId: 'i-1' }),
    archiveStorageKey({ workspaceId: 'ws-b', instanceId: 'i-1' }),
  )
  assert.notEqual(
    archiveStorageKey({ workspaceId: 'ws-a', instanceId: 'i-1' }),
    archiveStorageKey({ workspaceId: 'ws-a', instanceId: 'i-2' }),
  )
})

test('parseArchivedIds 对损坏值 fail-open，不隐藏会话', () => {
  assert.deepEqual([...parseArchivedIds('{bad')], [])
  assert.deepEqual([...parseArchivedIds('{}')], [])
  assert.deepEqual([...parseArchivedIds('["a", 1, "", "b", "a"]')], ['a', 'b'])
})

test('setSessionArchived 支持归档/恢复并去重排序', () => {
  const storage = memoryStorage()
  const scope = { workspaceId: 'ws-a', instanceId: 'i-1' }
  setSessionArchived(storage, scope, 'b', true)
  setSessionArchived(storage, scope, 'a', true)
  setSessionArchived(storage, scope, 'b', true)
  assert.deepEqual([...readArchivedIds(storage, scope)].sort(), ['a', 'b'])
  setSessionArchived(storage, scope, 'a', false)
  assert.deepEqual([...readArchivedIds(storage, scope)], ['b'])
})

test('不同 scope 数据不串', () => {
  const storage = memoryStorage()
  const a = { workspaceId: 'ws-a', instanceId: 'i-1' }
  const b = { workspaceId: 'ws-b', instanceId: 'i-1' }
  setSessionArchived(storage, a, 's-1', true)
  assert.equal(readArchivedIds(storage, a).has('s-1'), true)
  assert.equal(readArchivedIds(storage, b).has('s-1'), false)
})
