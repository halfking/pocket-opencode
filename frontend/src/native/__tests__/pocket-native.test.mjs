/**
 * pocket-native Web filesystem + share 单测（2026-09-25 Phase 9.1 + 9.3 增量）。
 *
 * 覆盖 makeWebFilesystem 的语义契约（node 端用 Map-backed fake store 跑，
 * 无需 fake-indexeddb）：
 *   1. writeFile 走 store.put 持久化
 *   2. readFile 把 store 数据反序列化成 base64 字符串
 *   3. deleteFile 转发 store.delete；不存在不抛错
 *   4. getUri 返回 data URL 形式（web 没有真沙盒）
 *   5. 任何 store 抛错都会向上抛（不静默吞）
 *   6. webFsKey 用 `pocket:fs:<dir>:<path>` 形式
 *
 * 平台注：createWebShare / createAndroidBridge.share 涉及 DOM 与动态 import，
 * 单测只覆盖纯逻辑 makeWebFilesystem 工厂；平台分发由集成测试覆盖。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import {
  makeWebFilesystem,
  webFsKey,
} from '../pocket-native.ts'

/** Map-backed fake store，满足 WebFilesystemStore 契约。 */
function makeMemoryStore() {
  /** @type {Map<string, string>} */
  const kv = new Map()
  return {
    kv,
    get: async (/** @type {string} */ key) => kv.get(key),
    put: async (/** @type {string} */ key, /** @type {string} */ value) => {
      kv.set(key, value)
    },
    delete: async (/** @type {string} */ key) => {
      kv.delete(key)
    },
  }
}

test('webFsKey: 输出 pocket:fs:<dir>:<path>', () => {
  assert.equal(webFsKey('data', 'media/foo.png'), 'pocket:fs:data:media/foo.png')
  assert.equal(webFsKey('documents', 'flashcards.json'), 'pocket:fs:documents:flashcards.json')
  assert.equal(webFsKey('cache', 'a/b/c.bin'), 'pocket:fs:cache:a/b/c.bin')
})

test('writeFile: 走 store.put 持久化(base64)', async () => {
  const store = makeMemoryStore()
  const fs = makeWebFilesystem(store)

  await fs.writeFile('media/foo.png', 'aGVsbG8=', 'data')
  assert.equal(store.kv.get('pocket:fs:data:media/foo.png'), 'aGVsbG8=')
})

test('writeFile: 默认 directory = data', async () => {
  const store = makeMemoryStore()
  const fs = makeWebFilesystem(store)

  await fs.writeFile('notes/a.txt', 'Y2FyZA==')
  assert.equal(store.kv.has('pocket:fs:data:notes/a.txt'), true)
  assert.equal(store.kv.has('pocket:fs:cache:notes/a.txt'), false)
})

test('readFile: 反序列化成 base64 字符串', async () => {
  const store = makeMemoryStore()
  const fs = makeWebFilesystem(store)
  store.kv.set('pocket:fs:data:notes/a.txt', '5q+U5rWL6K+V')

  const text = await fs.readFile('notes/a.txt', 'data')
  assert.equal(text, '5q+U5rWL6K+V')
})

test('readFile: 不存在时抛[PocketNative:web] 错误', async () => {
  const store = makeMemoryStore()
  const fs = makeWebFilesystem(store)

  await assert.rejects(
    fs.readFile('missing.bin', 'data'),
    /\[PocketNative:web\] filesystem\.readFile: not found: data:missing\.bin/,
  )
})

test('deleteFile: 转发到 store.delete', async () => {
  const store = makeMemoryStore()
  const fs = makeWebFilesystem(store)
  store.kv.set('pocket:fs:data:media/x.png', 'AAAA')

  await fs.deleteFile('media/x.png', 'data')
  assert.equal(store.kv.has('pocket:fs:data:media/x.png'), false)
})

test('deleteFile: 不存在不抛错(与 @capacitor/filesystem 对齐)', async () => {
  const store = makeMemoryStore()
  const fs = makeWebFilesystem(store)

  await assert.doesNotReject(fs.deleteFile('never-existed.bin', 'data'))
})

test('getUri: 返回 data URL 形式', async () => {
  const store = makeMemoryStore()
  const fs = makeWebFilesystem(store)
  store.kv.set('pocket:fs:data:media/foo.png', 'aGVsbG8=')

  const { uri } = await fs.getUri('media/foo.png', 'data')
  assert.equal(uri, 'data:application/octet-stream;base64,aGVsbG8=')
})

test('store.put 抛错时不静默吞,异常透传', async () => {
  const failingStore = {
    get: async () => undefined,
    put: async () => {
      throw new Error('quota exceeded')
    },
    delete: async () => {},
  }
  const fs = makeWebFilesystem(/** @type {any} */ (failingStore))
  await assert.rejects(
    fs.writeFile('media/x.png', 'AA', 'data'),
    /quota exceeded/,
  )
})

test('store.get 抛错时 readFile 同样透传', async () => {
  const missingStore = {
    get: async () => {
      throw new Error('gone')
    },
    put: async () => {},
    delete: async () => {},
  }
  const fs = makeWebFilesystem(/** @type {any} */ (missingStore))
  await assert.rejects(
    fs.readFile('media/x.png', 'data'),
    /gone/,
  )
})

test('directory 隔离:documents 与 data 不串', async () => {
  const store = makeMemoryStore()
  const fs = makeWebFilesystem(store)

  await fs.writeFile('flashcards.json', 'eyJ2IjoxfQ==', 'data')
  await fs.writeFile('flashcards.json', 'eyJ2IjoxfQ==', 'documents')

  assert.equal(store.kv.get('pocket:fs:data:flashcards.json'), 'eyJ2IjoxfQ==')
  assert.equal(store.kv.get('pocket:fs:documents:flashcards.json'), 'eyJ2IjoxfQ==')
})