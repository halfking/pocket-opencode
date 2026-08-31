import { strict as assert } from 'node:assert'
import { test } from 'node:test'

const originalWindow = globalThis.window

function installWindow(marker) {
  globalThis.window = marker === undefined ? {} : { __OPENCODE_POCKET_HARMONY__: marker }
}

function restoreWindow() {
  if (originalWindow === undefined) delete globalThis.window
  else globalThis.window = originalWindow
}

const runtime = await import('../runtime-platform.ts')

test('runtime platform stays web without the private Harmony bridge', () => {
  installWindow()
  assert.equal(runtime.runtimePlatform(), 'web')
  assert.equal(runtime.isHarmonyWebView(), false)
  assert.equal(runtime.isWebFallbackRuntime(), true)
  restoreWindow()
})

test('Harmony bridge requires a versioned host contract', () => {
  installWindow({ version: 1, capabilities: {} })
  assert.equal(runtime.runtimePlatform(), 'web')

  installWindow({ version: 2, host: 'arkts-webview', capabilities: {} })
  assert.equal(runtime.runtimePlatform(), 'web')
  restoreWindow()
})

test('Harmony build metadata alone never changes the Web fallback runtime', () => {
  installWindow()
  assert.equal(runtime.runtimePlatform(), 'web')
  assert.equal(runtime.isWebFallbackRuntime(), true)
  restoreWindow()
})

test('Harmony bridge is recognized but advertises no capability without invoke', () => {
  installWindow({ version: 1, host: 'arkts-webview', capabilities: { camera: true } })
  assert.equal(runtime.runtimePlatform(), 'harmony')
  assert.equal(runtime.isHarmonyWebView(), true)
  assert.equal(runtime.hasHarmonyCapability('camera'), false)
  restoreWindow()
})

test('Harmony capabilities are opt-in and bridge failures close safely', async () => {
  installWindow({
    version: 1,
    host: 'arkts-webview',
    capabilities: { camera: true, biometric: 'true' },
    invoke: async () => { throw new Error('bridge unavailable') },
  })

  assert.equal(runtime.hasHarmonyCapability('camera'), true)
  assert.equal(runtime.hasHarmonyCapability('biometric'), false)
  assert.equal(await runtime.invokeHarmony('camera', 'takePhoto'), null)
  restoreWindow()
})
