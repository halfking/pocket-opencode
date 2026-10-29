import assert from 'node:assert/strict'
import { test } from 'node:test'
import { DIM, VectorIndex } from '../../vector-index.ts'

test('foreign vectors cannot consume topK before the authorized-note filter', async () => {
  const vector = (x: number, y: number) => { const result = new Float32Array(DIM); result[0] = x; result[1] = y; return result }
  const index = new VectorIndex({ run: async () => 0, all: async () => [
    { note_id: 'foreign', embedding: vector(1, 0).buffer, dim: DIM },
    { note_id: 'mine', embedding: vector(0.8, 0.6).buffer, dim: DIM },
  ] })
  await index.load()
  assert.equal((await index.searchAsync(vector(1, 0), 1))[0]!.noteId, 'foreign')
  assert.equal((await index.searchAsync(vector(1, 0), 1, 1, new Set(['mine'])))[0]!.noteId, 'mine')
})
