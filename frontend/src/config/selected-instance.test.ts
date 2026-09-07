import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import {
  SELECTED_INSTANCE_ID_KEY,
  SELECTED_INSTANCE_KEY,
  clearSelectedInstance,
  readSelectedInstance,
  writeSelectedInstance,
} from './selected-instance.ts'

function memoryStorage(init: Record<string, string> = {}): Storage {
  const map = new Map(Object.entries(init))
  return {
    get length() {
      return map.size
    },
    clear() {
      map.clear()
    },
    getItem(key: string) {
      return map.has(key) ? map.get(key)! : null
    },
    key(index: number) {
      return [...map.keys()][index] ?? null
    },
    removeItem(key: string) {
      map.delete(key)
    },
    setItem(key: string, value: string) {
      map.set(key, String(value))
    },
  }
}

describe('selected instance', () => {
  it('writes JSON and id together so session routes can recover', () => {
    const storage = memoryStorage()
    writeSelectedInstance(
      { id: 'oc-1', displayName: '本机 OpenCode', environment: 'development' },
      storage,
    )
    assert.equal(storage.getItem(SELECTED_INSTANCE_ID_KEY), 'oc-1')
    const read = readSelectedInstance(storage)
    assert.equal(read?.id, 'oc-1')
    assert.equal(read?.displayName, '本机 OpenCode')
  })

  it('reads id-only fallback when JSON is missing', () => {
    const storage = memoryStorage({ [SELECTED_INSTANCE_ID_KEY]: 'oc-2' })
    const read = readSelectedInstance(storage)
    assert.equal(read?.id, 'oc-2')
    assert.equal(read?.displayName, 'oc-2')
  })

  it('clears both keys', () => {
    const storage = memoryStorage({
      [SELECTED_INSTANCE_KEY]: '{"id":"oc-1","displayName":"x"}',
      [SELECTED_INSTANCE_ID_KEY]: 'oc-1',
    })
    clearSelectedInstance(storage)
    assert.equal(readSelectedInstance(storage), null)
    assert.equal(storage.getItem(SELECTED_INSTANCE_ID_KEY), null)
  })

  it('rejects write without id', () => {
    const storage = memoryStorage()
    assert.throws(() => writeSelectedInstance({ id: '', displayName: 'x' }, storage), /instance-id/)
  })
})
