// syncPolicyStore.test.ts — 「存储与同步」策略 store 的行为判据。
//
// 覆盖（对应需求 6）：
//   1. shouldSync / moduleMode 纯函数（显式值 > system_default > cloud 兜底）；
//   2. load() 降级链：远端失败 → 有效缓存 → 暂停门户模块上传；
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
import { normalizeSyncPolicy, isPortalSyncModule } from '../../api/syncPolicyContract.ts'

import {
  installSyncPolicyStorage,
  installSyncPolicyActivationHost,
  installSyncPolicyScopeResolver,
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
    keys: () => map.keys(),
    getItem: (k: string) => map.get(k) ?? null,
    setItem: (k: string, v: string) => void map.set(k, v),
    removeItem: (k: string) => void map.delete(k),
    dump: () => map,
  }
}

let storage = memoryStorage()
function installHost() {
  installSyncPolicyActivationHost({
    captureContext() {
      const scope = storage.getItem('pocket_user') || storage.getItem('pocket_workspace_id')
        ? JSON.stringify([storage.getItem('pocket_user') ?? '', storage.getItem('pocket_workspace_id') ?? '']) : ''
      return { scope, isCurrent: () => true }
    },
    async activate(modes, publish) { publish(modes) },
  })
}

beforeEach(() => {
  storage = memoryStorage()
  installSyncPolicyStorage(storage)
  installHost()
  setActivePinia(createPinia())
})

test('策略缺失模块必须继承 system_default=local', async () => {
  const policy = normalizeSyncPolicy({ system_default: 'local', allow_override: true, modules: { rss: 'cloud' } })
  assert.equal(policy.modules.rss, 'cloud')
  assert.equal(policy.modules.email, 'local')
})

test('缓存无法写入时同步引擎仍遵守已生效的 local 策略', async () => {
  installSyncPolicyStorage({ ...storage, setItem() { throw new Error('quota') } })
  installHost()
  const store = useSyncPolicyStore()
  await assert.rejects(() => store.applyPolicy(normalizeSyncPolicy({ system_default: 'cloud', modules: { notes: 'local' } }), 1), /persist/)
  assert.equal(readCachedModuleMode('notes'), 'local')
})

test('切回 cloud 必须等服务端确认后才允许上传', async () => {
  const store = useSyncPolicyStore()
  await store.applyPolicy(normalizeSyncPolicy({ system_default: 'cloud', allow_override: true, modules: { notes: 'local' } }), 1)
  const orig = syncPolicyTransport.putModules
  let resolve!: (value: ReturnType<typeof normalizeSyncPolicy>) => void
  syncPolicyTransport.putModules = (modules) => new Promise((done) => { resolve = () => done(normalizeSyncPolicy({ system_default: 'cloud', modules })) })
  try {
    const pending = store.setModule('notes', 'cloud')
    assert.equal(readCachedModuleMode('notes'), 'local', '等待 PUT 期间不得上传')
    resolve(normalizeSyncPolicy(undefined))
    await pending
    assert.equal(readCachedModuleMode('notes'), 'cloud')
  } finally { syncPolicyTransport.putModules = orig }
})

test('切换账号后不得沿用上一账号的 cloud 缓存', async () => {
  storage.setItem('pocket_user', 'alice')
  storage.setItem('pocket_workspace_id', 'ws-a')
  const store = useSyncPolicyStore()
  await store.applyPolicy(normalizeSyncPolicy({ system_default: 'cloud' }), 1)
  assert.equal(readCachedModuleMode('email'), 'cloud')
  storage.setItem('pocket_user', 'bob')
  assert.equal(readCachedModuleMode('email'), 'local')
})

test('身份切换期间返回的旧账号 PUT 不得污染新账号', async () => {
  storage.setItem('pocket_user', 'alice')
  const store = useSyncPolicyStore()
  await store.applyPolicy(normalizeSyncPolicy({ system_default: 'cloud', allow_override: true }), 1)
  const orig = syncPolicyTransport.putModules
  let finish!: () => void
  syncPolicyTransport.putModules = (modules) => new Promise((done) => { finish = () => done(normalizeSyncPolicy({ system_default: 'cloud', modules })) })
  try {
    const pending = store.setModule('notes', 'local')
    storage.setItem('pocket_user', 'bob')
    await store.$reset()
    finish()
    assert.equal(await pending, false)
    assert.equal(store.modules.email, 'local')
    assert.equal(readCachedModuleMode('email'), 'local')
  } finally { syncPolicyTransport.putModules = orig }
})

test('A→B→A 后旧 PUT/GET 不得覆盖新会话或清除新保存状态', async () => {
  storage.setItem('pocket_user', 'alice')
  const store = useSyncPolicyStore()
  await store.applyPolicy(normalizeSyncPolicy({ system_default: 'cloud', allow_override: true }), 1)
  const origGet = syncPolicyTransport.fetchPolicy
  const origPut = syncPolicyTransport.putModules
  let finishGet!: () => void
  let finishPut!: () => void
  syncPolicyTransport.fetchPolicy = () => new Promise((done) => { finishGet = () => done(normalizeSyncPolicy({ system_default: 'cloud' })) })
  syncPolicyTransport.putModules = () => new Promise((done) => { finishPut = () => done(normalizeSyncPolicy({ system_default: 'cloud' })) })
  try {
    const get = store.load()
    const put = store.setModule('notes', 'local')
    storage.setItem('pocket_user', 'bob'); await store.$reset()
    storage.setItem('pocket_user', 'alice'); await store.$reset()
    await store.applyPolicy(normalizeSyncPolicy({ system_default: 'local', allow_override: true }), 2)
    store.saving = true
    finishGet(); finishPut()
    await get
    assert.equal(await put, false)
    assert.equal(store.modules.email, 'local')
    assert.equal(store.saving, true)
  } finally {
    syncPolicyTransport.fetchPolicy = origGet
    syncPolicyTransport.putModules = origPut
  }
})

test('四模块响应保留未接入门户策略模块的既有 local 缓存', async () => {
  storage.setItem(SYNC_POLICY_CACHE_KEY, JSON.stringify({ policy: normalizeSyncPolicy({ system_default: 'cloud', modules: { 'ai-chat': 'local', tasks: 'local' } }) }))
  const store = useSyncPolicyStore()
  await store.applyPolicy(normalizeSyncPolicy({ system_default: 'cloud', modules: { notes: 'local' } }), 1)
  assert.equal(store.modules['ai-chat'], 'local')
  assert.equal(store.modules.tasks, 'local')
  assert.equal(await store.setModule('ai-chat', 'cloud'), false)
})

test('local PUT 在途时新 GET 不得覆盖乐观 local', async () => {
  const store = useSyncPolicyStore()
  await store.applyPolicy(normalizeSyncPolicy({ system_default: 'cloud', allow_override: true }), 1)
  const origGet = syncPolicyTransport.fetchPolicy
  const origPut = syncPolicyTransport.putModules
  let finish!: () => void
  let gets = 0
  syncPolicyTransport.fetchPolicy = async () => { gets++; return normalizeSyncPolicy({ system_default: 'cloud' }) }
  syncPolicyTransport.putModules = (modules) => new Promise((done) => { finish = () => done(normalizeSyncPolicy({ system_default: 'cloud', modules })) })
  try {
    const saving = store.setModule('notes', 'local')
    await store.load()
    assert.equal(gets, 0)
    assert.equal(readCachedModuleMode('notes'), 'local')
    finish(); await saving
  } finally {
    syncPolicyTransport.fetchPolicy = origGet
    syncPolicyTransport.putModules = origPut
  }
})

test('策略写入期间不并发提交，过期的 GET 不覆盖用户设置', async () => {
  const store = useSyncPolicyStore()
  await store.applyPolicy(normalizeSyncPolicy({ system_default: 'cloud', allow_override: true }), 1)
  const origGet = syncPolicyTransport.fetchPolicy
  const origPut = syncPolicyTransport.putModules
  let resolveGet!: () => void
  let resolvePut!: () => void
  syncPolicyTransport.fetchPolicy = () => new Promise((done) => { resolveGet = () => done(normalizeSyncPolicy({ system_default: 'cloud' })) })
  syncPolicyTransport.putModules = (modules) => new Promise((done) => { resolvePut = () => done(normalizeSyncPolicy({ system_default: 'cloud', modules })) })
  try {
    const loading = store.load()
    const saving = store.setModule('notes', 'local')
    assert.equal(await store.setModule('rss', 'local'), false)
    resolvePut()
    await saving
    resolveGet()
    await loading
    assert.equal(store.modules.notes, 'local')
    assert.equal(store.saving, false)
  } finally {
    syncPolicyTransport.fetchPolicy = origGet
    syncPolicyTransport.putModules = origPut
  }
})

// ---------------------------------------------------------------------------
// 1. 纯函数
// ---------------------------------------------------------------------------

test('shouldSync：显式 modules 值优先', async () => {
  const policy = { system_default: 'cloud' as const, modules: { notes: 'local' as const } }
  assert.equal(shouldSync(policy, 'notes'), false, '显式 local 必须不同步')
  assert.equal(shouldSync(policy, 'email'), true, '未覆盖模块走默认 cloud')
})

test('shouldSync：modules 缺项回退 system_default', async () => {
  assert.equal(shouldSync({ system_default: 'local' }, 'rss'), false, '系统默认 local 时未覆盖模块应跳过')
  assert.equal(shouldSync({ system_default: 'cloud' }, 'rss'), true)
})

test('shouldSync：明确 cloud 默认时缺项可同步', async () => {
  assert.equal(shouldSync({ system_default: 'cloud', modules: {} }, 'ai-chat'), true)
  assert.equal(moduleMode({ system_default: 'cloud', modules: { ai: 'bogus' as never } }, 'ai-chat'), 'cloud')
})

test('登录态写盘失败时也按内存身份隔离策略', async () => {
  let user = 'alice'
  installSyncPolicyScopeResolver(() => user)
  installSyncPolicyActivationHost({ captureContext: () => ({ scope: user, isCurrent: () => true }), async activate(m,p) { p(m) } })
  const store = useSyncPolicyStore()
  await store.applyPolicy(normalizeSyncPolicy({ system_default: 'cloud' }), 1)
  user = 'bob'
  assert.equal(readCachedModuleMode('email'), 'local')
})

// ---------------------------------------------------------------------------
// 2. load() 降级链
// ---------------------------------------------------------------------------

test('load()：无缓存 + 远端失败 ⇒ 暂停上传，不抛错且留 loadError', async () => {
  const orig = syncPolicyTransport.fetchPolicy
  syncPolicyTransport.fetchPolicy = async () => {
    throw new Error('portal unreachable')
  }
  try {
    const store = useSyncPolicyStore()
    await store.load()
    for (const m of ['ai-chat', 'email', 'rss', 'notes', 'meetings', 'tasks'] as const) {
      assert.equal(store.modules[m], isPortalSyncModule(m) ? 'local' : 'cloud', `${m} 未确认门户策略时只暂停受管模块上传`)
      assert.equal(readCachedModuleMode(m), isPortalSyncModule(m) ? 'local' : 'cloud')
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
    assert.equal(store.modules.meetings, 'local', '损坏缓存不得自动开启上传')
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
    await store.applyPolicy(normalizeSyncPolicy({ system_default: 'cloud', allow_override: true }), 1)
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

test('setModule()：PUT 失败 ⇒ 保持local与待保存标记并抛错', async () => {
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
    await store.restoreFromCache()
    await assert.rejects(
      () => store.setModule('email', 'local'),
      /put failed/,
      '失败必须抛错让组件 toast',
    )
    assert.equal(store.modules.email, 'local', '本机local意图不能回滚')
    assert.equal(readCachedModuleMode('email'), 'local')
    assert.deepEqual(store.pendingLocal, ['email'])
    assert.match(store.saveError, /put failed/)
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
    await store.restoreFromCache()
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

test('recordSyncReportEntry：按模块覆盖合并并落 localStorage', async () => {
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

test('recordSyncReportEntry：活动的 Pinia store 同步看到新报告', async () => {
  const store = useSyncPolicyStore()
  recordSyncReportEntry({ module: 'meetings', status: 'skipped', at: 1 })
  assert.equal(store.lastSyncReport.length, 1)
  assert.equal(store.lastSyncReport[0].module, 'meetings')
})

test('报告缓存损坏按空处理，不抛错', async () => {
  storage.setItem(SYNC_REPORT_CACHE_KEY, '[[[')
  const store = useSyncPolicyStore()
  assert.equal(store.lastSyncReport.length, 0)
})

// ---------------------------------------------------------------------------
// 5. 本地默认（未配置任何东西时与旧行为一致）
// ---------------------------------------------------------------------------

test('零配置：门户模块等待远端确认，未受管模块在默认快照激活后保留既有默认', async () => {
  for (const m of ['ai-chat', 'email', 'rss', 'notes', 'meetings', 'tasks'] as const) assert.equal(readCachedModuleMode(m), 'local')
  await useSyncPolicyStore().restoreFromCache()
  for (const m of ['ai-chat', 'email', 'rss', 'notes', 'meetings', 'tasks'] as const) {
    assert.equal(readCachedModuleMode(m), isPortalSyncModule(m) ? 'local' : 'cloud')
  }
})
