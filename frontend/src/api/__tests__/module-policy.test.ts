import { strict as assert } from 'node:assert'
import { test } from 'node:test'
import { blockedModuleWrite } from '../module-policy.ts'

test('仅本地拦截所有旧数据 API 的写入，保留读与云端清理', () => {
  for (const [path, module] of [
    ['/api/rss/sources', 'rss'], ['/api/email/accounts', 'email'],
    ['/api/emails/ops/sync', 'email'], ['/api/notes', 'notes'], ['/api/meetings/m1/refine', 'meetings'],
  ]) {
    assert.equal(blockedModuleWrite(path, 'POST', () => 'local'), module)
    assert.equal(blockedModuleWrite(path, 'PATCH', () => 'local'), module)
    assert.equal(blockedModuleWrite(path, 'POST', () => 'cloud'), null)
    assert.equal(blockedModuleWrite(path, 'GET', () => 'local'), null)
    assert.equal(blockedModuleWrite(path, 'DELETE', () => 'local'), null)
  }
  assert.equal(blockedModuleWrite('/api/auth/refresh', 'POST', () => 'local'), null)
  assert.equal(blockedModuleWrite('/api/emailing', 'POST', () => 'local'), null)
})
