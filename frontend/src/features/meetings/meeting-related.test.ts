import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import {
  mergeRecommendations, parseWikipediaOpenSearch, relatedQueryFromTranscript, wikipediaSearchUrl,
} from './meeting-related.ts'

describe('meeting-related', () => {
  it('builds a short query from the latest utterances', () => {
    assert.equal(relatedQueryFromTranscript(['', '预算', '发布窗口']), '预算 发布窗口')
    assert.equal(relatedQueryFromTranscript([]), '')
    assert.equal(
      relatedQueryFromTranscript(['今天评审第三季度预算，李四下周提交方案。']),
      '第三季度 预算',
    )
  })

  it('dedupes recommendations by type+id', () => {
    const a = { type: 'note' as const, id: 'n1', title: 'A', snippet: '', score: 1 }
    const b = { type: 'note' as const, id: 'n1', title: 'A2', snippet: '', score: 2 }
    const c = { type: 'email' as const, id: 'n1', title: 'E', snippet: '', score: 1 }
    assert.deepEqual(mergeRecommendations([a], [b, c], 3).map((i) => `${i.type}:${i.id}`), [
      'note:n1', 'email:n1',
    ])
  })

  it('builds and parses wikipedia open search', () => {
    assert.equal(wikipediaSearchUrl('  '), '')
    assert.match(wikipediaSearchUrl('Q3 预算'), /search=Q3/)
    assert.deepEqual(
      parseWikipediaOpenSearch(['q', ['预算'], ['释义'], ['https://zh.wikipedia.org/wiki/预算']]).map((i) => i.type),
      ['web'],
    )
    assert.deepEqual(parseWikipediaOpenSearch(null), [])
  })
})
