import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { CLOUD_STT_NEED_BLOB, requireCloudAudioBlob } from './stt-cloud.ts'

describe('requireCloudAudioBlob', () => {
  it('throws when only a blob URL path is given', () => {
    assert.throws(
      () => requireCloudAudioBlob({ audioPath: 'blob:https://localhost/abc' }),
      (err: Error) => err.message === CLOUD_STT_NEED_BLOB,
    )
  })

  it('returns the blob for cloud upload', () => {
    const blob = new Blob([new Uint8Array([1, 2, 3])], { type: 'audio/webm' })
    assert.equal(requireCloudAudioBlob({ audioBlob: blob }), blob)
  })

  it('throws when neither blob nor path is given', () => {
    assert.throws(
      () => requireCloudAudioBlob({}),
      (err: Error) => err.message.includes('audioBlob or audioPath'),
    )
  })
})
