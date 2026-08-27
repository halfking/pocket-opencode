/**
 * draftStore 测试：CRUD + local_drafts 表迁移幂等（照 native/__tests__ 既有
 * 模式，node:sqlite 跑真实 SQL，MemoryDraftStore 同步覆盖 web 降级行为）。
 */
import { strict as assert } from 'node:assert'
import { test } from 'node:test'

import { NodeSqlDb } from './helpers.mjs'
import { SCHEMA_SQL } from '../schema.ts'
import { MemoryDraftStore, SqliteDraftStore } from '../draftStore.ts'

test('迁移：local_drafts 表随 SCHEMA_SQL 建立，列形状与契约一致', async () => {
  const db = new NodeSqlDb()
  const cols = await db.all('PRAGMA table_info(local_drafts)')
  const byName = Object.fromEntries(cols.map((c) => [c.name, c]))

  assert.ok(byName.session_id, 'session_id 列存在')
  assert.equal(String(byName.session_id.type).toUpperCase(), 'TEXT')
  assert.equal(byName.session_id.pk, 1, 'session_id 为主键')

  assert.ok(byName.text, 'text 列存在')
  assert.equal(String(byName.text.type).toUpperCase(), 'TEXT')
  assert.equal(byName.text.notnull, 1, 'text NOT NULL')
  assert.equal(byName.text.dflt_value, "''", "text DEFAULT ''")

  assert.ok(byName.updated_at, 'updated_at 列存在')
  assert.equal(String(byName.updated_at.type).toUpperCase(), 'INTEGER')
  assert.equal(byName.updated_at.notnull, 1, 'updated_at NOT NULL')
})

test('迁移幂等：SCHEMA_SQL 重复执行不报错且既有草稿保留', async () => {
  const db = new NodeSqlDb()
  const store = new SqliteDraftStore(db)
  await store.saveDraft('sess_1', 'hello', 1000)

  // 升级路径：老库二次执行整段 schema（全部 IF NOT EXISTS，必须幂等）
  db.db.exec(SCHEMA_SQL)

  const row = await store.getDraft('sess_1')
  assert.equal(row.text, 'hello')
  assert.equal(row.updatedAt, 1000)
})

test('SqliteDraftStore CRUD：save → get（含 updated_at）→ 覆盖 → clear', async () => {
  const store = new SqliteDraftStore(new NodeSqlDb())

  // 无草稿 → null
  assert.equal(await store.getDraft('s1'), null)

  // 新增
  await store.saveDraft('s1', '第一版草稿', 1000)
  let row = await store.getDraft('s1')
  assert.equal(row.sessionId, 's1')
  assert.equal(row.text, '第一版草稿')
  assert.equal(row.updatedAt, 1000)

  // 同 key 覆盖（防抖落盘的最终值 wins）
  await store.saveDraft('s1', '第二版草稿', 2000)
  row = await store.getDraft('s1')
  assert.equal(row.text, '第二版草稿')
  assert.equal(row.updatedAt, 2000)

  // 多会话互不影响
  await store.saveDraft('s2', '另一个会话', 3000)
  assert.equal((await store.getDraft('s2')).text, '另一个会话')

  // 清除
  await store.clearDraft('s1')
  assert.equal(await store.getDraft('s1'), null)
  assert.equal((await store.getDraft('s2')).text, '另一个会话')

  // 重复 clear 不存在的行不报错
  await store.clearDraft('s1')
})

test('MemoryDraftStore 与 SQLite 版行为一致（web 降级路径）', async () => {
  const store = new MemoryDraftStore()
  assert.equal(await store.getDraft('s1'), null)

  await store.saveDraft('s1', 'mem draft', 111)
  assert.equal((await store.getDraft('s1')).text, 'mem draft')
  assert.equal((await store.getDraft('s1')).updatedAt, 111)

  await store.saveDraft('s1', 'updated', 222)
  const row = await store.getDraft('s1')
  assert.equal(row.text, 'updated')
  assert.equal(row.updatedAt, 222)

  await store.clearDraft('s1')
  assert.equal(await store.getDraft('s1'), null)
})
