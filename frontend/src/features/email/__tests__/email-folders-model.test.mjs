/**
 * 自定义邮件目录纯逻辑回归：目录展示名、幂等键、服务端目录行 → 本地镜像映射。
 *
 * UI/DB 层不在此测（需要本地 SQLite 运行时）；这里锁死的是被列表页、详情页、
 * 目录页三处共用的约定——改坏了任何一条，三处一起坏。
 */
import test from 'node:test'
import assert from 'node:assert/strict'
import {
  folderDisplayName,
  mapServerFolder,
  opsIdempotencyKey,
} from '../email-folders-model.ts'

test('folderDisplayName 取层级分隔符后的最后一段', () => {
  assert.equal(folderDisplayName('账单'), '账单')
  assert.equal(folderDisplayName('其他文件夹/垃圾邮件'), '垃圾邮件')
  assert.equal(folderDisplayName('INBOX/Sub/Receipts'), 'Receipts')
  assert.equal(folderDisplayName(''), '')
})

test('opsIdempotencyKey 同一封邮件同一种操作永远同键', () => {
  const a = opsIdempotencyKey({ emailId: 'e1', action: 'move', targetFolder: '账单' })
  const b = opsIdempotencyKey({ emailId: 'e1', action: 'move', targetFolder: '账单' })
  assert.equal(a, b)
  assert.notEqual(
    a,
    opsIdempotencyKey({ emailId: 'e1', action: 'move', targetFolder: '通知' }),
    '不同目标目录必须是不同操作',
  )
  assert.notEqual(
    a,
    opsIdempotencyKey({ emailId: 'e1', action: 'delete', targetFolder: '' }),
    '移动与删除是不同操作',
  )
  assert.notEqual(
    a,
    opsIdempotencyKey({ emailId: 'e2', action: 'move', targetFolder: '账单' }),
    '不同邮件互不相干',
  )
  // 与服务端 InsertOpsLogScoped 的派生规则保持一致（无客户端键时的兜底形态）。
  assert.equal(a, 'ops:e1:move:账单')
})

test('mapServerFolder 服务端目录行落到本地镜像形状', () => {
  const now = 1_760_000_000_000
  const row = mapServerFolder(
    {
      id: 'fld-1',
      accountId: 'acct-1',
      name: '其他文件夹/通知',
      displayName: '',
      special: '',
      source: 'user',
      serverSynced: true,
      extra: { emailCount: 7 },
    },
    now,
  )
  assert.equal(row.id, 'fld-1')
  assert.equal(row.accountId, 'acct-1')
  assert.equal(row.name, '其他文件夹/通知')
  assert.equal(row.displayName, '通知', '缺展示名时按最后一段兜底')
  assert.equal(row.serverSynced, true)
  assert.equal(row.emailCount, 7)
  assert.equal(row.createdAt, now)
  assert.equal(row.updatedAt, now)
})

test('mapServerFolder 缺省字段安全兜底', () => {
  const row = mapServerFolder({ id: 'fld-2', accountId: 'a', name: 'INBOX' }, 1)
  assert.equal(row.special, '')
  assert.equal(row.source, 'server', '未标注来源的发现目录按 server 记')
  assert.equal(row.serverSynced, false)
  assert.equal(row.emailCount, 0)
})
