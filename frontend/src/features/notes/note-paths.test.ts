/**
 * 笔记附件目录：按日期 + id 前缀分片，避免单目录文件过多。
 * Run: node --test --experimental-strip-types src/features/notes/note-paths.test.ts
 */
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import {
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
