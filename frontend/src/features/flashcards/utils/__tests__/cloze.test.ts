/**
 * Cloze 解析器测试（Anki {{c1::answer}} / {{c1::answer::hint}}）。
 *
 * Run: node --test --experimental-strip-types src/features/flashcards/utils/__tests__/cloze.test.ts
 */
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import {
  parseCloze,
  isClozeText,
  locateClozeSpans,
} from '../cloze.ts'

describe('parseCloze', () => {
  it('returns pure text when no cloze markup', () => {
    const r = parseCloze('hello world')
    assert.equal(r.clozeCount, 0)
    assert.equal(r.indices.length, 0)
    assert.deepEqual(r.segments, [{ type: 'text', text: 'hello world' }])
  })

  it('parses single c1 cloze', () => {
    const r = parseCloze('before {{c1::answer}} after')
    assert.equal(r.clozeCount, 1)
    assert.deepEqual(r.indices, [1])
    assert.equal(r.segments.length, 3)
    assert.deepEqual(r.segments[0], { type: 'text', text: 'before ' })
    assert.deepEqual(r.segments[1], { type: 'cloze', index: 1, answer: 'answer', hint: undefined })
    assert.deepEqual(r.segments[2], { type: 'text', text: ' after' })
  })

  it('parses c1 with hint', () => {
    const r = parseCloze('{{c1::H2O::氢二氧}}')
    assert.equal(r.clozeCount, 1)
    assert.deepEqual(r.segments[0], { type: 'cloze', index: 1, answer: 'H2O', hint: '氢二氧' })
  })

  it('parses multiple c<n> in order', () => {
    const r = parseCloze('水的化学式是 {{c1::H₂O}}，沸点是 {{c2::100℃}}')
    assert.equal(r.clozeCount, 2)
    assert.deepEqual(r.indices, [1, 2])
    assert.equal(r.segments.filter((s) => s.type === 'cloze').length, 2)
  })

  it('handles non-sequential cloze numbers', () => {
    const r = parseCloze('{{c3::third}} {{c1::first}} {{c2::second}}')
    assert.deepEqual(r.indices, [1, 2, 3])
    assert.equal(r.clozeCount, 3)
  })

  it('skips empty cloze {{c1::}}', () => {
    const r = parseCloze('before {{c1::}} after')
    assert.equal(r.clozeCount, 0)
    assert.equal(r.segments.length, 1)
    assert.deepEqual(r.segments[0], { type: 'text', text: 'before {{c1::}} after' })
  })

  it('skips invalid cloze index c0', () => {
    const r = parseCloze('{{c0::zero}} {{c1::one}}')
    assert.equal(r.clozeCount, 1)
    assert.deepEqual(r.indices, [1])
  })

  it('handles cloze at start and end of string', () => {
    const r = parseCloze('{{c1::start}} middle {{c1::end}}')
    assert.equal(r.clozeCount, 2)
    assert.equal(r.segments.length, 3)
    assert.equal((r.segments[0] as any).text, undefined) // cloze has no leading text
  })

  it('handles consecutive cloze with no separator', () => {
    const r = parseCloze('{{c1::a}}{{c2::b}}')
    assert.equal(r.clozeCount, 2)
    assert.equal(r.segments.length, 2)
  })

  it('handles Chinese + emoji in answer/hint', () => {
    const r = parseCloze('{{c1::答案::💡提示}}')
    assert.equal(r.clozeCount, 1)
    assert.deepEqual(r.segments[0], { type: 'cloze', index: 1, answer: '答案', hint: '💡提示' })
  })

  it('does not parse nested braces', () => {
    // Anki 不支持嵌套；这里 regex 在第一个 `}}` 处结束 cloze 内容。
    // 实际产出：cloze count=1, answer=outer {{c2, hint=inner （后续 ::hint}} 视为纯文本）
    const r = parseCloze('{{c1::outer {{c2::inner}} ::hint}}')
    assert.equal(r.clozeCount, 1)
    assert.deepEqual(r.segments[0], { type: 'cloze', index: 1, answer: 'outer {{c2', hint: 'inner' })
  })
})

describe('isClozeText', () => {
  it('returns true for cloze markup', () => {
    assert.equal(isClozeText('{{c1::a}}'), true)
    assert.equal(isClozeText('before {{c2::b}} after'), true)
  })

  it('returns false for plain text', () => {
    assert.equal(isClozeText('hello world'), false)
    assert.equal(isClozeText('{ not cloze }'), false)
    assert.equal(isClozeText('{{broken'), false)
  })

  it('returns false for empty cloze', () => {
    assert.equal(isClozeText('{{c1::}}'), false)
  })
})

describe('locateClozeSpans', () => {
  it('returns start/end offsets', () => {
    const input = 'before {{c1::answer}} after'
    const spans = locateClozeSpans(input)
    assert.equal(spans.length, 1)
    const span = spans[0]!
    assert.equal(span.answer, 'answer')
    assert.equal(span.start, 7)
    assert.equal(span.end, 7 + '{{c1::answer}}'.length)
  })

  it('returns multiple spans sorted by offset', () => {
    const input = '{{c1::a}} {{c2::b}} {{c3::c}}'
    const spans = locateClozeSpans(input)
    assert.equal(spans.length, 3)
    assert.deepEqual(spans.map((s) => s.answer), ['a', 'b', 'c'])
    // 按出现顺序排列（不按 cloze index，因为可能有 c3 在 c1 之前）
    for (let i = 1; i < spans.length; i++) {
      assert.ok(spans[i]!.start > spans[i - 1]!.start)
    }
  })

  it('preserves hint field', () => {
    const spans = locateClozeSpans('{{c1::x::hint-y}}')
    assert.equal(spans[0]!.hint, 'hint-y')
  })
})