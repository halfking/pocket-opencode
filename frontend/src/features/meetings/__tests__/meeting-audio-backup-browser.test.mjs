import test from 'node:test'
import assert from 'node:assert/strict'
import { chromium } from 'playwright'
import { createMeetingAudioFixture } from './fixtures/meeting-audio-browser.mjs'

test('device archive restores both real IndexedDB audio stores on a separate installation', async () => {
  const fixture = await createMeetingAudioFixture()
  let browser
  try {
    browser = await chromium.launch({ executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE })
    const source = await browser.newPage()
    const target = await browser.newPage()
    await source.goto(fixture.url)
    await target.goto(fixture.url)
    await source.locator('#legacy').click()
    await source.getByText('legacy retained').waitFor()

    const encrypted = await source.evaluate(async () => {
      const { saveMeetingAudio, snapshotMeetingAudio } = await import('/src/native/meeting-audio.ts')
      const { writeBackup, backupBlobSource } = await import('/src/native/device-backup/archive.ts')
      const owner = { userId: 'A', workspaceId: 'one' }
      const assertCurrent = () => {}
      for (const [userId, audio, type] of [['A', 'audio A', 'audio/webm'], ['B', 'audio B', 'audio/wav']]) {
        await saveMeetingAudio('same-id', new Blob([audio], { type }), { owner: { ...owner, userId }, assertCurrent })
      }
      const db = backupBlobSource(new Blob(['opaque-example-db']))
      const entries = [{ entry: { kind: 'database', size: db.size }, read: db.read }]
      for (const { blob, ...metadata } of await snapshotMeetingAudio(assertCurrent)) {
        const audio = backupBlobSource(blob)
        entries.push({ entry: { kind: 'recording', size: audio.size, ...metadata }, read: audio.read })
      }
      const output = []
      await writeBackup(owner, entries, 'fixture passphrase', async chunk => output.push(chunk))
      return Array.from(new Uint8Array(await new Blob(output).arrayBuffer()))
    })

    const result = await target.evaluate(async archive => {
      const { restoreMeetingAudio, loadMeetingAudio, snapshotMeetingAudio } = await import('/src/native/meeting-audio.ts')
      const { restoreBackup, backupBlobSource } = await import('/src/native/device-backup/archive.ts')
      const owner = { userId: 'A', workspaceId: 'one' }
      const assertCurrent = () => {}
      const load = async (userId, workspaceId) => {
        const url = await loadMeetingAudio('same-id', { owner: { userId, workspaceId }, assertCurrent })
        if (!url) return null
        try {
          const blob = await (await fetch(url)).blob()
          return [blob.type, await blob.text()]
        } finally { URL.revokeObjectURL(url) }
      }

      const before = await load('A', 'one')
      const backup = backupBlobSource(new Blob([new Uint8Array(archive)]))
      const staged = []
      await restoreBackup(backup, 'fixture passphrase', owner, async () => ({
        async open(entry) {
          const chunks = []
          return {
            async write(chunk) { chunks.push(chunk) },
            async close() {
              if (entry.kind === 'recording') staged.push({ ...entry, blob: new Blob(chunks, { type: entry.mimeType }) })
            },
          }
        },
        async commit() { await restoreMeetingAudio(staged, assertCurrent) },
        async abort() { staged.length = 0 },
      }))
      const legacy = (await snapshotMeetingAudio(assertCurrent)).find(record => record.store === 'recordings')
      const rejected = await restoreMeetingAudio(staged, assertCurrent).then(() => '', error => error.message)
      return {
        before, a: await load('A', 'one'), b: await load('B', 'one'), wrongWorkspace: await load('A', 'two'),
        legacy: [legacy?.key, await legacy?.blob.text()], rejected,
      }
    }, encrypted)

    assert.deepEqual(result, {
      before: null, a: ['audio/webm', 'audio A'], b: ['audio/wav', 'audio B'], wrongWorkspace: null,
      legacy: ['same-id', 'legacy private'], rejected: 'Meeting audio restore target is not empty',
    })

    const empty = await browser.newPage()
    await empty.goto(fixture.url)
    const rolledBack = await empty.evaluate(async () => {
      const { restoreMeetingAudio, snapshotMeetingAudio } = await import('/src/native/meeting-audio.ts')
      const assertCurrent = () => {}
      const record = { store: 'recordings', key: 'legacy', mimeType: '', blob: new Blob(['original']) }
      const failure = await restoreMeetingAudio([record, record], assertCurrent).then(() => null, error => error.name)
      const after = await snapshotMeetingAudio(assertCurrent)
      return [failure, after.length]
    })
    assert.deepEqual(rolledBack, ['ConstraintError', 0])

    const crashOutcomes = await empty.evaluate(async () => {
      const { restoreMeetingAudio, settleMeetingAudioRestore, snapshotMeetingAudio } = await import('/src/native/meeting-audio.ts')
      const assertCurrent = () => {}
      const record = { store: 'recordings', key: 'legacy', mimeType: '', blob: new Blob(['original']) }
      await restoreMeetingAudio([record], assertCurrent, 'rollback-stage')
      const wrongMarkerRejected = await settleMeetingAudioRestore('different-stage', false).then(() => false, () => true)
      await settleMeetingAudioRestore('rollback-stage', false)
      const afterRollback = (await snapshotMeetingAudio(assertCurrent)).length
      await restoreMeetingAudio([record], assertCurrent, 'published-stage')
      await settleMeetingAudioRestore('published-stage', true)
      await settleMeetingAudioRestore('published-stage', true)
      const afterPublish = await snapshotMeetingAudio(assertCurrent)
      return [wrongMarkerRejected, afterRollback, afterPublish.length, await afterPublish[0].blob.text()]
    })
    assert.deepEqual(crashOutcomes, [true, 0, 1, 'original'])
  } finally {
    await browser?.close()
    await fixture.server.close()
  }
})
