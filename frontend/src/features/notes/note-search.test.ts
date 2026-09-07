/**
 * 智能搜索：解析 LLM JSON，失败时降级为 FTS 命中。
 * Run: node --test --experimental-strip-types src/features/notes/note-search.test.ts
 */
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { parseNoteSearchResponse, fallbackSearchBriefing } from './note-search-parse.ts'

describe('parseNoteSearchResponse', () => {
  it('reads summary and ranked matches from a JSON object', () => {
    const parsed = parseNoteSearchResponse(
      '{"summary":"两篇都在谈周报","matches":[{"id":"note-1","reason":"周报草稿"},{"id":"note-2","reason":"发送清单"}]}',
      ['note-1', 'note-2', 'note-3'],
    )
    assert.equal(parsed.summary, '两篇都在谈周报')
    assert.deepEqual(parsed.matches, [
      { id: 'note-1', reason: '周报草稿' },
      { id: 'note-2', reason: '发送清单' },
    ])
  })

  it('ignores unknown ids and falls back when JSON is broken', () => {
    const broken = parseNoteSearchResponse('不是 JSON', ['note-1', 'note-2'])
    assert.equal(broken.summary, '')
    assert.deepEqual(broken.matches, [])
    const filtered = parseNoteSearchResponse(
      '{"summary":"x","matches":[{"id":"ghost","reason":"no"}]}',
      ['note-1'],
    )
    assert.deepEqual(filtered.matches, [])
  })
})

describe('fallbackSearchBriefing', () => {
  it('states how many FTS hits were kept', () => {
    assert.equal(fallbackSearchBriefing(0), '没有找到相关笔记。')
    assert.equal(fallbackSearchBriefing(3), '找到 3 条相关笔记，离线仅展示全文匹配。')
  })
})
