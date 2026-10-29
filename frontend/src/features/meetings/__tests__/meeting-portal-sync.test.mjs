import test from 'node:test'
import assert from 'node:assert/strict'
import { fixture, gate } from '../../../test-support/notes-sqlite-fixture.mjs'

async function setup(f) {
  const meetings = await f.loadModule('/src/features/meetings/meetings-store.ts')
  const { meetingPortalStore, meetingSyncAdapter } = await f.loadModule(
    '/src/features/meetings/meeting-sync-adapter.ts',
  )
  const db = f.db.bindOperation(f.db.captureOperation({ kind: 'local-edit' })),
    store = meetingPortalStore(db)
  const { applyRecordPage, syncRecordsOnce } = await f.loadModule('/src/native/portal-sync/record-engine.ts')
  await store.observeEpoch(0)
  return { meetings, meetingPortalStore, meetingSyncAdapter, db, store, applyRecordPage, syncRecordsOnce }
}
const remote = (
  extId,
  at,
  op = 'remote',
  payload = { held_at: '2026-10-01T00:00:00Z', attendees: ['Remote'], minutes: 'Remote minutes' },
) => ({
  module: 'meetings',
  kind: 'record',
  ext_id: extId,
  title: 'Remote title',
  payload,
  client_updated_at: new Date(at).toISOString(),
  op_id: op,
  deleted: false,
  epoch: 0,
  change_seq: 1,
})

test('meeting portable edits and deletion atomically retain immutable owned operations without recording data', async () => {
  await fixture(async (f) => {
    const meetings = await f.loadModule('/src/features/meetings/meetings-store.ts')
    const { meetingPortalStore } = await f.loadModule('/src/features/meetings/meeting-sync-adapter.ts')
    const store = meetingPortalStore(f.db.bindOperation(f.db.captureOperation({ kind: 'local-edit' })))
    await store.observeEpoch(0)
    const m = await meetings.createMeeting({
      title: 'Owned meeting',
      participants: ['Alice'],
      audioPath: 'private.wav',
    })
    await meetings.updateMeeting(m.id, {
      summary: 'Decision',
      transcript: 'RAW PRIVATE',
      refinedTranscript: 'REFINED PRIVATE',
    })
    const pending = await store.pending()
    assert.equal(pending.length, 2)
    assert.equal(pending[1].payload.minutes, 'Decision')
    assert.deepEqual(Object.keys(pending[1].payload).sort(), ['attendees', 'held_at', 'minutes'])
    assert.equal(JSON.stringify(pending).includes('PRIVATE'), false)
    assert.equal(JSON.stringify(pending).includes('private.wav'), false)
    assert.equal(pending[0].client_updated_at, new Date(m.updatedAt).toISOString())
    await meetings.updateMeeting(m.id, { transcript: 'new local transcript', audioPath: 'private2.wav' })
    assert.equal((await store.pending()).length, 2)
    await meetings.deleteMeeting(m.id)
    const after = await store.pending()
    assert.equal(after.length, 3)
    assert.equal(after[2].deleted, true)
    assert.equal(
      f.raw.exec('SELECT transcript FROM local_meetings WHERE id=?', [m.id])[0].values[0][0],
      'new local transcript',
    )
    await f.login('B')
    assert.equal(
      (await meetingPortalStore(f.db.bindOperation(f.db.captureOperation({ kind: 'local-edit' }))).pending()).length,
      0,
    )
  })
})

test('remote meeting versions update only the portable projection and tombstones retain recordings and segments', async () => {
  await fixture(async (f) => {
    const s = await setup(f),
      m = await s.meetings.createMeeting({ title: 'Local', audioPath: 'recording.wav' })
    await s.meetings.updateMeeting(m.id, {
      summary: 'Local minutes',
      transcript: 'Original transcript',
      refinedTranscript: 'Original refined',
      noteId: 'private-note',
    })
    await f.db.run(
      `INSERT INTO local_meeting_segments(id,meeting_id,text,start_ms,end_ms,owner_user_id,owner_workspace_id) VALUES('seg',?,'Original segment',0,1,'user-A','workspace')`,
      [m.id],
    )
    const [queued] = await s.store.pending(),
      newer = remote(queued.ext_id, Date.now() + 1000)
    await s.applyRecordPage(s.store, { changes: [newer], next_cursor: '1', epoch: 0 })
    const changed = await s.meetings.getMeeting(m.id)
    assert.equal(changed.title, 'Remote title')
    assert.equal(changed.summary, 'Remote minutes')
    assert.equal(changed.transcript, 'Original transcript')
    assert.equal(changed.refinedTranscript, 'Original refined')
    assert.equal(changed.audioPath, 'recording.wav')
    assert.equal(changed.noteId, 'private-note')
    await s.applyRecordPage(s.store, {
      changes: [
        {
          ...newer,
          deleted: true,
          payload: {},
          op_id: 'remote-delete',
          client_updated_at: new Date(Date.now() + 2000).toISOString(),
        },
      ],
      next_cursor: '2',
      epoch: 0,
    })
    assert.equal(await s.meetings.getMeeting(m.id), null)
    const raw = await s.db.queryOne('SELECT transcript,audio_path,note_id FROM local_meetings WHERE id=?', [m.id])
    assert.deepEqual(raw, { transcript: 'Original transcript', audio_path: 'recording.wav', note_id: 'private-note' })
    assert.equal(
      (await s.db.queryOne('SELECT text FROM local_meeting_segments WHERE id=?', ['seg'])).text,
      'Original segment',
    )
  })
})

test('remote-first meetings and equal-time conflicts use exact versions and do not create an upload loop', async () => {
  await fixture(async (f) => {
    const s = await setup(f),
      at = Date.now() - 1000
    const first = remote('remote-only', at, 'z-op')
    await s.applyRecordPage(s.store, { changes: [first], next_cursor: '1', epoch: 0 })
    const row = await s.store.byExternalId('remote-only'),
      meeting = await s.meetings.getMeeting(row.source_id)
    assert.equal(meeting.status, 'completed')
    assert.equal(meeting.audioPath, null)
    await s.applyRecordPage(s.store, {
      changes: [{ ...first, title: 'Should lose', op_id: 'a-op' }],
      next_cursor: '2',
      epoch: 0,
    })
    assert.equal((await s.meetings.getMeeting(row.source_id)).title, 'Remote title')
    assert.equal((await s.store.pending()).length, 0)
    await s.meetings.updateMeeting(row.source_id, { summary: 'Edited here' })
    const [pending] = await s.store.pending()
    assert.equal(pending.ext_id, 'remote-only')
    assert.equal(pending.payload.minutes, 'Edited here')
  })
})

test('source writes and managed operations roll back together while historical rows are not adopted', async () => {
  await fixture(async (f) => {
    const s = await setup(f)
    await s.db.run(
      `CREATE TRIGGER reject_module_outbox BEFORE INSERT ON local_portal_record_outbox BEGIN SELECT RAISE(ABORT,'fixture outbox failure'); END`,
    )
    await assert.rejects(() => s.meetings.createMeeting({ title: 'Must roll back' }), /fixture outbox failure/)
    assert.equal((await s.db.queryOne('SELECT COUNT(*) AS n FROM local_meetings')).n, 0)
    await s.db.run('DROP TRIGGER reject_module_outbox')
    await s.db.run(
      `INSERT INTO local_meetings(id,title,started_at,created_at,owner_user_id,owner_workspace_id) VALUES('old','Old',1,1,'user-A','workspace')`,
    )
    await s.meetings.updateMeeting('old', { summary: 'Local edit' })
    assert.equal((await s.store.pending()).length, 0)
    assert.equal((await s.meetings.getMeeting('old')).summary, 'Local edit')
  })
})

test('reset epochs fence old and in-flight meeting operations without rewriting their time or local content', async () => {
  await fixture(async (f) => {
    const s = await setup(f),
      m = await s.meetings.createMeeting({ title: 'Keep local' })
    const before = (await s.store.pending())[0]
    const { PortalResetStore } = await f.loadModule('/src/native/portal-sync/reset-store.ts')
    const { portalNotesStore } = await f.loadModule('/src/native/portal-sync/storage.ts')
    const { captureModuleEpoch } = await f.loadModule('/src/native/portal-sync/epochs.ts')
    const resets = new PortalResetStore(portalNotesStore(s.db)),
      intent = await resets.begin(['meetings'])
    assert.equal(captureModuleEpoch(s.db.owner, 'meetings'), null)
    await resets.complete(intent, { reset_id: intent.reset_id, purged: { meetings: 1 }, epochs: { meetings: 1 } })
    assert.equal(captureModuleEpoch(s.db.owner, 'meetings'), 1)
    assert.equal((await s.store.pending()).length, 0)
    const original = await s.store.decode(
      (await s.db.queryOne('SELECT item_json FROM local_portal_record_outbox')).item_json,
    )
    assert.deepEqual(original, before)
    assert.equal((await s.meetings.getMeeting(m.id)).title, 'Keep local')
    await s.meetings.updateMeeting(m.id, { summary: 'New explicit edit' })
    assert.equal((await s.store.pending())[0].epoch, 1)
    const pause = gate()
    f.cryptoConfig.setFieldEncryption('enabled')
    f.onEncrypt(() => pause.pause())
    const saving = s.meetings.updateMeeting(m.id, { summary: 'Captured before next reset' })
    await pause.started
    const next = await resets.begin(['meetings'])
    await resets.complete(next, { reset_id: next.reset_id, purged: { meetings: 0 }, epochs: { meetings: 2 } })
    pause.release()
    await saving
    assert.equal((await s.store.pending()).length, 0)
    assert.equal((await s.meetings.getMeeting(m.id)).summary, 'Captured before next reset')
    assert.equal(
      (await s.db.queryOne(`SELECT reset_epoch FROM local_portal_record_outbox ORDER BY rowid DESC LIMIT 1`))
        .reset_epoch,
      1,
    )
  })
})

test('meeting acknowledgements remove only exact operations and keep rejected or skipped intents', async () => {
  await fixture(async (f) => {
    const s = await setup(f),
      m = await s.meetings.createMeeting({ title: 'Retry' })
    const [first] = await s.store.pending()
    await s.meetings.updateMeeting(m.id, { summary: 'Second' })
    await s.store.acknowledge(first, {
      module: 'meetings',
      ext_id: first.ext_id,
      op_id: first.op_id,
      outcome: 'applied',
      current_epoch: 0,
    })
    const [second] = await s.store.pending()
    assert.equal(second.payload.minutes, 'Second')
    await s.store.acknowledge(second, {
      module: 'meetings',
      ext_id: second.ext_id,
      op_id: second.op_id,
      outcome: 'skipped_local',
      current_epoch: 0,
    })
    assert.equal((await s.store.pending()).length, 0)
    await s.store.resumeSkipped()
    const [resumed] = await s.store.pending()
    assert.notEqual(resumed.op_id, second.op_id)
    assert.equal(resumed.client_updated_at, second.client_updated_at)
    await s.store.acknowledge(resumed, {
      module: 'meetings',
      ext_id: resumed.ext_id,
      op_id: resumed.op_id,
      outcome: 'rejected',
      reason: 'clock_skew',
      clock_skew: true,
      current_epoch: 0,
    })
    assert.equal((await s.store.pending()).length, 0)
    assert.equal((await s.meetings.getMeeting(m.id)).summary, 'Second')
  })
})

test('multiple kinds sharing an external ID have independent projections and atomic tombstones', async () => {
  await fixture(async (f) => {
    const { PortalRecordStore } = await f.loadModule('/src/native/portal-sync/record-store.ts')
    const { parseRecordPage } = await f.loadModule('/src/native/portal-sync/record-contract.ts')
    const { applyRecordPage } = await f.loadModule('/src/native/portal-sync/record-engine.ts')
    const adapter = {
      module: 'email',
      kind: 'account',
      kinds: ['account', 'summary'],
      payload: (x) => x,
      materialize: () => [],
    }
    const db = f.db.bindOperation(f.db.captureOperation({ kind: 'local-edit' })),
      store = new PortalRecordStore(db, adapter)
    await store.observeEpoch(0)
    const page = parseRecordPage(adapter, {
      epochs: { email: 0 },
      next_cursor: '2',
      changes: [
        {
          ...remote('same', 1000, 'account'),
          module: 'email',
          kind: 'account',
          last_op_id: 'account',
          payload: { address: 'a@example.invalid' },
        },
        {
          ...remote('same', 1000, 'summary'),
          module: 'email',
          kind: 'summary',
          last_op_id: 'summary',
          payload: { snippet: 'a' },
          change_seq: 2,
        },
      ],
    })
    await applyRecordPage(store, page)
    const a = await store.byExternalId('same', 'account'),
      b = await store.byExternalId('same', 'summary')
    assert.notEqual(a.source_id, b.source_id)
    await store.commitMany(
      [],
      [
        {
          snapshot: {
            id: a.source_id,
            kind: 'account',
            title: 'A',
            payload: {},
            modifiedAt: 2000,
            epoch: 0,
            deleted: true,
          },
          created: false,
        },
        {
          snapshot: {
            id: b.source_id,
            kind: 'summary',
            title: 'B',
            payload: {},
            modifiedAt: 2000,
            epoch: 0,
            deleted: true,
          },
          created: false,
        },
      ],
    )
    const pending = await store.pending()
    assert.equal(pending.length, 2)
    assert.deepEqual(pending.map((x) => x.kind).sort(), ['account', 'summary'])
    assert(pending.every((x) => x.ext_id === 'same' && x.deleted))
  })
})

test('an offline retry preserves the operation and network acknowledgement applies only its exact version', async () => {
  await fixture(async (f) => {
    const s = await setup(f),
      m = await s.meetings.createMeeting({ title: 'Offline' })
    const before = await s.store.pending()
    const transport = {
      changes: async () => ({ changes: [], next_cursor: '', epochs: { meetings: 0 } }),
      push: async () => {
        throw new Error('offline')
      },
    }
    await assert.rejects(() => s.syncRecordsOnce(s.store, transport, () => s.db.assertCurrent()), /offline/)
    assert.deepEqual(await s.store.pending(), before)
    transport.push = async (items) => ({
      results: items.map((item) => ({
        module: item.module,
        ext_id: item.ext_id,
        op_id: item.op_id,
        current_epoch: 0,
        outcome: 'applied',
      })),
    })
    assert.equal(await s.syncRecordsOnce(s.store, transport, () => s.db.assertCurrent()), 1)
    assert.equal((await s.store.pending()).length, 0)
    assert.equal((await s.meetings.getMeeting(m.id)).title, 'Offline')
  })
})

test('production meeting transport uses its original binding and stops at local policy or account changes', async () => {
  await fixture(async (f) => {
    const oldWindow = Object.getOwnPropertyDescriptor(globalThis, 'window')
      Object.defineProperty(globalThis, 'window', { configurable: true, value: Object.assign(new EventTarget(), {location:{origin:'https://fixture.invalid',hash:''}}) })
    const ready = await f.loadModule('/src/native/lobster-init.ts')
    try {
      const s = await setup(f)
      await f.setModules({ meetings: 'cloud' })
      ready.lobsterReady.value = true
      const { meetingSyncRuntime } = await f.loadModule('/src/features/meetings/meeting-sync-runtime.ts')
      await s.meetings.createMeeting({ title: 'Portable only', audioPath: 'PRIVATE.wav' })
      f.respond(async (url, options) => {
        if (url.endsWith('/binding'))
          return {
            binding: 'fixture-binding',
            user_id: 'user-A',
            workspace_id: 'workspace',
            tenant_id: 'tenant',
            expires_at: Math.floor(Date.now() / 1000) + 300,
          }
        if (url.includes('/changes?')) return { changes: [], next_cursor: '', epochs: { meetings: 0 } }
        const items = JSON.parse(options.body).items
        return {
          results: items.map((item) => ({
            module: item.module,
            ext_id: item.ext_id,
            op_id: item.op_id,
            current_epoch: 0,
            outcome: 'applied',
          })),
        }
      })
      assert.equal(await meetingSyncRuntime.syncNow(), 1)
      const push = f.requests.find((x) => x.url.includes('/sync/v2/push'))
      assert.equal(push.options.headers.Authorization, 'Bearer token-A')
      assert.equal(push.options.headers['X-Workspace-Binding'], 'fixture-binding')
      assert.equal(push.options.body.includes('PRIVATE.wav'), false)
      await f.setModules({ meetings: 'local' })
      const count = f.requests.length
      await meetingSyncRuntime.syncNow()
      assert.equal(f.requests.length, count)
      await f.setModules({ meetings: 'cloud' })
      await s.meetings.updateMeeting((await s.db.queryOne('SELECT id FROM local_meetings')).id, {
        summary: 'Wait for binding',
      })
      const pause = gate()
      f.respond(async () => {
        await pause.pause()
        return {
          binding: 'old',
          user_id: 'user-A',
          workspace_id: 'workspace',
          tenant_id: 'tenant',
          expires_at: Math.floor(Date.now() / 1000) + 300,
        }
      })
      const syncing = meetingSyncRuntime.syncNow()
      await pause.started
      await f.login('B')
      pause.release()
      await syncing
      assert.equal(f.requests.filter((x) => x.url.includes('/sync/v2/push')).length, 1)
    } finally {
      ready.lobsterReady.value = false
      if (oldWindow) Object.defineProperty(globalThis, 'window', oldWindow)
      else delete globalThis.window
    }
  })
})

test('sequential same-millisecond local edits order intentions without changing the original timestamp', async () => {
  await fixture(async (f) => {
    const s = await setup(f), m = await s.meetings.createMeeting({ title: 'Initial' })
    const now = Date.now, uuid = crypto.randomUUID, at = now() + 1
    let next = 'z-operation'
    try {
      Date.now = () => at
      crypto.randomUUID = () => next
      await s.meetings.updateMeeting(m.id, { summary: 'First edit' })
      next = 'a-operation'
      await s.meetings.updateMeeting(m.id, { summary: 'Second edit' })
      const source = await s.meetings.getMeeting(m.id)
      const projection = await s.store.decode((await s.store.find(m.id)).item_json)
      const pending = await s.store.pending(), first = pending.at(-2), second = pending.at(-1)
      assert.equal(source.summary, 'Second edit')
      assert.equal(projection.payload.minutes, source.summary)
      assert.equal(second.payload.minutes, source.summary)
      assert.equal(first.payload.minutes, 'First edit')
      assert.equal(first.client_updated_at, new Date(at).toISOString())
      assert.equal(second.client_updated_at, first.client_updated_at)
      assert.equal(second.op_id, 'z-operation~a-operation')
      assert.equal((await s.db.queryOne('SELECT ? > ? AS wins', [second.op_id, first.op_id])).wins, 1)
    } finally { Date.now = now; crypto.randomUUID = uuid }
  })
})

test('maximum UTF-8 operation ids and older source clocks explicitly reject instead of silently saving', async () => {
  await fixture(async (f) => {
    const s = await setup(f), at = Date.now()
    const id = '\u{10ffff}'.repeat(50)
    assert.equal(new TextEncoder().encode(id).length, 200)
    await s.applyRecordPage(s.store, { changes: [remote('max-id', at, id)], next_cursor: '1', epoch: 0 })
    const projection = await s.store.byExternalId('max-id')
    const now = Date.now, uuid = crypto.randomUUID
    try {
      crypto.randomUUID = () => 'a-operation'
      Date.now = () => at
      await assert.rejects(() => s.meetings.updateMeeting(projection.source_id, { summary: 'Retained draft' }), /keep your changes and retry/)
      Date.now = () => at - 1
      await assert.rejects(() => s.meetings.updateMeeting(projection.source_id, { summary: 'Older draft' }), /keep your changes and retry/)
      assert.equal((await s.meetings.getMeeting(projection.source_id)).summary, 'Remote minutes')
      assert.equal((await s.store.byExternalId('max-id')).item_json, projection.item_json)
      assert.equal((await s.store.pending()).length, 0)
    } finally { Date.now = now; crypto.randomUUID = uuid }
    const { compareVersions } = await f.loadModule('/src/native/portal-sync/contract.ts')
    const a = { client_updated_at: new Date(at).toISOString(), op_id: '\u{10000}' }
    const b = { ...a, op_id: '\ue000' }
    assert.equal(compareVersions(a, b), 1)
    assert.equal((await s.db.queryOne('SELECT ? > ? AS wins', [a.op_id, b.op_id])).wins, 1)
  })
})

test('a remote version arriving during local encryption rolls back the complete local write', async () => {
  await fixture(async (f) => {
    const s = await setup(f), m = await s.meetings.createMeeting({ title: 'Original' })
    const pause = gate(), now = Date.now, at = now() + 10
    f.cryptoConfig.setFieldEncryption('enabled')
    f.onEncrypt(() => pause.pause())
    try {
      Date.now = () => at
      const saving = s.meetings.updateMeeting(m.id, { summary: 'Pending local draft' })
      const rejected = assert.rejects(saving, /keep your changes and retry/)
      await pause.started
      const { PortalRecordStore } = await f.loadModule('/src/native/portal-sync/record-store.ts')
      const remoteStore = new PortalRecordStore(s.db, s.meetingSyncAdapter)
      await s.applyRecordPage(remoteStore, { changes: [remote(`meeting:${m.id}`, at, 'remote-winner')], next_cursor: '1', epoch: 0 })
      pause.release()
      await rejected
      assert.equal((await s.meetings.getMeeting(m.id)).summary, 'Remote minutes')
      assert.equal((await s.store.decode((await s.store.find(m.id)).item_json)).payload.minutes, 'Remote minutes')
      assert.equal((await s.store.pending()).length, 1)
      assert.equal((await s.db.queryOne('SELECT COUNT(*) AS n FROM local_portal_record_write_guards')).n, 0)
    } finally { pause.release(); Date.now = now }
  })
})

test('batch version conflicts roll back every source and tombstone, and duplicate identities are rejected', async () => {
  await fixture(async (f) => {
    const s = await setup(f), first = await s.meetings.createMeeting({ title: 'First' }), second = await s.meetings.createMeeting({ title: 'Second' })
    const { PortalRecordStore } = await f.loadModule('/src/native/portal-sync/record-store.ts')
    const pause = gate()
    let encoded = 0
    const store = new PortalRecordStore(s.db, s.meetingSyncAdapter, async (item) => {
      if (++encoded === 2) await pause.pause()
      return JSON.stringify(item)
    })
    const at = Date.now() + 100
    const entries = [first, second].map((m) => ({ snapshot: { id: m.id, title: m.title, payload: {}, modifiedAt: at, epoch: 0, deleted: true }, created: false }))
    const saving = store.commitMany([{ statement: 'UPDATE local_meetings SET deleted_at=? WHERE id IN (?,?)', values: [at, first.id, second.id] }], entries)
    const rejected = assert.rejects(saving, /keep your changes and retry/)
    await pause.started
    await s.applyRecordPage(s.store, { changes: [remote(`meeting:${second.id}`, at + 1)], next_cursor: '1', epoch: 0 })
    pause.release()
    await rejected
    assert.equal((await s.meetings.getMeeting(first.id)).title, 'First')
    assert.equal((await s.meetings.getMeeting(second.id)).title, 'Remote title')
    assert.equal((await s.store.pending()).length, 2)
    assert.equal((await s.store.pending()).some((x) => x.deleted), false)
    await assert.rejects(() => s.store.commitMany([{ statement: 'DELETE FROM local_meetings', values: [] }], [entries[0], entries[0]]), /Duplicate module identity/)
    assert.equal((await s.meetings.getMeeting(first.id)).title, 'First')
    const guarded = entries.map((entry) => ({ ...entry, snapshot: {
      ...entry.snapshot, modifiedAt: at + 2,
      guard: { clause: 'EXISTS(SELECT 1 FROM local_meetings WHERE id=? AND title=?)', values: [entry.snapshot.id, 'Source must match'] },
    } }))
    await assert.rejects(() => s.store.commitMany([
      { statement: 'UPDATE local_meetings SET title=? WHERE id=?', values: ['Source must match', first.id] },
    ], guarded), /keep your changes and retry/)
    assert.equal((await s.meetings.getMeeting(first.id)).title, 'First')
    assert.equal((await s.meetings.getMeeting(second.id)).title, 'Remote title')
    assert.equal((await s.store.pending()).length, 2)
  })
})
