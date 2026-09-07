import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import {
  isBiometricUserCancel,
  isMissingMasterSecret,
  loginDisplayName,
  needsBiometricEnrollment,
} from '../biometric-errors.ts'

describe('biometric bind errors', () => {
  it('treats negative-button and user-cancel as silent cancel', () => {
    assert.equal(isBiometricUserCancel('biometric error 13: Cancel'), true)
    assert.equal(isBiometricUserCancel('biometric error 10: '), true)
    assert.equal(isBiometricUserCancel('biometric error 11: No biometrics'), false)
  })

  it('unbound click should bind; none enrolled goes to settings', () => {
    assert.equal(needsBiometricEnrollment('biometric error 11: No biometrics enrolled'), true)
    assert.equal(needsBiometricEnrollment('biometric error 12: '), true)
    assert.equal(needsBiometricEnrollment('crypto failed: x'), false)
  })

  it('unlock cancel stays silent; missing master secret is a fallback hint', () => {
    assert.equal(isBiometricUserCancel('biometric error 5: '), true)
    assert.equal(isMissingMasterSecret('no master secret bound'), true)
    assert.equal(isMissingMasterSecret('biometric error 13: Cancel'), false)
  })

  it('reads username from raw string or pocket_user JSON', () => {
    assert.equal(loginDisplayName('alice'), 'alice')
    assert.equal(loginDisplayName('{"username":"bob"}'), 'bob')
  })
})
