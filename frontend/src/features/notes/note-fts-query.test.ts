/**
 * 移动端 FTS 查询：中文不能整句加引号，否则 MATCH 几乎为零。
 * Run: node --test --experimental-strip-types src/features/notes/note-fts-query.test.ts
 */
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import {
  buildFtsQuery,
  cjkBigramTokens,
  extractiveBriefing,
  mergeSearchHits,
} from './note-fts-query.ts'

describe('buildFtsQuery', () => {
  it('quotes latin tokens to avoid FTS operators', () => {
    assert.equal(buildFtsQuery('docker AND k8s'), '"docker" "k8s"')
  })

  it('turns a Chinese phrase into overlapping bigrams', () => {
    assert.equal(buildFtsQuery('周会纪要'), '"周会" OR "会纪" OR "纪要"')
  })

  it('mixes chinese bigrams with quoted english words', () => {
    const q = buildFtsQuery('周会 docker')
    assert.match(q, /"周会"/)
    assert.match(q, /"docker"/)
  })
})

describe('cjkBigramTokens / mergeSearchHits', () => {
  it('emits overlapping two-character tokens for unicode61 FTS', () => {
    assert.equal(cjkBigramTokens('周会纪要'), '周会 会纪 纪要')
  })

  it('keeps first-list order and drops duplicate ids', () => {
    const merged = mergeSearchHits(
      [{ note: { id: 'a' }, score: 2 }, { note: { id: 'b' }, score: 1 }],
      [{ note: { id: 'b' }, score: 9 }, { note: { id: 'c' }, score: 0 }],
      3,
    )
    assert.deepEqual(merged.map((r) => r.note.id), ['a', 'b', 'c'])
  })
})

describe('extractiveBriefing', () => {
  it('writes a readable digest from titles and first sentences', () => {
    const text = extractiveBriefing('周会', [
      { title: '产品周会', content: '决定下周三发布。还要改文案。' },
      { title: null, content: '记得同步设计稿给张三。' },
    ])
    assert.match(text, /2 条/)
    assert.match(text, /产品周会/)
    assert.match(text, /下周三发布/)
    assert.match(text, /同步设计稿/)
  })

  it('says none when the hit list is empty', () => {
    assert.equal(extractiveBriefing('周会', []), '没有找到相关笔记。')
  })
})
