import { strict as assert } from 'node:assert'
import { test } from 'node:test'
import { emailCatLabel, formatEmailDate, looksLikeHtml, parseForwardRecipients, quotedForwardBody } from '../email-body-format.ts'

test('looksLikeHtml detects markup and ignores plain text', () => {
  assert.equal(looksLikeHtml('<div>hi</div>'), true)
  assert.equal(looksLikeHtml('<HTML><BODY>hi</BODY></HTML>'), true)
  assert.equal(looksLikeHtml('hello < world'), false)
  assert.equal(looksLikeHtml('普通正文\n第二行'), false)
})

test('quotedForwardBody keeps original body after a header', () => {
  const out = quotedForwardBody({
    from: 'Ada <a@x.com>',
    date: '2026-09-08 10:00',
    subject: 'Hello',
    body: 'Line 1\nLine 2',
  })
  assert.match(out, /转发邮件/)
  assert.match(out, /Ada <a@x.com>/)
  assert.match(out, /Hello/)
  assert.match(out, /Line 1\nLine 2/)
})

test('parseForwardRecipients splits and trims emails', () => {
  assert.deepEqual(parseForwardRecipients('a@x.com, b@y.com ; c@z.com'), [
    'a@x.com',
    'b@y.com',
    'c@z.com',
  ])
  assert.deepEqual(parseForwardRecipients('  '), [])
})

test('formatEmailDate is zero-padded local timestamp', () => {
  const out = formatEmailDate(Date.UTC(2026, 8, 8, 2, 5))
  assert.match(out, /^\d{4}-\d{2}-\d{2} \d{2}:\d{2}$/)
})

test('emailCatLabel maps known categories and keeps unknown', () => {
  assert.equal(emailCatLabel('work'), '工作')
  assert.equal(emailCatLabel('bill'), '账单')
  assert.equal(emailCatLabel(null), '')
  assert.equal(emailCatLabel('custom'), 'custom')
})

