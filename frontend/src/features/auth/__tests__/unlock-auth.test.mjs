import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import {
  unlockButtonEnabled,
  unlockButtonLabel,
  unlockHint,
  unlockPasswordPlaceholder,
  unlockSubmitMode,
} from '../unlock-auth.ts'

describe('master-password unlock with bound biometrics', () => {
  it('lets a bound user submit without typing the master password', () => {
    assert.equal(unlockSubmitMode({ password: '', biometricBound: true }), 'biometric')
    assert.equal(unlockButtonEnabled({ password: '', biometricBound: true, loading: false }), true)
    assert.equal(unlockButtonLabel({ loading: false, biometricBound: true, password: '' }), '认证')
  })

  it('prefers a typed master password over biometric after cancel', () => {
    assert.equal(
      unlockSubmitMode({ password: 'correct-horse', biometricBound: true }),
      'password',
    )
    assert.equal(
      unlockButtonLabel({ loading: false, biometricBound: true, password: 'correct-horse' }),
      '解锁',
    )
  })

  it('still requires a password when biometric is not bound', () => {
    assert.equal(unlockSubmitMode({ password: '', biometricBound: false }), 'need-password')
    assert.equal(unlockButtonEnabled({ password: '', biometricBound: false, loading: false }), false)
    assert.equal(unlockButtonLabel({ loading: false, biometricBound: false, password: '' }), '解锁')
  })
})

describe('unlock copy', () => {
  it('explains cancel-then-password when bound', () => {
    assert.match(unlockHint(true), /取消后输入主密码/)
    assert.equal(unlockPasswordPlaceholder(true), '可留空，点认证使用指纹或人脸')
  })

  it('asks for the master password when unbound', () => {
    assert.match(unlockHint(false), /请重新输入主密码/)
    assert.equal(unlockPasswordPlaceholder(false), '输入主密码解锁')
  })
})
