import assert from 'node:assert/strict'
import { test } from 'node:test'
import { createServer } from 'vite'
import { createPinia, setActivePinia } from 'pinia'

const note = { id: 'note-local', workspace_id: 'w1', title: 'Local match', content: 'Private local text', content_type: 'text', domain: null, category: null, tags: null, audio_path: null, audio_duration_ms: 0, created_by_voice: 0, created_at: 1, updated_at: 1, deleted_at: null }

test('automatic related searches keep private query text local and preserve local matches', async () => {
  const oldStorage = Object.getOwnPropertyDescriptor(globalThis, 'localStorage')
  const oldFetch = globalThis.fetch
  const values = new Map()
  const storage = { keys: () => values.keys(), getItem: key => values.get(key) ?? null, setItem: (key, value) => values.set(key, value), removeItem: key => values.delete(key) }
  Object.defineProperty(globalThis, 'localStorage', { configurable: true, value: storage })
  const requests = []
  globalThis.fetch = async (url, init) => {
    requests.push([String(url), init?.body])
    return new Response(JSON.stringify(String(url).includes('/api/embed') ? { embedding: [1, 0], model: 'test' } : ['', [], [], []]), { headers: { 'Content-Type': 'application/json' } })
  }
  globalThis.__privateSearchNote = note
  setActivePinia(createPinia())
  const vite = await createServer({ configFile: false, server: { middlewareMode: true, hmr: false }, optimizeDeps: { noDiscovery: true }, plugins: [{
    name: 'local-search-storage-boundaries', enforce: 'pre',
    resolveId(source, importer) {
      if (source.endsWith('/native/local-db') || source.endsWith('/native/local-db.ts')) return 'virtual:private-local-db'
      // ⚠ 2026-10-30：f5c8b0cd 起 notes-search 经 portal-sync/storage 拿读取作用域，
      //   该模块 import 真实 local-db（'../local-db' 不落在上面的 /native/local-db 桩里）
      //   ⇒ 一进来就吃「LocalDB 未初始化」。这里整桩掉：作用域恒「未认领」，db 复用虚拟 localDB。
      if (source.endsWith('/portal-sync/storage')) return 'virtual:private-portal-storage'
      if (source.endsWith('/native/asset-store')) return 'virtual:private-assets'
      if (source.endsWith('/native/vector')) return 'virtual:private-vector'
      if (source === './notes-fts-ready') return 'virtual:private-fts-ready'
      if (source === '../notes/notes-store' && importer?.endsWith('/meeting-related-search.ts')) return '/src/features/notes/notes-search.ts'
    },
    load(id) {
      if (id === 'virtual:private-local-db') return 'export const localDB = { isReady: () => true, query: async sql => sql.includes("local_notes") ? [{...globalThis.__privateSearchNote, score:1}] : [] }'
      if (id === 'virtual:private-portal-storage') return 'const localDB = { isReady: () => true, query: async sql => sql.includes("local_notes") ? [{...globalThis.__privateSearchNote, score:1}] : [] }; export async function portalNoteReadScope() { return { db: localDB, clause: "1=1", values: [] } }; export async function canReadPortalNote() { return true }'
      if (id === 'virtual:private-assets') return 'export const assetStore = { search: async () => [] }'
      if (id === 'virtual:private-vector') return 'export const vectorIndex = { searchAsync: async () => [{noteId:"note-local",score:1}] }'
      if (id === 'virtual:private-fts-ready') return 'export const ensureNotesSearchIndex = async () => {}'
    },
  }] })
  try {
    const policy = await vite.ssrLoadModule('/src/stores/syncPolicyStore.ts')
    policy.installSyncPolicyStorage(storage)
    policy.installSyncPolicyActivationHost({ captureContext: () => ({ scope: '', isCurrent: () => true }), async activate(modes, publish) { publish(modes) } })
    const store = policy.useSyncPolicyStore()
    await store.applyPolicy({ system_default: 'cloud', allow_override: true, modules: { notes: 'local', meetings: 'local' } }, 1)
    const notes = await vite.ssrLoadModule('/src/features/notes/notes-search.ts')
    const meetings = await vite.ssrLoadModule('/src/features/meetings/meeting-related-search.ts')
    const direct = await notes.searchSemantic('PRIVATE_NOTE_FULL_TEXT', 5, 'w1')
    assert.equal(direct[0]?.note.id, 'note-local', 'the detail-page entry must retain local recommendations')
    const result = await notes.searchHybrid('PRIVATE_NOTE_FULL_TEXT', 5, 'w1')
    assert.equal(result[0]?.note.id, 'note-local')
    assert.equal(requests.length, 0, 'notes query must not reach embedding service')
    await store.applyPolicy({ system_default: 'cloud', allow_override: true, modules: { notes: 'cloud', meetings: 'local' } }, 2)
    const related = await meetings.searchRelatedContext('PRIVATE_MEETING_TOPIC', { workspaceId: 'w1' })
    assert.ok(related.some(item => item.id === 'note-local'))
    assert.equal(requests.length, 0, 'meeting topic must not reach embedding or Wikipedia')
    await store.applyPolicy({ system_default: 'cloud', allow_override: true, modules: { meetings: 'cloud' } }, 3)
    globalThis.fetch = async (url) => {
      requests.push([String(url)])
      await store.applyPolicy({ system_default: 'cloud', allow_override: true, modules: { meetings: 'local' } }, 4)
      return Response.json(['', [], [], []])
    }
    await meetings.searchRelatedWeb('PRIVATE MEETING')
    assert.equal(requests.length, 1, 'fallback must recheck policy after the first request completes')
    requests.length = 0
    const { useAuthStore } = await vite.ssrLoadModule('/src/stores/auth.ts')
    const auth = useAuthStore()
    await auth.syncFromStorage()
    // ⚠ 2026-10-30：aa8a6dfb 起 http() 对非直接权威先走资源凭据交换（409 之前抛），
    //   本判据守「刷新窗口内的策略翻转」⇒ 设成直接权威让请求走到策略层。
    auth.$patch({ token: '[REDACTED:secret]', user: '[REDACTED:account]', authMethod: 'password', activationStatus: 'ready' })
    let finishRefresh
    auth.maybeRefresh = () => new Promise(resolve => { finishRefresh = resolve })
    await store.applyPolicy({ system_default: 'cloud', allow_override: true, modules: { notes: 'cloud' } }, 3)
    const pending = notes.searchSemantic('PRIVATE_NOTE_DURING_REFRESH', 5, 'w1')
    for (let tries = 0; !finishRefresh && tries < 10; tries++) await Promise.resolve()
    await store.applyPolicy({ system_default: 'cloud', allow_override: true, modules: { notes: 'local' } }, 4)
    finishRefresh()
    await pending
    assert.equal(requests.length, 0, 'switching local during refresh must block the actual send')
  } finally {
    await vite.close()
    globalThis.fetch = oldFetch
    delete globalThis.__privateSearchNote
    if (oldStorage) Object.defineProperty(globalThis, 'localStorage', oldStorage)
    else delete globalThis.localStorage
  }
})

test('email translation preserves policy rejection and does not send private body text', async () => {
  const oldStorage = Object.getOwnPropertyDescriptor(globalThis, 'localStorage')
  const oldFetch = globalThis.fetch
  const values = new Map()
  const storage = { keys: () => values.keys(), getItem: key => values.get(key) ?? null, setItem: (key, value) => values.set(key, value), removeItem: key => values.delete(key) }
  Object.defineProperty(globalThis, 'localStorage', { configurable: true, value: storage })
  setActivePinia(createPinia())
  let requests = 0
  globalThis.fetch = async () => { requests++; return Response.json({ content: 'Translated' }) }
  const vite = await createServer({ configFile: false, server: { middlewareMode: true, hmr: false }, optimizeDeps: { noDiscovery: true } })
  try {
    const policy = await vite.ssrLoadModule('/src/stores/syncPolicyStore.ts')
    policy.installSyncPolicyStorage(storage)
    policy.installSyncPolicyActivationHost({ captureContext: () => ({ scope: '', isCurrent: () => true }), async activate(modes, publish) { publish(modes) } })
    const store = policy.useSyncPolicyStore()
    await store.applyPolicy({ system_default: 'cloud', allow_override: true, modules: { email: 'local' } }, 1)
    const { translateBodyPipeline } = await vite.ssrLoadModule('/src/features/email/translate-email.ts')
    const { ModulePolicyLocalError } = await vite.ssrLoadModule('/src/api/module-policy.ts')
    const localError = error => error.body?.error === 'module_policy_local'
    // ⚠ 2026-10-30：aa8a6dfb 起 http() 对非直接权威（authMethod ∉ password/legacy/dev-bypass）
    //   的请求先走资源凭据交换，/api/llm/chat scope='unavailable' ⇒ 409 之前就抛
    //   PocketResourceUnavailableError（会被翻译降级吞掉 ⇒ 断言收不到 409）。
    //   本判据守的是「模块策略门」，不是鉴权面 ⇒ 先设成直接权威再发起请求。
    const { useAuthStore } = await vite.ssrLoadModule('/src/stores/auth.ts')
    const auth = useAuthStore()
    await auth.syncFromStorage()
    auth.$patch({ token: '[REDACTED:secret]', user: '[REDACTED:account]', authMethod: 'password', activationStatus: 'ready' })
    await assert.rejects(translateBodyPipeline('PRIVATE_EMAIL_BODY', 'zh-CN', async () => { throw new ModulePolicyLocalError('email') }), localError)
    const { emailTranslationChat } = await vite.ssrLoadModule('/src/features/email/email-translation-client.ts')
    await assert.rejects(translateBodyPipeline('PRIVATE_EMAIL_BODY', 'zh-CN', emailTranslationChat), localError)
    assert.equal(requests, 0)
    let finishRefresh
    auth.maybeRefresh = () => new Promise(resolve => { finishRefresh = resolve })
    await store.applyPolicy({ system_default: 'cloud', allow_override: true, modules: { email: 'cloud' } }, 2)
    const pending = emailTranslationChat('PRIVATE_EMAIL_DURING_REFRESH')
    await store.applyPolicy({ system_default: 'cloud', allow_override: true, modules: { email: 'local' } }, 3)
    finishRefresh()
    await assert.rejects(pending, localError)
    assert.equal(requests, 0)
  } finally {
    await vite.close()
    globalThis.fetch = oldFetch
    if (oldStorage) Object.defineProperty(globalThis, 'localStorage', oldStorage)
    else delete globalThis.localStorage
  }
})
