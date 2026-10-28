import assert from 'node:assert/strict'
import { test } from 'node:test'
import { createServer } from 'vite'
import { createPinia, setActivePinia } from 'pinia'
import { effectScope } from 'vue'

async function fixture(run, platform = 'android') {
  const originals = Object.fromEntries(['window', 'document', 'localStorage'].map(key => [key, Object.getOwnPropertyDescriptor(globalThis, key)]))
  const values = new Map()
  const storage = { keys: () => values.keys(), getItem: key => values.get(key) ?? null, setItem: (key, value) => values.set(key, value), removeItem: key => values.delete(key) }
  Object.defineProperty(globalThis, 'localStorage', { configurable: true, value: storage })
  const window = new EventTarget()
  window.localStorage = storage
  window.location = { origin: 'https://native-email-audit.test' }
  Object.defineProperty(globalThis, 'window', { configurable: true, value: window })
  const document = new EventTarget()
  document.visibilityState = 'visible'
  Object.defineProperty(globalThis, 'document', { configurable: true, value: document })
  const calls = []
  const finishConfigure = []
  const native = {
    configure: async options => { calls.push(['configure', options]); if (globalThis.__delayEmailConfigure) await new Promise(resolve => { finishConfigure.push(resolve) }) },
    schedule: async () => { calls.push(['schedule']) },
    cancel: async () => { calls.push(['cancel']) },
    runNow: async () => { calls.push(['run']); return { synced: 1 } },
  }
  globalThis.__emailPolicyFixture = { native, calls, platform }
  setActivePinia(createPinia())
  const vite = await createServer({ configFile: false, server: { middlewareMode: true, hmr: false }, ssr: { noExternal: ['@capacitor/core', '@capacitor/app'] }, optimizeDeps: { noDiscovery: true }, plugins: [{
    name: 'native-email-boundaries', enforce: 'pre',
    resolveId(source, importer) {
      if (source === '@capacitor/core') return 'virtual:email-capacitor'
      if (source === '@capacitor/app') return 'virtual:email-app'
      if (source === '../../api/email' && importer?.endsWith('/email-fetch-run.ts')) return 'virtual:email-api'
      if (source === '../../i18n' && importer?.endsWith('/email-fetch-run.ts')) return 'virtual:email-i18n'
      if (source === './email-inbox-page' && importer?.endsWith('/email-fetch-run.ts')) return 'virtual:email-inbox'
    },
    load(id) {
      if (id === 'virtual:email-capacitor') return 'export const Capacitor={isNativePlatform:()=>true,getPlatform:()=>globalThis.__emailPolicyFixture.platform}; export const registerPlugin=()=>globalThis.__emailPolicyFixture.native'
      if (id === 'virtual:email-app') return 'export const App={addListener:async()=>({remove(){}})}'
      if (id === 'virtual:email-api') return 'export const emailApi={classifyInbox:async()=>{globalThis.__emailPolicyFixture.calls.push(["classify"]);return {classified:1}}}'
      if (id === 'virtual:email-i18n') return 'export default {global:{t:key=>key}}'
      if (id === 'virtual:email-inbox') return 'export const pullInboxFromServer=async()=>{globalThis.__emailPolicyFixture.calls.push(["pull"])};export const syncInboxFromServer=async()=>{globalThis.__emailPolicyFixture.calls.push(["sync"]);return ""}'
    },
  }] })
  const settle = async () => { for (let i = 0; i < 30; i++) await new Promise(resolve => setImmediate(resolve)) }
  try {
    const policy = await vite.ssrLoadModule('/src/stores/syncPolicyStore.ts')
    policy.installSyncPolicyStorage(storage)
    policy.installSyncPolicyActivationHost({
      captureContext() {
        const scope = storage.getItem('pocket_user') || storage.getItem('pocket_workspace_id')
          ? JSON.stringify([storage.getItem('pocket_user') ?? '', storage.getItem('pocket_workspace_id') ?? '']) : ''
        return { scope, isCurrent: () => true }
      },
      async activate(modes, publish) { publish(modes) },
    })
    const store = policy.useSyncPolicyStore()
    const { useAuthStore } = await vite.ssrLoadModule('/src/stores/auth.ts')
    const auth = useAuthStore()
    await auth.setAuthWithWorkspace('account-a-token', 'Account A', 'account-a', 'workspace-a')
    const mode = value => store.applyPolicy({ system_default: 'cloud', allow_override: true, modules: { email: value } }, Date.now())
    await run({ vite, native, calls, store, auth, mode, settle, release: () => { finishConfigure.splice(0).forEach(resolve => resolve()) } })
  } finally {
    finishConfigure.splice(0).forEach(resolve => resolve())
    await settle()
    await vite.close()
    delete globalThis.__emailPolicyFixture
    delete globalThis.__delayEmailConfigure
    for (const key of Object.keys(originals)) {
      if (originals[key]) Object.defineProperty(globalThis, key, originals[key])
      else delete globalThis[key]
    }
  }
}

test('local manual refresh neither configures nor invokes native or server sync', () => fixture(async ({ vite, calls, mode }) => {
  await mode('local')
  const { runDelegatedEmailFetch } = await vite.ssrLoadModule('/src/features/email/email-fetch-run.ts')
  await runDelegatedEmailFetch()
  assert.equal(calls.filter(([kind]) => ['configure', 'run', 'pull', 'sync', 'classify'].includes(kind)).length, 0)
}))

test('host startup cancels old native credentials instead of scheduling in local mode', () => fixture(async ({ vite, calls, mode, settle }) => {
  await mode('local')
  const { startEmailFetchHost } = await vite.ssrLoadModule('/src/features/email/email-fetch-host.ts')
  startEmailFetchHost()
  await settle()
  assert.ok(calls.some(([kind]) => kind === 'cancel'))
  assert.equal(calls.filter(([kind]) => ['configure', 'schedule', 'run', 'pull'].includes(kind)).length, 0)
}))

test('switching local during native configure prevents late run and schedule', () => fixture(async ({ vite, calls, mode, settle, release }) => {
  await mode('cloud')
  globalThis.__delayEmailConfigure = true
  const { startEmailFetchHost } = await vite.ssrLoadModule('/src/features/email/email-fetch-host.ts')
  startEmailFetchHost()
  await settle()
  assert.ok(calls.some(([kind]) => kind === 'configure'), JSON.stringify(calls))
  await mode('local')
  release()
  await settle()
  assert.ok(calls.some(([kind]) => kind === 'cancel'))
  assert.equal(calls.filter(([kind]) => ['schedule', 'run'].includes(kind)).length, 0)
}))

test('cloud host runs, cancels on logout, and needs the new account policy before resuming', () => fixture(async ({ vite, calls, mode, auth, settle }) => {
  await mode('cloud')
  const { startEmailFetchHost } = await vite.ssrLoadModule('/src/features/email/email-fetch-host.ts')
  startEmailFetchHost()
  await settle()
  assert.equal(calls.filter(([kind]) => kind === 'run').length, 1)
  assert.equal(calls.filter(([kind]) => kind === 'schedule').length, 1)
  calls.length = 0
  await auth.clearLocal()
  await settle()
  assert.ok(calls.some(([kind]) => kind === 'cancel'))
  await auth.setAuthWithWorkspace('account-b-token', 'Account B', 'account-b', 'workspace-b')
  await settle()
  assert.equal(calls.filter(([kind]) => kind === 'run').length, 0)
  await mode('cloud')
  await settle()
  assert.equal(calls.filter(([kind]) => kind === 'run').length, 1)
  const configs = calls.filter(([kind]) => kind === 'configure')
  assert.ok(configs.length > 0)
  assert.ok(configs.every(([, options]) => options.token === 'account-b-token' && options.enabled === true))
}))

test('host still cancels native credentials after its starting component unmounts', () => fixture(async ({ vite, calls, mode, auth, settle }) => {
  await mode('cloud')
  const { startEmailFetchHost } = await vite.ssrLoadModule('/src/features/email/email-fetch-host.ts')
  const component = effectScope()
  component.run(() => startEmailFetchHost())
  await settle()
  component.stop()
  calls.length = 0
  await auth.clearLocal()
  await settle()
  assert.ok(calls.some(([kind]) => kind === 'cancel'))
  assert.equal(calls.filter(([kind]) => ['run', 'schedule'].includes(kind)).length, 0)
}))

test('unconfirmed native stop stays visible, retries within a bound, and blocks cloud reauthorization', t => fixture(async ({ vite, native, calls, mode, settle }) => {
  await mode('cloud')
  const bridge = await vite.ssrLoadModule('/src/features/email/email-fetch-native.ts')
  assert.equal(await bridge.configureNativeEmailFetch('https://api.test', 'account-a-token'), true)
  native.cancel = async () => { calls.push(['cancel']); throw new Error('disk unavailable') }
  t.mock.timers.enable({ apis: ['setTimeout'] })
  try {
    await mode('local')
    await settle()
    assert.equal(bridge.nativeEmailStopState.unconfirmed, true)
    assert.equal(bridge.nativeEmailStopState.retrying, false)
    const configurations = calls.filter(([kind]) => kind === 'configure').length
    await mode('cloud')
    assert.equal(await bridge.configureNativeEmailFetch('https://api.test', 'account-a-token'), false)
    assert.equal(await bridge.scheduleNativeEmailFetch(1000), false)
    assert.equal((await bridge.runNativeEmailFetch()).used, false)
    t.mock.timers.tick(1000)
    await settle()
    t.mock.timers.tick(5000)
    await settle()
    const attempts = calls.filter(([kind]) => kind === 'cancel').length
    assert.equal(attempts, 4, JSON.stringify(calls)) // Initial confirmed stop + three failed attempts.
    t.mock.timers.tick(60000)
    await settle()
    assert.equal(calls.filter(([kind]) => kind === 'cancel').length, attempts)
    assert.equal(calls.filter(([kind]) => kind === 'configure').length, configurations)
    assert.equal(bridge.nativeEmailStopState.unconfirmed, true)
    native.cancel = async () => { calls.push(['cancel']) }
    assert.equal(await bridge.retryNativeEmailFetchCancellation(), true)
    assert.equal(bridge.nativeEmailStopState.unconfirmed, false)
    assert.equal(await bridge.configureNativeEmailFetch('https://api.test', 'account-a-token'), true)
  } finally {
    t.mock.timers.reset()
  }
}))

test('a successful automatic stop retry clears the warning without granting a native account', t => fixture(async ({ vite, native, calls, mode, settle }) => {
  await mode('cloud')
  const bridge = await vite.ssrLoadModule('/src/features/email/email-fetch-native.ts')
  assert.equal(await bridge.configureNativeEmailFetch('https://api.test', 'account-a-token'), true)
  let failures = 1
  native.cancel = async () => { calls.push(['cancel']); if (failures-- > 0) throw new Error('bridge temporarily disconnected') }
  t.mock.timers.enable({ apis: ['setTimeout'] })
  try {
    await mode('local')
    await settle()
    assert.equal(bridge.nativeEmailStopState.unconfirmed, true)
    t.mock.timers.tick(1000)
    await settle()
    assert.equal(bridge.nativeEmailStopState.unconfirmed, false)
    assert.equal((await bridge.runNativeEmailFetch()).used, false)
    assert.equal(calls.filter(([kind]) => kind === 'configure').length, 1)
  } finally {
    t.mock.timers.reset()
  }
}))

test('iOS has no Android email grant to stop and never reports an unsupported-plugin failure', () => fixture(async ({ vite, native, calls, mode, settle }) => {
  native.cancel = async () => { throw new Error('EmailFetch.cancel is not implemented on ios') }
  await mode('cloud')
  const bridge = await vite.ssrLoadModule('/src/features/email/email-fetch-native.ts')
  assert.equal(await bridge.configureNativeEmailFetch('https://api.test', 'account-a-token'), false)
  await mode('local')
  await settle()
  assert.equal(await bridge.cancelNativeEmailFetch(), true)
  assert.equal(bridge.nativeEmailStopState.unconfirmed, false)
  assert.equal(bridge.nativeEmailStopState.retrying, false)
  assert.equal(calls.length, 0)
}, 'ios'))

test('Android without the cancel method remains unconfirmed until an actual stop succeeds', () => fixture(async ({ vite, native, mode }) => {
  await mode('cloud')
  const bridge = await vite.ssrLoadModule('/src/features/email/email-fetch-native.ts')
  assert.equal(await bridge.configureNativeEmailFetch('https://api.test', 'account-a-token'), true)
  native.cancel = undefined
  assert.equal(await bridge.cancelNativeEmailFetch(), false)
  assert.equal(bridge.nativeEmailStopState.unconfirmed, true)
  assert.equal(await bridge.configureNativeEmailFetch('https://api.test', 'account-a-token'), false)
  native.cancel = async () => {}
  assert.equal(await bridge.retryNativeEmailFetchCancellation(), true)
  assert.equal(bridge.nativeEmailStopState.unconfirmed, false)
}))
