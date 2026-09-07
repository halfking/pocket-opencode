import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { nextPermissionAction } from './permission-action.ts'
import {
  androidSettingsPlan,
  permissionRowClickAction,
  permissionStatusLabel,
} from './permission-settings.ts'

describe('permissionRowClickAction', () => {
  it('blocked microphone opens the app-scoped system settings page', () => {
    assert.equal(
      permissionRowClickAction({ kind: 'microphone', status: 'denied', canRequestAgain: false }),
      'open-settings',
    )
    assert.equal(nextPermissionAction('denied'), 'open-settings')
  })

  it('first deny still requests in-app instead of jumping to settings', () => {
    assert.equal(
      permissionRowClickAction({
        kind: 'microphone',
        status: 'prompt-with-rationale',
        canRequestAgain: true,
      }),
      'request',
    )
  })

  it('request that stays permanently denied then opens settings', () => {
    assert.equal(
      permissionRowClickAction({
        kind: 'microphone',
        status: 'denied',
        canRequestAgain: false,
        afterRequest: true,
      }),
      'open-settings',
    )
  })

  it('camera and photos request in-app like microphone; unbound biometric binds', () => {
    assert.equal(
      permissionRowClickAction({ kind: 'camera', status: 'prompt-with-rationale', canRequestAgain: true }),
      'request',
    )
    assert.equal(
      permissionRowClickAction({ kind: 'photos', status: 'denied', canRequestAgain: false }),
      'open-settings',
    )
    assert.equal(
      permissionRowClickAction({ kind: 'biometric', status: 'unavailable' }),
      'open-settings',
    )
    assert.equal(permissionRowClickAction({ kind: 'biometric', status: 'prompt' }), 'request')
  })

  it('already granted does nothing', () => {
    assert.equal(
      permissionRowClickAction({ kind: 'microphone', status: 'granted', canRequestAgain: false }),
      'none',
    )
  })
})

describe('permissionStatusLabel', () => {
  it('labels camera and photos as unauthorized until granted', () => {
    assert.equal(permissionStatusLabel('prompt', true), '未授权')
    assert.equal(permissionStatusLabel('denied', false), '已拒绝')
    assert.equal(permissionStatusLabel('granted', false), '已授权')
  })
})

describe('androidSettingsPlan', () => {
  it('microphone locates this app on the microphone permission page', () => {
    const plan = androidSettingsPlan('microphone', 'com.kaixuan.opencode.pocket')
    assert.equal(plan[0].action, 'android.intent.action.MANAGE_APP_PERMISSION')
    assert.equal(plan[0].extras['android.intent.extra.PACKAGE_NAME'], 'com.kaixuan.opencode.pocket')
    assert.equal(plan[0].extras['android.intent.extra.PERMISSION_GROUP_NAME'], 'android.permission-group.MICROPHONE')
  })

  it('notifications locates this app on the notification settings page', () => {
    const plan = androidSettingsPlan('notifications', 'com.kaixuan.opencode.pocket')
    assert.equal(plan[0].action, 'android.settings.APP_NOTIFICATION_SETTINGS')
    assert.equal(plan[0].extras['android.provider.extra.APP_PACKAGE'], 'com.kaixuan.opencode.pocket')
  })

  it('camera and photos use their permission groups and still carry the package', () => {
    const camera = androidSettingsPlan('camera', 'com.kaixuan.opencode.pocket')
    assert.equal(camera[0].extras['android.intent.extra.PERMISSION_GROUP_NAME'], 'android.permission-group.CAMERA')
    const photos = androidSettingsPlan('photos', 'com.kaixuan.opencode.pocket')
    assert.equal(
      photos[0].extras['android.intent.extra.PERMISSION_GROUP_NAME'],
      'android.permission-group.READ_MEDIA_VISUAL',
    )
  })

  it('biometric fallback is fingerprint/security settings, not BIOMETRIC_ENROLL', () => {
    const plan = androidSettingsPlan('biometric', 'com.kaixuan.opencode.pocket')
    assert.equal(plan[0].action, 'android.settings.FINGERPRINT_ENROLL')
    assert.equal(plan[1].action, 'android.settings.SECURITY_SETTINGS')
    assert.equal(plan.some((step) => step.action === 'android.settings.BIOMETRIC_ENROLL'), false)
  })

  it('every plan ends at application details for this package', () => {
    for (const name of ['microphone', 'notifications', 'camera', 'photos', 'biometric'] as const) {
      const plan = androidSettingsPlan(name, 'com.kaixuan.opencode.pocket')
      const last = plan[plan.length - 1]
      assert.equal(last.action, 'android.settings.APPLICATION_DETAILS_SETTINGS')
      assert.equal(last.data, 'package:com.kaixuan.opencode.pocket')
    }
  })
})
