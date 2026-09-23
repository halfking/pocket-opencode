/**
 * flashcardIo 单元测试（buildExportBundle / importJsonFromText）。
 */
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import {
  buildExportBundle,
  importJsonFromText,
} from '../flashcardIo.ts'
import type { FlashcardNote, FlashcardCard, FlashcardDeckConfig } from '../../../../types/flashcards.ts'

const sampleNote: FlashcardNote = {
  id: 'n1',
  userId: 'u1',
  deckId: 'd1',
  front: 'front',
  back: 'back',
  tags: ['t1'],
  usn: 0,
  createdAt: 1000,
  updatedAt: 1000,
}

const sampleCard: FlashcardCard = {
  id: 'c1',
  noteId: 'n1',
  userId: 'u1',
  deckId: 'd1',
  state: 1,
  due: 2000,
  intervalDays: 0,
  stability: 0,
  difficulty: 0,
  reps: 0,
  lapses: 0,
  lastReviewAt: 0,
  usn: 0,
  createdAt: 1000,
  updatedAt: 1000,
}

const sampleDeck: FlashcardDeckConfig = {
  deckId: 'd1',
  userId: 'u1',
  name: 'Default',
  newPerDay: 20,
  reviewsPerDay: 200,
  learningStepsMin: [1, 10],
  graduatingIntervalDays: 1,
  easyIntervalDays: 4,
  fsrsWeights: [],
  desiredRetention: 0.9,
  usn: 0,
  createdAt: 1000,
  updatedAt: 1000,
}

describe('buildExportBundle', () => {
  it('bundles notes + cards + decks with version + exportedAt', () => {
    const bundle = buildExportBundle({
      notes: [sampleNote],
      cards: [sampleCard],
      deckConfigs: [sampleDeck],
    })
    assert.equal(bundle.version, 1)
    assert.ok(typeof bundle.exportedAt === 'number')
    assert.equal(bundle.notes.length, 1)
    assert.equal(bundle.cards.length, 1)
    assert.equal(bundle.deckConfigs.length, 1)
  })

  it('preserves reviewLogs when provided', () => {
    const log = {
      id: 'log1',
      cardId: 'c1',
      userId: 'u1',
      reviewedAt: 1500,
      rating: 3 as const,
      prevState: 1 as const,
      nextState: 2 as const,
      prevInterval: 0,
      nextInterval: 1,
      elapsedDays: 0,
    }
    const bundle = buildExportBundle({
      notes: [sampleNote],
      cards: [sampleCard],
      deckConfigs: [sampleDeck],
      reviewLogs: [log],
    })
    assert.equal(bundle.reviewLogs?.length, 1)
    assert.equal(bundle.reviewLogs?.[0]?.id, 'log1')
  })
})

describe('importJsonFromText', () => {
  it('round-trips through buildExportBundle', () => {
    const bundle = buildExportBundle({
      notes: [sampleNote],
      cards: [sampleCard],
      deckConfigs: [sampleDeck],
    })
    const json = JSON.stringify(bundle)
    const parsed = importJsonFromText(json)
    assert.equal(parsed.notes.length, 1)
    assert.equal(parsed.cards.length, 1)
    assert.equal(parsed.deckConfigs.length, 1)
    assert.equal(parsed.notes[0]?.front, 'front')
    assert.equal(parsed.deckConfigs[0]?.name, 'Default')
  })

  it('rejects invalid JSON', () => {
    assert.throws(() => importJsonFromText('not json'), /not valid JSON/)
  })

  it('rejects JSON missing required arrays', () => {
    assert.throws(
      () => importJsonFromText(JSON.stringify({ version: 1, exportedAt: 0 })),
      /missing notes/,
    )
  })

  it('rejects unsupported version', () => {
    assert.throws(
      () =>
        importJsonFromText(
          JSON.stringify({
            version: 999,
            exportedAt: 0,
            notes: [],
            cards: [],
            deckConfigs: [],
          }),
        ),
      /unsupported version/,
    )
  })

  it('handles missing reviewLogs field', () => {
    const json = JSON.stringify({
      version: 1,
      exportedAt: 0,
      notes: [],
      cards: [],
      deckConfigs: [],
    })
    const bundle = importJsonFromText(json)
    assert.equal(bundle.reviewLogs, undefined)
  })
})