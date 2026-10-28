import assert from 'node:assert/strict'
import { test } from 'node:test'
import { createServer } from 'vite'
import { createPinia, setActivePinia } from 'pinia'

test('仅本地的真实会议与 STT 客户端不触发网络降级，先可靠保存音频分片', async () => {
  const originalStorage = Object.getOwnPropertyDescriptor(globalThis, 'localStorage')
  const originalFetch = globalThis.fetch
  const originalWindow = Object.getOwnPropertyDescriptor(globalThis, 'window')
  const values = new Map()
  const storage = { keys: () => values.keys(), getItem: (key) => values.get(key) ?? null, setItem: (key, value) => values.set(key, value), removeItem: (key) => values.delete(key) }
  Object.defineProperty(globalThis, 'localStorage', { configurable: true, value: storage })
  let requests = 0
  globalThis.fetch = async () => { requests++; return new Response('{}', { status: 200, headers: { 'Content-Type': 'application/json' } }) }
  setActivePinia(createPinia())
  const vite = await createServer({ configFile: false, server: { middlewareMode: true }, optimizeDeps: { noDiscovery: true }, plugins: [{
    name: 'audio-persistence-boundary',
    enforce: 'pre',
    resolveId(source, importer) {
      if (source === './audio-parts' && importer?.endsWith('/ingest-speech.ts')) return 'virtual:privacy-audio-parts'
      if (source.endsWith('/composables/useToast')) return 'virtual:privacy-toast'
    },
    load(id) {
      if (id === 'virtual:privacy-audio-parts') return 'export const saveAudioPart = (...args) => globalThis.__privacySaveAudioPart(...args)'
      if (id === 'virtual:privacy-toast') return 'export const useToast = () => ({ warning() {}, error() {}, success() {}, info() {} })'
    },
  }] })
  try {
    const policy = await vite.ssrLoadModule('/src/stores/syncPolicyStore.ts')
    policy.installSyncPolicyStorage(storage)
    policy.installSyncPolicyActivationHost({ captureContext: () => ({ scope: '', isCurrent: () => true }), async activate(modes, publish) { publish(modes) } })
    storage.setItem(policy.SYNC_POLICY_CACHE_KEY, JSON.stringify({ policy: { system_default: 'local', allow_override: true, modules: { notes: 'local', meetings: 'local' } } }))
    const { meetingsApi } = await vite.ssrLoadModule('/src/api/meetings.ts')
    const { sttApi } = await vite.ssrLoadModule('/src/api/stt.ts')
    const { sttSettingsApi } = await vite.ssrLoadModule('/src/api/stt-settings.ts')
    const { liveTranslate } = await vite.ssrLoadModule('/src/features/meetings/live-translate.ts')
    const localError = (error) => error.status === 409 && error.body?.error === 'module_policy_local'
    await assert.rejects(meetingsApi.summarize('m1', []), localError)
    await assert.rejects(meetingsApi.refine('m1', []), localError)
    await assert.rejects(liveTranslate('private transcript'), localError)
    await assert.rejects(sttApi.transcribe({ audioBlob: new Blob(['private']), module: 'notes' }), localError)
    await assert.rejects(sttSettingsApi.transcribeFull(new Blob(['private']), 'meeting.wav', undefined, 'meetings'), localError)
    await assert.rejects(sttSettingsApi.transcribeIncremental({ sessionId: 'n1', audioBase64: 'private' }, 'notes'), localError)
    assert.equal(requests, 0)

    // 请求前的 token 续期存在 await：期间切 local，真正发送前仍必须拦截。
    const currentPolicy = policy.useSyncPolicyStore()
    const { useAuthStore } = await vite.ssrLoadModule('/src/stores/auth.ts')
    const auth = useAuthStore()
    await auth.syncFromStorage()
    const originalRefresh = auth.maybeRefresh
    for (const request of [
      () => sttSettingsApi.transcribeIncremental({ sessionId: 'n1', audioBase64: 'private' }, 'notes'),
      () => liveTranslate('private transcript'),
    ]) {
      await currentPolicy.applyPolicy({ system_default: 'cloud', allow_override: true, modules: {} }, 1)
      let finishRefresh
      auth.maybeRefresh = () => new Promise((resolve) => { finishRefresh = resolve })
      const pending = request()
      await currentPolicy.applyPolicy({ system_default: 'local', allow_override: true, modules: {} }, 2)
      finishRefresh()
      await assert.rejects(pending, localError)
      assert.equal(requests, 0)
    }
    auth.maybeRefresh = originalRefresh

    const { ingestSpeechBlob } = await vite.ssrLoadModule('/src/features/meetings/ingest-speech.ts')
    let saved = 0
    let finishSave
    globalThis.__privacySaveAudioPart = () => new Promise((resolve) => { finishSave = () => { saved++; resolve() } })
    const ingest = ingestSpeechBlob({ meetingId: 'm1', blob: new Blob(['private']), seq: 1, startMs: 0, endMs: 1, segments: [], segmentProfiles: new Map(), diarizer: {} })
    let settled = false
    const rejected = assert.rejects(ingest, localError).then(() => { settled = true })
    await Promise.resolve()
    assert.equal(settled, false)
    finishSave()
    await rejected
    assert.equal(saved, 1)
    assert.equal(requests, 0)
    globalThis.__privacySaveAudioPart = async () => { throw new Error('disk full') }
    await assert.rejects(ingestSpeechBlob({ meetingId: 'm1', blob: new Blob(['private']), seq: 2, startMs: 1, endMs: 2, segments: [], segmentProfiles: new Map(), diarizer: {} }), /disk full/)

    const { finalizeRecording } = await vite.ssrLoadModule('/src/features/meetings/meeting-recording-finalize.ts')
    const notices = []
    await finalizeRecording('m1', [], 1000, { notify: (...notice) => notices.push(notice), transcribeFull: async () => { throw new Error('must not transcribe') } })
    assert.ok(notices.some((notice) => notice[1].includes('仅本地')))
    assert.equal(requests, 0)

    // 未指定 notes/meetings 的通用 STT 操作保持可用。
    await sttSettingsApi.transcribeIncremental({ sessionId: 'general', audioBase64: 'fixture' })
    assert.equal(requests, 1)

    // 系统 SpeechRecognition 可能联网：既拒绝 local 启动，也停止已启动的识别。
    let starts = 0
    let stops = 0
    const events = new EventTarget()
    events.SpeechRecognition = class { start() { starts++ } stop() { stops++ } }
    Object.defineProperty(globalThis, 'window', { configurable: true, value: events })
    const { MeetingRecorderRuntime } = await vite.ssrLoadModule('/src/native/recordingRuntime.ts')
    const recorder = new MeetingRecorderRuntime()
    await currentPolicy.applyPolicy({ system_default: 'cloud', allow_override: true, modules: { meetings: 'cloud' } }, 1)
    recorder.startLiveCaption()
    assert.equal(starts, 1)
    await currentPolicy.applyPolicy({ system_default: 'local', allow_override: true, modules: { meetings: 'local' } }, 2)
    assert.equal(stops, 1)
    recorder.startLiveCaption()
    assert.equal(starts, 1)
  } finally {
    await vite.close()
    globalThis.fetch = originalFetch
    delete globalThis.__privacySaveAudioPart
    if (originalStorage) Object.defineProperty(globalThis, 'localStorage', originalStorage)
    else delete globalThis.localStorage
    if (originalWindow) Object.defineProperty(globalThis, 'window', originalWindow)
    else delete globalThis.window
  }
})
