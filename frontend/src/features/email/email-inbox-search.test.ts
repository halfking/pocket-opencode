/**
 * Run: node --test src/features/email/email-inbox-search.test.ts
 */
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { hasInboxSearch, matchInboxSearch } from './email-inbox-search.ts'

const mail = {
  fromAddress: 'promo@shop.com',
  fromName: 'Shop',
  subject: '九月账单提醒',
  snippet: '您的发票已出',
  aiSummary: '账单通知',
  date: 1_700_000_000_000,
}

describe('inbox search', () => {
  it('treats empty query as no-op', () => {
    assert.equal(hasInboxSearch({}), false)
    assert.equal(matchInboxSearch(mail, {}), true)
  })

  it('matches keyword against sender, title, or summary', () => {
    assert.equal(matchInboxSearch(mail, { q: 'shop' }), true)
    assert.equal(matchInboxSearch(mail, { q: '账单' }), true)
    assert.equal(matchInboxSearch(mail, { q: '发票' }), true)
    assert.equal(matchInboxSearch(mail, { q: '通知' }), true)
    assert.equal(matchInboxSearch(mail, { q: '无关' }), false)
  })

  it('filters sender, subject, and time range', () => {
    assert.equal(matchInboxSearch(mail, { from: 'shop.com' }), true)
    assert.equal(matchInboxSearch(mail, { from: 'other.com' }), false)
    assert.equal(matchInboxSearch(mail, { subject: '账单' }), true)
    assert.equal(matchInboxSearch(mail, { sinceMs: 1_600_000_000_000, untilMs: 1_800_000_000_000 }), true)
    assert.equal(matchInboxSearch(mail, { untilMs: 1_600_000_000_000 }), false)
  })
})
