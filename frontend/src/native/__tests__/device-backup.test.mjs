import assert from 'node:assert/strict'
import { test } from 'node:test'
import { backupBlobSource, restoreBackup, writeBackup } from '../device-backup/archive.ts'
import { validateManifest } from '../device-backup/manifest.ts'

const owner = { userId: 'fixture-user', workspaceId: 'fixture-workspace' }
const password = 'isolated test passphrase'
const database = { kind: 'database', size: 16 }
const file = { kind: 'file', path: 'notes/n1/body.md', size: 2 }
const recording = { kind: 'recording', store: 'owned-recordings', key: '["owned-v1","fixture-user","fixture-workspace","m1"]', mimeType: 'audio/wav', size: 1 }

async function makeArchive() {
  const fileParts = []
  const content = [database, { ...file, size: 1_048_580 }, recording, { ...file, path: 'notes/n2/body.md', size: 0 }]
  const input = content.map((entry, index) => ({
    entry,
    async read(offset, length) {
      assert.ok(length <= 1_048_576)
      assert.ok(offset + length <= entry.size)
      return new Uint8Array(length).fill(index + 1).buffer
    },
  }))
  await writeBackup(owner, input, password, async bytes => fileParts.push(bytes))
  return { file: new Blob(fileParts), fileParts, content }
}

function staging() {
  const status = { committed: false, aborted: false, opened: [], sizes: [], bodies: [] }
  return {
    status,
    async open(entry) {
      status.opened.push(entry)
      const chunks = []
      return {
        async write(chunk) { chunks.push(chunk) },
        async close() {
          status.sizes.push(chunks.reduce((size, chunk) => size + chunk.byteLength, 0))
          status.bodies.push(chunks)
        },
      }
    },
    async commit() { status.committed = true },
    async abort() { status.aborted = true },
  }
}

test('authenticates the manifest and moves multiple bounded chunks into staging in order', async () => {
  const { file, content } = await makeArchive()
  const target = staging()
  await restoreBackup(backupBlobSource(file), password, owner, async manifest => {
    assert.deepEqual(manifest, { owner, entries: content })
    manifest.entries.length = 0 // An adapter cannot change frame boundaries.
    return target
  })
  assert.deepEqual(target.status.opened, content)
  assert.deepEqual(target.status.sizes, content.map(entry => entry.size))
  assert.equal(target.status.bodies[1].length, 2)
  assert.deepEqual(target.status.bodies[1].map(chunk => new Uint8Array(chunk)[0]), [2, 2])
  assert.equal(target.status.committed, true)
  assert.equal(target.status.aborted, false)
})

test('wrong password or owner, truncated and extended files never open staging', async () => {
  const { file, fileParts } = await makeArchive()
  const shouldNotStage = () => assert.fail('opened staging for a rejected backup')
  const wrongHeader = new Uint8Array(fileParts[0].slice(0))
  wrongHeader[8] ^= 1
  const corruptPrefix = new Blob([wrongHeader, ...fileParts.slice(1)])
  for (const [input, passphrase, identity, code] of [
    [file, 'wrong', owner, 'authentication'],
    [file, password, { ...owner, workspaceId: 'another-workspace' }, 'owner'],
    [file.slice(0, file.size - 1), password, owner, 'format'],
    [new Blob([file, new Uint8Array(1)]), password, owner, 'format'],
    [corruptPrefix, password, owner, 'authentication'],
  ]) {
    await assert.rejects(restoreBackup(backupBlobSource(input), passphrase, identity, shouldNotStage), { code })
  }
})

test('late tampering and staged write failure abort without publishing a partially restored backup', async () => {
  const { file, fileParts } = await makeArchive()
  const finalFrame = new Uint8Array(fileParts.at(-1).slice(0))
  finalFrame[0] ^= 1
  const altered = new Blob([...fileParts.slice(0, -1), finalFrame])
  const corruptTarget = staging()
  await assert.rejects(restoreBackup(backupBlobSource(altered), password, owner, async () => corruptTarget), {
    code: 'authentication',
  })
  assert.deepEqual(corruptTarget.status.sizes, [16, 1_048_580])
  assert.equal(corruptTarget.status.aborted, true)
  assert.equal(corruptTarget.status.committed, false)

  const interrupted = staging()
  interrupted.commit = async () => { throw new Error('installer rejected the SQLCipher candidate') }
  await assert.rejects(restoreBackup(backupBlobSource(file), password, owner, async () => interrupted), /installer rejected/)
  assert.equal(interrupted.status.aborted, true)
  assert.equal(interrupted.status.committed, false)
})

test('archive rejects unsafe paths, conflicting file identities, malformed ownership and repeated record ids', () => {
  for (const rest of [
    [{ ...file, path: 'notes/../Documents/token' }],
    [{ ...file, path: '/notes/n1/body.md' }],
    [{ ...file, path: 'notes/%2e%2e/secret' }],
    [{ ...file, path: 'other/n1/body.md' }],
    [file, { ...file, path: 'notes/n1/BODY.md' }],
    [file, { ...file, path: 'notes/N1/persist.md' }],
    [file, { ...file, path: 'notes/n1/body.md/child' }],
    [{ ...file, path: 'notes/n1/body.md/child' }, file],
    [recording, recording],
    [{ ...recording, key: 'unowned-legacy' }],
    [{ ...recording, key: '["owned-v1","fixture-user","","m1"]' }],
    [{ ...file, size: -1 }],
    [database],
  ]) assert.throws(() => validateManifest({ owner, entries: [database, ...rest] }), { code: 'format' })
  assert.throws(() => validateManifest({ owner, entries: [file] }), { code: 'format' })
  assert.equal(validateManifest({ owner, entries: [database, recording, { ...recording, store: 'recordings' }] }).entries.length, 3)
})

test('snapshot short reads and overlong archives fail before a complete backup is written', async () => {
  const writes = []
  const read = async () => new ArrayBuffer(0)
  await assert.rejects(writeBackup(owner, [{ entry: database, read }], password, async chunk => writes.push(chunk)), { code: 'source' })
  assert.equal(writes.length, 2)
  writes.length = 0
  await assert.rejects(writeBackup(owner, [{ entry: { ...database, size: 2 ** 36 + 1 }, read }], password, async chunk => writes.push(chunk)), { code: 'format' })
  assert.equal(writes.length, 0)
})
