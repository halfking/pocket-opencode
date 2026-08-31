import { strict as assert } from 'node:assert'
import { test } from 'node:test'

const originalWindow = globalThis.window

globalThis.window = {
  __OPENCODE_POCKET_HARMONY__: {
    version: 1,
    host: 'arkts-webview',
    capabilities: {},
  },
}

const { clampHarmonyNativeCapabilities } = await import('../runtime-platform.ts')

test('HarmonyOS Phase A clamps all unverified native capabilities', () => {
  const caps = clampHarmonyNativeCapabilities({
    audioRecording: true,
    biometricAuth: true,
    secureStorage: true,
    backgroundTask: true,
    push: true,
    networkReachable: true,
  })

  assert.equal(caps.audioRecording, false)
  assert.equal(caps.biometricAuth, false)
  assert.equal(caps.secureStorage, false)
  assert.equal(caps.backgroundTask, false)
  assert.equal(caps.push, false)
  assert.equal(caps.networkReachable, true)
})

test.after(() => {
  if (originalWindow === undefined) delete globalThis.window
  else globalThis.window = originalWindow
})
