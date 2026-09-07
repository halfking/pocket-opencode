import { strict as assert } from 'node:assert'
import { test } from 'node:test'
import {
  EMAIL_PROVIDERS,
  inferProviderId,
  isLocalTestAddress,
  providerById,
} from '../providers.ts'

test('qq / 163 / gmail / outlook infer from domain', () => {
  assert.equal(inferProviderId('56551681@qq.com'), 'qq')
  assert.equal(inferProviderId('kimmy.huang@163.com'), '163')
  assert.equal(inferProviderId('a@126.com'), '163')
  assert.equal(inferProviderId('a@gmail.com'), 'gmail')
  assert.equal(inferProviderId('a@outlook.com'), 'outlook')
})

test('custom domain does not pretend to be qq', () => {
  assert.equal(inferProviderId('huangxutao@kxpms.cn'), 'other')
})

test('qq and 163 use SSL SMTP 465 and require auth-code help URL', () => {
  const qq = providerById('qq')
  const wy = providerById('163')
  const ex = providerById('exmail')
  assert.equal(qq.smtpPort, 465)
  assert.equal(qq.imapHost, 'imap.qq.com')
  assert.equal(wy.smtpPort, 465)
  assert.equal(wy.imapHost, 'imap.163.com')
  assert.equal(ex.smtpHost, 'smtp.exmail.qq.com')
  assert.match(qq.authCodeUrl, /mail\.qq\.com/)
  assert.match(wy.authCodeUrl, /163\.com/)
  assert.match(ex.authCodeUrl, /exmail\.qq\.com/)
  assert.ok(qq.authCodeSteps.length >= 3)
})

test('catalog has the six user-facing providers', () => {
  assert.deepEqual(EMAIL_PROVIDERS.map((p) => p.id), [
    'exmail', 'qq', '163', 'gmail', 'outlook', 'other',
  ])
})

test('.local addresses are disposable test mirrors', () => {
  assert.equal(isLocalTestAddress('feikemanager@163.local'), true)
  assert.equal(isLocalTestAddress('56551681@qq.com'), false)
})
