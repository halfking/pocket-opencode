/**
 * 笔记分级落盘：纯文本短笔记进 SQLite，超长/多媒体走文件档。
 * Run: node --test --experimental-strip-types src/features/notes/note-storage-policy.test.ts
 */
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import {
  INLINE_CHAR_LIMIT,
  buildSearchText,
  decideNoteStorage,
  summarizeForDisplay,
} from './note-storage-policy.ts'

describe('decideNoteStorage', () => {
  it('keeps short text-only notes inline with full body in content', () => {
    const body = '今天要把周报发给张三。'
    const decided = decideNoteStorage({ title: '周报', body, tags: ['工作'], hasMedia: false })
    assert.equal(decided.tier, 'inline')
    assert.equal(decided.displayContent, body)
    assert.equal(decided.persistBodyFile, false)
    assert.match(decided.searchText, /周报/)
    assert.match(decided.searchText, /张三/)
    assert.match(decided.searchText, /工作/)
  })

  it('uses file tier at the 1000-character boundary', () => {
    const short = '字'.repeat(INLINE_CHAR_LIMIT)
    const long = '字'.repeat(INLINE_CHAR_LIMIT + 1)
    assert.equal(decideNoteStorage({ body: short, hasMedia: false }).tier, 'inline')
    const decided = decideNoteStorage({ title: '长文', body: long, hasMedia: false })
    assert.equal(decided.tier, 'file')
    assert.equal(decided.persistBodyFile, true)
    assert.ok(decided.displayContent.length < long.length)
    assert.match(decided.searchText, /字{10}/)
  })

  it('forces file tier when media exists even if text is short', () => {
    const decided = decideNoteStorage({
      title: '语音',
      body: '记得买菜',
      hasMedia: true,
    })
    assert.equal(decided.tier, 'file')
    assert.equal(decided.persistBodyFile, true)
    assert.equal(decided.summary, '记得买菜')
    assert.match(decided.searchText, /买菜/)
  })
})

describe('buildSearchText / summarizeForDisplay', () => {
  it('joins title body and tags for FTS', () => {
    const text = buildSearchText('标题', '正文里有关键词', ['标签A', '标签B'])
    assert.match(text, /^标题\n正文里有关键词\n标签A 标签B/)
    assert.match(text, /标题/)
    assert.match(text, /关键词/)
    assert.match(text, /关键/)
  })

  it('truncates long body at a paragraph or 280 chars', () => {
    const first = '第一段摘要。'
    const body = `${first}\n\n${'后面还有很多字。'.repeat(40)}`
    assert.equal(summarizeForDisplay(body), first)
    const noBreak = '甲'.repeat(400)
    assert.equal(summarizeForDisplay(noBreak).length, 280)
  })
})
