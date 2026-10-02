/**
 * digest-share 与 usePronounce 的单测。
 *
 * 运行：cd frontend && node --experimental-strip-types --test \
 *        src/features/rss/__tests__/digest-share.test.ts
 *
 * 为什么这两块要有测试：
 *  - 分享文本的条数上限 / 兜底提示是"用户发出去的东西"，写错了会直接发到
 *    微博上，事后才发现就晚了；
 *  - extractExampleSentence 决定朗读哪一句，贪婪匹配会把中文释义也念出来。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { shareDigestText, cardItems, dataURLToBase64, ellipsizeForTest } from '../digest-share.ts'
import { extractExampleSentence } from '../../../composables/usePronounce.ts'
import type { RSSDigest } from '../../../api/rss.ts'

function digest(over: Partial<RSSDigest> = {}): RSSDigest {
  return {
    id: 'd1',
    date: '2026-10-03',
    headline: '10月3日 全部信息摘要：5 条 · IT 科技 3 · 财经 2',
    body: 'body',
    sections: [
      {
        category: 'it',
        label: 'IT 科技',
        items: [
          { id: 'i1', title: '第一条', url: 'https://a.example/1', sourceId: 's1', sourceTitle: '源一', category: 'it', language: 'zh' },
          { id: 'i2', title: '第二条', url: 'https://a.example/2', sourceId: 's1', sourceTitle: '源一', category: 'it', language: 'zh' },
        ],
      },
      {
        category: 'finance',
        label: '财经',
        items: [
          { id: 'i3', title: '第三条', url: 'https://a.example/3', sourceId: 's2', sourceTitle: '源二', category: 'finance', language: 'zh' },
        ],
      },
    ],
    itemCount: 5,
    sourceCount: 2,
    generatedAt: '2026-10-03T08:30:00Z',
    ...over,
  }
}

test('shareDigestText includes headline, items and links', () => {
  const text = shareDigestText(digest())
  assert.ok(text.startsWith('10月3日 全部信息摘要'), 'must lead with the headline')
  assert.ok(text.includes('【IT 科技】'))
  assert.ok(text.includes('【财经】'))
  assert.ok(text.includes('第一条 — 源一'))
  assert.ok(text.includes('https://a.example/1'))
})

test('shareDigestText caps items and tells the reader what is left', () => {
  const items = Array.from({ length: 20 }, (_, i) => ({
    id: `i${i}`, title: `条目${i}`, url: `https://a.example/${i}`, sourceId: 's', sourceTitle: '源', category: 'it', language: 'zh',
  }))
  const text = shareDigestText(digest({ sections: [{ category: 'it', label: 'IT 科技', items }], itemCount: 20 }), 12)
  const numbered = text.split('\n').filter((l) => /^\d+\. /.test(l))
  assert.equal(numbered.length, 12, 'must not exceed the cap')
  assert.ok(text.includes('另有 8 条'), `must disclose the remainder, got:\n${text}`)
})

test('shareDigestText has no leftover blank-line noise for an empty digest', () => {
  const text = shareDigestText(digest({ sections: [], itemCount: 0, headline: '10月3日 暂无新内容' }))
  assert.equal(text.trim(), '10月3日 暂无新内容')
})

test('cardItems respects the per-card limit and keeps section labels', () => {
  const rows = cardItems(digest(), 2)
  assert.equal(rows.length, 2)
  assert.equal(rows[0].section, 'IT 科技')
  assert.equal(rows[1].item, '第二条')
})

test('cardItems handles a digest with no sections', () => {
  assert.deepEqual(cardItems(digest({ sections: [] }), 5), [])
})

test('dataURLToBase64 strips the data: prefix', () => {
  assert.equal(dataURLToBase64('data:image/png;base64,AAAB'), 'AAAB')
  assert.equal(dataURLToBase64('AAAB'), 'AAAB')
  assert.equal(dataURLToBase64(''), '')
})

test('ellipsizeForTest truncates long CJK titles with an ellipsis', () => {
  const out = ellipsizeForTest('这是一个非常非常长的中文标题需要被截断', 200, 40, 1)
  assert.ok(out.length < 40)
  assert.ok(out.endsWith('…'))
})

test('extractExampleSentence only takes the quoted English example', () => {
  const back = 'IPA: /ˈdeɪʒeɪt/ · n. 日期 · 例: "Let\'s meet on Monday."'
  assert.equal(extractExampleSentence(back), "Let's meet on Monday.")
})

test('extractExampleSentence handles Chinese quotes and colon variants', () => {
  assert.equal(extractExampleSentence('IPA: /x/ · 例： “Good morning.”'), 'Good morning.')
})

test('extractExampleSentence returns empty when there is no example', () => {
  assert.equal(extractExampleSentence('IPA: /x/ · n. 某词 · 仅释义'), '')
  assert.equal(extractExampleSentence(''), '')
  assert.equal(extractExampleSentence(undefined as unknown as string), '')
})
