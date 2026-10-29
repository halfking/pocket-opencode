import assert from 'node:assert/strict'
import { fileURLToPath } from 'node:url'
import { test } from 'node:test'
import { createServer } from 'vite'
import initSqlJs from 'sql.js'
import { createPinia, setActivePinia } from 'pinia'

const SQL = await initSqlJs()
const deferred = () => { let resolve; const promise = new Promise(r => { resolve = r }); return { promise, resolve } }
const aborted = error => error?.name === 'AbortError'

async function fixture(run) {
  const previousStorage = Object.getOwnPropertyDescriptor(globalThis, 'localStorage')
  const previousFetch = globalThis.fetch
  const previousReader = globalThis.FileReader
  const previousGlobals = new Map(['window','document','navigator','MediaRecorder','__openpocket_meetingRecorderRuntime__','__openpocket_noteRecorderRuntime__','__openpocket_recordingVoicePrompt__'].map(key => [key, Object.getOwnPropertyDescriptor(globalThis,key)]))
  const mediaStream = { getTracks: () => [{ stop() {}, enabled: true }], getAudioTracks: () => [{ stop() {}, enabled: true }] }
  const audioSaved = new Map()
  class FakeMediaRecorder { static isTypeSupported() { return true }; constructor() { this.state='inactive'; this.mimeType='audio/wav' }; start() { this.state='recording' }; stop() { this.state='inactive'; queueMicrotask(()=>this.onstop?.()) }; emit(blob) { this.ondataavailable?.({data:blob}) } }
  for (const [key,value] of Object.entries({window:Object.assign(new EventTarget(),{location:{origin:'https://fixture.invalid',hash:''}}), document:Object.assign(new EventTarget(),{visibilityState:'visible'}),navigator:{mediaDevices:Object.assign(new EventTarget(),{getUserMedia:async()=>mediaStream})},MediaRecorder:FakeMediaRecorder})) Object.defineProperty(globalThis,key,{configurable:true,value})
  delete globalThis.__openpocket_meetingRecorderRuntime__; delete globalThis.__openpocket_noteRecorderRuntime__; delete globalThis.__openpocket_recordingVoicePrompt__
  let runtimes
  let afterRead = async () => {}
  globalThis.FileReader = class { readAsDataURL(blob) { void blob.arrayBuffer().then(async bytes => { await afterRead(); this.result = 'data:audio/wav;base64,' + Buffer.from(bytes).toString('base64'); this.onloadend?.() }) } }
  const values = new Map(), stored = new Map(), pending = []
  const storage = { keys: () => values.keys(), getItem: key => values.get(key) ?? null, setItem: (key, value) => values.set(key, String(value)), removeItem: key => values.delete(key) }
  Object.defineProperty(globalThis, 'localStorage', { configurable: true, value: storage })
  setActivePinia(createPinia())
  let afterQuery = () => {}, afterRun = () => {}, db, raw, driver, UtilsSQLite, originalDriverRun
  const track = promise => { pending.push(promise); promise.catch(() => {}); return promise }
  const settled = async () => {
    while (pending.length) {
      const results = await Promise.allSettled(pending.splice(0))
      for (const result of results) if (result.status === 'rejected' && result.reason?.name !== 'AbortError') throw result.reason
    }
  }
  const key = `__meetingSQLite_${Math.random().toString(36).slice(2)}`
  const requests = []
  let respond = () => ({ refined_transcript: 'Remote refined content', todos: [], structured_minutes: {} })
  globalThis.fetch = async url => {
    requests.push(String(url))
    const response = await respond(String(url))
    return response instanceof Response ? response : Response.json(response)
  }
  const vite = await createServer({ root: fileURLToPath(new URL('../../../../', import.meta.url)), configFile: false,
    server: { middlewareMode: true, hmr: false }, ssr: { noExternal: ['@capacitor-community/sqlite'] }, optimizeDeps: { noDiscovery: true }, plugins: [{
      name: 'meeting-real-sqlite-platform', enforce: 'pre',
      resolveId(source, importer) {
        if (source.endsWith('/composables/useToast')) return '\0meeting-toast'
        if (source.endsWith('/composables/useMicPermission')) return '\0meeting-mic'
        if (importer?.endsWith('/recordingRuntime.ts') && source === './vad-segmenter') return '\0meeting-vad'
        if (importer?.endsWith('/recordingRuntime.ts') && source === './audio-inputs') return '\0meeting-inputs'
        if (source === '@capacitor-community/sqlite' && importer?.endsWith('/local-db.ts')) return '\0email-sqlite'
        if (importer?.endsWith('/local-db.ts') && source === './sqlite-web-init') return '\0email-web'
        if (importer?.endsWith('/local-db.ts') && source === './runtime-platform') return '\0email-platform'
        if (source.endsWith('/native/lobster-init')) return '\0email-ready'
      },
      load(id) {
        if (id === '\0meeting-toast') return 'export const useToast=()=>({error(){},warning(){},success(){},info(){}})'
        if (id === '\0meeting-mic') return 'export const useMicPermission=()=>({ensure:async()=>true,deniedLabel:{value:""}})'
        if (id === '\0meeting-vad') return 'export class VadSegmenter { constructor(options){this.options=options};async start(){};async stop(){return new Blob(["PRESERVED_AUDIO"],{type:"audio/wav"})} }'
        if (id === '\0meeting-inputs') return 'export const listAudioInputs=async()=>[];export const openPreferredMicStream=async()=>({stream:await navigator.mediaDevices.getUserMedia(),inputs:[]})'
        if (id === '\0email-sqlite') return `export const { SQLiteConnection, SQLiteDBConnection, CapacitorSQLite } = globalThis[${JSON.stringify(key)}]`
        if (id === '\0email-web') return 'export async function initSqliteWeb() {}'
        if (id === '\0email-platform') return 'export const isWebFallbackRuntime = () => true'
        if (id === '\0email-ready') return "import {ref} from 'vue'; export const lobsterReady = ref(true)"
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
    const platform = {
      initWebStore: async () => {}, checkConnectionsConsistency: async () => ({ result: true }), createConnection: async () => {},
      open: async () => { driver._isDBOpen = true }, close: async () => driver.close(), closeConnection: async () => {},
      saveToStore: async () => driver.saveToStore(), loadExtension: async () => {},
      execute: async o => ({ changes: { changes: await driver.executeSQL(o.statements, o.transaction) } }),
      executeSet: async o => ({ changes: await driver.execSet(o.set, o.transaction, o.returnMode) }),
      run: async o => { const changes = await driver.runSQL(o.statement, o.values, o.transaction, o.returnMode); await afterRun(o.statement); return { changes } },
      query: async o => { const rows = await driver.selectSQL(o.statement, o.values); await afterQuery(o.statement); return { values: rows } },
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
    await auth.setAuthWithWorkspace('fixture-token', 'Fixture', 'fixture-user', 'fixture-workspace')
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
    await setMode('cloud')
    const meetings = await vite.ssrLoadModule('/src/features/meetings/meetings-store.ts')
    const meeting = await meetings.createMeeting({ title: 'Original meeting' })
    const login = async name => { await auth.setAuthWithWorkspace(`token-${name}`, `User ${name}`, `user-${name}`, `ws-${name}`); await setMode('cloud') }
    const loadRuntime = async () => (runtimes ??= await vite.ssrLoadModule('/src/native/recordingRuntime.ts'))
    await run({ loadRuntime, audioSaved, meetings, meeting, setMode, requests, db, sqlite: raw, driver, UtilsSQLite, auth, track, settled, login,
      setResponse: fn => { respond = fn }, onRead: fn => { afterRead = fn }, onQuery: fn => { afterQuery = fn }, onRun: fn => { afterRun = fn }, vite })
    await settled()
  } finally {
    afterQuery = afterRun = () => {}
    if (UtilsSQLite) UtilsSQLite.run = originalDriverRun
    await settled().catch(() => {})
    if (db) await db.close().catch(() => {})
    try { raw?.close() } catch { /* driver may already have closed it */ }
    await vite.close()
    if (runtimes) for (const runtime of [runtimes.meetingRecorderRuntime,runtimes.noteRecorderRuntime]) { if (runtime.elapsedTimer) clearInterval(runtime.elapsedTimer); if(runtime.tick) clearInterval(runtime.tick); runtime.operationStop?.(); await runtime.cleanupMedia?.() }
    for (const [key,descriptor] of previousGlobals) { if(descriptor) Object.defineProperty(globalThis,key,descriptor); else delete globalThis[key] }
    globalThis.fetch = previousFetch
    globalThis.FileReader = previousReader
    delete globalThis[key]
    if (previousStorage) Object.defineProperty(globalThis, 'localStorage', previousStorage)
    else delete globalThis.localStorage
  }
}

for (const change of ['owner', 'ABA']) {
  test(`audio encoding retains its initiating owner (${change})`, () => fixture(async ({ vite, meeting, onRead, login, db }) => {
    const { saveAudioPart } = await vite.ssrLoadModule('/src/features/meetings/audio-parts.ts')
    const entered = deferred(), release = deferred()
    onRead(async () => { entered.resolve(); await release.promise })
    const pending = assert.rejects(saveAudioPart(meeting.id, 1, new Blob(['ORIGINAL_AUDIO'], { type: 'audio/wav' })), aborted)
    await entered.promise
    await login('b')
    if (change === 'ABA') await login('a')
    release.resolve()
    await pending
    assert.equal((await db.query('SELECT id FROM local_meeting_audio_parts')).length, 0)
  }))
}

test('finalization cannot use a new account after waiting for original audio', () => fixture(async ({ vite, meeting, login, requests }) => {
  const { finalizeRecording } = await vite.ssrLoadModule('/src/features/meetings/meeting-recording-finalize.ts')
  const entered = deferred(), release = deferred()
  const pending = assert.rejects(finalizeRecording(meeting.id, [], 1000, {
    notify: () => {},
    loadAudioChunks: () => (async function* () { entered.resolve(); await release.promise; yield { blob: new Blob(['AUDIO']), offsetSec: 0, durationSec: 1 } })(),
    transcribeFull: async () => ({ text: 'Original transcript', failed: 0 }),
  }), aborted)
  await entered.promise
  await login('b')
  release.resolve()
  await pending
  assert.equal(requests.length, 0)
}))

test('artifact ingestion cannot create an event with a later account', () => fixture(async ({ vite, meeting, setResponse, login, requests }) => {
  const { ingestMeetingArtifacts } = await vite.ssrLoadModule('/src/features/meetings/meeting-ingest.ts')
  const entered = deferred(), release = deferred()
  setResponse(async url => { if (url.includes('/calendar/events?')) { entered.resolve(); await release.promise; return { entries: [] } }; return {} })
  const pending = assert.rejects(ingestMeetingArtifacts(meeting, {
    refinedTranscript: '', noteId: 'existing-note', todos: [], translations: {},
    structuredMinutes: { agenda: [], decisions: [], actionItems: [], nextMeeting: '下周三下午三点' },
  }), aborted)
  await entered.promise
  await login('b')
  release.resolve()
  await pending
  assert.equal(requests.length, 1)
}))

for (const change of ['owner', 'policy ABA']) {
  test(`note recording does not upload an old decoded slice (${change})`, () => fixture(async ({loadRuntime,login,setMode,requests}) => {
    const {noteRecorderRuntime:runtime} = await loadRuntime()
    assert.equal(await runtime.start(),true)
    const entered=deferred(),release=deferred()
    runtime.transcodeSlice=async()=>{entered.resolve();await release.promise;return new Uint8Array(32000).buffer}
    const sending=runtime.sendSlice(new Blob(['AUDIO']),false)
    await entered.promise
    if(change==='owner') await login('b')
    else { await setMode('local');await setMode('cloud') }
    release.resolve();await sending
    assert.equal(requests.length,0)
  }))
}
