// meeting-ownership.test.mjs — 会议/录音/待办/声纹四张本地表的**行归属**行为门。
//
// ★ 这条门要挡的是一个**真实的跨账号数据泄漏**（与 config 切片同款，但对象更重）：
//   local_meetings / local_meeting_segments / local_meeting_audio_parts /
//   local_todos / local_voiceprints 原本**没有账号列**，而切账号**不清理**共享的
//   SQLite 库（DB_NAME='lobster'，单库单连接）⇒ A 账号录的会议，B 账号登录后
//   **读得到、删得掉**；A 的录音分片 B 也能读；A 的声纹（生物特征 embedding）
//   B 同样能拿去匹配语音。
//
//   ⇒ 与 config 切片的差别：这些表**不可从云端恢复**（原始音频分片、本地 ASR
//     结果、声纹 embedding）⇒ 迁移时**必须搬运旧行**，只是归属置空串（未认领）。
//     用户已确认的边界：「未知归属的历史数据保持未认领」。
//
// ★ 形态：真 sql.js（不是 mock）+ 真 LocalDB 迁移 + 真操作租约。
//   归属必须来自**租约的 principal** ⇒ 夹具要真的走一次已验证登录。
//
// ★ 负控：把归属过滤摘掉 ⇒ 用例必须转红（见文件末尾的负控说明）。
import assert from 'node:assert/strict'
import { fileURLToPath } from 'node:url'
import { test } from 'node:test'
import { createServer } from 'vite'
import initSqlJs from 'sql.js'
import { createPinia, setActivePinia } from 'pinia'

const SQL = await initSqlJs()

async function fixture(run, seed) {
  const previousStorage = Object.getOwnPropertyDescriptor(globalThis, 'localStorage')
  const previousFetch = globalThis.fetch
  const previousGlobals = new Map(['window', 'document', 'navigator'].map(key => [key, Object.getOwnPropertyDescriptor(globalThis, key)]))
  const values = new Map(), stored = new Map(), pending = []
  const storage = { keys: () => values.keys(), getItem: key => values.get(key) ?? null, setItem: (key, value) => values.set(key, String(value)), removeItem: key => values.delete(key) }
  Object.defineProperty(globalThis, 'localStorage', { configurable: true, value: storage })
  for (const [key, value] of Object.entries({
    window: Object.assign(new EventTarget(), { location: { origin: 'https://fixture.invalid', hash: '' } }),
    document: Object.assign(new EventTarget(), { visibilityState: 'visible' }),
    navigator: Object.assign(new EventTarget(), { mediaDevices: Object.assign(new EventTarget(), { getUserMedia: async () => ({ getTracks: () => [{ stop() {} }] }) }) }),
  })) Object.defineProperty(globalThis, key, { configurable: true, value })
  setActivePinia(createPinia())
  let db, raw, driver, UtilsSQLite, originalDriverRun
  const track = promise => { pending.push(promise); promise.catch(() => {}); return promise }
  const settled = async () => {
    while (pending.length) {
      const results = await Promise.allSettled(pending.splice(0))
      for (const result of results) if (result.status === 'rejected' && result.reason?.name !== 'AbortError') throw result.reason
    }
  }
  const key = `__meetingOwnerSQLite_${Math.random().toString(36).slice(2)}`
  globalThis.fetch = async () => Response.json({})
  const vite = await createServer({ root: fileURLToPath(new URL('../../../../', import.meta.url)), configFile: false,
    server: { middlewareMode: true, hmr: false }, ssr: { noExternal: ['@capacitor-community/sqlite'] }, optimizeDeps: { noDiscovery: true }, plugins: [{
      name: 'meeting-owner-platform', enforce: 'pre',
      resolveId(source, importer) {
        if (source === '@capacitor-community/sqlite' && importer?.endsWith('/local-db.ts')) return '\0owner-sqlite'
        if (importer?.endsWith('/local-db.ts') && source === './sqlite-web-init') return '\0owner-web'
        if (importer?.endsWith('/local-db.ts') && source === './runtime-platform') return '\0owner-platform'
        if (source.endsWith('/native/lobster-init')) return '\0owner-ready'
      },
      load(id) {
        if (id === '\0owner-sqlite') return `export const { SQLiteConnection, SQLiteDBConnection, CapacitorSQLite } = globalThis[${JSON.stringify(key)}]`
        if (id === '\0owner-web') return 'export async function initSqliteWeb() {}'
        if (id === '\0owner-platform') return 'export const isWebFallbackRuntime = () => true'
        if (id === '\0owner-ready') return "import {ref} from 'vue'; export const lobsterReady = ref(true)"
      },
    }] })
  try {
    const { Database } = await vite.ssrLoadModule('/node_modules/jeep-sqlite/dist/collection/utils/database.js')
    ;({ UtilsSQLite } = await vite.ssrLoadModule('/node_modules/jeep-sqlite/dist/collection/utils/utils-sqlite.js'))
    originalDriverRun = UtilsSQLite.run
    const definitions = await vite.ssrLoadModule('/node_modules/@capacitor-community/sqlite/dist/esm/definitions.js')
    const { registerPlugin } = await vite.ssrLoadModule('/node_modules/@capacitor/core/dist/index.js')
    raw = new SQL.Database()
    const store = { getItem: async key => stored.get(key), setItem: async (key, value) => stored.set(key, value), removeItem: async key => stored.delete(key) }
    driver = new Database('lobster', 1, {}, store, false, '')
    driver.mDb = raw
    // ⚠ seed：在 init（= 建表 + 跑迁移）**之前**把库改成「旧形状」。
    //   这才是真实升级场景：用户的库里已经是旧表，开库时迁移要把它搬过去。
    if (seed) await seed(raw)
    const platform = {
      initWebStore: async () => {}, checkConnectionsConsistency: async () => ({ result: true }), createConnection: async () => {},
      open: async () => { driver._isDBOpen = true }, close: async () => driver.close(), closeConnection: async () => {},
      saveToStore: async () => driver.saveToStore(), loadExtension: async () => {},
      execute: async o => ({ changes: { changes: await driver.executeSQL(o.statements, o.transaction) } }),
      executeSet: async o => ({ changes: await driver.execSet(o.set, o.transaction, o.returnMode) }),
      run: async o => ({ changes: await driver.runSQL(o.statement, o.values, o.transaction, o.returnMode) }),
      query: async o => ({ values: await driver.selectSQL(o.statement, o.values) }),
      beginTransaction: async () => ({ changes: { changes: await driver.beginTransaction() } }),
      commitTransaction: async () => ({ changes: { changes: await driver.commitTransaction() } }),
      rollbackTransaction: async () => ({ changes: { changes: await driver.rollbackTransaction() } }),
      isTransactionActive: async () => ({ result: driver.isTransActive() }),
    }
    globalThis[key] = { ...definitions, CapacitorSQLite: registerPlugin(key, { web: async () => platform }) }
    ;({ localDB: db } = await vite.ssrLoadModule('/src/native/local-db.ts'))
    await db.init('')
    const policy = await vite.ssrLoadModule('/src/stores/syncPolicyStore.ts')
    policy.installSyncPolicyStorage(storage)
    const policyStore = policy.useSyncPolicyStore()
    const { useAuthStore, captureAuthSession, isCurrentAuthSession } = await vite.ssrLoadModule('/src/stores/auth.ts')
    const auth = useAuthStore()
    const scope = () => JSON.stringify([auth.user, auth.workspaceId])
    policy.installSyncPolicyScopeResolver(scope)
    policy.installSyncPolicyActivationHost({
      captureContext() {
        if (auth.activationStatus !== 'ready') return null
        const session = captureAuthSession(auth), version = auth.activationVersion, captured = scope()
        return { scope: captured, intentScope: auth.userId ? JSON.stringify([auth.userId, auth.workspaceId]) : '', workspace: auth.workspaceId,
          isCurrent: () => scope() === captured && auth.activationVersion === version && isCurrentAuthSession(auth, session) }
      },
      activate: (modes, publish) => db.requestPolicySnapshotActivation(modes, publish),
    })
    const setMode = mode => track(policyStore.applyPolicy({ system_default: 'cloud', allow_override: true, modules: { meetings: mode, notes: mode } }, Date.now()))
    await auth.setAuthWithWorkspace('fixture-token', 'Fixture', 'fixture-user', 'fixture-workspace')
    await setMode('cloud')
    // 账号切换助手：走真实的已验证登录（归属由租约给出）。
    // ⚠ 'a' 就是夹具的**初始身份**（fixture-user / fixture-workspace）——
    //   用同一个 userId/workspaceId，才能验证「切走再切回」这条路径。
    const login = async (user) => {
      const id = user === 'a' ? 'fixture-user' : 'fixture-' + user
      const ws = user === 'a' ? 'fixture-workspace' : `ws-${user}`
      await auth.setAuthWithWorkspace(`fixture-${user}`, `User ${user}`, id, ws)
      await setMode('cloud')
    }
    const meetings = await vite.ssrLoadModule('/src/features/meetings/meetings-store.ts')
    const voiceprints = await vite.ssrLoadModule('/src/features/meetings/voiceprints-store.ts')
    await run({ db, raw, vite, auth, login, meetings, voiceprints, track, settled, storage })
    await settled()
  } finally {
    if (UtilsSQLite) UtilsSQLite.run = originalDriverRun
    await settled().catch(() => {})
    if (db) await db.close().catch(() => {})
    try { raw?.close() } catch { /* driver may already have closed it */ }
    await vite.close()
    globalThis.fetch = previousFetch
    delete globalThis[key]
    for (const [key, descriptor] of previousGlobals) { if (descriptor) Object.defineProperty(globalThis, key, descriptor); else delete globalThis[key] }
    if (previousStorage) Object.defineProperty(globalThis, 'localStorage', previousStorage)
    else delete globalThis.localStorage
  }
}

test('a meeting written by one account is invisible to the next account', () => fixture(async f => {
  const meeting = await f.meetings.createMeeting({ title: 'A private meeting' })
  assert.equal((await f.meetings.listMeetings()).length, 1, '本账号应当看得到自己刚建的会议')

  await f.login('b')
  const bMeetings = await f.meetings.listMeetings()
  assert.equal(bMeetings.length, 0, 'B 不得读到 A 的会议')
  assert.equal(await f.meetings.getMeeting(meeting.id), null, 'B 不得按 id 取到 A 的会议')

  await f.login('a')
  const aMeetings = await f.meetings.listMeetings()
  assert.equal(aMeetings.length, 1, 'A 切回来仍应当看得到自己的会议')
  assert.equal(aMeetings[0].title, 'A private meeting')
}))

test('the same meeting id in two accounts does not overwrite the other', () => fixture(async f => {
  await f.db.run(
    `INSERT INTO local_meetings (id, title, status, started_at, created_at, owner_user_id, owner_workspace_id)
     VALUES ('shared-id', 'A version', 'recording', 1, 1, 'user-a', 'ws-a')`,
  )
  await f.login('b')
  await f.db.run(
    `INSERT INTO local_meetings (id, title, status, started_at, created_at, owner_user_id, owner_workspace_id)
     VALUES ('shared-id', 'B version', 'recording', 1, 1, 'user-b', 'ws-b')`,
  )
  const rows = await f.db.query('SELECT title, owner_user_id FROM local_meetings WHERE id = ? ORDER BY owner_user_id', ['shared-id'])
  assert.equal(rows.length, 2, '同 id 不同归属必须各自成行')
  assert.deepEqual(rows.map(r => String(r.title)), ['A version', 'B version'])
}))

test('rows without a verified owner stay unclaimed and invisible to every account', () => fixture(async f => {
  // 模拟迁移前就存在的历史行（归属未知 = 未认领）。
  await f.db.run(
    `INSERT INTO local_meetings (id, title, status, started_at, created_at, owner_user_id, owner_workspace_id)
     VALUES ('legacy', 'Legacy meeting', 'recording', 1, 1, '', '')`,
  )
  await f.login('b')
  assert.equal((await f.meetings.listMeetings()).length, 0, '未认领的行不得出现在任何账号的列表里')
  assert.equal(await f.meetings.getMeeting('legacy'), null, '未认领的行不得被按 id 取到')

  await f.login('a')
  assert.equal((await f.meetings.listMeetings()).length, 0, '未认领的行不得被下一个账号捡走')
  // 但它**还在库里**（数据保住，用户已确认的边界：保持未认领，不删）。
  const rows = await f.db.query('SELECT COUNT(*) AS cnt FROM local_meetings WHERE id = ?', ['legacy'])
  assert.equal(Number(rows[0].cnt), 1, '未认领的历史行必须保留在库里')
}))

test('a voiceprint written by one account is invisible to the next account', () => fixture(async f => {
  await f.voiceprints.saveVoiceprint({ displayName: 'A voice', embedding: new Float32Array([0.1, 0.2, 0.3]) })
  assert.equal((await f.voiceprints.listVoiceprints()).length, 1, '本账号应当看得到自己刚建的声纹')

  await f.login('b')
  assert.equal((await f.voiceprints.listVoiceprints()).length, 0, 'B 不得看到 A 的声纹（生物特征）')

  await f.login('a')
  assert.equal((await f.voiceprints.listVoiceprints()).length, 1, 'A 切回来仍应当看得到自己的声纹')
}))

test('the ownership migration keeps the old rows and leaves them unclaimed', () => fixture(async f => {
  // ⚠ 这一条覆盖的是**迁移**语义：旧行必须**搬运**（会议录音不可从云端恢复），
  //   但归属置空串 ⇒ 数据保住，却**任何账号都读不到**。
  //   旧库形状：只有最早那几列（`location` / `session_id` / `owner_*` 都是
  //   后来的迁移补上去的）⇒ 迁移必须能把**缺列**的旧表也搬过去。
  const meetings = await f.db.query('SELECT id, owner_user_id, owner_workspace_id FROM local_meetings')
  assert.equal(meetings.length, 1, '旧会议行必须被搬运（会议录音不可从云端恢复）')
  assert.equal(String(meetings[0].owner_user_id), '', '搬运后的行归属必须为空串（未认领）')
  const segments = await f.db.query('SELECT id, text, owner_user_id FROM local_meeting_segments')
  assert.equal(segments.length, 1, '旧段落行必须被搬运')
  assert.equal(String(segments[0].text), 'old segment', '搬来的内容不能丢')
}, async raw => {
  // 建「旧形状」的库：只有最早那几列。
  raw.run(`CREATE TABLE local_meetings (
    id TEXT PRIMARY KEY, title TEXT,
    started_at INTEGER NOT NULL, created_at INTEGER NOT NULL, transcript TEXT)`)
  raw.run(`CREATE TABLE local_meeting_segments (
    id TEXT PRIMARY KEY, meeting_id TEXT NOT NULL, text TEXT NOT NULL,
    FOREIGN KEY (meeting_id) REFERENCES local_meetings(id) ON DELETE CASCADE)`)
  raw.run(`INSERT INTO local_meetings (id, title, started_at, created_at) VALUES ('old-m', 'Old meeting', 1, 1)`)
  raw.run(`INSERT INTO local_meeting_segments (id, meeting_id, text) VALUES ('old-s', 'old-m', 'old segment')`)
}))

// ---------------------------------------------------------------------------
// 负控（2026-10-29）：把归属过滤摘掉，上面两条必须**转红**。
//
// ⚠ 为什么要留这一段而不是只在提交说明里写一遍：门自己的输出比没有更坏
//   （docs §388）。若某天有人把 `ownerScopeSql` 换成恒真表达式，这两条
//   用例会**静默变绿** —— 而「跨账号读不到」正是这个切片存在的理由。
//   ⇒ 判据要能证明自己**有牙**。
//
// 复现方式：
//   1. 在 meetings-store.ts 的 listMeetings 里，把
//        meetingListSql(archived, scope.clause) / [...scope.params, limit, offset]
//      改回 meetingListSql(archived) / [limit, offset]（去掉归属过滤）；
//   2. 跑本文件 ⇒ 「a meeting written by one account ...」与
//      「rows without a verified owner ...」两条必须转红；
//   3. 改回来 ⇒ 5 条全绿。
// ---------------------------------------------------------------------------
