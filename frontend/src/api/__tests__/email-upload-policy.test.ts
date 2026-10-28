import { strict as assert } from 'node:assert'
import { test } from 'node:test'
import { isEmailMetadataWrite } from '../email-upload-policy.ts'

test('邮件账号与配置旁路拒绝凭据和嵌套敏感载荷，仅放行已知元数据类型', () => {
  const path = '/api/email/accounts/account-1'
  assert.equal(isEmailMetadataWrite(path, JSON.stringify({ displayName: 'Work', imapPort: 993, enabled: true })), true)
  for (const payload of [
    { password: 'secret' }, { oauthToken: 'secret' }, { smtpPassword: 'secret' },
    { displayName: { token: 'secret' } }, { rules: { credentials: { password: 'secret' } } },
    { rules: { keywords: [{ secret: 'secret' }] } }, { Credentials: 'secret' },
  ]) assert.equal(isEmailMetadataWrite(path, JSON.stringify(payload)), false)
  assert.equal(isEmailMetadataWrite('/api/user-settings/email_account/id', JSON.stringify({ payload: { displayName: 'Work' }, secret: 'secret', updatedAt: 1 })), false)
  assert.equal(isEmailMetadataWrite('/api/user-settings/email_account/id', JSON.stringify({ payload: { oauthToken: 'secret' }, updatedAt: 1 })), false)
  assert.equal(isEmailMetadataWrite('/api/user-settings/email_account/id', JSON.stringify({ payload: { displayName: 'Work' }, updatedAt: 1 })), true)
  const form = new FormData()
  form.set('password', 'secret')
  assert.equal(isEmailMetadataWrite('/api/email/accounts', form), false)
})
