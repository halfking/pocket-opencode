// syncPolicyStore.test.ts — 「存储与同步」策略 store 的行为判据。
//
// 覆盖（对应需求 6）：
//   1. shouldSync / moduleMode 纯函数（显式值 > system_default > cloud 兜底）；
//   2. load() 降级链：远端失败 → localStorage 缓存（op_sync_policy_v1）→ 默认全 cloud；
//   3. setModule() 乐观更新 + PUT 失败回滚（含缓存回滚）；
//   4. allow_override=false 时拒绝切换；
//   5. recordSyncReportEntry 按模块覆盖合并 + readCachedModuleMode 免 Pinia 读；
//   6. 缓存损坏（非法 JSON）按「无缓存」处理，不抛错。
//
// 运行（仓库标准基建，package.json 无 vitest 依赖）：
//   node --test src/stores/__tests__/syncPolicyStore.test.ts
// 同时兼容 vitest 跑法（npx vitest run 本文件）：下方双跑者桥接会把
// test/beforeEach 解析到当前可用的那一个 —— vitest 环境里 'vitest' 模块
// 可解析；node --test 环境里解析失败自然回落 node:test。仓库其余测试
// 文件保持纯 node:test 写法不受影响。

import { strict as assert } from 'node:assert'
import { createPinia, setActivePinia } from 'pinia'

import {
  installSyncPolicyStorage,
  moduleMode,
  readCachedModuleMode,
  readCachedSyncPolicy,
  readCachedSyncReport,
  recordSyncReportEntry,
  shouldSync,
  syncPolicyTransport,
  SYNC_POLICY_CACHE_KEY,
  SYNC_REPORT_CACHE_KEY,
  useSyncPolicyStore,
} from '../syncPolicyStore.ts'

// ── 双跑者桥接：vitest 优先，node:test 兜底（不新增依赖）─────────────
type TestFn = (name: string, fn: () => void | Promise<void>) => void
type BeforeEachFn = (fn: () => void | Promise<void>) => void
let test: TestFn
let beforeEach: BeforeEachFn
try {
  // 仅在 vitest 环境下可解析（node --test 会抛 ERR_MODULE_NOT_FOUND 落到 catch）。
  const v = await import('vitest')
  test = v.test as TestFn
  beforeEach = v.beforeEach as BeforeEachFn
} catch {
  const nt = await import('node:test')
  test = nt.test as unknown as TestFn
  beforeEach = nt.beforeEach as unknown as BeforeEachFn
}

/** 内存存储（localStorage 的最小契约实现）。 */
function memoryStorage() {
  const map = new Map<string, string>()
  return {
    getItem: (k: string) => map.get(k) ?? null,
    setItem: (k: string, v: string) => void map.set(k, v),
    removeItem: (k: string) => void map.delete(k),
    dump: () => map,
  }
}

let storage = memoryStorage()

beforeEach(() => {
  storage = memoryStorage()
  installSyncPolicyStorage(storage)
  setActivePinia(createPinia())
})

// ---------------------------------------------------------------------------
// 1. 纯函数
// ---------------------------------------------------------------------------

test('shouldSync：显式 modules 值优先', () => {
  const policy = { system_default: 'cloud' as const, modules: { notes: 'local' as const } }
  assert.equal(shouldSync(policy, 'notes'), false, '显式 local 必须不同步')
  assert.equal(shouldSync(policy, 'email'), true, '未覆盖模块走默认 cloud')
})

test('shouldSync：modules 缺项回退 system_default', () => {
  assert.equal(shouldSync({ system_default: 'local' }, 'rss'), false, '系统默认 local 时未覆盖模块应跳过')
  assert.equal(shouldSync({ system_default: 'cloud' }, 'rss'), true)
})

test('shouldSync：未知/残缺输入一律视为 cloud（不同步才需要显式声明）', () => {
  assert.equal(shouldSync({ system_default: 'cloud', modules: {} }, 'ai-chat'), true)
  assert.equal(moduleMode({ system_default: 'cloud', modules: { ai: 'bogus' as never } }, 'ai-chat'), 'cloud')
})

// ---------------------------------------------------------------------------
// 2. load() 降级链
// ---------------------------------------------------------------------------

test('load()：无缓存 + 远端失败 ⇒ 默认全 cloud，不抛错且留 loadError', async () => {
  const orig = syncPolicyTransport.fetchPolicy
  syncPolicyTransport.fetchPolicy = async () => {
    throw new Error('portal unreachable')
  }
  try {
    const store = useSyncPolicyStore()
    await store.load()
    for (const m of ['ai-chat', 'email', 'rss', 'notes', 'meetings', 'tasks'] as const) {
      assert.equal(store.modules[m], 'cloud', `${m} 应为默认 cloud`)
      assert.equal(readCachedModuleMode(m), 'cloud')
    }
    assert.ok(store.loadError.includes('portal unreachable'), '失败原因应写入 loadError')
    assert.equal(store.loaded, true)
  } finally {
    syncPolicyTransport.fetchPolicy = orig
  }
})

test('load()：远端失败 ⇒ 回退 localStorage 缓存（op_sync_policy_v1）', async () => {
  storage.setItem(
    SYNC_POLICY_CACHE_KEY,
    JSON.stringify({
      policy: {
        system_default: 'cloud',
        allow_override: true,
        modules: {
          'ai-chat': 'cloud', email: 'local', rss: 'cloud',
          notes: 'local', meetings: 'cloud', tasks: 'cloud',
        },
      },
      lastFetchedAt: 12345,
    }),
  )
  const orig = syncPolicyTransport.fetchPolicy
  syncPolicyTransport.fetchPolicy = async () => {
    throw new Error('down')
  }
  try {
    const store = useSyncPolicyStore()
    // state 初始化即 hydrate 缓存；load 失败必须**保住**这份缓存值。
    assert.equal(store.modules.email, 'local')
    await store.load()
    assert.equal(store.modules.email, 'local', '缓存回退后 email 保持 local')
    assert.equal(store.modules.notes, 'local')
    assert.equal(readCachedModuleMode('email'), 'local', '同步引擎侧也应读到 local')
    assert.equal(store.lastFetchedAt, 12345)
  } finally {
    syncPolicyTransport.fetchPolicy = orig
  }
})

test('load()：远端成功 ⇒ 覆盖 state 并写缓存', async () => {
  const orig = syncPolicyTransport.fetchPolicy
  syncPolicyTransport.fetchPolicy = async () => ({
    system_default: 'cloud',
    allow_override: true,
    modules: {
      'ai-chat': 'cloud', email: 'cloud', rss: 'local',
      notes: 'cloud', meetings: 'cloud', tasks: 'cloud',
    },
  })
  try {
    const store = useSyncPolicyStore()
    await store.load()
    assert.equal(store.modules.rss, 'local')
    assert.equal(store.loadError, '')
    const cached = readCachedSyncPolicy()
    assert.ok(cached, '远端成功后必须写缓存')
    assert.equal(cached!.policy.modules.rss, 'local')
    assert.ok(cached!.lastFetchedAt > 0)
  } finally {
    syncPolicyTransport.fetchPolicy = orig
  }
})

test('load()：缓存损坏（非法 JSON）按「无缓存」处理，不抛错', async () => {
  storage.setItem(SYNC_POLICY_CACHE_KEY, '{not-json')
  const orig = syncPolicyTransport.fetchPolicy
  syncPolicyTransport.fetchPolicy = async () => {
    throw new Error('down')
  }
  try {
    const store = useSyncPolicyStore()
    await store.load()
    assert.equal(store.modules.meetings, 'cloud', '损坏缓存 ⇒ 默认 cloud')
  } finally {
    syncPolicyTransport.fetchPolicy = orig
  }
})

// ---------------------------------------------------------------------------
// 3. setModule() 乐观更新 + 回滚
// ---------------------------------------------------------------------------

test('setModule()：成功路径 —— 乐观更新 + PUT 全量 modules + 缓存落盘', async () => {
  const seen: Array<Record<string, string>> = []
  const origPut = syncPolicyTransport.putModules
  syncPolicyTransport.putModules = async (modules) => {
    seen.push({ ...modules })
    return { system_default: 'cloud', allow_override: true, modules }
  }
  try {
    const store = useSyncPolicyStore()
    const ok = await store.setModule('meetings', 'local')
    assert.equal(ok, true)
    assert.equal(store.modules.meetings, 'local')
    assert.equal(seen.length, 1, 'PUT 恰好一次')
    assert.equal(seen[0].meetings, 'local', 'PUT body 带目标模块')
    assert.equal(seen[0].email, 'cloud', 'PUT body 是全量 modules')
    assert.equal(readCachedModuleMode('meetings'), 'local', '缓存同步落盘')
  } finally {
    syncPolicyTransport.putModules = origPut
  }
})

test('setModule()：PUT 失败 ⇒ 回滚 state 与缓存并抛错', async () => {
  storage.setItem(
    SYNC_POLICY_CACHE_KEY,
    JSON.stringify({
      policy: {
        system_default: 'cloud',
        allow_override: true,
        modules: {
          'ai-chat': 'cloud', email: 'cloud', rss: 'cloud',
          notes: 'cloud', meetings: 'cloud', tasks: 'cloud',
        },
      },
      lastFetchedAt: 99,
    }),
  )
  const origPut = syncPolicyTransport.putModules
  syncPolicyTransport.putModules = async () => {
    throw new Error('put failed')
  }
  try {
    const store = useSyncPolicyStore()
    await assert.rejects(
      () => store.setModule('email', 'local'),
      /put failed/,
      '失败必须抛错让组件 toast',
    )
    assert.equal(store.modules.email, 'cloud', 'state 回滚')
    assert.equal(readCachedModuleMode('email'), 'cloud', '缓存也必须回滚（否则引擎按新模式过滤）')
  } finally {
    syncPolicyTransport.putModules = origPut
  }
})

test('setModule()：allow_override=false ⇒ 拒绝切换且不触发 PUT', async () => {
  storage.setItem(
    SYNC_POLICY_CACHE_KEY,
    JSON.stringify({
      policy: {
        system_default: 'cloud',
        allow_override: false,
        modules: {
          'ai-chat': 'cloud', email: 'cloud', rss: 'cloud',
          notes: 'cloud', meetings: 'cloud', tasks: 'cloud',
        },
      },
      lastFetchedAt: 1,
    }),
  )
  let putCalls = 0
  const origPut = syncPolicyTransport.putModules
  syncPolicyTransport.putModules = async (modules) => {
    putCalls++
    return { system_default: 'cloud', allow_override: false, modules }
  }
  try {
    const store = useSyncPolicyStore()
    const ok = await store.setModule('rss', 'local')
    assert.equal(ok, false, '管控态应返回 false')
    assert.equal(putCalls, 0, '不得发 PUT')
    assert.equal(store.modules.rss, 'cloud')
  } finally {
    syncPolicyTransport.putModules = origPut
  }
})

// ---------------------------------------------------------------------------
// 4. 同步报告（引擎侧入口）
// ---------------------------------------------------------------------------

test('recordSyncReportEntry：按模块覆盖合并并落 localStorage', () => {
  recordSyncReportEntry({ module: 'email', status: 'skipped', at: 100 })
  recordSyncReportEntry({ module: 'ai-chat', status: 'synced', count: 3, at: 200 })
  // 同模块第二次上报覆盖旧条目（报告是「最近一次」语义）。
  recordSyncReportEntry({ module: 'email', status: 'synced', count: 5, at: 300 })
  const report = readCachedSyncReport()
  assert.equal(report.length, 2)
  const email = report.find((e) => e.module === 'email')
  assert.ok(email, 'email 条目存在')
  assert.equal(email.status, 'synced', '旧 skipped 被覆盖')
  assert.equal(email.count, 5)
  const aiChat = report.find((e) => e.module === 'ai-chat')
  assert.equal(aiChat.count, 3)
  // 报告缓存键与策略缓存键分离（清策略不该丢报告）。
  assert.ok(storage.dump().has(SYNC_REPORT_CACHE_KEY))
})

test('recordSyncReportEntry：活动的 Pinia store 同步看到新报告', () => {
  const store = useSyncPolicyStore()
  recordSyncReportEntry({ module: 'meetings', status: 'skipped', at: 1 })
  assert.equal(store.lastSyncReport.length, 1)
  assert.equal(store.lastSyncReport[0].module, 'meetings')
})

test('报告缓存损坏按空处理，不抛错', () => {
  storage.setItem(SYNC_REPORT_CACHE_KEY, '[[[')
  const store = useSyncPolicyStore()
  assert.equal(store.lastSyncReport.length, 0)
})

// ---------------------------------------------------------------------------
// 5. 本地默认（未配置任何东西时与旧行为一致）
// ---------------------------------------------------------------------------

test('零配置：readCachedModuleMode 一律 cloud（策略不改变既有同步）', () => {
  for (const m of ['ai-chat', 'email', 'rss', 'notes', 'meetings', 'tasks'] as const) {
    assert.equal(readCachedModuleMode(m), 'cloud')
  }
})
