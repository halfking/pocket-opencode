import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import {
  resolveApiErrorMessage,
  shouldSurfaceRawMessage,
} from './api-error-message.ts'

describe('resolveApiErrorMessage', () => {
  it('returns the fallback when nothing usable was thrown', () => {
    assert.equal(resolveApiErrorMessage(undefined, '保存失败'), '保存失败')
    assert.equal(resolveApiErrorMessage(null, '保存失败'), '保存失败')
    assert.equal(resolveApiErrorMessage(new Error(''), '保存失败'), '保存失败')
    assert.equal(resolveApiErrorMessage('   ', '保存失败'), '保存失败')
  })

  it('surfaces Error.message', () => {
    assert.equal(resolveApiErrorMessage(new Error('network down'), '保存失败'), 'network down')
  })

  it('surfaces a non-empty thrown string', () => {
    assert.equal(resolveApiErrorMessage('boom', '保存失败'), 'boom')
  })

  it('reads message off a plain object shaped like ApiError', () => {
    assert.equal(resolveApiErrorMessage({ status: 409, message: 'deck name taken' }, '保存失败'), 'deck name taken')
  })

  it('falls back when the object has a blank message', () => {
    assert.equal(resolveApiErrorMessage({ status: 500, message: '  ' }, '保存失败'), '保存失败')
  })
})

describe('shouldSurfaceRawMessage', () => {
  it('suppresses 5xx internals and keeps 4xx user-correctable messages', () => {
    assert.equal(shouldSurfaceRawMessage({ status: 500, message: 'pq: duplicate key' }), false)
    assert.equal(shouldSurfaceRawMessage({ status: 502, message: 'upstream html' }), false)
    assert.equal(shouldSurfaceRawMessage({ status: 409, message: 'deck name taken' }), true)
    assert.equal(shouldSurfaceRawMessage({ status: 400, message: 'invalid rating 0' }), true)
  })

  it('surfaces messages when there is no status to judge by', () => {
    assert.equal(shouldSurfaceRawMessage(new Error('boom')), true)
    assert.equal(shouldSurfaceRawMessage('boom'), true)
    assert.equal(shouldSurfaceRawMessage(undefined), true)
  })

  it('composes with resolveApiErrorMessage into the caller-facing contract', () => {
    const render = (err: unknown, fallback: string) =>
      shouldSurfaceRawMessage(err) ? resolveApiErrorMessage(err, fallback) : fallback
    // 5xx 内部细节不得糊到界面上
    assert.equal(render({ status: 500, message: 'pq: duplicate key value violates unique constraint' }, '保存失败'), '保存失败')
    // 4xx 保留用户可纠正的具体原因
    assert.equal(render({ status: 400, message: 'invalid rating 0' }, '保存失败'), 'invalid rating 0')
    // 无状态码的普通异常照常展示
    assert.equal(render(new Error('Network request failed'), '保存失败'), 'Network request failed')
  })
})
