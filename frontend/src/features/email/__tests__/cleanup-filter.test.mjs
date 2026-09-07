import { strict as assert } from 'node:assert'
import { test } from 'node:test'
import {
  emailDateToMs,
  formatEmailRelTime,
  hasCleanupConstraint,
  matchCleanup,
} from '../cleanup-filter.ts'

test('account-only filter is not a batch-delete constraint', () => {
  assert.equal(hasCleanupConstraint({}), false)
  assert.equal(hasCleanupConstraint({ accountId: 'acct-1' }), false)
  assert.equal(hasCleanupConstraint({ subject: '发票' }), true)
  assert.equal(hasCleanupConstraint({ from: 'promo@' }), true)
  assert.equal(hasCleanupConstraint({ since: 100 }), true)
  assert.equal(hasCleanupConstraint({ until: 200 }), true)
})

test('matchCleanup uses subject, source and unix-second range', () => {
  const e = {
    fromAddress: 'promo@shop.com',
    fromName: 'Shop Promo',
    subject: '限时抢购 全场秒杀',
    date: 1_725_000_000,
  }
  assert.equal(matchCleanup(e, { subject: '抢购' }), true)
  assert.equal(matchCleanup(e, { subject: '会议纪要' }), false)
  assert.equal(matchCleanup(e, { from: 'shop.com' }), true)
  assert.equal(matchCleanup(e, { from: 'Promo' }), true)
  assert.equal(matchCleanup(e, { since: 1_724_000_000, until: 1_726_000_000 }), true)
  assert.equal(matchCleanup(e, { since: 1_726_000_001 }), false)
})

test('emailDateToMs treats unix seconds as seconds', () => {
  assert.equal(emailDateToMs(1_725_000_000), 1_725_000_000_000)
  assert.equal(emailDateToMs(1_725_000_000_000), 1_725_000_000_000)
})

test('formatEmailRelTime does not treat seconds as milliseconds', () => {
  const nowSec = Math.floor(Date.now() / 1000)
  const label = formatEmailRelTime(nowSec - 3600)
  assert.match(label, /小时前/)
  assert.equal(label.includes('20683'), false)
})
