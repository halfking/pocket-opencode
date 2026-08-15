/**
 * splitSqlStatements tests — Android 插件按 `;` 切分会截断触发器体
 * （全新安装 "Execute: incomplete input ... CREATE TRIGGER"），切分器
 * 必须把 BEGIN...END 作为一个整体保留，且结果逐条可被真实 SQLite 执行。
 */
import { strict as assert } from 'node:assert'
import { test } from 'node:test'
import { DatabaseSync } from 'node:sqlite'

import { SCHEMA_SQL, splitSqlStatements } from '../schema.ts'

test('触发器 BEGIN...END 不被体内分号截断', () => {
  const statements = splitSqlStatements(SCHEMA_SQL)
  const triggers = statements.filter((s) => /^CREATE(\s+OR\s+REPLACE)?\s+TRIGGER/i.test(s))
  assert.ok(triggers.length >= 6, `应有 6+ 个触发器，实际 ${triggers.length}`)
  for (const t of triggers) {
    assert.match(t, /BEGIN/, '触发器应含 BEGIN')
    assert.match(t, /END;?$/, '触发器应以 END 结尾')
    // 体内 INSERT 语句（含分号）必须完整保留在语句内
    assert.ok(t.includes('INSERT INTO'), '触发器体应包含 INSERT')
  }
})

test('切分结果逐条可执行（node:sqlite 真实建库）', () => {
  const statements = splitSqlStatements(SCHEMA_SQL)
  const db = new DatabaseSync(':memory:')
  db.exec('BEGIN')
  for (const s of statements) db.exec(s)
  db.exec('COMMIT')
  const tables = db
    .prepare("SELECT name FROM sqlite_master WHERE type='table' ORDER BY name")
    .all()
    .map((r) => r.name)
  assert.ok(tables.includes('local_notes'))
  assert.ok(tables.includes('local_notes_fts'))
  assert.ok(tables.includes('local_mobile_sessions'))
})

test('普通多语句与注释混排切分正确', () => {
  const out = splitSqlStatements(`
-- 注释里有;分号
CREATE TABLE t (a INTEGER); -- 行尾注释
CREATE INDEX idx_t ON t(a);
  `)
  assert.equal(out.length, 2)
  assert.match(out[0], /^CREATE TABLE t/)
  assert.match(out[1], /^CREATE INDEX idx_t/)
})
