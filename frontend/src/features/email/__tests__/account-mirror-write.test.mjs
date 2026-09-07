/**
 * 远程账户下行到 local_email_accounts 时，不能把 credential_encrypted 绑成
 * NULL / 空串。jeep-sqlite 会把 '' 当成 NULL；SQLite UPSERT 会先按 INSERT
 * 做 NOT NULL 校验，已有行也会直接 1299。
 */
import { strict as assert } from 'node:assert'
import { test } from 'node:test'
import { DatabaseSync } from 'node:sqlite'
import {
  REMOTE_MIRROR_CREDENTIAL,
  buildMirrorAccountWrite,
} from '../account-mirror-write.ts'

const acc = {
  id: 'acct-1',
  displayName: 'Kaixuan',
  emailAddress: 'ops@example.com',
  imapHost: 'imap.example.com',
  imapPort: 993,
  authType: 'password',
  syncIntervalMin: 15,
  enabled: true,
  updatedAt: 100,
}

function openMirrorDb() {
  const db = new DatabaseSync(':memory:')
  db.exec(`
    CREATE TABLE local_email_accounts (
      id TEXT PRIMARY KEY,
      display_name TEXT NOT NULL,
      email_address TEXT NOT NULL,
      imap_host TEXT NOT NULL,
      imap_port INTEGER,
      auth_type TEXT,
      credential_encrypted TEXT NOT NULL,
      sync_interval_min INTEGER,
      enabled INTEGER,
      created_at INTEGER NOT NULL,
      updated_at INTEGER
    )
  `)
  return db
}

test('upsert with NULL credential fails NOT NULL even when the row exists', () => {
  const db = openMirrorDb()
  db.prepare(`
    INSERT INTO local_email_accounts
      (id, display_name, email_address, imap_host, imap_port, auth_type, credential_encrypted,
       sync_interval_min, enabled, created_at, updated_at)
    VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
  `).run(acc.id, acc.displayName, acc.emailAddress, acc.imapHost, acc.imapPort, acc.authType,
    'already-encrypted', 15, 1, 1, 50)

  assert.throws(() => {
    db.prepare(`
      INSERT INTO local_email_accounts
        (id, display_name, email_address, imap_host, imap_port, auth_type, credential_encrypted,
         sync_interval_min, enabled, created_at, updated_at)
      VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
      ON CONFLICT(id) DO UPDATE SET display_name=excluded.display_name
    `).run(acc.id, acc.displayName, acc.emailAddress, acc.imapHost, acc.imapPort, acc.authType,
      null, 15, 1, 1, acc.updatedAt)
  }, /NOT NULL constraint failed: local_email_accounts\.credential_encrypted/)
})

test('new remote account inserts a non-empty placeholder credential', () => {
  const stmt = buildMirrorAccountWrite(null, acc, 123)
  assert.match(stmt.sql, /INSERT INTO local_email_accounts/)
  assert.match(stmt.sql, /credential_encrypted/)
  assert.equal(stmt.values.includes(''), false)
  assert.equal(stmt.values.includes(null), false)
  assert.equal(stmt.values[6], REMOTE_MIRROR_CREDENTIAL)
  assert.ok(REMOTE_MIRROR_CREDENTIAL.length > 0)

  const db = openMirrorDb()
  db.prepare(stmt.sql).run(...stmt.values)
  const row = db.prepare('SELECT credential_encrypted FROM local_email_accounts WHERE id = ?').get(acc.id)
  assert.equal(row.credential_encrypted, REMOTE_MIRROR_CREDENTIAL)
})

test('existing account updates metadata and never touches credential_encrypted', () => {
  const stmt = buildMirrorAccountWrite({ createdAt: 1 }, { ...acc, displayName: 'Renamed' }, 1)
  assert.match(stmt.sql, /^UPDATE local_email_accounts/)
  assert.equal(stmt.sql.includes('credential_encrypted'), false)

  const db = openMirrorDb()
  db.prepare(`
    INSERT INTO local_email_accounts
      (id, display_name, email_address, imap_host, imap_port, auth_type, credential_encrypted,
       sync_interval_min, enabled, created_at, updated_at)
    VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
  `).run(acc.id, acc.displayName, acc.emailAddress, acc.imapHost, acc.imapPort, acc.authType,
    'already-encrypted', 15, 1, 1, 50)
  db.prepare(stmt.sql).run(...stmt.values)
  const row = db.prepare(
    'SELECT display_name, credential_encrypted, updated_at FROM local_email_accounts WHERE id = ?',
  ).get(acc.id)
  assert.equal(row.display_name, 'Renamed')
  assert.equal(row.credential_encrypted, 'already-encrypted')
  assert.equal(row.updated_at, acc.updatedAt)
})
