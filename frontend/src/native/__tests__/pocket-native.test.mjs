/**
 * pocket-native Web filesystem + share 单测（2026-09-25 Phase 9.1 + 9.3 增量）。
 *
 * 覆盖 makeWebFilesystem 的语义契约（node 端用 Map-backed fake store 跑，
 * 无需 fake-indexeddb）：
 *   1. writeFile 走 store.put 持久化
 *   2. readFile 把 store 数据反序列化成 base64 字符串
 *   3. deleteFile 转发 store.delete；**仅**「不存在」不抛错
 *   4. getUri 返回 data URL 形式（web 没有真沙盒）
 *   5. 任何 store 抛错都会向上抛（不静默吞）——§877 起 **delete 也在内**
 *      （旧实现 deleteFile 是裸 `catch {}`，本文件头第 5 条此前是**假的**）
 *   6. webFsKey 用 `pocket:fs:<dir>:<path>` 形式
 *   7. §877 D46：IndexedDB 写事务必须以 `tx.oncomplete` 为准
 *   8. §877 D48：每次 openDb 都要 close（成功与失败路径都算）
 *
 * 平台注：createWebShare / createAndroidBridge.share 涉及 DOM 与动态 import，
 * 单测只覆盖纯逻辑 makeWebFilesystem 工厂；平台分发由集成测试覆盖。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import {
  makeWebFilesystem,
  webFsKey,
  createIndexedDbWebStore,
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

/* ═══════════════════════════════════════════════════════════════════════════
 * §877：IndexedDB 层（D46 写事务假成功 / D48 连接不归还 / D47 删除侧吞错）
 *
 * ⚠ 这里手写替身而不是用 fake-indexeddb（不在依赖里）。**关键不是造一个浏览器**，
 *   而是造出 IndexedDB 的**事件顺序**：`request.onsuccess` 先于 `tx.oncomplete` /
 *   `tx.onabort`。D46 的全部要害就在这个顺序上 —— 只要 abort 发生在请求成功之前，
 *   「按请求成功 resolve」和「按事务提交 resolve」就测不出区别，判据会假绿。
 * ═══════════════════════════════════════════════════════════════════════════ */

/**
 * 最小 IndexedDB 替身。
 * @param {{writeOutcome?: 'commit'|'abort'}} opts writeOutcome 决定写事务结局
 * @returns {{indexedDB: object, stats: {opens: number, closes: number}, data: Map<string, any>}}
 */
function makeFakeIndexedDb(opts = {}) {
  const writeOutcome = opts.writeOutcome ?? 'commit'
  const stats = { opens: 0, closes: 0 }
  const data = new Map()

  function newTx(mode) {
    /** 数据只在事务提交时才落进 data —— abort 时回滚，与真实语义一致。 */
    const pending = []
    const tx = {
      error: null,
      oncomplete: null,
      onerror: null,
      onabort: null,
      objectStore() {
        return {
          put: (value, key) => request(tx, mode, pending, () => void pending.push(() => data.set(key, value))),
          get: (key) => request(tx, mode, pending, () => data.get(key)),
          delete: (key) => request(tx, mode, pending, () => void pending.push(() => data.delete(key))),
        }
      },
    }
    return tx
  }

  /** 复刻规范顺序：request 先 success，事务结局在其后（另一个宏任务）。 */
  function request(tx, mode, pending, compute) {
    const req = { result: undefined, error: null, onsuccess: null, onerror: null }
    setTimeout(() => {
      req.result = compute()
      if (req.onsuccess) req.onsuccess({ target: req })
      setTimeout(() => {
        // 只有**写事务**才有"提交阶段失败"这回事；只读事务恒定提交。
        if (writeOutcome === 'abort' && mode === 'readwrite') {
          tx.error = new Error('fake: 提交阶段失败（配额 / I/O）')
          if (tx.onabort) tx.onabort({ target: tx })
          if (tx.onerror) tx.onerror({ target: tx })
          return
        }
        for (const p of pending) p()
        if (tx.oncomplete) tx.oncomplete({ target: tx })
      }, 0)
    }, 0)
    return req
  }

  const indexedDB = {
    open(_name, _version) {
      const req = { result: undefined, onupgradeneeded: null, onsuccess: null, onerror: null }
      setTimeout(() => {
        stats.opens++
        req.result = {
          objectStoreNames: { contains: () => true },
          createObjectStore: () => ({}),
          transaction: (_store, mode) => newTx(mode),
          close: () => void stats.closes++,
        }
        if (req.onsuccess) req.onsuccess({ target: req })
      }, 0)
      return req
    },
  }

  return { indexedDB, stats, data }
}

/** 在替身 IndexedDB 上跑一段逻辑。 */
async function withFakeIndexedDb(opts, fn) {
  const fake = makeFakeIndexedDb(opts)
  const prev = globalThis.indexedDB
  globalThis.indexedDB = fake.indexedDB
  try {
    return await fn(fake)
  } finally {
    if (prev === undefined) delete globalThis.indexedDB
    else globalThis.indexedDB = prev
  }
}

test('§877 D46 正控:事务提交后 put 才算成功,值能读回', async () => {
  await withFakeIndexedDb({ writeOutcome: 'commit' }, async (fake) => {
    const store = createIndexedDbWebStore()
    const fs = makeWebFilesystem(store)

    await fs.writeFile('media/x.png', 'aGVsbG8=', 'data')
    assert.equal(await fs.readFile('media/x.png', 'data'), 'aGVsbG8=')
    assert.equal(fake.data.get('pocket:fs:data:media/x.png'), 'aGVsbG8=')
  })
})

test('§877 D46:事务 abort 时 put 必须 reject(不是"排队成功"就报成功)', async () => {
  await withFakeIndexedDb({ writeOutcome: 'abort' }, async (fake) => {
    const fs = makeWebFilesystem(createIndexedDbWebStore())

    // ⚠ 断言错误消息本身：`assert.rejects` 接受任何 reject（§876 M33 教训），
    //   只断言「reject 了」的话，替身自己抛的 TypeError 也会让它变绿。
    await assert.rejects(
      fs.writeFile('media/x.png', 'aGVsbG8=', 'data'),
      /提交阶段失败/,
    )
    // abort ⇒ 数据回滚，不得留下任何"看起来写进去了"的字节
    assert.equal(fake.data.has('pocket:fs:data:media/x.png'), false)
  })
})

test('§877 D46 对照组:同一替身上,旧写法(request 成功即 resolve)确实会假成功', async () => {
  // ⚠ 没有这一条，上面两条就是「我编的顺序」：必须证明替身真的能造出
  //   「先 request 成功、后事务 abort」，且旧写法在这个替身上**读数不同**。
  await withFakeIndexedDb({ writeOutcome: 'abort' }, async () => {
    const legacyStore = makeLegacyRequestOnlyStore()
    const legacyFs = makeWebFilesystem(legacyStore)

    // 旧实现：put 会 resolve —— 这就是缺陷本身
    await legacyFs.writeFile('media/x.png', 'aGVsbG8=', 'data')
    assert.equal(true, true, '对照组：旧写法在 abort 替身上不报错')
  })
})

/** 复刻修复前的 withStore：request 成功就 resolve（§877 D46 的原始缺陷形态）。 */
function makeLegacyRequestOnlyStore() {
  return {
    get: async (key) => new Promise((resolve, reject) => {
      const req = globalThis.indexedDB.open('pocket-fs', 1)
      req.onsuccess = () => {
        const db = req.result
        const r = db.transaction('files', 'readonly').objectStore('files').get(key)
        r.onsuccess = () => resolve(r.result)
        r.onerror = () => reject(r.error)
      }
    }),
    put: async (key, value) => new Promise((resolve, reject) => {
      const req = globalThis.indexedDB.open('pocket-fs', 1)
      req.onsuccess = () => {
        const tx = req.result.transaction('files', 'readwrite')
        const r = tx.objectStore('files').put(value, key)
        r.onsuccess = () => resolve() // ← 缺陷：不等 tx.oncomplete
        r.onerror = () => reject(r.error)
        tx.onerror = () => reject(tx.error)
        tx.onabort = () => reject(tx.error)
      }
    }),
    delete: async () => {},
  }
}

test('§877 D48:每次读写都要 close 连接(提交与 abort 两条路径都算)', async () => {
  for (const writeOutcome of ['commit', 'abort']) {
    await withFakeIndexedDb({ writeOutcome }, async (fake) => {
      const fs = makeWebFilesystem(createIndexedDbWebStore())
      for (let i = 0; i < 5; i++) {
        await fs.readFile(`f${i}.bin`, 'data').catch(() => {})
        await fs.writeFile(`f${i}.bin`, 'AAAA', 'data').catch(() => {})
      }
      assert.equal(
        fake.stats.closes,
        fake.stats.opens,
        `writeOutcome=${writeOutcome}: opens=${fake.stats.opens} closes=${fake.stats.closes}`,
      )
    })
  }
})

test('§877 D47:delete 失败(配额/事务中止)必须上抛,不能报成"已删除"', async () => {
  const failingStore = {
    get: async () => undefined,
    put: async () => {},
    delete: async () => {
      throw new Error('QuotaExceededError')
    },
  }
  const fs = makeWebFilesystem(/** @type {any} */ (failingStore))
  await assert.rejects(fs.deleteFile('media/x.png', 'data'), /QuotaExceededError/)
})

test('§877 D47:仍然只把"文件本来就不存在"当作成功', async () => {
  const notFoundStore = {
    get: async () => undefined,
    put: async () => {},
    delete: async () => {
      throw new Error('[PocketNative:web] filesystem.deleteFile: not found: data:media/x.png')
    },
  }
  const fs = makeWebFilesystem(/** @type {any} */ (notFoundStore))
  await assert.doesNotReject(fs.deleteFile('media/x.png', 'data'))
})