/**
 * 笔记附件目录：按日期 + id 前缀分片，避免单目录文件过多。
 * Run: node --test --experimental-strip-types src/features/notes/note-paths.test.ts
 */
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import {
  noteAudioRelativePath,
  noteDir,
  noteFileRelPath,
  noteIdPrefix,
} from './note-paths.ts'

describe('noteDir', () => {
  it('shards by local date and first two id chars', () => {
    const createdAt = Date.UTC(2026, 8, 8, 4, 0, 0)
    assert.equal(
      noteDir('note-abc123', createdAt),
      'notes/2026/09/08/no/note-abc123',
    )
  })

  it('uses a fallback prefix when the id is shorter than 2 chars', () => {
    assert.equal(noteIdPrefix('n'), 'n_')
    assert.equal(noteIdPrefix(''), '__')
  })
})

describe('noteFileRelPath', () => {
  it('places audio images videos and body under typed folders', () => {
    const createdAt = Date.UTC(2026, 0, 2, 0, 0, 0)
    const dir = noteDir('note-zz9', createdAt)
    assert.equal(noteFileRelPath(dir, 'body'), `${dir}/body.md`)
    assert.equal(noteFileRelPath(dir, 'audio', 1, 'webm'), `${dir}/audio/01.webm`)
    assert.equal(noteFileRelPath(dir, 'image', 2, 'jpg'), `${dir}/images/02.jpg`)
    assert.equal(noteFileRelPath(dir, 'video', 1, 'mp4'), `${dir}/videos/01.mp4`)
  })
})

describe('noteAudioRelativePath', () => {
  const id = 'note-r6'
  const createdAt = Date.UTC(2026, 0, 2)
  const root = noteDir(id, createdAt)
  const audio = `${root}/versions/r-42/audio/01.webm`

  it('pairs relative and old sandbox references with the same recording', () => {
    assert.equal(noteAudioRelativePath(audio, id, createdAt), audio)
    assert.equal(noteAudioRelativePath(`file:///old-container/Documents/${audio}`, id, createdAt), audio)
    assert.equal(noteAudioRelativePath(`${root}/audio/42.m4a`, id, createdAt), `${root}/audio/42.m4a`)
  })

  it('leaves externally linked and unrelated media outside managed resolution', () => {
    for (const path of [
      'blob:recorded-meeting', `https://example.test/${audio}`, `sqlite:${audio}`,
      `file:///old-container/Documents/${audio.replace(id, 'note-other')}`,
      `${root}/versions/r-42/audio/../02.webm`, `${root}/audio/01.webm?other`,
    ]) assert.equal(noteAudioRelativePath(path, id, createdAt), null, path)
  })
})
