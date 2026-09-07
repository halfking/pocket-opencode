import { strict as assert } from 'node:assert'
import { test } from 'node:test'
import {
  EMAIL_LANGS,
  buildTranslatePrompt,
  extractTranslatedBody,
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
