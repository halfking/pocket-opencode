import { strict as assert } from 'node:assert'
import { test } from 'node:test'
import {
  DEFAULT_EMAIL_LANG,
  EMAIL_LANGS,
  buildTranslatePrompt,
  extractTranslatedBody,
  isMostlyChinese,
  langShortLabel,
  resolveDisplayBody,
} from '../translate-email.ts'

test('language catalog includes original plus zh/en/ja', () => {
  const codes = EMAIL_LANGS.map((l) => l.code)
  assert.ok(codes.includes('original'))
  assert.ok(codes.includes('zh-CN'))
  assert.ok(codes.includes('en-US'))
  assert.ok(codes.includes('ja-JP'))
  assert.equal(langShortLabel('original'), '原文')
  assert.equal(langShortLabel('zh-CN'), '中')
})

test('translate prompt asks to keep format and forbids extra commentary', () => {
  const prompt = buildTranslatePrompt('<p>Hello</p>\n\nThanks', 'zh-CN')
  assert.match(prompt, /简体中文/)
  assert.match(prompt, /<p>Hello<\/p>/)
  assert.match(prompt, /不要添加说明/)
  assert.match(prompt, /保留/)
})

test('extractTranslatedBody strips fences and surrounding chatter', () => {
  assert.equal(extractTranslatedBody('```html\n<p>你好</p>\n```'), '<p>你好</p>')
  assert.equal(
    extractTranslatedBody('如下是译文：\n\n<p>你好</p>\n\n（已保留格式）'),
    '<p>你好</p>',
  )
})

test('resolveDisplayBody prefers cache then original', () => {
  const original = 'Hello'
  const cache = { 'zh-CN': '你好' }
  assert.equal(resolveDisplayBody(original, cache, 'original'), 'Hello')
  assert.equal(resolveDisplayBody(original, cache, 'zh-CN'), '你好')
  assert.equal(resolveDisplayBody(original, cache, 'en-US'), 'Hello')
})

// ── 默认语言 = 中文（2026-10-01 需求）───────────────────────────────────────

test('默认目标语言是简体中文', () => {
  assert.equal(DEFAULT_EMAIL_LANG, 'zh-CN')
})

test('中文正文判为「已是中文」，不触发翻译', () => {
  assert.equal(isMostlyChinese('您的发票已开具，请查收附件。'), true)
  assert.equal(isMostlyChinese('尊敬的用户，您的订单已发货。'), true)
})

test('英文/日文正文判为「非中文」，应触发翻译', () => {
  assert.equal(isMostlyChinese('Your invoice is ready, please check the attachment.'), false)
  assert.equal(isMostlyChinese('請求書を発行しました。添付ファイルをご確認ください。'), false)
  assert.equal(isMostlyChinese('청구서가 발행되었습니다.'), false)
  assert.equal(isMostlyChinese(''), false)
})

test('中英混排按占比判定（少量汉字不触发）', () => {
  // 英文为主、只夹一个中文产品名 → 不该翻
  assert.equal(
    isMostlyChinese('The Alipay invoice for order A-12345 has been generated successfully.'),
    false,
  )
  // 中文为主、夹少量英文术语 → 该翻（其实是中文正文，保持原文即可）
  assert.equal(isMostlyChinese('您的 invoice 已开具，请注意查收。'), true)
})

test('HTML 标签与实体不干扰中文占比统计', () => {
  const html = '<div class="wrapper"><p>您好，账单已生成。</p></div>'
  assert.equal(isMostlyChinese(html), true)
  // 纯英文 HTML 不应因为标签名里的字母被算成中文
  assert.equal(isMostlyChinese('<div class="a"><p>Your bill is ready.</p></div>'), false)
})

test('翻译提示要求保留 img src（否则译文会丢图）', () => {
  const prompt = buildTranslatePrompt('<img src="https://x/y.png"><p>Hello</p>', 'zh-CN')
  assert.match(prompt, /src 必须原样保留/)
  assert.match(prompt, /不要翻译人名、公司名、订单号、金额、日期与 URL/)
})
