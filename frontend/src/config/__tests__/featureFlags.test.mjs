/**
 * PR1 + PR14 feature flag tests (pure ESM).
 */

import { strict as assert } from 'node:assert'
import { test } from 'node:test'

// Mirror of featureFlags.ts (intentional duplication; see
// test-evidence/PR11-mobile-fault-fixtures.md for rationale).

const flags = {
  'realtime.ws_envelope_v1': { defaultValue: false },
  'realtime.idempotent_ws_bus': { defaultValue: false },
  'approval.bottom_sheet_v1': { defaultValue: false },
  'approval.server_confirm_required': { defaultValue: true },
  'audio.voice_input_v1': { defaultValue: false },
  'security.keystore_v1': { defaultValue: false },
  'notifications.push_v1': { defaultValue: false },
  'background.task_v1': { defaultValue: false },
}

const useFeatureFlag = (key) => {
  const entry = flags[key]
  if (!entry) return false
  return entry.defaultValue
}

test('useFeatureFlag: known flag returns default', () => {
  assert.equal(useFeatureFlag('approval.server_confirm_required'), true)
  assert.equal(useFeatureFlag('realtime.ws_envelope_v1'), false)
})

test('useFeatureFlag: unknown flag returns false (legacy behaviour)', () => {
  assert.equal(useFeatureFlag('not.a.flag'), false)
})

test('flags: every required key from PR1 §7 is present', () => {
  const required = [
    'realtime.ws_envelope_v1',
    'approval.bottom_sheet_v1',
    'approval.server_confirm_required',
    'realtime.idempotent_ws_bus',
  ]
  for (const k of required) {
    assert.ok(flags[k], `flag ${k} must be defined`)
  }
})
