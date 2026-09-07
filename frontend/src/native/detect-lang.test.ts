import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { detectLang, translateTargetLang } from './detect-lang.ts'

describe('detectLang', () => {
  it('detects chinese, english, and mixed', () => {
    assert.equal(detectLang('我们今天讨论预算'), 'zh')
    assert.equal(detectLang('The deadline is next Friday'), 'en')
    assert.equal(detectLang('我们 confirm the budget tomorrow'), 'mixed')
  })
})

describe('translateTargetLang', () => {
  it('mirrors iflytek mixed translate rule', () => {
    assert.equal(translateTargetLang('zh'), 'en')
    assert.equal(translateTargetLang('en'), 'zh')
    assert.equal(translateTargetLang('mixed'), 'en')
  })
})
