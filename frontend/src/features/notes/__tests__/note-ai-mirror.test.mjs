import assert from 'node:assert/strict'
import { test } from 'node:test'
import { fixture, until } from '../../../test-support/notes-sqlite-fixture.mjs'

// The versioned portal projection replaces the former unversioned pocket copy.
// These behavioral tests fail if createNote sends a legacy mirror again.
test('cloud saves queue the complete portable body without creating a legacy backend note', () => fixture(async f => {
  const content = 'Complete note body '.repeat(100)
  const note = await f.notes.createNote({ content, title: 'Versioned note', workspaceId: 'workspace' })
  const store = f.portalNotesStore(f.db.bindOperation(f.db.captureOperation({ kind: 'local-edit' })))
  const [item] = await store.pending()
  assert.equal(item.payload.content, content)
  assert.equal((await f.notes.getNote(note.id, false, 'workspace')).id, note.id)
  await assert.rejects(f.notes.awaitNoteMirror(note.id), /note_ai_metadata_unavailable/)
  assert.equal(f.requests.some(r => /\/api\/notes(?:\/|$)/.test(r.url)), false)
}))

test('legacy AI summary and classification dependency reports unavailable before dispatch', () => fixture(async f => {
  f.auth.authMethod='redclaw'; await f.setMode('cloud')
  const note = await f.notes.createNote({ content: 'Retained original', workspaceId: 'workspace' }, { backgroundCloud: false })
  const before = f.requests.length
  await assert.rejects(f.notes.awaitNoteMirror(note.id), /note_ai_metadata_unavailable/)
  assert.equal(f.requests.length, before)
  assert.equal((await f.notes.getNote(note.id, false, 'workspace')).content, 'Retained original')
  const { resolveErrorI18nKey } = await f.loadModule('/src/api/error-message.ts')
  assert.equal(resolveErrorI18nKey(new Error('note_ai_metadata_unavailable')), 'errors.noteAiMetadataUnavailable')
}))

test('a direct pocket login can use an existing unversioned backend note without uploading its local original', () => fixture(async f => {
  const note=await f.notes.createNote({content:'PRIVATE ORIGINAL',workspaceId:'workspace'},{backgroundCloud:false})
  f.respond(async url => {
    assert.ok(url.endsWith(`/api/notes/${note.id}/summarize`))
    return {summary:'Existing legacy backend summary'}
  })
  await f.notes.awaitNoteMirror(note.id)
  const {notesApi}=await f.loadModule('/src/api/notes.ts')
  assert.equal((await notesApi.summarize(note.id)).summary,'Existing legacy backend summary')
  assert.equal(f.requests.length,1)
  assert.equal(f.requests[0].options.body,undefined)
  assert.equal((await f.notes.getNote(note.id,false,'workspace')).content,'PRIVATE ORIGINAL')
}))

test('changing to a direct pocket mode cannot route a v2 note through the legacy AI endpoint', () => fixture(async f => {
  f.auth.authMethod='redclaw'; await f.setMode('cloud')
  const note=await f.notes.createNote({content:'Managed original',workspaceId:'workspace'})
  await f.auth.setAuthWithWorkspace('direct-token','A','user-A','workspace','legacy');await f.setMode('cloud')
  await assert.rejects(f.notes.awaitNoteMirror(note.id),/note_ai_metadata_unavailable/)
  assert.equal(f.requests.some(r => /\/api\/notes(?:\/|$)/.test(r.url)),false)
}))

test('a direct legacy AI check cannot cross an identity change during its projection lookup', () => fixture(async f => {
  const note=await f.notes.createNote({content:'Original',workspaceId:'workspace'},{backgroundCloud:false})
  let changing
  f.onQuery(sql => {if(!changing && sql.startsWith('SELECT 1 FROM local_portal_notes')) changing=f.login('B')})
  await assert.rejects(f.notes.awaitNoteMirror(note.id),{name:'AbortError'})
  await changing
  assert.equal(f.requests.length,0)
}))

test('a rejected optional embedding does not imply that the note itself was synchronized', () => fixture(async f => {
  f.respond(async url => {
    if (url.endsWith('/api/embed')) throw new Error('resource unavailable')
    throw new Error('Unexpected legacy mirror')
  })
  const note = await f.notes.createNote({ content: 'Safe local content', workspaceId: 'workspace' })
  await until(() => f.warnings.some(row => String(row[0]).includes('嵌入失败')))
  const store = f.portalNotesStore(f.db.bindOperation(f.db.captureOperation({ kind: 'local-edit' })))
  assert.equal((await store.pending()).length, 1)
  assert.equal((await f.notes.getNote(note.id, false, 'workspace')).content, 'Safe local content')
  assert.equal(f.requests.some(r => /\/api\/notes(?:\/|$)/.test(r.url)), false)
}))
