import assert from 'node:assert/strict'
import { test } from 'node:test'
import { fixture } from '../../../test-support/notes-sqlite-fixture.mjs'

const cases = [
  ['notes', 'note', { format: 'text', content: 'remote text' }],
  ['meetings', 'record', { held_at: '', attendees: [], minutes: 'remote minutes' }, 'meeting-sync-adapter', 'meetingSyncAdapter'],
  ['rss', 'source', { url: 'https://example.com/feed', category: '' }, 'rss-sync-adapter', 'rssSyncAdapter'],
  ['email', 'summary', { from: 'test@example.com', subject: '', snippet: '', received_at: '' }, 'portal-mail-adapter', 'portalMailAdapter'],
]

async function setup(f, [module, kind, payload, adapterPath, exportName]) {
  const load = file => f.loadModule('/src/native/portal-sync/' + file + '.ts')
  const { PortalCursorExpired, portalChangePageSize } = await load('snapshot-recovery')
  const db = f.db.bindOperation(f.db.captureOperation({ kind: 'local-edit' }))
  const notes = module === 'notes'
  const adapter = notes ? null : (await f.loadModule(`/src/features/${module}/${adapterPath}.ts`))[exportName]
  const store = notes ? new (await load('store')).PortalNotesStore(db) : new (await load('record-store')).PortalRecordStore(db, adapter)
  const sync = notes ? (await load('engine')).syncNotesOnce : (await load('record-engine')).syncRecordsOnce
  await store.observeEpoch(0)
  const projection = notes ? (await load('contract')).notePayload(payload) : payload
  const change = (id, sequence) => ({
    module, kind, ext_id: id, title: id, payload: projection, change_seq: sequence, last_op_id: `op-${id}`,
    client_updated_at: new Date(sequence * 1000).toISOString(), deleted: false,
  })
  const page = (changes, next_cursor, epoch = 0) => ({ changes, next_cursor, epochs: { [module]: epoch } })
  const apply = (changes, push = () => assert.fail('unexpected push'), onApplied) =>
    sync(store, { changes, push }, () => db.assertCurrent(), ...(notes ? [undefined, onApplied] : [onApplied]))
  return { db, store, PortalCursorExpired, portalChangePageSize, change, page, apply, projection }
}

for (const spec of cases) {
  const [module, kind] = spec
  test(`${module} recovers after an interrupted 410 snapshot and preserves owned data and immutable edits`, () => fixture(async f => {
    const { db, store, PortalCursorExpired, portalChangePageSize, change, page, apply, projection } = await setup(f, spec)
    const missing = change('gone', 2), edited = change('edited', 3)
    await apply(async cursor => cursor ? page([], cursor) : page([missing, edited], '3'))
    const mirror = await store.byExternalId(missing.ext_id, kind)
    const editedMirror = await store.byExternalId(edited.ext_id, kind)
    const isNotes = module === 'notes'
    const local = { title: 'local draft', payload: projection, modifiedAt: 20000, epoch: 0, kind, source: 'note' }
    await store.commit([], { ...local, id: editedMirror.source_id }, false)
    await store.commit([], { ...local, id: 'unpublished-original' }, true)
    const immutable = await store.pending()
    const edit = immutable.find(item => item.ext_id === edited.ext_id)
    const fresh = immutable.find(item => item.ext_id !== edited.ext_id)
    assert.ok(edit && fresh)
    const rows = Array.from({length: portalChangePageSize}, (_, i) => change(`active-${i}`, i + 21))
    await assert.rejects(apply(async cursor => {
      if (cursor === '3') throw new PortalCursorExpired()
      if (cursor === '') return page(rows, 'snapshot:100:70')
      assert.equal(cursor, 'snapshot:100:70')
      throw new Error('lost connection')
    }), /lost connection/)
    assert.equal(await store.cursor(), '3')
    assert.equal((await db.queryOne('SELECT cursor FROM local_portal_recovery')).cursor, 'snapshot:100:70')
    assert.ok(await store.byExternalId(missing.ext_id, kind))

    const resumed = await setup(f, spec)
    const sent = []
    const cursors = []
    const accepted = await resumed.apply(async cursor => {
      cursors.push(cursor)
      if (cursor === 'snapshot:100:70') return page([], '100')
      if (cursor === '100') return page([], cursor)
      assert.fail(`unexpected cursor ${cursor}`)
    }, async items => {
      sent.push(...items)
      return {results: items.map(item => ({...item, outcome:'applied', current_epoch:0}))}
    })
    assert.equal(accepted, 1)
    assert.deepEqual(sent, [fresh])
    assert.deepEqual(cursors, ['snapshot:100:70', '100'])
    assert.equal(await resumed.store.cursor(), '100')
    assert.equal(await db.queryOne('SELECT epoch FROM local_portal_recovery'), null)
    assert.equal(await db.queryOne('SELECT ext_id FROM local_portal_recovery_seen'), null)

    if (isNotes || module === 'meetings') {
      const table = isNotes ? 'local_notes' : 'local_meetings'
      const field = isNotes ? 'content' : 'summary'
      const row = await db.queryOne(`SELECT ${field} AS body,deleted_at FROM ${table} WHERE id=?`, [mirror.source_id])
      assert.ok(row.body)
      assert.ok(row.deleted_at > 0)
      assert.equal((await db.queryOne(`SELECT deleted_at FROM ${table} WHERE id=?`, [editedMirror.source_id])).deleted_at, null)
      assert.equal((await resumed.store.byExternalId(missing.ext_id, kind)).source_id, mirror.source_id)
    } else {
      assert.equal(await resumed.store.byExternalId(missing.ext_id, kind), null)
    }
    assert.ok(await resumed.store.byExternalId(rows[0].ext_id, kind))
    const outbox = isNotes ? 'local_portal_outbox' : 'local_portal_record_outbox'
    const retained = await db.queryOne(`SELECT item_json,last_error FROM ${outbox} WHERE op_id=?`, [edit.op_id])
    assert.deepEqual(await resumed.store.decode(retained.item_json), edit)
    assert.equal(retained.last_error, 'snapshot_missing')
  }))
}

test('recovery restarts on a second watermark and discards seen rows from the expired snapshot', () => fixture(async f => {
  const { store, PortalCursorExpired, portalChangePageSize, change, page, apply } = await setup(f, cases[2])
  await apply(async cursor => cursor ? page([], cursor) : page([change('old', 1)], '1'))
  const oldSnapshotRows = Array.from({length:portalChangePageSize}, (_, i) => change(i === 0 ? 'old' : `prior-${i}`, i + 1))
  let start = 0
  await apply(async cursor => {
    if (cursor === '1' || cursor === 'snapshot:100:50') throw new PortalCursorExpired()
    assert.equal(cursor, '')
    if (start++ === 0) return page(oldSnapshotRows, 'snapshot:100:50')
    return page([change('present', 101)], '101')
  })
  assert.equal(start, 2)
  assert.equal(await store.cursor(), '101')
  assert.equal(await store.byExternalId('old', 'source'), null)
  assert.equal(await store.byExternalId('prior-1', 'source'), null)
  assert.ok(await store.byExternalId('present', 'source'))
}))

test('a failed final SQLite transaction retains the old checkpoint and keeps other accounts untouched', () => fixture(async f => {
  const first = await setup(f, cases[0])
  await first.apply(async cursor => cursor ? first.page([], cursor) : first.page([first.change('a-mirror', 4)], '4'))
  await f.login('B')
  const other = await setup(f, cases[0])
  await other.apply(async cursor => cursor ? other.page([], cursor) : other.page([other.change('b-mirror', 5)], '5'))
  const bId = (await other.store.byExternalId('b-mirror')).source_id

  await f.login('A')
  const active = await setup(f, cases[0])
  const aId = (await active.store.byExternalId('a-mirror')).source_id
  const recover = async cursor => {
    if (cursor === '4') throw new active.PortalCursorExpired()
    assert.equal(cursor, '')
    return active.page([], '600')
  }
  f.raw.exec(`CREATE TRIGGER fail_checkpoint BEFORE UPDATE ON local_portal_cursors WHEN NEW.cursor='600'
    BEGIN SELECT RAISE(ABORT, 'failed checkpoint'); END;`)
  await assert.rejects(active.apply(recover), /failed checkpoint/)
  assert.equal(await active.store.cursor(), '4')
  assert.equal((await active.db.queryOne('SELECT deleted_at FROM local_notes WHERE id=?', [aId])).deleted_at, null)
  f.raw.exec('DROP TRIGGER fail_checkpoint')
  await active.apply(recover)
  assert.equal(await active.store.cursor(), '600')
  assert.ok((await active.db.queryOne('SELECT deleted_at FROM local_notes WHERE id=?', [aId])).deleted_at)
  await f.login('B')
  const reopenedOther = await setup(f, cases[0])
  assert.equal(await reopenedOther.store.cursor(), '5')
  assert.equal((await reopenedOther.db.queryOne('SELECT deleted_at FROM local_notes WHERE id=?', [bId])).deleted_at, null)
}))

test('a reset epoch change during snapshot recovery restarts from the beginning', () => fixture(async f => {
  const { store, PortalCursorExpired, portalChangePageSize, change, page, apply } = await setup(f, cases[2])
  await apply(async cursor => cursor ? page([], cursor) : page([change('original', 1)], '1'))
  let restarted = false
  const beforeReset = Array.from({length:portalChangePageSize}, (_, i) => change(`previous-${i}`, i + 1))
  const cursors = []
  await apply(async cursor => {
    cursors.push(cursor)
    if (cursor === '1') throw new PortalCursorExpired()
    if (cursor === 'snapshot:100:50') {
      restarted = true
      return page([], '100', 1)
    }
    assert.equal(cursor, '')
    return restarted ? page([change('new', 101)], '101', 1) : page(beforeReset, 'snapshot:100:50')
  })
  assert.deepEqual(cursors, ['1', '', 'snapshot:100:50', ''])
  assert.equal(await store.currentEpoch(), 1)
  assert.equal(await store.cursor(), '101')
  assert.equal(await store.byExternalId('previous-0', 'source'), null)
  assert.ok(await store.byExternalId('new', 'source'))
}))

test('legacy notes and portable notes keep separate identities during snapshot and edit', () => fixture(async f => {
  const { db, store, PortalCursorExpired, change, page, apply } = await setup(f, cases[0])
  const canonical = change('shared', 1)
  const legacy = { ...change('shared', 2), kind: 'item', payload: { body: 'legacy body', tags: ['old'] } }
  await apply(async cursor => cursor ? page([], cursor) : page([canonical, legacy], '2'))
  const first = await store.byExternalId('shared', 'note')
  const second = await store.byExternalId('shared', 'item')
  assert.notEqual(first.source_id, second.source_id)
  const readNative = id => db.queryOne('SELECT content,deleted_at FROM local_notes WHERE id=?', [id])
  assert.equal((await readNative(first.source_id)).content, 'remote text')
  assert.equal((await readNative(second.source_id)).content, 'legacy body')

  const editPayload = (await f.loadModule('/src/native/portal-sync/contract.ts')).notePayload({ content:'edited legacy', format:'text' })
  await store.commit([], { source:'note', id:second.source_id, title:'legacy edit', payload:editPayload, modifiedAt:10000, epoch:0 }, false)
  const queued = (await store.pending())[0]
  assert.equal(queued.kind, 'item')
  assert.equal(queued.ext_id, 'shared')
  const sent = []
  await apply(async cursor => {
    if (cursor === '2') throw new PortalCursorExpired()
    if (cursor === '') return page([legacy], '10')
    assert.equal(cursor, '10')
    return page([], cursor)
  }, async items => {
    sent.push(...items)
    return { results:items.map(item => ({ ...item, outcome:'applied', current_epoch:0 })) }
  })
  assert.deepEqual(sent, [queued])
  assert.ok((await readNative(first.source_id)).deleted_at)
  assert.equal((await readNative(second.source_id)).deleted_at, null)
  assert.equal((await store.byExternalId('shared', 'item')).source_id, second.source_id)
  assert.deepEqual(await store.pending(), [])
}))

test('the notes identity upgrade preserves existing source IDs, encrypted bytes and queue metadata', () => fixture(async f => {
  const { db, store, change, page, apply } = await setup(f, cases[0])
  const upgraded = await store.byExternalId('shared', 'note')
  assert.equal(upgraded.source_id, 'saved-local')
  assert.equal(upgraded.item_json, 'e1:opaque-projection')
  assert.equal(upgraded.reset_epoch, 0)
  assert.deepEqual(await db.queryOne('SELECT kind,item_json,last_error,reset_epoch FROM local_portal_outbox WHERE op_id=?', ['saved-op']),
    { kind:'note', item_json:'e1:opaque-outbox', last_error:'previous_error', reset_epoch:0 })

  await apply(async cursor => cursor ? page([], cursor) : page([{ ...change('shared', 2), kind:'item', payload:{ content:'legacy after upgrade' } }], '2'))
  assert.equal((await store.byExternalId('shared', 'note')).source_id, 'saved-local')
  assert.notEqual((await store.byExternalId('shared', 'item')).source_id, 'saved-local')
}, { prepareDatabase(raw) {
  raw.run(`CREATE TABLE local_portal_notes (
    owner_user_id TEXT NOT NULL,owner_workspace_id TEXT NOT NULL,
    ext_id TEXT NOT NULL,source TEXT NOT NULL,source_id TEXT NOT NULL,item_json TEXT NOT NULL,
    version_ms INTEGER NOT NULL,version_key TEXT NOT NULL,op_id TEXT NOT NULL,reset_epoch INTEGER,
    PRIMARY KEY(owner_user_id,owner_workspace_id,ext_id), UNIQUE(owner_user_id,owner_workspace_id,source,source_id));
    CREATE TABLE local_portal_outbox (
    owner_user_id TEXT NOT NULL,owner_workspace_id TEXT NOT NULL,
    op_id TEXT NOT NULL,ext_id TEXT NOT NULL,item_json TEXT NOT NULL,last_error TEXT NOT NULL DEFAULT '',reset_epoch INTEGER,
    PRIMARY KEY(owner_user_id,owner_workspace_id,op_id));
    INSERT INTO local_portal_notes VALUES('user-A','workspace','shared','note','saved-local','e1:opaque-projection',1000,'1000','saved-op',0);
    INSERT INTO local_portal_outbox VALUES('user-A','workspace','saved-op','shared','e1:opaque-outbox','previous_error',0);`)
} }))
