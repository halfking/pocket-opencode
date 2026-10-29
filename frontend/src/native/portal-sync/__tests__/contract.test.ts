import assert from 'node:assert/strict'
import { test } from 'node:test'
import { compareVersions, notePayload, validateItem, parsePage } from '../contract.ts'

test('notes projection excludes local file paths and keeps the complete body', () => {
  const content = '正文'.repeat(30000)
  const payload = notePayload({ content, format: 'text', tags: ['私有'], audioPath: '/private/audio.webm', password: 'secret' })
  assert.equal(payload.content, content)
  assert.equal('audioPath' in payload, false)
  assert.equal('password' in payload, false)
  assert.throws(() => validateItem({ module: 'notes', kind: 'note', ext_id: 'n', title: '', payload: notePayload({ content: '中'.repeat(90000), format: 'text' }), op_id: '1', client_updated_at: '2026-10-30T00:00:00Z', epoch:0, deleted: false }), /256 KiB/)
})

test('LWW compares original modification time, then the larger operation id', () => {
  const item = (time: string, op_id: string) => ({ client_updated_at: time, op_id })
  assert.equal(compareVersions(item('2026-01-01T00:00:01Z', 'a'), item('2026-01-01T00:00:00Z', 'z')), 1)
  assert.equal(compareVersions(item('2026-01-01T00:00:00Z', 'b'), item('2026-01-01T00:00:00Z', 'a')), 1)
  assert.equal(compareVersions(item('2026-01-01T00:00:00Z', 'a'), item('2026-01-01T00:00:00Z', 'a')), 0)
  assert.equal(compareVersions(item('2026-01-01T00:00:00.000002Z', 'a'), item('2026-01-01T00:00:00.000001Z', 'z')), 1)
  assert.equal(compareVersions(item('2026-01-01T01:00:00.000002+01:00', 'a'), item('2026-01-01T00:00:00.000002Z', 'a')), 0)
})

test('legacy portal content/tags and empty operation ids are explicit, bounded compatibility', () => {
  const legacy = {module:'notes',kind:'note',ext_id:'portal-old',title:'Old',payload:{content:'full legacy body',tags:['t']},client_updated_at:'2026-01-01T00:00:00.000001Z',last_op_id:'',change_seq:1,epoch:0,deleted:false}
  const page = parsePage({changes:[legacy],epochs:{notes:0},next_cursor:'one'})
  assert.equal(page.changes[0].payload.content,'full legacy body')
  assert.equal(page.changes[0].op_id,'')
  assert.throws(()=>parsePage({changes:[{...legacy,payload:{...legacy.payload,unknown:'retain'}}],epochs:{notes:0},next_cursor:'one'}),/Unsupported/)
})
